package asr

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// Compile-time interface check.
var _ Backend = (*WhisperBackend)(nil)

// newTestWhisperBackend creates a WhisperBackend with nil whisper for testing
// non-inference paths. Mirrors the existing newTestEngine() pattern.
func newTestWhisperBackend() *WhisperBackend {
	return &WhisperBackend{
		resampler: NewResampler(48000, 2, 16000),
		ringBuf:   NewAudioRingBuf(16000, 30),
		vad: NewVAD(VADConfig{
			ThresholdDB: -35,
			HangoverMs:  500,
			SampleRate:  16000,
		}),
		language:  "en",
		translate: true,
		inferMs:   3000,
		modelsDir: "/tmp/test-models",
		modelPath: "/tmp/test-models/ggml-base.bin",
	}
}

func TestWhisperBackend_Name(t *testing.T) {
	b := newTestWhisperBackend()
	if got := b.Name(); got != "whisper" {
		t.Fatalf("expected name 'whisper', got %q", got)
	}
}

func TestWhisperBackend_ImplementsBackend(t *testing.T) {
	// This is a compile-time check (var _ Backend = ...) above,
	// but we also verify at runtime for clarity.
	var b Backend = newTestWhisperBackend()
	if b.Name() != "whisper" {
		t.Fatal("WhisperBackend does not satisfy Backend interface")
	}
}

func TestWhisperBackend_IngestAudioResamples(t *testing.T) {
	b := newTestWhisperBackend()
	defer b.Close()

	// 2048 samples of stereo 48kHz = 1024 frames. After resampling to 16kHz mono:
	// 1024 * (16000/48000) = ~341 samples.
	pcm := make([]float32, 2048)
	for i := range pcm {
		pcm[i] = 0.1
	}
	b.IngestAudio(pcm, 0, 48000, 2)

	b.mu.Lock()
	count := b.ringBuf.SampleCount()
	b.mu.Unlock()

	if count < 330 || count > 350 {
		t.Fatalf("expected ~341 resampled samples, got %d", count)
	}
}

func TestWhisperBackend_IngestAudioSkipsAfterClose(t *testing.T) {
	b := newTestWhisperBackend()
	b.Close()

	pcm := make([]float32, 2048)
	for i := range pcm {
		pcm[i] = 0.5
	}
	b.IngestAudio(pcm, 0, 48000, 2)

	b.mu.Lock()
	count := b.ringBuf.SampleCount()
	b.mu.Unlock()

	if count != 0 {
		t.Fatalf("expected 0 samples after close, got %d", count)
	}
}

func TestWhisperBackend_Reset(t *testing.T) {
	b := newTestWhisperBackend()
	defer b.Close()

	// Write some audio and trigger speech.
	loud := make([]float32, 2048)
	for i := range loud {
		loud[i] = 0.5
	}
	b.IngestAudio(loud, 0, 48000, 2)

	b.Reset()

	b.mu.Lock()
	defer b.mu.Unlock()
	if b.ringBuf.SampleCount() != 0 {
		t.Fatal("expected ring buffer cleared after Reset")
	}
	if b.wasSpeaking {
		t.Fatal("expected wasSpeaking=false after Reset")
	}
	if b.vad.State() != VADIdle {
		t.Fatal("expected VAD idle after Reset")
	}
}

func TestWhisperBackend_Config(t *testing.T) {
	b := newTestWhisperBackend()
	defer b.Close()

	raw := b.Config()

	var cfg WhisperConfig
	if err := json.Unmarshal(raw, &cfg); err != nil {
		t.Fatalf("failed to unmarshal config: %v", err)
	}

	if cfg.Language != "en" {
		t.Fatalf("expected language 'en', got %q", cfg.Language)
	}
	if !cfg.Translate {
		t.Fatal("expected translate=true")
	}
	// Default inferMs=3000 -> "balanced"
	if cfg.Speed != "balanced" {
		t.Fatalf("expected speed 'balanced', got %q", cfg.Speed)
	}
	// Default VAD -35dB -> "quiet"
	if cfg.NoiseFloor != "quiet" {
		t.Fatalf("expected noiseFloor 'quiet', got %q", cfg.NoiseFloor)
	}
}

