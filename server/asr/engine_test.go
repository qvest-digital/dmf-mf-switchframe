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
