package main

import (
	"log/slog"
	"path/filepath"
	"strings"
	"sync"

	"github.com/zsiec/switchframe/server/asr"
	"github.com/zsiec/switchframe/server/audio"
	"github.com/zsiec/switchframe/server/caption"
	"github.com/zsiec/switchframe/server/control"
	"github.com/zsiec/switchframe/server/internal"
)

// splitWords splits text on whitespace, preserving word boundaries.
func splitWords(text string) []string {
	return strings.Fields(text)
}

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
		ModelPath: a.cfg.ASRModelPath,
		UseGPU:    true,
	})
	if err != nil {
		slog.Warn("ASR engine unavailable", "error", err)
		return nil // Non-fatal
	}

	a.asrEngine = engine

	// Wire transcript output to caption manager with line wrapping.
	// CEA-608 supports 32 chars per line. We word-wrap and use newlines
	// to scroll, keeping captions readable on screen.
	const maxLineChars = 32
	var captionLinePos int

	engine.OnTranscript(func(text string, isFinal bool) {
		// Word-wrap: split text into chunks that fit in 32-char lines.
		words := splitWords(text)
		for _, word := range words {
			wordLen := len(word)
			if wordLen == 0 {
				continue
			}

			// If this word would overflow the current line, start a new line.
			needSpace := captionLinePos > 0
			spaceNeeded := wordLen
			if needSpace {
				spaceNeeded++ // for the space before the word
			}

			if captionLinePos+spaceNeeded > maxLineChars && captionLinePos > 0 {
				a.captionMgr.IngestNewline()
				captionLinePos = 0
				needSpace = false
			}

			if needSpace {
				a.captionMgr.IngestText(" ")
				captionLinePos++
			}
			a.captionMgr.IngestText(word)
			captionLinePos += wordLen
		}

		if isFinal {
			a.captionMgr.IngestNewline()
			captionLinePos = 0
		}
	})

	// Wire audio input — fan-out with existing sink if present.
	a.wireASRAudioSink()

	// Note: caption mode is set to ModeAuto later in run() after the API is
	// initialized, because SetMode triggers broadcastState which requires
	// a.api to be non-nil.

	slog.Info("ASR engine initialized",
		"model", filepath.Base(a.cfg.ASRModelPath),
		"language", engine.Language(),
	)
	return nil
}

// wireASRAudioSink sets the mixer's raw audio sink to feed the ASR engine,
// preserving any existing sink (e.g., MXL output) via fan-out.
func (a *App) wireASRAudioSink() {
	var logOnce sync.Once
	asrSink := audio.RawAudioSink(func(pcm []float32, pts int64, sampleRate, channels int) {
		logOnce.Do(func() {
			slog.Info("ASR audio sink: first audio received",
				"samples", len(pcm),
				"sampleRate", sampleRate,
				"channels", channels,
			)
		})
		a.asrEngine.IngestAudio(pcm, pts, sampleRate, channels)
	})

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
