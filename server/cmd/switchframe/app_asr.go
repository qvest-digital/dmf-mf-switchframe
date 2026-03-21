package main

import (
	"log/slog"
	"path/filepath"

	"github.com/zsiec/switchframe/server/asr"
	"github.com/zsiec/switchframe/server/audio"
	"github.com/zsiec/switchframe/server/caption"
	"github.com/zsiec/switchframe/server/control"
	"github.com/zsiec/switchframe/server/internal"
)

// initASR sets up the ASR engine when --asr-model is provided.
// Requires --captions to be enabled (needs caption manager for output).
// Non-fatal: if the engine fails to initialize, ASR is simply unavailable.
func (a *App) initASR() error {
	if a.cfg.ASRModelPath == "" {
		return nil
	}
	if a.captionMgr == nil {
		slog.Warn("ASR requires --captions flag, skipping")
		return nil
	}

	engine, err := asr.NewEngine(asr.EngineConfig{
		ModelDir: a.cfg.ASRModelPath,
		UseFP16:  true,
	})
	if err != nil {
		slog.Warn("ASR engine unavailable", "error", err)
		return nil // Non-fatal
	}

	a.asrEngine = engine

	// Wire transcript output to caption manager.
	engine.OnTranscript(func(text string, isFinal bool) {
		a.captionMgr.IngestText(text)
		if isFinal {
			a.captionMgr.IngestNewline()
		}
	})

	// Wire audio input — fan-out with existing sink if present.
	a.wireASRAudioSink()

	// Set caption mode to auto.
	a.captionMgr.SetMode(caption.ModeAuto)

	slog.Info("ASR engine initialized",
		"model", filepath.Base(a.cfg.ASRModelPath),
		"language", engine.Language(),
	)
	return nil
}

// wireASRAudioSink sets the mixer's raw audio sink to feed the ASR engine,
// preserving any existing sink (e.g., MXL output) via fan-out.
func (a *App) wireASRAudioSink() {
	asrSink := audio.RawAudioSink(a.asrEngine.IngestAudio)

	if a.mxlOutput != nil {
		// MXL output already has a sink set. Create fan-out that calls both.
		mxlWriter := a.mxlOutput.Writer()
		a.mixer.SetRawAudioSink(func(pcm []float32, pts int64, sampleRate, channels int) {
			mxlWriter.WriteAudio(pcm, pts, sampleRate, channels)
			asrSink(pcm, pts, sampleRate, channels)
		})
	} else {
		a.mixer.SetRawAudioSink(asrSink)
	}
}

// enrichASRState adds ASR state to a ControlRoomState snapshot.
func (a *App) enrichASRState(state internal.ControlRoomState) internal.ControlRoomState {
	if a.asrEngine == nil {
		return state
	}
	state.ASR = &internal.ASRState{
		Available: true,
		Active:    a.captionMgr != nil && a.captionMgr.Mode() == caption.ModeAuto,
		Language:  a.asrEngine.Language(),
		ModelName: filepath.Base(a.cfg.ASRModelPath),
	}
	return state
}

// asrManagerAdapter implements control.ASRManager.
type asrManagerAdapter struct {
	engine     *asr.Engine
	captionMgr *caption.Manager
	modelPath  string
}

var _ control.ASRManager = (*asrManagerAdapter)(nil)

func (m *asrManagerAdapter) IsASRAvailable() bool { return m.engine != nil }

func (m *asrManagerAdapter) IsASRActive() bool {
	if m.captionMgr == nil {
		return false
	}
	return m.captionMgr.Mode() == caption.ModeAuto
}

func (m *asrManagerAdapter) SetASRActive(active bool) error {
	if m.captionMgr == nil {
		return nil
	}
	if active {
		m.captionMgr.SetMode(caption.ModeAuto)
	} else {
		m.captionMgr.SetMode(caption.ModeOff)
	}
	return nil
}

func (m *asrManagerAdapter) SetASRLanguage(lang string) {
	if m.engine != nil {
		m.engine.SetLanguage(lang)
	}
}

func (m *asrManagerAdapter) ASRLanguage() string {
	if m.engine != nil {
		return m.engine.Language()
	}
	return ""
}

func (m *asrManagerAdapter) ASRModelName() string {
	return filepath.Base(m.modelPath)
}
