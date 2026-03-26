package asr

import (
	"encoding/json"
	"log/slog"
	"math"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// WhisperConfig is the JSON-serializable configuration for WhisperBackend.
type WhisperConfig struct {
	Language   string `json:"language,omitempty"`
	Translate  bool   `json:"translate"`
	Speed      string `json:"speed,omitempty"`      // "fast", "balanced", "accurate"
	NoiseFloor string `json:"noiseFloor,omitempty"` // "quiet", "normal", "noisy"
}

// WhisperBackend implements Backend using whisper.cpp for speech recognition.
// It owns the full audio pipeline: resampler, ring buffer, VAD, and inference
// scheduling with grow-and-deduplicate semantics.
type WhisperBackend struct {
	mu sync.Mutex

	resampler *Resampler
	ringBuf   *AudioRingBuf
	vad       *VAD
	whisper   *WhisperCpp

	language  string
	translate bool
	inferMs   int
	modelPath string // current model path
	modelsDir string // directory containing ggml-*.bin files

	onTranscript func(words []TranscriptWord, isFinal bool)

	// Inference state
	speechStart   time.Time
	lastInfer     time.Time
	inferCount    int
	wasSpeaking   bool
	lastWordCount int
	inferring     sync.Mutex // serializes whisper inference

	// Lifecycle
	closed bool
}

// WhisperBackendConfig configures the WhisperBackend.
type WhisperBackendConfig struct {
	ModelPath      string
	VADModelPath   string
	Language       string
	Translate      bool // translate to English (caller sets default)
	VADThresholdDB float64
	InferMs        int
	UseGPU         bool
	ModelsDir      string // directory to scan for models; defaults to dir of ModelPath
}

// NewWhisperBackend creates a new WhisperBackend. Returns an error if
// whisper.cpp is not available (e.g., non-cgo build) or the model fails to load.
func NewWhisperBackend(cfg WhisperBackendConfig) (*WhisperBackend, error) {
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
		vadThreshold = -26
	}
	inferMs := cfg.InferMs
	if inferMs == 0 {
		inferMs = 3000
	}
	lang := cfg.Language
	if lang == "" {
		lang = "auto"
	}
	modelsDir := cfg.ModelsDir
	if modelsDir == "" {
		modelsDir = filepath.Dir(cfg.ModelPath)
	}

	return &WhisperBackend{
		resampler: NewResampler(48000, 2, 16000),
		ringBuf:   NewAudioRingBuf(16000, 30),
		vad: NewVAD(VADConfig{
			ThresholdDB: vadThreshold,
			HangoverMs:  500,
			SampleRate:  16000,
		}),
		whisper:   w,
		language:  lang,
		translate: cfg.Translate,
		inferMs:   inferMs,
		modelPath: cfg.ModelPath,
		modelsDir: modelsDir,
	}, nil
}

// Name returns "whisper".
func (b *WhisperBackend) Name() string { return "whisper" }

// Start begins the inference loop. The backend calls onTranscript with
// recognized words. Audio flows in via IngestAudio after Start.
func (b *WhisperBackend) Start(onTranscript func(words []TranscriptWord, isFinal bool)) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.onTranscript = onTranscript
	return nil
}

// IngestAudio feeds raw PCM to the backend. Handles resampling (48kHz stereo
// to 16kHz mono), ring buffer write, and VAD-driven inference scheduling.
func (b *WhisperBackend) IngestAudio(pcm []float32, pts int64, sampleRate, channels int) {
	mono16k := b.resampler.Process(pcm)
	if len(mono16k) == 0 {
		return
	}

	b.mu.Lock()
	defer b.mu.Unlock()

	if b.closed {
		return
	}

	b.ringBuf.Write(mono16k)

	prevState := b.vad.State()
	b.vad.Process(mono16k)
	newState := b.vad.State()

	now := time.Now()

	switch {
	case prevState == VADIdle && newState == VADSpeaking:
		b.speechStart = now
		b.lastInfer = now
		b.wasSpeaking = true
		b.inferCount = 0
		b.lastWordCount = 0

	case newState == VADSpeaking || newState == VADTrailing:
		// Adaptive inference timing: start fast (1s), ramp to inferMs.
		var inferInterval time.Duration
		switch b.inferCount {
		case 0:
			inferInterval = 1000 * time.Millisecond
		case 1:
			inferInterval = 2000 * time.Millisecond
		default:
			inferInterval = time.Duration(b.inferMs) * time.Millisecond
		}

		if b.wasSpeaking && now.Sub(b.lastInfer) >= inferInterval {
			b.lastInfer = now
			b.inferCount++
			go b.runInference(false)
		}

	case prevState == VADTrailing && newState == VADIdle:
		if b.wasSpeaking {
			b.wasSpeaking = false
			go b.runInference(true)
		}
	}
}

// runInference takes a snapshot of the ring buffer and runs whisper.cpp.
//
// Non-final: the buffer is NOT drained. Whisper re-transcribes the growing
// speech segment each time. Only NEW words (beyond lastWordCount) are emitted.
//
// Final: the buffer is cleared and all remaining words are emitted.
func (b *WhisperBackend) runInference(isFinal bool) {
	b.inferring.Lock()
	defer b.inferring.Unlock()

	start := time.Now()

	b.mu.Lock()
	audio := b.ringBuf.Snapshot()
	audioLen := len(audio)
	lang := b.language
	xlate := b.translate
	prevWordCount := b.lastWordCount
	b.mu.Unlock()

	if audioLen < 1600 {
		return
	}

	// Check audio energy -- skip inference if mostly silence/noise.
	var sumSq float64
	for _, s := range audio {
		sumSq += float64(s) * float64(s)
	}
	rms := math.Sqrt(sumSq / float64(audioLen))
	if rms < 0.02 {
		return
	}

	words, err := b.whisper.TranscribeWithConfidence(audio, lang, xlate)
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
		b.mu.Lock()
		b.ringBuf.Clear()
		b.lastWordCount = 0
		cb := b.onTranscript
		b.mu.Unlock()

		var newWords []TranscriptWord
		if prevWordCount < len(words) {
			newWords = words[prevWordCount:]
		}
		if cb != nil && len(newWords) > 0 {
			cb(newWords, true)
		}
	} else {
		b.mu.Lock()
		b.lastWordCount = len(words)
		cb := b.onTranscript
		b.mu.Unlock()

		if len(words) > prevWordCount {
			newWords := words[prevWordCount:]
			if cb != nil && len(newWords) > 0 {
				cb(newWords, false)
			}
		}
	}
}

