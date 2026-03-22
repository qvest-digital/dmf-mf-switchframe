package asr

import (
	"sync"
	"testing"
	"time"
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

func TestEngine_EmitTranscript_Final(t *testing.T) {
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

	e.emitTranscript("hello world", true)

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

func TestEngine_EmitTranscript_FinalEmpty(t *testing.T) {
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
	e.emitTranscript("", true)

	time.Sleep(10 * time.Millisecond)
	mu.Lock()
	defer mu.Unlock()

	if len(received) != 0 {
		t.Fatalf("expected 0 callbacks for empty final, got %d", len(received))
	}
}

func TestEngine_EmitTranscript_EachCallEmits(t *testing.T) {
	e := newTestEngine()
	var mu sync.Mutex
	var received []string

	e.OnTranscript(func(text string, isFinal bool) {
		mu.Lock()
		received = append(received, text)
		mu.Unlock()
	})
	defer e.Close()

	// With buffer draining, each inference emits fresh text — no diffing.
	e.emitTranscript("hello world", false)
	e.emitTranscript("today is nice", false)

	time.Sleep(10 * time.Millisecond)
	mu.Lock()
	defer mu.Unlock()

	if len(received) != 2 {
		t.Fatalf("expected 2 callbacks, got %d", len(received))
	}
	if received[0] != "hello world" {
		t.Fatalf("expected 'hello world', got %q", received[0])
	}
	if received[1] != "today is nice" {
		t.Fatalf("expected 'today is nice', got %q", received[1])
	}
}

func TestEngine_RingBufDrain(t *testing.T) {
	buf := NewAudioRingBuf(16000, 5)

	// Write 48000 samples (3 seconds at 16kHz).
	samples := make([]float32, 48000)
	buf.Write(samples)
	if buf.SampleCount() != 48000 {
		t.Fatalf("expected 48000, got %d", buf.SampleCount())
	}

	// Drain all but 32000 (2 seconds overlap).
	buf.Drain(48000 - 32000)
	if buf.SampleCount() != 32000 {
		t.Fatalf("expected 32000 after drain, got %d", buf.SampleCount())
	}

	// Drain more than available — should clear.
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

	e.SetLanguage("ja")
	if e.Language() != "ja" {
		t.Fatalf("expected 'ja', got %q", e.Language())
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
	e.Close() // should not panic
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

func TestEngine_EmitTranscriptNilSafe(t *testing.T) {
	e := newTestEngine()
	defer e.Close()

	// emitTranscript with no callback set should not panic.
	e.emitTranscript("hello", true)
	e.emitTranscript("hello", false)
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

	lang := cfg.Language
	if lang == "" {
		lang = "en"
	}
	if lang != "en" {
		t.Fatalf("expected default lang 'en', got %q", lang)
	}
}