func TestWhisperBackend_SetConfig(t *testing.T) {
	b := newTestWhisperBackend()
	defer b.Close()

	cfg := WhisperConfig{
		Language:   "fr",
		Translate:  false,
		Speed:      "fast",
		NoiseFloor: "noisy",
	}
	raw, _ := json.Marshal(cfg)
	if err := b.SetConfig(raw); err != nil {
		t.Fatalf("SetConfig failed: %v", err)
	}

	b.mu.Lock()
	defer b.mu.Unlock()
	if b.language != "fr" {
		t.Fatalf("expected language 'fr', got %q", b.language)
	}
	if b.translate {
		t.Fatal("expected translate=false")
	}
	if b.inferMs != 1000 {
		t.Fatalf("expected inferMs=1000 for 'fast', got %d", b.inferMs)
	}
	if b.vad.ThresholdDB() != -20 {
		t.Fatalf("expected VAD -20dB for 'noisy', got %v", b.vad.ThresholdDB())
	}
}

func TestWhisperBackend_SetConfigIgnoresUnknownPresets(t *testing.T) {
	b := newTestWhisperBackend()
	defer b.Close()

	// Set known values first.
	b.mu.Lock()
	b.inferMs = 3000
	b.mu.Unlock()

	cfg := WhisperConfig{
		Speed:      "bogus",
		NoiseFloor: "invalid",
	}
	raw, _ := json.Marshal(cfg)
	if err := b.SetConfig(raw); err != nil {
		t.Fatalf("SetConfig failed: %v", err)
	}

	// Should remain unchanged.
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.inferMs != 3000 {
		t.Fatalf("expected inferMs unchanged at 3000, got %d", b.inferMs)
	}
}

func TestWhisperBackend_SetConfigPartialUpdate(t *testing.T) {
	b := newTestWhisperBackend()
	defer b.Close()

	// Only set language. Speed/NoiseFloor empty strings are ignored.
	// Translate is a bool, so it always applies (false is a valid value).
	cfg := WhisperConfig{
		Language:  "de",
		Translate: true,
	}
	raw, _ := json.Marshal(cfg)
	if err := b.SetConfig(raw); err != nil {
		t.Fatalf("SetConfig failed: %v", err)
	}

	b.mu.Lock()
	defer b.mu.Unlock()
	if b.language != "de" {
		t.Fatalf("expected language 'de', got %q", b.language)
	}
	// Speed/NoiseFloor should remain at defaults since empty presets are ignored.
	if b.inferMs != 3000 {
		t.Fatalf("expected inferMs still 3000, got %d", b.inferMs)
	}
	if !b.translate {
		t.Fatal("expected translate still true")
	}
}

func TestWhisperBackend_StartSetsCallback(t *testing.T) {
	b := newTestWhisperBackend()
	defer b.Close()

	called := false
	err := b.Start(func(words []TranscriptWord, isFinal bool) {
		called = true
	})
	if err != nil {
		t.Fatalf("Start failed: %v", err)
	}

	b.mu.Lock()
	cb := b.onTranscript
	b.mu.Unlock()

	if cb == nil {
		t.Fatal("expected onTranscript to be set after Start")
	}

	// Verify callback works.
	cb([]TranscriptWord{{Text: "test"}}, false)
	if !called {
		t.Fatal("expected callback to be invoked")
	}
}

func TestWhisperBackend_StopAndClose(t *testing.T) {
	b := newTestWhisperBackend()

	err := b.Start(func(words []TranscriptWord, isFinal bool) {})
	if err != nil {
		t.Fatalf("Start failed: %v", err)
	}

	b.Stop()

	// After Stop, onTranscript should be nil.
	b.mu.Lock()
	if b.onTranscript != nil {
		b.mu.Unlock()
		t.Fatal("expected onTranscript nil after Stop")
	}
	b.mu.Unlock()

	// Close should be idempotent.
	b.Close()
	b.Close()
}

func TestWhisperBackend_ModelName(t *testing.T) {
	b := newTestWhisperBackend()
	defer b.Close()

	if got := b.ModelName(); got != "ggml-base.bin" {
		t.Fatalf("expected 'ggml-base.bin', got %q", got)
	}
}

func TestWhisperBackend_Models(t *testing.T) {
	b := newTestWhisperBackend()
	defer b.Close()

	// With a non-existent modelsDir, should return empty.
	models := b.Models()
	if len(models) != 0 {
		t.Fatalf("expected 0 models for non-existent dir, got %d", len(models))
	}
}

func TestWhisperBackend_ModelsWithTempDir(t *testing.T) {
	dir := t.TempDir()

	// Create some model files.
	for _, name := range []string{
		"ggml-base.bin",
		"ggml-small.bin",
		"ggml-medium.en.bin",
		"ggml-silero-v6.2.0.bin", // should be excluded
		"for-tests-tiny.bin",      // should be excluded
		"not-a-model.txt",         // should be excluded
	} {
		touchFile(t, dir, name)
	}

	b := newTestWhisperBackend()
	b.modelsDir = dir
	defer b.Close()

	models := b.Models()
	if len(models) != 3 {
		t.Fatalf("expected 3 models, got %d: %v", len(models), models)
	}

	// Check that the model names are correct (stripped prefix/suffix).
	expected := map[string]bool{"base": true, "small": true, "medium.en": true}
	for _, m := range models {
		if !expected[m] {
			t.Fatalf("unexpected model %q", m)
		}
	}
}

