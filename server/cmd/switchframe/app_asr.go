package main

import (
	"log/slog"
	"os"
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

	// Auto-detect Silero VAD model in the same directory as the whisper model.
	// Silero VAD is a neural-network speech detector that filters background
	// noise/music before Whisper processes the audio — far more accurate than
	// RMS energy thresholds.
	vadModelPath := ""
	modelsDir := filepath.Dir(a.cfg.ASRModelPath)
	vadCandidates := []string{
		filepath.Join(modelsDir, "ggml-silero-v6.2.0.bin"),
		filepath.Join(modelsDir, "ggml-silero-v5.1.2.bin"),
	}
	for _, p := range vadCandidates {
		if _, err := os.Stat(p); err == nil {
			vadModelPath = p
			break
		}
	}

	engine, err := asr.NewEngine(asr.EngineConfig{
		ModelPath:    a.cfg.ASRModelPath,
		VADModelPath: vadModelPath,
		UseGPU:       true,
	})
	if err != nil {
		slog.Warn("ASR engine unavailable", "error", err)
		return nil // Non-fatal
	}
	if vadModelPath != "" {
		slog.Info("ASR: Silero VAD enabled", "model", filepath.Base(vadModelPath))
	} else {
		slog.Info("ASR: using energy-based VAD (no Silero model found)")
	}

	a.asrEngine = engine

	// Paced caption output: ASR produces words with confidence scores and
	// inter-segment gap timing. The pacer drains words at a steady rate
	// with speaker-change detection (>> prefix) and confidence-based
	// holdback (low-confidence words wait one more cycle).
	pacer := newCaptionPacer(a.captionMgr)
	engine.OnTranscript(func(words []asr.TranscriptWord, isFinal bool) {
		pacer.EnqueueTranscriptWords(words, isFinal)
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

// resetASROnProgramChange clears the ASR engine and caption pacer when the
// program source changes. Without this, stale text from the old source
// continues to drain through the pacer for minutes after a cut.
func (a *App) resetASROnProgramChange() {
	if a.asrEngine != nil {
		a.asrEngine.Reset()
	}
	if a.captionPacer != nil {
		a.captionPacer.Flush()
	}
	if a.captionMgr != nil {
		a.captionMgr.Clear()
	}
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
	modelsDir  string // directory containing ggml-*.bin files
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

func (m *asrManagerAdapter) SetASRModel(name string) error {
	if m.engine == nil {
		return nil
	}
	path := filepath.Join(m.modelsDir, "ggml-"+name+".bin")
	if err := m.engine.SetModel(path); err != nil {
		return err
	}
	m.modelPath = path
	return nil
}

func (m *asrManagerAdapter) AvailableASRModels() []string {
	entries, err := os.ReadDir(m.modelsDir)
	if err != nil {
		return nil
	}
	var models []string
	for _, e := range entries {
		name := e.Name()
		if strings.HasPrefix(name, "ggml-") && strings.HasSuffix(name, ".bin") &&
			!strings.HasPrefix(name, "for-tests-") &&
			!strings.Contains(name, "silero") {
			// "ggml-medium.en.bin" → "medium.en"
			model := strings.TrimPrefix(name, "ggml-")
			model = strings.TrimSuffix(model, ".bin")
			models = append(models, model)
		}
	}
	return models
}

// --- Caption Pacer ---

// pacerWord is a word in the pacer queue with metadata from Whisper.
type pacerWord struct {
	text       string
	confidence float32 // 0.0-1.0 from whisper token probability
	gapBefore  int     // ms of silence before this word (from whisper segments)
	isFinal    bool    // last word of a final transcript — insert newline after
}

// captionPacer drains words from a queue at a steady rate (~3 words/sec)
// with three broadcast-quality features:
//
//  1. CEA-608 line wrapping at 32 characters
//  2. Speaker-change detection via Whisper segment gaps (>> prefix)
//  3. Confidence-based holdback: low-confidence words wait one tick
type captionPacer struct {
	mgr *caption.Manager

	mu           sync.Mutex
	queue        []pacerWord
	held         *pacerWord // low-confidence word waiting for confirmation
	stopCh       chan struct{}
	stopped      bool
	linePos      int       // current position on the CEA-608 line
	lastEmitTime time.Time // when the last word was displayed
	cleared      bool      // true if we already cleared after timeout
}

const (
	pacerMaxLineChars     = 32
	pacerWordsPerSecond   = 3             // ~180 WPM
	pacerConfidenceThresh = 0.7           // words below this are held back one tick
	pacerSpeakerChangeGap = 300           // ms gap in whisper segments → speaker change
	pacerMaxQueueWords    = 50            // cap to prevent unbounded growth
	pacerClearTimeout     = 5 * time.Second // clear display after 5s of no new words
)

func newCaptionPacer(mgr *caption.Manager) *captionPacer {
	p := &captionPacer{
		mgr:    mgr,
		stopCh: make(chan struct{}),
	}
	go p.run()
	return p
}

// Flush discards all queued words. Called on program source changes
// to prevent stale text from the old source draining for minutes.
func (p *captionPacer) Flush() {
	p.mu.Lock()
	p.queue = p.queue[:0]
	p.held = nil
	p.linePos = 0
	p.mu.Unlock()
}

// EnqueueTranscriptWords adds words with confidence and gap metadata.
func (p *captionPacer) EnqueueTranscriptWords(words []asr.TranscriptWord, isFinal bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, w := range words {
		p.queue = append(p.queue, pacerWord{
			text:       w.Text,
			confidence: w.Confidence,
			gapBefore:  w.GapBefore,
		})
	}
	if isFinal && len(p.queue) > 0 {
		p.queue[len(p.queue)-1].isFinal = true
	}
	// Cap queue to prevent unbounded growth.
	if len(p.queue) > pacerMaxQueueWords {
		p.queue = p.queue[len(p.queue)-pacerMaxQueueWords:]
	}
}

// run is the pacer goroutine. Uses a fast base tick and emits 1-3 words
// per tick depending on queue depth — speeding up when falling behind,
// slowing down when caught up. This keeps captions in sync with speech
// cadence regardless of speaking speed.
func (p *captionPacer) run() {
	// Base tick at the max drain rate (6 words/sec). The number of words
	// emitted per tick scales with queue depth:
	//   queue <= 5:  1 word/tick  (~6 WPS, comfortable reading)
	//   queue 6-15:  2 words/tick (~12 WPS, catching up)
	//   queue > 15:  3 words/tick (~18 WPS, fast drain to recover)
	const baseTick = time.Second / 6
	ticker := time.NewTicker(baseTick)
	defer ticker.Stop()

	for {
		select {
		case <-p.stopCh:
			return
		case <-ticker.C:
			p.mu.Lock()
			depth := len(p.queue)
			p.mu.Unlock()

			wordsThisTick := 1
			if depth > 15 {
				wordsThisTick = 3
			} else if depth > 5 {
				wordsThisTick = 2
			}

			for i := 0; i < wordsThisTick; i++ {
				p.emitNextWord()
			}

			// Auto-clear: if no words emitted for 5s, erase displayed captions.
			p.mu.Lock()
			if !p.lastEmitTime.IsZero() && !p.cleared && time.Since(p.lastEmitTime) >= pacerClearTimeout {
				p.cleared = true
				p.linePos = 0
				p.mu.Unlock()
				p.mgr.Clear()
			} else {
				p.mu.Unlock()
			}
		}
	}
}

// emitNextWord pops one word from the queue and sends it to the caption
// manager. Low-confidence words are held for one tick — giving the next
// inference a chance to correct them. After one tick, held words emit
// regardless (better late than never in live captioning).
func (p *captionPacer) emitNextWord() {
	p.mu.Lock()

	// Emit held word from last tick (held long enough).
	if p.held != nil {
		w := *p.held
		p.held = nil
		p.mu.Unlock()
		p.emitWord(w)
		return
	}

	if len(p.queue) == 0 {
		p.mu.Unlock()
		return
	}

	word := p.queue[0]
	p.queue = p.queue[1:]
	p.mu.Unlock()

	// Confidence holdback: low-confidence words wait one tick.
	if word.confidence > 0 && word.confidence < pacerConfidenceThresh && !word.isFinal {
		p.mu.Lock()
		p.held = &word
		p.mu.Unlock()
		return
	}

	p.emitWord(word)
}

// emitWord sends a word to the caption manager with speaker-change
// detection and line wrapping. Updates lastEmitTime for auto-clear.
func (p *captionPacer) emitWord(word pacerWord) {
	p.mu.Lock()
	p.lastEmitTime = time.Now()
	p.cleared = false
	p.mu.Unlock()
	// Speaker change: Whisper segment gap > 300ms → new line with >> prefix.
	if word.gapBefore >= pacerSpeakerChangeGap {
		p.mgr.IngestNewline()
		p.mgr.IngestText(">> ")
		p.linePos = 3
	}

	wordLen := len(word.text)
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
	p.mgr.IngestText(word.text)
	p.linePos += wordLen

	// Final word: insert newline to close the utterance.
	if word.isFinal {
		p.mgr.IngestNewline()
		p.linePos = 0
	}
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
