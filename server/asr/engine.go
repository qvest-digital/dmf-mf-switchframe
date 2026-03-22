package asr

import (
	"log/slog"
	"math"
	"sync"
	"time"
)

// EngineConfig configures the ASR engine.
type EngineConfig struct {
	// ModelPath is the path to a whisper.cpp ggml model file (e.g., ggml-base.bin).
	ModelPath string
	// Language is the ISO 639-1 language code (e.g., "en", "fr"). Defaults to "en".
	Language string
	// VADThresholdDB is the RMS threshold in dBFS for voice activity detection.
	// Defaults to -35.
	VADThresholdDB float64
	// InferIntervalMs is the minimum interval between inference runs in milliseconds.
	// Defaults to 3000.
	InferIntervalMs int
	// UseGPU enables CUDA GPU acceleration for whisper.cpp.
	UseGPU bool
}

// Engine is the main ASR orchestrator. It connects resampler, ring buffer,
// VAD, and whisper.cpp inference to produce streaming transcription from
// raw audio input.
//
// Audio flows through: IngestAudio -> resample (48kHz stereo -> 16kHz mono)
// -> ring buffer + VAD -> whisper.cpp inference -> text -> callback.
//
// Inference is triggered by VAD state transitions:
//   - Idle->Speaking: start of speech, reset state
//   - Speaking/Trailing: periodic inference at InferIntervalMs
//   - Trailing->Idle: final inference with isFinal=true
type Engine struct {
	mu sync.Mutex

	resampler *Resampler
	ringBuf   *AudioRingBuf
	vad       *VAD
	whisper   *WhisperCpp

	language     string
	inferMs      int
	onTranscript func(words []TranscriptWord, isFinal bool)

	// Inference state
	speechStart time.Time
	lastInfer   time.Time
	inferCount  int  // number of inferences in current speech segment
	wasSpeaking bool
	inferring   sync.Mutex // serializes whisper inference (one at a time)

	// Lifecycle
	doneCh chan struct{}
	closed bool
}

// NewEngine creates a new ASR engine with the given configuration.
// Returns an error if the whisper.cpp backend is not available.
func NewEngine(cfg EngineConfig) (*Engine, error) {
	w, err := NewWhisperCpp(WhisperCppConfig{
		ModelPath: cfg.ModelPath,
		UseGPU:    cfg.UseGPU,
	})
	if err != nil {
		return nil, err
	}

	vadThreshold := cfg.VADThresholdDB
	if vadThreshold == 0 {
		vadThreshold = -35
	}
	inferMs := cfg.InferIntervalMs
	if inferMs == 0 {
		inferMs = 3000
	}
	lang := cfg.Language
	if lang == "" {
		lang = "en"
	}

	return &Engine{
		resampler: NewResampler(48000, 2, 16000),
		ringBuf:   NewAudioRingBuf(16000, 30),
		vad: NewVAD(VADConfig{
			ThresholdDB: vadThreshold,
			HangoverMs:  500,
			SampleRate:  16000,
		}),
		whisper:  w,
		language: lang,
		inferMs:  inferMs,
		doneCh:   make(chan struct{}),
	}, nil
}

// OnTranscript sets the callback for transcript updates.
// words carry per-word confidence scores and inter-segment gap timing
// from Whisper (GapBefore > 300ms indicates likely speaker change).
func (e *Engine) OnTranscript(fn func(words []TranscriptWord, isFinal bool)) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.onTranscript = fn
}