func TestWhisperBackend_SetModelNilWhisper(t *testing.T) {
	b := newTestWhisperBackend()
	defer b.Close()

	// With nil whisper, SetModel should return ErrASRNotAvailable (from stub).
	err := b.SetModel("base")
	if err == nil {
		t.Fatal("expected error from SetModel with nil whisper")
	}
}

func TestWhisperBackend_VADTransition(t *testing.T) {
	b := newTestWhisperBackend()
	defer b.Close()

	// Silence -> idle.
	silence := make([]float32, 2048)
	b.IngestAudio(silence, 0, 48000, 2)
	if b.vad.State() != VADIdle {
		t.Fatalf("expected idle after silence, got %v", b.vad.State())
	}

	// Loud signal -> speaking.
	loud := make([]float32, 2048)
	for i := range loud {
		loud[i] = 0.5
	}
	b.IngestAudio(loud, 90000, 48000, 2)
	if b.vad.State() != VADSpeaking {
		t.Fatalf("expected speaking after loud signal, got %v", b.vad.State())
	}
}

// --- Helper preset mapping tests ---

func TestInferMsToPreset(t *testing.T) {
	tests := []struct {
		ms   int
		want string
	}{
		{500, "fast"},
		{1000, "fast"},
		{1001, "balanced"},
		{3000, "balanced"},
		{3001, "accurate"},
		{5000, "accurate"},
		{10000, "accurate"},
	}
	for _, tt := range tests {
		if got := inferMsToPreset(tt.ms); got != tt.want {
			t.Errorf("inferMsToPreset(%d) = %q, want %q", tt.ms, got, tt.want)
		}
	}
}

func TestPresetToInferMs(t *testing.T) {
	tests := []struct {
		preset string
		want   int
		ok     bool
	}{
		{"fast", 1000, true},
		{"balanced", 3000, true},
		{"accurate", 5000, true},
		{"bogus", 0, false},
	}
	for _, tt := range tests {
		got, ok := presetToInferMs(tt.preset)
		if ok != tt.ok || got != tt.want {
			t.Errorf("presetToInferMs(%q) = (%d, %v), want (%d, %v)", tt.preset, got, ok, tt.want, tt.ok)
		}
	}
}

func TestVadDBToPreset(t *testing.T) {
	tests := []struct {
		db   float64
		want string
	}{
		{-35, "quiet"},
		{-30, "quiet"},
		{-29, "quiet"},
		{-28, "normal"},
		{-26, "normal"},
		{-23, "normal"},
		{-22, "noisy"},
		{-20, "noisy"},
		{-10, "noisy"},
	}
	for _, tt := range tests {
		if got := vadDBToPreset(tt.db); got != tt.want {
			t.Errorf("vadDBToPreset(%v) = %q, want %q", tt.db, got, tt.want)
		}
	}
}

func TestPresetToVADDB(t *testing.T) {
	tests := []struct {
		preset string
		want   float64
		ok     bool
	}{
		{"quiet", -30, true},
		{"normal", -26, true},
		{"noisy", -20, true},
		{"invalid", 0, false},
	}
	for _, tt := range tests {
		got, ok := presetToVADDB(tt.preset)
		if ok != tt.ok || got != tt.want {
			t.Errorf("presetToVADDB(%q) = (%v, %v), want (%v, %v)", tt.preset, got, ok, tt.want, tt.ok)
		}
	}
}

func TestWhisperModelDisplayName(t *testing.T) {
	tests := []struct {
		path string
		want string
	}{
		{"/opt/models/ggml-base.bin", "ggml-base.bin"},
		{"/opt/models/ggml-medium.en.bin", "ggml-medium.en.bin"},
		{"ggml-large.bin", "ggml-large.bin"},
	}
	for _, tt := range tests {
		if got := whisperModelDisplayName(tt.path); got != tt.want {
			t.Errorf("whisperModelDisplayName(%q) = %q, want %q", tt.path, got, tt.want)
		}
	}
}

// touchFile creates an empty file in dir with the given name.
func touchFile(t *testing.T, dir, name string) {
	t.Helper()
	f, err := os.Create(filepath.Join(dir, name))
	if err != nil {
		t.Fatalf("failed to create test file %s: %v", name, err)
	}
	f.Close()
}
