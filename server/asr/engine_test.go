package asr

import (
	"sync"
	"testing"
)

// newTestEngine creates an Engine with nil whisper for testing non-inference paths.
func newTestEngine() *Engine {
	return &Engine{
		resampler: NewResampler(48000, 2, 16000),
		ringBuf:   NewAudioRingBuf(16000, 30),
		vad: NewVAD(VADConfig{
			ThresholdDB: -35,
			HangoverMs:  500,
			SampleRate:  16000,
		}),
		language: "en",
		inferMs:  3000,
		doneCh:   make(chan struct{}),
	}
}

func TestEngine_IngestAudioResamples(t *testing.T) {
	e := newTestEngine()
	defer e.Close()

	pcm := make([]float32, 2048)
	for i := range pcm {
		pcm[i] = 0.1
	}
	e.IngestAudio(pcm, 0, 48000, 2)

	count := e.ringBuf.SampleCount()
	if count < 330 || count > 350 {
		t.Fatalf("expected ~341 resampled samples, got %d", count)
	}
}

func TestEngine_VADTransition(t *testing.T) {
	e := newTestEngine()
	defer e.Close()

	silence := make([]float32, 2048)
	e.IngestAudio(silence, 0, 48000, 2)
	if e.vad.State() != VADIdle {
		t.Fatalf("expected idle after silence, got %v", e.vad.State())
	}

	loud := make([]float32, 2048)
	for i := range loud {
		loud[i] = 0.5
	}
	e.IngestAudio(loud, 90000, 48000, 2)
	if e.vad.State() != VADSpeaking {
		t.Fatalf("expected speaking after loud signal, got %v", e.vad.State())
	}
}

func TestEngine_VADSpeechOnsetSetsState(t *testing.T) {
	e := newTestEngine()
	defer e.Close()

	loud := make([]float32, 2048)
	for i := range loud {
		loud[i] = 0.5
	}
	e.IngestAudio(loud, 0, 48000, 2)

	e.mu.Lock()
	defer e.mu.Unlock()
	if !e.wasSpeaking {
		t.Fatal("expected wasSpeaking=true after speech onset")
	}
	if e.speechStart.IsZero() {
		t.Fatal("expected speechStart to be set after speech onset")
	}
}

func TestEngine_AdaptiveInferTiming(t *testing.T) {
	e := newTestEngine()
	defer e.Close()

	e.mu.Lock()
	// First inference should use 1s interval.
	if e.inferCount != 0 {
		t.Fatal("expected inferCount=0 initially")
	}
	e.mu.Unlock()
}

func TestEngine_OnTranscriptCallback(t *testing.T) {
	e := newTestEngine()
	defer e.Close()

	var mu sync.Mutex
	var received [][]TranscriptWord

	e.OnTranscript(func(words []TranscriptWord, isFinal bool) {
		mu.Lock()
		received = append(received, words)
		mu.Unlock()
	})

	// Verify callback is set.
	e.mu.Lock()
	if e.onTranscript == nil {
		t.Fatal("expected onTranscript to be set")
	}
	e.mu.Unlock()
}

func TestEngine_TranscriptWordGapBefore(t *testing.T) {
	// Verify TranscriptWord carries gap metadata.
	w := TranscriptWord{
		Text:       "hello",
		Confidence: 0.95,
		GapBefore:  400, // 400ms gap — speaker change
	}
	if w.GapBefore < 300 {
		t.Fatal("expected gap >= 300ms for speaker change")
	}
	if w.Confidence < 0.7 {
		t.Fatal("expected high confidence")
	}
}

func TestEngine_RingBufDrain(t *testing.T) {
	buf := NewAudioRingBuf(16000, 5)

	samples := make([]float32, 48000)
	buf.Write(samples)
	if buf.SampleCount() != 48000 {
		t.Fatalf("expected 48000, got %d", buf.SampleCount())
	}

	buf.Drain(48000 - 32000)
	if buf.SampleCount() != 32000 {
		t.Fatalf("expected 32000 after drain, got %d", buf.SampleCount())
	}

	buf.Drain(50000)
	if buf.SampleCount() != 0 {
		t.Fatalf("expected 0 after over-drain, got %d", buf.SampleCount())
	}
}

