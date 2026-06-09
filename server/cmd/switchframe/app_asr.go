package main

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/zsiec/switchframe/server/asr"
	"github.com/zsiec/switchframe/server/audio"
	"github.com/zsiec/switchframe/server/caption"
	"github.com/zsiec/switchframe/server/control"
	"github.com/zsiec/switchframe/server/internal"
)

// initASR sets up the ASR sidecar engine when --asr-sidecar is provided.
// Requires --captions to be enabled (needs caption manager for output).
// Non-fatal: if the sidecar fails to initialize, ASR is simply unavailable.
func (a *App) initASR() error {
	if a.cfg.ASRSidecarBin == "" {
		return nil
	}
	if a.captionMgr == nil {
		slog.Warn("ASR requires --captions flag, skipping")
		return nil
	}

	cfg := asr.EngineConfig{
		Sidecar: &asr.SidecarConfig{
			BinPath:          a.cfg.ASRSidecarBin,
			ShmName:          fmt.Sprintf("/switchframe-asr-%d", os.Getpid()),
			ModelDir:         a.cfg.ASRSidecarModelDir,
			WhisperModelsDir: a.cfg.ASRWhisperModelsDir,
			DefaultBackend:   a.cfg.ASRDefaultBackend,
			Device:           0,
			WhisperThreads:   a.cfg.ASRWhisperThreads,
		},
	}

	engine, err := asr.NewEngine(cfg)
	if err != nil {
		slog.Warn("ASR engine unavailable", "error", err)
		return nil // Non-fatal
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

	// Update cached state + broadcast after async backend switches complete.
	engine.OnBackendSwitch(func() {
		a.updateASRStateCache()
		a.broadcastState(nil)
	})

	// Also update when the sidecar reports status changes (backend/model/config).
	// This handles the async timing: SwitchBackend returns immediately,
	// but the sidecar processes the switch and reports back via IPC later.
	if sb := engine.SidecarBackend(); sb != nil {
		sb.OnStatusUpdate(func() {
			a.updateASRStateCache()
			// Only broadcast if API is initialized — the first status
			// arrives during initASR before the API is wired up.
			if a.api != nil {
				a.broadcastState(nil)
			}
		})
	}

	// Note: caption mode is set to ModeAuto later in run() after the API is
	// initialized, because SetMode triggers broadcastState which requires
	// a.api to be non-nil.

	slog.Info("ASR engine initialized",
		"backend", engine.ActiveBackend(),
		"model", engine.ModelName(),
		"availableBackends", strings.Join(engine.AvailableBackends(), ","),
	)
	a.updateASRStateCache()
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
// Uses cached values to avoid taking any locks — this function is called
// from broadcastState which may be invoked while ASR engine locks are held.
func (a *App) enrichASRState(state internal.ControlRoomState) internal.ControlRoomState {
	if a.asrEngine == nil {
		return state
	}
	cached := a.cachedASRState.Load()
	if cached != nil {
		state.ASR = cached
	}
	return state
}

// updateASRStateCache rebuilds the cached ASR state. Called after ASR config
// changes, outside of any lock critical sections.
func (a *App) updateASRStateCache() {
	if a.asrEngine == nil {
		return
	}
	backends := a.asrEngine.AvailableBackends()
	if backends == nil {
		backends = []string{}
	}
	asrState := &internal.ASRState{
		Available:         true,
		Active:            a.captionMgr != nil && a.captionMgr.Mode() == caption.ModeAuto,
		Backend:           a.asrEngine.ActiveBackend(),
		AvailableBackends: backends,
		ModelName:         a.asrEngine.ModelName(),
		BackendConfig:     a.asrEngine.BackendConfig(),
	}
	if a.captionPacer != nil {
		asrState.ConfidenceFilter = a.captionPacer.ConfidencePreset()
		asrState.SpeakerDetection = a.captionPacer.SpeakerDetectionPreset()
	}
	a.cachedASRState.Store(asrState)
}

// asrManagerAdapter implements control.ASRManager, bridging the multi-backend
// asr.Engine to the control package's REST API interface.
type asrManagerAdapter struct {
	engine        *asr.Engine
	captionMgr    *caption.Manager
	pacer         *captionPacer
	onCacheUpdate func() // called after state-changing operations to update cached ASR state
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
	if m.onCacheUpdate != nil {
		m.onCacheUpdate()
	}
	return nil
}

// Backend selection — delegated to asr.Engine.

func (m *asrManagerAdapter) ActiveBackend() string {
	if m.engine == nil {
		return ""
	}
	return m.engine.ActiveBackend()
}

func (m *asrManagerAdapter) AvailableBackends() []string {
	if m.engine == nil {
		return nil
	}
	return m.engine.AvailableBackends()
}

func (m *asrManagerAdapter) SwitchBackend(name string) error {
	if m.engine == nil {
		return nil
	}
	err := m.engine.SwitchBackend(name)
	if err == nil && m.onCacheUpdate != nil {
		m.onCacheUpdate()
	}
	return err
}

// Per-backend model management — delegated to asr.Engine.

func (m *asrManagerAdapter) ASRModelName() string {
	if m.engine == nil {
		return ""
	}
	return m.engine.ModelName()
}

func (m *asrManagerAdapter) SetASRModel(name string) error {
	if m.engine == nil {
		return nil
	}
	err := m.engine.SetModel(name)
	if err == nil && m.onCacheUpdate != nil {
		m.onCacheUpdate()
	}
	return err
}

func (m *asrManagerAdapter) AvailableASRModels() []string {
	if m.engine == nil {
		return nil
	}
	return m.engine.Models()
}

// Backend-specific config (JSON pass-through) — delegated to asr.Engine.

func (m *asrManagerAdapter) BackendConfig() json.RawMessage {
	if m.engine == nil {
		return nil
	}
	return m.engine.BackendConfig()
}

func (m *asrManagerAdapter) SetBackendConfig(raw json.RawMessage) error {
	if m.engine == nil {
		return nil
	}
	err := m.engine.SetBackendConfig(raw)
	if err == nil && m.onCacheUpdate != nil {
		m.onCacheUpdate()
	}
	return err
}

// Pacer settings (backend-agnostic).

func (m *asrManagerAdapter) SetASRConfidence(preset string) {
	if m.pacer != nil {
		m.pacer.SetConfidencePreset(preset)
	}
	if m.onCacheUpdate != nil {
		m.onCacheUpdate()
	}
}

func (m *asrManagerAdapter) ASRConfidence() string {
	if m.pacer != nil {
		return m.pacer.ConfidencePreset()
	}
	return "medium"
}

func (m *asrManagerAdapter) SetASRSpeakerDetection(preset string) {
	if m.pacer != nil {
		m.pacer.SetSpeakerDetectionPreset(preset)
	}
	if m.onCacheUpdate != nil {
		m.onCacheUpdate()
	}
}

func (m *asrManagerAdapter) ASRSpeakerDetection() string {
	if m.pacer != nil {
		return m.pacer.SpeakerDetectionPreset()
	}
	return "normal"
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

	mu               sync.Mutex
	queue            []pacerWord
	held             *pacerWord // low-confidence word waiting for confirmation
	stopCh           chan struct{}
	stopped          bool
	linePos          int       // current position on the CEA-608 line
	lastEmitTime     time.Time // when the last word was displayed
	cleared          bool      // true if we already cleared after timeout
	confidenceThresh float32   // default 0.7; words below this held back one tick
	speakerChangeGap int       // default 300; ms gap → speaker change
}

const (
	pacerMaxLineChars   = 32
	pacerWordsPerSecond = 3               // ~180 WPM
	pacerMaxQueueWords  = 50              // cap to prevent unbounded growth
	pacerClearTimeout   = 5 * time.Second // clear display after 5s of no new words
)

func newCaptionPacer(mgr *caption.Manager) *captionPacer {
	p := &captionPacer{
		mgr:              mgr,
		stopCh:           make(chan struct{}),
		confidenceThresh: 0.7,
		speakerChangeGap: 300,
	}
	go p.run()
	return p
}

// SetConfidencePreset sets the confidence holdback threshold from a preset name.
func (p *captionPacer) SetConfidencePreset(preset string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	switch preset {
	case "low":
		p.confidenceThresh = 0.5
	case "medium":
		p.confidenceThresh = 0.7
	case "high":
		p.confidenceThresh = 0.9
	}
}

// ConfidencePreset returns the current confidence threshold as a preset name.
func (p *captionPacer) ConfidencePreset() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.confidenceThresh <= 0.55 {
		return "low"
	}
	if p.confidenceThresh <= 0.75 {
		return "medium"
	}
	return "high"
}

// SetSpeakerDetectionPreset sets the speaker-change gap threshold from a preset name.
func (p *captionPacer) SetSpeakerDetectionPreset(preset string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	switch preset {
	case "sensitive":
		p.speakerChangeGap = 200
	case "normal":
		p.speakerChangeGap = 300
	case "relaxed":
		p.speakerChangeGap = 500
	}
}

// SpeakerDetectionPreset returns the current speaker-change gap as a preset name.
func (p *captionPacer) SpeakerDetectionPreset() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.speakerChangeGap <= 250 {
		return "sensitive"
	}
	if p.speakerChangeGap <= 400 {
		return "normal"
	}
	return "relaxed"
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
	thresh := p.confidenceThresh
	p.mu.Unlock()

	// Confidence holdback: low-confidence words wait one tick.
	if word.confidence > 0 && word.confidence < thresh && !word.isFinal {
		p.mu.Lock()
		p.held = &word
		p.mu.Unlock()
		return
	}

	p.emitWord(word)
}

