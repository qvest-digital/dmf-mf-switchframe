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
	// VADModelPath is the path to a Silero VAD GGML model for neural speech detection.
	// When set, whisper.cpp uses Silero VAD to filter non-speech audio before
	// transcription — much more accurate than RMS energy thresholds.
	VADModelPath string
	// Language is the ISO 639-1 language code (e.g., "en", "fr"). Defaults to "en".
	Language string
	// VADThresholdDB is the RMS threshold in dBFS for the backup energy-based VAD.
	// Only used when VADModelPath is empty. Defaults to -26.
	VADThresholdDB float64
	// InferIntervalMs is the minimum interval between inference runs in milliseconds.
	// Defaults to 3000.
	InferIntervalMs int
	// UseGPU enables GPU acceleration for whisper.cpp (CUDA on Linux, Metal on macOS).
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
	translate    bool // translate to English (default true)
	inferMs      int
	onTranscript func(words []TranscriptWord, isFinal bool)

	// Inference state
	speechStart   time.Time
	lastInfer     time.Time
	inferCount    int // number of inferences in current speech segment
	wasSpeaking   bool
	lastWordCount int        // words emitted from current speech segment (for dedup)
	inferring     sync.Mutex // serializes whisper inference (one at a time)

	// Lifecycle
	doneCh chan struct{}
	closed bool
}

