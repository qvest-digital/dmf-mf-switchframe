package asr

import (
	"math"
	"sync"
	"testing"
)

// newTestWhisperEngine creates an Engine backed by a test WhisperBackend
// (nil whisper, for testing non-inference paths).
func newTestWhisperEngine() *Engine {
	b := newTestWhisperBackend()
	return newEngineWithBackends(map[string]Backend{
		"whisper": b,
	}, "whisper")
}

// testWhisperBackend returns the WhisperBackend from a test engine.
func testWhisperBackend(e *Engine) *WhisperBackend {
	e.mu.Lock()
	defer e.mu.Unlock()
	b := e.backends["whisper"]
	return b.(*WhisperBackend)
}

func TestIntegration_FullPipeline(t *testing.T) {
	e := newTestWhisperEngine()
	defer e.Close()

	sampleRate := 48000
	duration := 5 * sampleRate
	chunkSize := 1024

	for i := 0; i < duration; i += chunkSize {
		chunk := make([]float32, chunkSize*2)
		for j := 0; j < chunkSize && i+j < duration; j++ {
			val := float32(0.3 * math.Sin(2*math.Pi*440*float64(i+j)/float64(sampleRate)))
			chunk[j*2] = val
			chunk[j*2+1] = val
		}
		pts := int64(i) * 90000 / int64(sampleRate)
		e.IngestAudio(chunk, pts, sampleRate, 2)
	}

	wb := testWhisperBackend(e)
	if wb.vad.State() != VADSpeaking {
		t.Fatalf("expected speaking after 5s of tone, got %v", wb.vad.State())
	}

	expected := 5 * 16000
	wb.mu.Lock()
	actual := wb.ringBuf.SampleCount()
	wb.mu.Unlock()
	tolerance := 500
	if actual < expected-tolerance || actual > expected+tolerance {
		t.Fatalf("expected ~%d samples in ring buffer, got %d", expected, actual)
	}
}

func TestIntegration_SilenceSuppression(t *testing.T) {
	e := newTestWhisperEngine()
	defer e.Close()

	sampleRate := 48000
	duration := 3 * sampleRate
	chunkSize := 1024

	for i := 0; i < duration; i += chunkSize {
		silence := make([]float32, chunkSize*2)
		pts := int64(i) * 90000 / int64(sampleRate)
		e.IngestAudio(silence, pts, sampleRate, 2)
	}

	wb := testWhisperBackend(e)
	if wb.vad.State() != VADIdle {
		t.Fatalf("expected idle after silence, got %v", wb.vad.State())
	}

	wb.mu.Lock()
	ws := wb.wasSpeaking
	wb.mu.Unlock()
	if ws {
		t.Fatal("wasSpeaking should be false after only silence")
	}
}

func TestIntegration_SpeechThenSilence(t *testing.T) {
	e := newTestWhisperEngine()
	defer e.Close()

	sampleRate := 48000
	chunkSize := 1024

	for i := 0; i < 2*sampleRate; i += chunkSize {
		chunk := make([]float32, chunkSize*2)
		for j := 0; j < chunkSize; j++ {
			val := float32(0.4 * math.Sin(2*math.Pi*440*float64(i+j)/float64(sampleRate)))
			chunk[j*2] = val
			chunk[j*2+1] = val
		}
		e.IngestAudio(chunk, 0, sampleRate, 2)
	}

	wb := testWhisperBackend(e)
	if wb.vad.State() != VADSpeaking {
		t.Fatalf("expected speaking after tone, got %v", wb.vad.State())
	}

	for i := 0; i < sampleRate; i += chunkSize {
		silence := make([]float32, chunkSize*2)
		e.IngestAudio(silence, 0, sampleRate, 2)
	}

	if wb.vad.State() != VADIdle {
		t.Fatalf("expected idle after 1s silence (500ms hangover), got %v", wb.vad.State())
	}
}

func TestIntegration_TranscriptCallback(t *testing.T) {
	var mu sync.Mutex
	var results []struct {
		words   []TranscriptWord
		isFinal bool
	}

	e := newTestWhisperEngine()
	e.OnTranscript(func(words []TranscriptWord, isFinal bool) {
		mu.Lock()
		results = append(results, struct {
			words   []TranscriptWord
			isFinal bool
		}{words, isFinal})
		mu.Unlock()
	})
	defer e.Close()

	// Get the backend's callback (set by OnTranscript -> Start).
	wb := testWhisperBackend(e)
	wb.mu.Lock()
	cb := wb.onTranscript
	wb.mu.Unlock()

	cb([]TranscriptWord{{Text: "hello", Confidence: 0.9}}, false)
	cb([]TranscriptWord{{Text: "world", Confidence: 0.95}}, false)
	cb([]TranscriptWord{{Text: "today", Confidence: 0.85}}, true)

	mu.Lock()
	defer mu.Unlock()

	if len(results) != 3 {
		t.Fatalf("expected 3 callbacks, got %d", len(results))
	}

	last := results[len(results)-1]
	if !last.isFinal {
		t.Fatal("last callback should be isFinal")
	}
	if last.words[0].Text != "today" {
		t.Fatalf("expected 'today', got %q", last.words[0].Text)
	}
}
