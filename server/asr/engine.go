package asr

import (
	"log/slog"
	"sync"
	"time"
)

// EngineConfig configures the ASR engine.
type EngineConfig struct {
	// ModelDir is the directory containing Whisper TensorRT model files
	// (encoder.onnx, decoder.onnx, vocab.json).
	ModelDir string
	// Language is the ISO 639-1 language code (e.g., "en", "fr"). Defaults to "en".
	Language string
	// VADThresholdDB is the RMS threshold in dBFS for voice activity detection.
	// Defaults to -35.
	VADThresholdDB float64
	// InferIntervalMs is the minimum interval between inference runs in milliseconds.
	// Defaults to 3000.
	InferIntervalMs int
	// UseFP16 enables FP16 precision for TensorRT inference.
	UseFP16 bool
}

// Engine is the main ASR orchestrator. It connects resampler, ring buffer,
// VAD, mel spectrogram, TensorRT inference, and tokenizer to produce
// streaming transcription from raw audio input.
//
// Audio flows through: IngestAudio -> resample (48kHz stereo -> 16kHz mono)
// -> ring buffer + VAD -> mel spectrogram -> TRT encode -> TRT decode
// -> tokenize -> confirmed/tentative text diffing -> callback.
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
	mel       *MelSpectrogram
	trt       *WhisperTRT
	tokenizer *Tokenizer

	language     string
	inferMs      int
	onTranscript func(text string, isFinal bool)

	// Inference state
	lastTranscript string
	confirmedText  string
	speechStart    time.Time
	lastInfer      time.Time
	wasSpeaking    bool

	// Lifecycle
	doneCh chan struct{}
	closed bool
}

// NewEngine creates a new ASR engine with the given configuration.
// Returns an error if the TensorRT backend is not available (e.g., on non-CUDA builds).
func NewEngine(cfg EngineConfig) (*Engine, error) {
	trt, err := NewWhisperTRT(WhisperTRTConfig{
		ModelDir: cfg.ModelDir,
		UseFP16:  cfg.UseFP16,
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

	tok := NewTokenizer()
	if cfg.ModelDir != "" {
		if loadErr := tok.LoadVocab(cfg.ModelDir); loadErr != nil {
			slog.Warn("ASR: could not load vocab.json, token decoding will be limited", "error", loadErr)
		}
	}

	return &Engine{
		resampler: NewResampler(48000, 2, 16000),
		ringBuf:   NewAudioRingBuf(16000, 30),
		vad: NewVAD(VADConfig{
			ThresholdDB: vadThreshold,
			HangoverMs:  500,
			SampleRate:  16000,
		}),
		mel:       NewMelSpectrogram(),
		trt:       trt,
		tokenizer: tok,
		language:  lang,
		inferMs:   inferMs,
		doneCh:    make(chan struct{}),
	}, nil
}

// OnTranscript sets the callback for transcript updates. The callback is
// called with isFinal=false for tentative confirmed text deltas and
// isFinal=true for the final transcript when speech ends.
func (e *Engine) OnTranscript(fn func(text string, isFinal bool)) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.onTranscript = fn
}

// IngestAudio is called from the audio sink with raw PCM samples.
// It resamples to 16kHz mono, feeds the VAD and ring buffer, and
// triggers inference when appropriate based on VAD state transitions.
//
// The pts, sampleRate, and channels parameters describe the incoming audio
// format. The resampler handles conversion to 16kHz mono internally.
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
		// Speech onset: reset state for new utterance.
		e.speechStart = now
		e.lastInfer = now
		e.wasSpeaking = true
		e.confirmedText = ""
		e.lastTranscript = ""

	case newState == VADSpeaking || newState == VADTrailing:
		// Ongoing speech: run inference periodically.
		if e.wasSpeaking && now.Sub(e.lastInfer) >= time.Duration(e.inferMs)*time.Millisecond {
			e.lastInfer = now
			go e.runInference(false)
		}

	case prevState == VADTrailing && newState == VADIdle:
		// Speech ended: run final inference.
		if e.wasSpeaking {
			e.wasSpeaking = false
			go e.runInference(true)
		}
	}
}

// runInference takes a snapshot of the ring buffer, computes mel spectrogram,
// runs TRT encode+decode, and processes the resulting transcript.
// It is called from a goroutine to avoid blocking the audio ingest path.
func (e *Engine) runInference(isFinal bool) {
	e.mu.Lock()
	audio := e.ringBuf.Snapshot()
	e.mu.Unlock()

	// Need at least 100ms of audio (1600 samples at 16kHz) for meaningful inference.
	if len(audio) < 1600 {
		return
	}

	melSpec := e.mel.Compute(audio)

	// Flatten [80][3000] mel spectrogram to contiguous [80*3000] for TRT input.
	flat := make([]float32, 80*3000)
	for i := 0; i < 80; i++ {
		copy(flat[i*3000:], melSpec[i])
	}

	encOut, err := e.trt.Encode(flat)
	if err != nil {
		slog.Warn("ASR encoder failed", "error", err)
		return
	}

	tok := e.tokenizer
	initialTokens := []int{
		tok.SOT(),
		tok.LanguageToken(e.language),
		tok.Transcribe(),
		tok.NoTimestamps(),
	}

	outputTokens, err := e.trt.Decode(encOut, initialTokens)
	if err != nil {
		slog.Warn("ASR decoder failed", "error", err)
		return
	}

	text := tok.Decode(outputTokens)
	e.processTranscript(text, isFinal)
}

// processTranscript compares the new transcript with the previous one,
// finds confirmed (stable) text via longest common prefix at word boundaries,
// and emits deltas through the callback.
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

	// Find longest common prefix between previous and current transcript.
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

	// Snap to word boundary so we don't confirm partial words.
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

// Close shuts down the engine and releases TensorRT resources.
// Safe to call multiple times.
func (e *Engine) Close() {
	e.mu.Lock()
	if e.closed {
		e.mu.Unlock()
		return
	}
	e.closed = true
	close(e.doneCh)
	e.mu.Unlock()
	if e.trt != nil {
		e.trt.Close()
	}
}