// Reset clears the audio buffer, VAD, and inference state.
func (b *WhisperBackend) Reset() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.ringBuf.Clear()
	b.vad.Reset()
	b.wasSpeaking = false
}

// Stop halts the inference loop but keeps the model loaded.
func (b *WhisperBackend) Stop() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.onTranscript = nil
}

// Close releases all resources.
func (b *WhisperBackend) Close() {
	b.mu.Lock()
	if b.closed {
		b.mu.Unlock()
		return
	}
	b.closed = true
	b.mu.Unlock()

	if b.whisper != nil {
		b.whisper.Close()
	}
}

// Models lists available whisper models in the configured models directory.
func (b *WhisperBackend) Models() []string {
	return whisperModelsInDir(b.modelsDir)
}

// SetModel swaps the whisper model at runtime. Blocks until any in-flight
// inference completes, loads the new model, and clears the audio buffer.
func (b *WhisperBackend) SetModel(name string) error {
	b.inferring.Lock()
	defer b.inferring.Unlock()

	path := filepath.Join(b.modelsDir, "ggml-"+name+".bin")
	if err := b.whisper.LoadModel(path); err != nil {
		return err
	}

	b.mu.Lock()
	b.modelPath = path
	b.ringBuf.Clear()
	b.vad.Reset()
	b.wasSpeaking = false
	b.mu.Unlock()

	slog.Info("ASR model changed", "model", path)
	return nil
}

// ModelName returns the display name of the currently loaded model.
func (b *WhisperBackend) ModelName() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return whisperModelDisplayName(b.modelPath)
}

// Config returns the backend configuration as JSON.
func (b *WhisperBackend) Config() json.RawMessage {
	b.mu.Lock()
	defer b.mu.Unlock()

	cfg := WhisperConfig{
		Language:   b.language,
		Translate:  b.translate,
		Speed:      inferMsToPreset(b.inferMs),
		NoiseFloor: vadDBToPreset(b.vad.ThresholdDB()),
	}
	raw, _ := json.Marshal(cfg)
	return raw
}

// SetConfig applies backend configuration from JSON.
func (b *WhisperBackend) SetConfig(raw json.RawMessage) error {
	var cfg WhisperConfig
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return err
	}

	b.mu.Lock()
	defer b.mu.Unlock()

	if cfg.Language != "" {
		b.language = cfg.Language
	}
	// Translate is a bool -- always apply if present in JSON.
	// We detect "was it in the JSON" by checking if Language was set,
	// which indicates a real config update vs zero-value.
	// Actually, just always set it -- the WhisperConfig struct has json tag
	// without omitempty for Translate, so it will always be present.
	b.translate = cfg.Translate

	if ms, ok := presetToInferMs(cfg.Speed); ok {
		b.inferMs = ms
	}
	if db, ok := presetToVADDB(cfg.NoiseFloor); ok {
		b.vad.SetThresholdDB(db)
	}

	return nil
}

// --- Helper functions ---

// inferMsToPreset maps inference interval milliseconds to a named preset.
func inferMsToPreset(ms int) string {
	switch {
	case ms <= 1000:
		return "fast"
	case ms <= 3000:
		return "balanced"
	default:
		return "accurate"
	}
}

// presetToInferMs maps a named preset to inference interval milliseconds.
// Returns (0, false) for unknown presets.
func presetToInferMs(preset string) (int, bool) {
	switch preset {
	case "fast":
		return 1000, true
	case "balanced":
		return 3000, true
	case "accurate":
		return 5000, true
	default:
		return 0, false
	}
}

// vadDBToPreset maps a VAD threshold in dBFS to a named preset.
func vadDBToPreset(db float64) string {
	switch {
	case db <= -29:
		return "quiet"
	case db <= -23:
		return "normal"
	default:
		return "noisy"
	}
}

// presetToVADDB maps a named preset to a VAD threshold in dBFS.
// Returns (0, false) for unknown presets.
func presetToVADDB(preset string) (float64, bool) {
	switch preset {
	case "quiet":
		return -30, true
	case "normal":
		return -26, true
	case "noisy":
		return -20, true
	default:
		return 0, false
	}
}

// whisperModelsInDir lists whisper model names found in the given directory.
// Scans for ggml-*.bin files, excluding silero VAD models and test fixtures.
// Returns display names like "base", "medium.en", etc.
func whisperModelsInDir(dir string) []string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var models []string
	for _, e := range entries {
		name := e.Name()
		if strings.HasPrefix(name, "ggml-") && strings.HasSuffix(name, ".bin") &&
			!strings.HasPrefix(name, "for-tests-") &&
			!strings.Contains(name, "silero") {
			model := strings.TrimPrefix(name, "ggml-")
			model = strings.TrimSuffix(model, ".bin")
			models = append(models, model)
		}
	}
	return models
}

// whisperModelDisplayName returns the filename portion of a model path.
func whisperModelDisplayName(path string) string {
	return filepath.Base(path)
}
