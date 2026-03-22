package asr

import (
	"log/slog"
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
	onTranscript func(text string, isFinal bool)

	// Inference state
	lastTranscript string
	confirmedText  string
	speechStart    time.Time
	lastInfer      time.Time
	wasSpeaking    bool
	inferring      sync.Mutex // serializes whisper inference (one at a time)

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
func (e *Engine) OnTranscript(fn func(text string, isFinal bool)) {
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
		e.confirmedText = ""
		e.lastTranscript = ""

	case newState == VADSpeaking || newState == VADTrailing:
		if e.wasSpeaking && now.Sub(e.lastInfer) >= time.Duration(e.inferMs)*time.Millisecond {
			e.lastInfer = now
			go e.runInference(false)
		}

	case prevState == VADTrailing && newState == VADIdle:
		if e.wasSpeaking {
			e.wasSpeaking = false
			go e.runInference(true)
		}
	}
}

// runInference takes a snapshot of the ring buffer and runs whisper.cpp.
func (e *Engine) runInference(isFinal bool) {
	e.inferring.Lock()
	defer e.inferring.Unlock()

	start := time.Now()

	e.mu.Lock()
	audio := e.ringBuf.Snapshot()
	lang := e.language
	e.mu.Unlock()

	if len(audio) < 1600 {
		return
	}

	text, err := e.whisper.Transcribe(audio, lang)
	if err != nil {
		slog.Warn("ASR inference failed", "error", err)
		return
	}

	slog.Info("ASR inference complete",
		"ms", time.Since(start).Milliseconds(),
		"audioSamples", len(audio),
		"textLen", len(text),
		"isFinal", isFinal,
		"text", text,
	)

	e.processTranscript(text, isFinal)
}

// processTranscript compares the new transcript with the previous one,
// finds confirmed (stable) text, and emits deltas through the callback.
func (e *Engine) processTranscript(text string, isFinal bool) {
	e.mu.Lock()
	cb := e.onTranscript

	if isFinal {
		e.confirmedText = ""
		e.lastTranscript = ""
		e.mu.Unlock()
		if cb != nil && text != "" {
			cb(text, true)
		}
		return
	}

	prev := e.lastTranscript
	e.lastTranscript = text

	commonLen := 0
	minLen := len(prev)
	if len(text) < minLen {
		minLen = len(text)
	}
	for i := 0; i < minLen; i++ {
		if prev[i] == text[i] {
			commonLen = i + 1
		} else {
			break
		}
	}
	for commonLen > 0 && commonLen < len(text) && text[commonLen-1] != ' ' {
		commonLen--
	}

	newConfirmed := text[:commonLen]
	if len(newConfirmed) > len(e.confirmedText) {
		delta := newConfirmed[len(e.confirmedText):]
		e.confirmedText = newConfirmed
		e.mu.Unlock()
		if cb != nil && delta != "" {
			cb(delta, false)
		}
		return
	}

	e.mu.Unlock()
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