// IngestAudio is called from the audio sink with raw PCM samples.
func (e *Engine) IngestAudio(pcm []float32, pts int64, sampleRate, channels int) {
	mono16k := e.resampler.Process(pcm)
	if len(mono16k) == 0 {
		return
	}

	e.mu.Lock()
	defer e.mu.Unlock()

	if e.closed {
		return
	}

	e.ringBuf.Write(mono16k)

	prevState := e.vad.State()
	e.vad.Process(mono16k)
	newState := e.vad.State()

	now := time.Now()

	switch {
	case prevState == VADIdle && newState == VADSpeaking:
		e.speechStart = now
		e.lastInfer = now
		e.wasSpeaking = true
		e.inferCount = 0

	case newState == VADSpeaking || newState == VADTrailing:
		// Adaptive inference timing: start fast (1s), ramp to inferMs.
		// First inference at 1s gives fast initial words on screen.
		// Second at 2s balances speed and accuracy. Then steady state.
		inferInterval := time.Duration(e.inferMs) * time.Millisecond
		if e.inferCount == 0 {
			inferInterval = 1000 * time.Millisecond
		} else if e.inferCount == 1 {
			inferInterval = 2000 * time.Millisecond
		}

		if e.wasSpeaking && now.Sub(e.lastInfer) >= inferInterval {
			e.lastInfer = now
			e.inferCount++
			go e.runInference(false)
		}

	case prevState == VADTrailing && newState == VADIdle:
		if e.wasSpeaking {
			e.wasSpeaking = false
			go e.runInference(true)
		}
	}
}

// contextOverlapSamples: zero overlap — each inference gets only fresh
// audio. Overlap causes Whisper to re-transcribe the tail of the previous
// segment, producing duplicate words. Whisper handles cold-start well
// enough that context overlap isn't worth the repetition cost.
const contextOverlapSamples = 0

// runInference takes a snapshot of the ring buffer and runs whisper.cpp.
// After successful transcription, drains the consumed audio from the ring
// buffer (keeping a small overlap for context) so the next inference only
// processes new audio.
func (e *Engine) runInference(isFinal bool) {
	e.inferring.Lock()
	defer e.inferring.Unlock()

	start := time.Now()

	e.mu.Lock()
	audio := e.ringBuf.Snapshot()
	audioLen := len(audio)
	lang := e.language
	e.mu.Unlock()

	if audioLen < 1600 {
		return
	}

	// Check audio energy — skip inference if the audio is mostly silence.
	// Whisper hallucinates plausible text ("just admit that you're rope")
	// when fed low-energy audio that slipped past VAD.
	var sumSq float64
	for _, s := range audio {
		sumSq += float64(s) * float64(s)
	}
	rms := math.Sqrt(sumSq / float64(audioLen))
	if rms < 0.005 { // ~-46 dBFS — well below any real speech
		return
	}

	words, err := e.whisper.TranscribeWithConfidence(audio, lang)
	if err != nil {
		slog.Warn("ASR inference failed", "error", err)
		return
	}

	slog.Info("ASR inference complete",
		"ms", time.Since(start).Milliseconds(),
		"audioSamples", audioLen,
		"words", len(words),
		"isFinal", isFinal,
	)

	// Drain all transcribed audio from the ring buffer so the next
	// inference only sees fresh audio. No overlap — Whisper handles
	// cold-start well and overlap causes word repetition.
	e.mu.Lock()
	e.ringBuf.Clear()
	e.mu.Unlock()

	e.mu.Lock()
	cb := e.onTranscript
	e.mu.Unlock()

	if cb != nil && len(words) > 0 {
		cb(words, isFinal)
	}
}

// SetLanguage changes the target language for transcription.
func (e *Engine) SetLanguage(lang string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.language = lang
}

// Language returns the current target language.
func (e *Engine) Language() string {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.language
}

// Reset clears the audio buffer and resets VAD state. Called on program
// source changes to prevent stale audio from the old source being transcribed.
func (e *Engine) Reset() {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.ringBuf.Clear()
	e.vad.Reset()
	e.wasSpeaking = false
}

// Close shuts down the engine and releases resources.
func (e *Engine) Close() {
	e.mu.Lock()
	if e.closed {
		e.mu.Unlock()
		return
	}
	e.closed = true
	close(e.doneCh)
	e.mu.Unlock()
	if e.whisper != nil {
		e.whisper.Close()
	}
}