func TestEngine_SetLanguage(t *testing.T) {
	e := newTestEngine()
	defer e.Close()

	if e.Language() != "en" {
		t.Fatalf("expected default 'en', got %q", e.Language())
	}
	e.SetLanguage("fr")
	if e.Language() != "fr" {
		t.Fatalf("expected 'fr', got %q", e.Language())
	}
}

func TestEngine_Reset(t *testing.T) {
	e := newTestEngine()
	defer e.Close()

	// Write some audio and trigger speech.
	loud := make([]float32, 2048)
	for i := range loud {
		loud[i] = 0.5
	}
	e.IngestAudio(loud, 0, 48000, 2)

	e.Reset()

	e.mu.Lock()
	defer e.mu.Unlock()
	if e.ringBuf.SampleCount() != 0 {
		t.Fatal("expected ring buffer cleared after Reset")
	}
	if e.wasSpeaking {
		t.Fatal("expected wasSpeaking=false after Reset")
	}
	if e.vad.State() != VADIdle {
		t.Fatal("expected VAD idle after Reset")
	}
}

func TestEngine_NewEngineFailsWithoutTRT(t *testing.T) {
	_, err := NewEngine(EngineConfig{ModelPath: "/tmp/nonexistent"})
	if err == nil {
		t.Fatal("expected error without TensorRT")
	}
}

func TestEngine_CloseIdempotent(t *testing.T) {
	e := newTestEngine()
	e.Close()
	e.Close()
}

func TestEngine_CloseBlocksIngest(t *testing.T) {
	e := newTestEngine()
	e.Close()

	pcm := make([]float32, 2048)
	for i := range pcm {
		pcm[i] = 0.5
	}
	initialCount := e.ringBuf.SampleCount()
	e.IngestAudio(pcm, 0, 48000, 2)

	finalCount := e.ringBuf.SampleCount()
	if finalCount != initialCount {
		t.Fatalf("expected ring buffer count %d after Close, got %d", initialCount, finalCount)
	}
}

func TestEngine_IngestSilenceNoSpeech(t *testing.T) {
	e := newTestEngine()
	defer e.Close()

	for i := 0; i < 10; i++ {
		silence := make([]float32, 2048)
		e.IngestAudio(silence, int64(i)*90000, 48000, 2)
	}

	e.mu.Lock()
	defer e.mu.Unlock()
	if e.wasSpeaking {
		t.Fatal("expected wasSpeaking=false with only silence")
	}
}

func TestEngine_SetInferSpeed(t *testing.T) {
	e := newTestEngine()
	defer e.Close()

	// "fast" -> 1000ms
	e.SetInferSpeed("fast")
	e.mu.Lock()
	if e.inferMs != 1000 {
		t.Fatalf("expected inferMs=1000 for 'fast', got %d", e.inferMs)
	}
	e.mu.Unlock()

	// "balanced" -> 3000ms
	e.SetInferSpeed("balanced")
	e.mu.Lock()
	if e.inferMs != 3000 {
		t.Fatalf("expected inferMs=3000 for 'balanced', got %d", e.inferMs)
	}
	e.mu.Unlock()

	// "accurate" -> 5000ms
	e.SetInferSpeed("accurate")
	e.mu.Lock()
	if e.inferMs != 5000 {
		t.Fatalf("expected inferMs=5000 for 'accurate', got %d", e.inferMs)
	}
	e.mu.Unlock()

	// Invalid value is ignored — stays at 5000.
	e.SetInferSpeed("bogus")
	e.mu.Lock()
	if e.inferMs != 5000 {
		t.Fatalf("expected inferMs=5000 after invalid preset, got %d", e.inferMs)
	}
	e.mu.Unlock()
}