// NewEngine creates a new ASR engine with the given configuration.
// Returns an error if the whisper.cpp backend is not available.
func NewEngine(cfg EngineConfig) (*Engine, error) {
	w, err := NewWhisperCpp(WhisperCppConfig{
		ModelPath:    cfg.ModelPath,
		VADModelPath: cfg.VADModelPath,
		UseGPU:       cfg.UseGPU,
	})
	if err != nil {
		return nil, err
	}

	vadThreshold := cfg.VADThresholdDB
	if vadThreshold == 0 {
		vadThreshold = -26 // ~-26 dBFS — above typical background music/noise
	}
	inferMs := cfg.InferIntervalMs
	if inferMs == 0 {
		inferMs = 3000
	}
	lang := cfg.Language
	if lang == "" {
		lang = "auto"
	}

	return &Engine{
		resampler: NewResampler(48000, 2, 16000),
		ringBuf:   NewAudioRingBuf(16000, 30),
		vad: NewVAD(VADConfig{
			ThresholdDB: vadThreshold,
			HangoverMs:  500,
			SampleRate:  16000,
		}),
		whisper:   w,
		language:  lang,
		translate: true, // translate to English by default
		inferMs:   inferMs,
		doneCh:    make(chan struct{}),
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
		e.lastWordCount = 0

	case newState == VADSpeaking || newState == VADTrailing:
		// Adaptive inference timing: start fast (1s), ramp to inferMs.
		// First inference at 1s gives fast initial words on screen.
		// Second at 2s balances speed and accuracy. Then steady state.
		var inferInterval time.Duration
		switch e.inferCount {
		case 0:
			inferInterval = 1000 * time.Millisecond
		case 1:
			inferInterval = 2000 * time.Millisecond
		default:
			inferInterval = time.Duration(e.inferMs) * time.Millisecond
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

// runInference takes a snapshot of the ring buffer and runs whisper.cpp.
//
// Non-final: the buffer is NOT drained — Whisper re-transcribes the growing
// speech segment each time. Only NEW words (beyond lastWordCount) are emitted
// to avoid duplicates. This gives Whisper full context for better accuracy.
//
// Final: the buffer is cleared and all remaining words are emitted.
func (e *Engine) runInference(isFinal bool) {
	e.inferring.Lock()
	defer e.inferring.Unlock()

	start := time.Now()

	e.mu.Lock()
	audio := e.ringBuf.Snapshot()
	audioLen := len(audio)
	lang := e.language
	xlate := e.translate
	prevWordCount := e.lastWordCount
	e.mu.Unlock()

	if audioLen < 1600 {
		return
	}

	// Check audio energy — skip inference if mostly silence/noise.
	var sumSq float64
	for _, s := range audio {
		sumSq += float64(s) * float64(s)
	}
	rms := math.Sqrt(sumSq / float64(audioLen))
	if rms < 0.02 {
		return
	}

	words, err := e.whisper.TranscribeWithConfidence(audio, lang, xlate)
	if err != nil {
		slog.Warn("ASR inference failed", "error", err)
		return
	}

	slog.Info("ASR inference complete",
		"ms", time.Since(start).Milliseconds(),
		"audioSamples", audioLen,
		"totalWords", len(words),
		"newWords", max(0, len(words)-prevWordCount),
		"isFinal", isFinal,
	)

	if isFinal {
		// Final: clear buffer, emit any words beyond what we've already sent.
		e.mu.Lock()
		e.ringBuf.Clear()
		e.lastWordCount = 0
		cb := e.onTranscript
		e.mu.Unlock()

		var newWords []TranscriptWord
		if prevWordCount < len(words) {
			newWords = words[prevWordCount:]
		}
		if cb != nil && len(newWords) > 0 {
			cb(newWords, true)
		}
	} else {
		// Non-final: don't drain buffer. Emit only new words.
		e.mu.Lock()
		e.lastWordCount = len(words)
		cb := e.onTranscript
		e.mu.Unlock()

		if len(words) > prevWordCount {
			newWords := words[prevWordCount:]
			if cb != nil && len(newWords) > 0 {
				cb(newWords, false)
			}
		}
	}
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

// SetModel swaps the whisper model at runtime. Blocks until any in-flight
// inference completes, loads the new model, and clears the audio buffer.
func (e *Engine) SetModel(modelPath string) error {
	// Serialize with inference to prevent concurrent model access.
	e.inferring.Lock()
	defer e.inferring.Unlock()

	if err := e.whisper.LoadModel(modelPath); err != nil {
		return err
	}

	e.mu.Lock()
	e.ringBuf.Clear()
	e.vad.Reset()
	e.wasSpeaking = false
	e.mu.Unlock()

	slog.Info("ASR model changed", "model", modelPath)
	return nil
}

// ModelPath returns the path to a model file given a model name.
// Searches the standard whisper.cpp models directory.
func ModelPath(modelsDir, name string) string {
	return modelsDir + "/ggml-" + name + ".bin"
}

// SetInferSpeed sets the inference interval from a named preset.
// Valid presets: "fast" (1000ms), "balanced" (3000ms), "accurate" (5000ms).
// Invalid values are silently ignored.
func (e *Engine) SetInferSpeed(preset string) {
	var ms int
	switch preset {
	case "fast":
		ms = 1000
	case "balanced":
		ms = 3000
	case "accurate":
		ms = 5000
	default:
		return
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	e.inferMs = ms
}

// InferSpeed returns the current inference speed preset name.
// <=1000ms -> "fast", <=3000ms -> "balanced", else -> "accurate".
func (e *Engine) InferSpeed() string {
	e.mu.Lock()
	defer e.mu.Unlock()
	switch {
	case e.inferMs <= 1000:
		return "fast"
	case e.inferMs <= 3000:
		return "balanced"
	default:
		return "accurate"
	}
}

// SetVADSensitivity sets the VAD threshold from a named preset.
// Valid presets: "quiet" (-30 dBFS), "normal" (-26 dBFS), "noisy" (-20 dBFS).
// Invalid values are silently ignored.
func (e *Engine) SetVADSensitivity(preset string) {
	var db float64
	switch preset {
	case "quiet":
		db = -30
	case "normal":
		db = -26
	case "noisy":
		db = -20
	default:
		return
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	e.vad.SetThresholdDB(db)
}

// VADSensitivity returns the current VAD sensitivity preset name.
// <=-29 dBFS -> "quiet", <=-23 dBFS -> "normal", else -> "noisy".
func (e *Engine) VADSensitivity() string {
	e.mu.Lock()
	defer e.mu.Unlock()
	db := e.vad.ThresholdDB()
	switch {
	case db <= -29:
		return "quiet"
	case db <= -23:
		return "normal"
	default:
		return "noisy"
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

// SetTranslate enables or disables translation to English.
func (e *Engine) SetTranslate(on bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.translate = on
}

// Translate returns whether translation to English is enabled.
func (e *Engine) Translate() bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.translate
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
