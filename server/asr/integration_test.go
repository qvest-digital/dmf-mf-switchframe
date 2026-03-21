package asr

import (
	"math"
	"sync"
	"testing"
	"time"
)

func TestIntegration_FullPipeline(t *testing.T) {
	// Verifies: 48kHz stereo -> resample -> ring buffer -> VAD -> mel spectrogram.
	// Does NOT test TensorRT inference (requires GPU).

	e := newTestEngine()
	defer e.Close()

	// Generate 5 seconds of 48kHz stereo 440Hz tone (simulates speech).
	sampleRate := 48000
	duration := 5 * sampleRate
	chunkSize := 1024

	for i := 0; i < duration; i += chunkSize {
		chunk := make([]float32, chunkSize*2) // stereo
		for j := 0; j < chunkSize && i+j < duration; j++ {
			val := float32(0.3 * math.Sin(2*math.Pi*440*float64(i+j)/float64(sampleRate)))
			chunk[j*2] = val
			chunk[j*2+1] = val
		}
		pts := int64(i) * 90000 / int64(sampleRate)
		e.IngestAudio(chunk, pts, sampleRate, 2)
	}

	// VAD should have detected speech.
	if e.vad.State() != VADSpeaking {
		t.Fatalf("expected speaking after 5s of tone, got %v", e.vad.State())
	}

	// Ring buffer should have ~5s of 16kHz audio.
	// The resampler uses linear interpolation with fractional phase tracking,
	// so the exact count can vary slightly from the ideal 80000.
	expected := 5 * 16000
	actual := e.ringBuf.SampleCount()
	tolerance := 500
	if actual < expected-tolerance || actual > expected+tolerance {
		t.Fatalf("expected ~%d samples in ring buffer, got %d", expected, actual)
	}

	// Mel spectrogram should produce valid output.
	audio := e.ringBuf.Snapshot()
	mel := e.mel.Compute(audio)
	if len(mel) != 80 {
		t.Fatalf("expected 80 mel bins, got %d", len(mel))
	}
	if len(mel[0]) != 3000 {
		t.Fatalf("expected 3000 mel frames, got %d", len(mel[0]))
	}

	// Verify mel has non-zero energy in the tone region.
	nonZero := 0
	for b := 0; b < 80; b++ {
		for f := 0; f < 3000; f++ {
			if mel[b][f] != 0 {
				nonZero++
			}
		}
	}
	if nonZero == 0 {
		t.Fatal("mel spectrogram is all zeros for tonal input")
	}
}

func TestIntegration_SilenceSuppression(t *testing.T) {
	// Verifies: silence -> VAD stays idle, no spurious inference triggers.

	e := newTestEngine()
	defer e.Close()

	// Feed 3 seconds of silence.
	sampleRate := 48000
	duration := 3 * sampleRate
	chunkSize := 1024

	for i := 0; i < duration; i += chunkSize {
		silence := make([]float32, chunkSize*2)
		pts := int64(i) * 90000 / int64(sampleRate)
		e.IngestAudio(silence, pts, sampleRate, 2)
	}

	if e.vad.State() != VADIdle {
		t.Fatalf("expected idle after silence, got %v", e.vad.State())
	}

	// Ring buffer should have audio but VAD never triggered.
	e.mu.Lock()
	ws := e.wasSpeaking
	e.mu.Unlock()
	if ws {
		t.Fatal("wasSpeaking should be false after only silence")
	}
}

func TestIntegration_SpeechThenSilence(t *testing.T) {
	// Verifies: speech -> silence -> VAD transitions through speaking -> trailing -> idle.

	e := newTestEngine()
	defer e.Close()

	sampleRate := 48000
	chunkSize := 1024

	// 2 seconds of speech (loud tone).
	for i := 0; i < 2*sampleRate; i += chunkSize {
		chunk := make([]float32, chunkSize*2)
		for j := 0; j < chunkSize; j++ {
			val := float32(0.4 * math.Sin(2*math.Pi*440*float64(i+j)/float64(sampleRate)))
			chunk[j*2] = val
			chunk[j*2+1] = val
		}
		e.IngestAudio(chunk, 0, sampleRate, 2)
	}

	if e.vad.State() != VADSpeaking {
		t.Fatalf("expected speaking after tone, got %v", e.vad.State())
	}

	// 1 second of silence (exceeds 500ms hangover).
	for i := 0; i < sampleRate; i += chunkSize {
		silence := make([]float32, chunkSize*2)
		e.IngestAudio(silence, 0, sampleRate, 2)
	}

	if e.vad.State() != VADIdle {
		t.Fatalf("expected idle after 1s silence (500ms hangover), got %v", e.vad.State())
	}
}

func TestIntegration_TranscriptCallback(t *testing.T) {
	// Verifies: processTranscript delivers text to callback.

	var mu sync.Mutex
	var results []struct {
		text    string
		isFinal bool
	}

	e := newTestEngine()
	e.OnTranscript(func(text string, isFinal bool) {
		mu.Lock()
		results = append(results, struct {
			text    string
			isFinal bool
		}{text, isFinal})
		mu.Unlock()
	})
	defer e.Close()

	// Simulate confirmed text diffing.
	e.processTranscript("hello ", false)
	e.processTranscript("hello world ", false) // "hello " confirmed
	e.processTranscript("hello world today", true)

	time.Sleep(10 * time.Millisecond)

	mu.Lock()
	defer mu.Unlock()

	if len(results) < 2 {
		t.Fatalf("expected at least 2 callbacks, got %d", len(results))
	}

	// Last callback should be final.
	last := results[len(results)-1]
	if !last.isFinal {
		t.Fatal("last callback should be isFinal")
	}
}

func TestIntegration_MelSpectrogramPerformance(t *testing.T) {
	mel := NewMelSpectrogram()
	audio := make([]float32, 480000) // 30s at 16kHz
	for i := range audio {
		audio[i] = float32(math.Sin(2 * math.Pi * 440 * float64(i) / 16000))
	}

	start := time.Now()
	result := mel.Compute(audio)
	elapsed := time.Since(start)

	t.Logf("Mel spectrogram: %v for 30s audio", elapsed)

	// Budget is generous (2s) to accommodate -race overhead. Without race
	// detector this typically runs in <200ms. The goal is to catch
	// catastrophic regressions, not micro-benchmark.
	if elapsed > 2*time.Second {
		t.Fatalf("mel spectrogram too slow: %v (target <2s with race)", elapsed)
	}

	if len(result) != 80 || len(result[0]) != 3000 {
		t.Fatal("wrong output shape")
	}
}