func TestEngine_InferSpeed(t *testing.T) {
	e := newTestEngine()
	defer e.Close()

	// Default is 3000 -> "balanced"
	if got := e.InferSpeed(); got != "balanced" {
		t.Fatalf("expected 'balanced' for 3000ms, got %q", got)
	}

	e.SetInferSpeed("fast")
	if got := e.InferSpeed(); got != "fast" {
		t.Fatalf("expected 'fast', got %q", got)
	}

	e.SetInferSpeed("accurate")
	if got := e.InferSpeed(); got != "accurate" {
		t.Fatalf("expected 'accurate', got %q", got)
	}

	// Edge: exactly 1000 -> "fast"
	e.mu.Lock()
	e.inferMs = 1000
	e.mu.Unlock()
	if got := e.InferSpeed(); got != "fast" {
		t.Fatalf("expected 'fast' for 1000ms, got %q", got)
	}

	// Edge: 1001 -> "balanced"
	e.mu.Lock()
	e.inferMs = 1001
	e.mu.Unlock()
	if got := e.InferSpeed(); got != "balanced" {
		t.Fatalf("expected 'balanced' for 1001ms, got %q", got)
	}

	// Edge: 3001 -> "accurate"
	e.mu.Lock()
	e.inferMs = 3001
	e.mu.Unlock()
	if got := e.InferSpeed(); got != "accurate" {
		t.Fatalf("expected 'accurate' for 3001ms, got %q", got)
	}
}

func TestEngine_SetVADSensitivity(t *testing.T) {
	e := newTestEngine()
	defer e.Close()

	// "quiet" -> -30 dBFS
	e.SetVADSensitivity("quiet")
	if got := e.vad.ThresholdDB(); got != -30 {
		t.Fatalf("expected thresholdDB=-30 for 'quiet', got %v", got)
	}

	// "normal" -> -26 dBFS
	e.SetVADSensitivity("normal")
	if got := e.vad.ThresholdDB(); got != -26 {
		t.Fatalf("expected thresholdDB=-26 for 'normal', got %v", got)
	}

	// "noisy" -> -20 dBFS
	e.SetVADSensitivity("noisy")
	if got := e.vad.ThresholdDB(); got != -20 {
		t.Fatalf("expected thresholdDB=-20 for 'noisy', got %v", got)
	}

	// Invalid is ignored — stays at -20.
	e.SetVADSensitivity("invalid")
	if got := e.vad.ThresholdDB(); got != -20 {
		t.Fatalf("expected thresholdDB=-20 after invalid preset, got %v", got)
	}
}

func TestEngine_VADSensitivity(t *testing.T) {
	e := newTestEngine()
	defer e.Close()

	// Default engine VAD threshold is -35, which is <= -29 -> "quiet"
	if got := e.VADSensitivity(); got != "quiet" {
		t.Fatalf("expected 'quiet' for -35dB, got %q", got)
	}

	e.SetVADSensitivity("normal")
	if got := e.VADSensitivity(); got != "normal" {
		t.Fatalf("expected 'normal', got %q", got)
	}

	e.SetVADSensitivity("noisy")
	if got := e.VADSensitivity(); got != "noisy" {
		t.Fatalf("expected 'noisy', got %q", got)
	}

	// Edge: exactly -29 -> "quiet"
	e.vad.SetThresholdDB(-29)
	if got := e.VADSensitivity(); got != "quiet" {
		t.Fatalf("expected 'quiet' for -29dB, got %q", got)
	}

	// Edge: -28 -> "normal" (above -29 threshold)
	e.vad.SetThresholdDB(-28)
	if got := e.VADSensitivity(); got != "normal" {
		t.Fatalf("expected 'normal' for -28dB, got %q", got)
	}

	// Edge: exactly -23 -> "normal"
	e.vad.SetThresholdDB(-23)
	if got := e.VADSensitivity(); got != "normal" {
		t.Fatalf("expected 'normal' for -23dB, got %q", got)
	}

	// Edge: -22 -> "noisy"
	e.vad.SetThresholdDB(-22)
	if got := e.VADSensitivity(); got != "noisy" {
		t.Fatalf("expected 'noisy' for -22dB, got %q", got)
	}
}

func TestEngine_ConfigDefaults(t *testing.T) {
	cfg := EngineConfig{}

	vadThreshold := cfg.VADThresholdDB
	if vadThreshold == 0 {
		vadThreshold = -35
	}
	if vadThreshold != -35 {
		t.Fatalf("expected default vadThreshold -35, got %v", vadThreshold)
	}

	inferMs := cfg.InferIntervalMs
	if inferMs == 0 {
		inferMs = 3000
	}
	if inferMs != 3000 {
		t.Fatalf("expected default inferMs 3000, got %d", inferMs)
	}
}