// pacerOp is a single caption-manager command produced by emitWord's layout
// pass. text == "" means IngestNewline; otherwise IngestText(text).
type pacerOp struct {
	text string
}

// emitWord sends a word to the caption manager with speaker-change
// detection and line wrapping. Updates lastEmitTime for auto-clear.
//
// The layout pass (which reads/mutates linePos) runs under p.mu so it stays
// consistent with concurrent Flush(); the resulting manager commands are then
// replayed AFTER releasing p.mu. This keeps the queue mutex off the
// caption-manager/broadcast-sink path (Manager.IngestText/IngestNewline fan out
// to Relay.BroadcastCaptions), so a slow subscriber can never stall
// EnqueueTranscriptWords or Flush. Lock ordering is unchanged and safe:
// caption.Manager never calls back into captionPacer.
func (p *captionPacer) emitWord(word pacerWord) {
	p.mu.Lock()

	p.lastEmitTime = time.Now()
	p.cleared = false

	var ops []pacerOp

	// Speaker change: segment gap exceeds threshold → new line with >> prefix.
	if word.gapBefore >= p.speakerChangeGap {
		ops = append(ops, pacerOp{}, pacerOp{text: ">> "})
		p.linePos = 3 // ">> " is ASCII, so byte count == display cells.
	}

	// Count display cells, not bytes: CEA-608 maps most accented runes (é, ñ)
	// to a single on-screen cell, so len() over-counts non-English words and
	// would wrap lines prematurely. DisplayWidth mirrors the encoder's output
	// width (e.g. "…" -> "..." = 3 cells).
	wordLen := caption.DisplayWidth(word.text)
	needSpace := p.linePos > 0
	spaceNeeded := wordLen
	if needSpace {
		spaceNeeded++
	}

	// Word-wrap at 32 characters.
	if p.linePos+spaceNeeded > pacerMaxLineChars && p.linePos > 0 {
		ops = append(ops, pacerOp{})
		p.linePos = 0
		needSpace = false
	}

	if needSpace {
		ops = append(ops, pacerOp{text: " "})
		p.linePos++
	}
	ops = append(ops, pacerOp{text: word.text})
	p.linePos += wordLen

	// Final word: insert newline to close the utterance.
	if word.isFinal {
		ops = append(ops, pacerOp{})
		p.linePos = 0
	}

	// Release the queue lock before replaying the layout into the caption
	// manager: IngestText/IngestNewline fan out to Relay.BroadcastCaptions, and
	// holding p.mu across them would let a slow subscriber stall
	// EnqueueTranscriptWords (ASR read loop) and Flush (program-source change).
	p.mu.Unlock()

	// Replay outside the lock: these calls fan out to network broadcast sinks.
	for _, op := range ops {
		if op.text == "" {
			p.mgr.IngestNewline()
		} else {
			p.mgr.IngestText(op.text)
		}
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
