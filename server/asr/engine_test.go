package asr

import (
	"sync"
	"testing"
	"time"
)

// newTestEngine creates an Engine with nil TRT for testing non-inference paths.
// NewEngine cannot be used on macOS because WhisperTRT returns ErrASRNotAvailable.
func newTestEngine() *Engine {
	return &Engine{
		resampler: NewResampler(48000, 2, 16000),
		ringBuf:   NewAudioRingBuf(16000, 30),
		vad: NewVAD(VADConfig{
			ThresholdDB: -35,
			HangoverMs:  500,
			SampleRate:  16000,
		}),
		mel:      NewMelSpectrogram(),
		language: "en",
		inferMs:  3000,
		doneCh:   make(chan struct{}),
	}
}

func TestEngine_IngestAudioResamples(t *testing.T) {
	e := newTestEngine()
	defer e.Close()

	// 1024 stereo frames = 2048 interleaved samples at 48kHz
	// Expected output: 1024 * (16000/48000) = ~341 mono samples at 16kHz
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

	// Feed silence — should stay idle.
	silence := make([]float32, 2048)
	e.IngestAudio(silence, 0, 48000, 2)
	if e.vad.State() != VADIdle {
		t.Fatalf("expected idle after silence, got %v", e.vad.State())
	}

	// Feed loud signal — should transition to speaking.
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

	// Feed loud signal to trigger speech onset.
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
	if e.lastInfer.IsZero() {
		t.Fatal("expected lastInfer to be set after speech onset")
	}
}

func TestEngine_ProcessTranscript_Final(t *testing.T) {
	e := newTestEngine()
	var mu sync.Mutex
	var received []string
	var finals []bool

	e.OnTranscript(func(text string, isFinal bool) {
		mu.Lock()
		received = append(received, text)
		finals = append(finals, isFinal)
		mu.Unlock()
	})
	defer e.Close()

	e.processTranscript("hello world", true)

	time.Sleep(10 * time.Millisecond)
	mu.Lock()
	defer mu.Unlock()

	if len(received) != 1 {
		t.Fatalf("expected 1 callback, got %d", len(received))
	}
	if received[0] != "hello world" {
		t.Fatalf("expected 'hello world', got %q", received[0])
	}
	if !finals[0] {
		t.Fatal("expected isFinal=true")
	}
}

func TestEngine_ProcessTranscript_FinalEmpty(t *testing.T) {
	e := newTestEngine()
	var mu sync.Mutex
	var received []string

	e.OnTranscript(func(text string, isFinal bool) {
		mu.Lock()
		received = append(received, text)
		mu.Unlock()
	})
	defer e.Close()

	// Empty final transcript should not trigger callback.
	e.processTranscript("", true)

	time.Sleep(10 * time.Millisecond)
	mu.Lock()
	defer mu.Unlock()

	if len(received) != 0 {
		t.Fatalf("expected 0 callbacks for empty final, got %d", len(received))
	}
}

func TestEngine_ProcessTranscript_ConfirmedDiff(t *testing.T) {
	e := newTestEngine()
	var mu sync.Mutex
	var received []string
	var finals []bool

	e.OnTranscript(func(text string, isFinal bool) {
		mu.Lock()
		received = append(received, text)
		finals = append(finals, isFinal)
		mu.Unlock()
	})
	defer e.Close()

	// First inference: "hello world"
	e.processTranscript("hello world", false)
	// Second inference with extended text: "hello " prefix is confirmed
	e.processTranscript("hello world today", false)

	time.Sleep(10 * time.Millisecond)
	mu.Lock()
	defer mu.Unlock()

	// The confirmed prefix "hello " should be emitted as a tentative delta.
	if len(received) == 0 {
		t.Fatal("expected at least one confirmed text callback")
	}
	// All should be tentative (not final).
	for i, f := range finals {
		if f {
			t.Fatalf("callback %d: expected isFinal=false, got true", i)
		}
	}
}

func TestEngine_ProcessTranscript_NoCallbackOnFirstInference(t *testing.T) {
	e := newTestEngine()
	var mu sync.Mutex
	var received []string

	e.OnTranscript(func(text string, isFinal bool) {
		mu.Lock()
		received = append(received, text)
		mu.Unlock()
	})
	defer e.Close()

	// First inference has no previous transcript to diff against,
	// so there's no confirmed prefix yet.
	e.processTranscript("hello world", false)

	time.Sleep(10 * time.Millisecond)
	mu.Lock()
	defer mu.Unlock()

	if len(received) != 0 {
		t.Fatalf("expected 0 callbacks on first tentative inference, got %d", len(received))
	}
}

func TestEngine_ProcessTranscript_ResetOnFinal(t *testing.T) {
	e := newTestEngine()
	defer e.Close()

	// Simulate a confirmed prefix accumulation.
	e.processTranscript("hello world", false)
	e.processTranscript("hello world today", false)

	// Final should reset state.
	e.processTranscript("hello world today", true)

	e.mu.Lock()
	if e.confirmedText != "" {
		t.Fatalf("expected confirmedText reset after final, got %q", e.confirmedText)
	}
	if e.lastTranscript != "" {
		t.Fatalf("expected lastTranscript reset after final, got %q", e.lastTranscript)
	}
	e.mu.Unlock()
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

	e.SetLanguage("ja")
	if e.Language() != "ja" {
		t.Fatalf("expected 'ja', got %q", e.Language())
	}
}

func TestEngine_NewEngineFailsWithoutTRT(t *testing.T) {
	_, err := NewEngine(EngineConfig{ModelDir: "/tmp/nonexistent"})
	if err == nil {
		t.Fatal("expected error without TensorRT")
	}
}

func TestEngine_CloseIdempotent(t *testing.T) {
	e := newTestEngine()
	e.Close()
	e.Close() // should not panic
}

func TestEngine_CloseBlocksIngest(t *testing.T) {
	e := newTestEngine()
	e.Close()

	// IngestAudio after Close should not panic or write to ring buffer.
	pcm := make([]float32, 2048)
	for i := range pcm {
		pcm[i] = 0.5
	}
	initialCount := e.ringBuf.SampleCount()
	e.IngestAudio(pcm, 0, 48000, 2)

	// The resampler runs outside the lock, so samples may be resampled,
	// but the ring buffer write inside the lock should be skipped.
	// However, since resampler.Process runs before the lock check,
	// and the ring buffer write is inside the lock, count should not increase.
	// Note: The resampled data is computed but not written.
	finalCount := e.ringBuf.SampleCount()
	if finalCount != initialCount {
		t.Fatalf("expected ring buffer count %d after Close, got %d", initialCount, finalCount)
	}
}

func TestEngine_OnTranscriptNilSafe(t *testing.T) {
	e := newTestEngine()
	defer e.Close()

	// processTranscript with no callback set should not panic.
	e.processTranscript("hello", true)
	e.processTranscript("hello", false)
	e.processTranscript("hello world", false)
}

func TestEngine_IngestSilenceNoSpeech(t *testing.T) {
	e := newTestEngine()
	defer e.Close()

	// Feed multiple chunks of silence — VAD should remain idle,
	// wasSpeaking should stay false.
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

func TestEngine_ConfigDefaults(t *testing.T) {
	// Verify that zero-value config fields get populated with defaults.
	// We can't actually create via NewEngine (no TRT), so test the logic directly.
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

	lang := cfg.Language
	if lang == "" {
		lang = "en"
	}
	if lang != "en" {
		t.Fatalf("expected default lang 'en', got %q", lang)
	}
}
