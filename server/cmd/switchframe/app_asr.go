package main

import (
	"log/slog"
	"path/filepath"
	"strings"
	"sync"
	"time"

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

	// Paced caption output: ASR dumps words into a queue, a timer drains
	// them at ~3 words/sec (~180 WPM) with CEA-608 line wrapping at 32 chars.
	// This prevents burst output when Whisper returns a full transcript.
	pacer := newCaptionPacer(a.captionMgr)
	engine.OnTranscript(func(text string, isFinal bool) {
		words := splitWords(text)
		pacer.EnqueueWords(words, isFinal)
	})
	a.captionPacer = pacer

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

// --- Caption Pacer ---

// captionPacer drains words from a queue at a steady rate (~3 words/sec)
// with CEA-608 line wrapping at 32 characters. This prevents the burst
// output that occurs when Whisper returns a full transcript at once.
type captionPacer struct {
	mgr *caption.Manager

	mu      sync.Mutex
	queue   []string // pending words
	stopCh  chan struct{}
	stopped bool
	linePos int // current position on the CEA-608 line
}

const (
	pacerMaxLineChars   = 32
	pacerWordsPerSecond = 3 // ~180 WPM — slightly fast for readability
)

func newCaptionPacer(mgr *caption.Manager) *captionPacer {
	p := &captionPacer{
		mgr:    mgr,
		stopCh: make(chan struct{}),
	}
	go p.run()
	return p
}

// EnqueueWords adds words to the output queue.
func (p *captionPacer) EnqueueWords(words []string, isFinal bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.queue = append(p.queue, words...)
	if isFinal {
		p.queue = append(p.queue, "\n") // sentinel for newline
	}
}

// run is the pacer goroutine that drains words at a steady rate.
func (p *captionPacer) run() {
	ticker := time.NewTicker(time.Second / pacerWordsPerSecond)
	defer ticker.Stop()

	for {
		select {
		case <-p.stopCh:
			return
		case <-ticker.C:
			p.emitNextWord()
		}
	}
}

// emitNextWord pops one word from the queue and sends it to the caption manager.
func (p *captionPacer) emitNextWord() {
	p.mu.Lock()
	if len(p.queue) == 0 {
		p.mu.Unlock()
		return
	}
	word := p.queue[0]
	p.queue = p.queue[1:]
	p.mu.Unlock()

	if word == "\n" {
		p.mgr.IngestNewline()
		p.linePos = 0
		return
	}

	wordLen := len(word)
	needSpace := p.linePos > 0
	spaceNeeded := wordLen
	if needSpace {
		spaceNeeded++
	}

	// Word-wrap at 32 characters.
	if p.linePos+spaceNeeded > pacerMaxLineChars && p.linePos > 0 {
		p.mgr.IngestNewline()
		p.linePos = 0
		needSpace = false
	}

	if needSpace {
		p.mgr.IngestText(" ")
		p.linePos++
	}
	p.mgr.IngestText(word)
	p.linePos += wordLen
}

// Stop halts the pacer goroutine.
func (p *captionPacer) Stop() {
	p.mu.Lock()
	if !p.stopped {
		p.stopped = true
		close(p.stopCh)
	}
	p.mu.Unlock()
}
