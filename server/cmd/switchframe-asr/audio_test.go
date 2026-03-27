package main

import (
	"math"
	"testing"
)

func TestDownmixStereoToMono(t *testing.T) {
	p := newAudioProcessor(48000, 2)

	// Feed stereo pairs where L and R cancel: (0.5, -0.5), (0.5, -0.5)
	stereo := []float32{0.5, -0.5, 0.5, -0.5}
	mono := p.downmix(stereo)

	if len(mono) != 2 {
		t.Fatalf("expected 2 mono samples, got %d", len(mono))
	}
	for i, s := range mono {
		if math.Abs(float64(s)) > 1e-6 {
			t.Errorf("mono[%d] = %f, want 0.0", i, s)
		}
	}
}

func TestDownmixStereoToMonoIdentical(t *testing.T) {
	p := newAudioProcessor(48000, 2)

	// Feed stereo pairs where L == R: (0.8, 0.8)
	stereo := []float32{0.8, 0.8}
	mono := p.downmix(stereo)

	if len(mono) != 1 {
		t.Fatalf("expected 1 mono sample, got %d", len(mono))
	}
	if math.Abs(float64(mono[0])-0.8) > 1e-6 {
		t.Errorf("mono[0] = %f, want 0.8", mono[0])
	}
}

func TestProcessOutputLength(t *testing.T) {
	p := newAudioProcessor(48000, 2)

	// 1024 stereo samples at 48kHz = 512 mono frames at 48kHz
	// After 48→16kHz resample: 512 * (16000/48000) = ~170.67 samples
	stereo := make([]float32, 1024)
	for i := range stereo {
		stereo[i] = 0.1 // non-zero to avoid empty
	}

	out := p.process(stereo)

	// The resampler is 3:1 downsampling (48kHz→16kHz).
	// 512 mono input frames → ~170 output samples.
	// Allow ±10% tolerance for filter transients.
	expected := 512.0 * 16000.0 / 48000.0
	lo := int(expected * 0.9)
	hi := int(expected * 1.1)

	if len(out) < lo || len(out) > hi {
		t.Errorf("output length %d not in expected range [%d, %d]", len(out), lo, hi)
	}
}

func TestComputeRMS(t *testing.T) {
	threshold := 0.005

	// Silence: all zeros
	silence := make([]float32, 100)
	if computeRMS(silence) >= threshold {
		t.Error("expected all-zeros to be below threshold")
	}

	// Loud signal: 0.5 amplitude
	loud := make([]float32, 100)
	for i := range loud {
		loud[i] = 0.5
	}
	if computeRMS(loud) < threshold {
		t.Error("expected 0.5 amplitude signal to be above threshold")
	}

	// Just below threshold
	quiet := make([]float32, 100)
	for i := range quiet {
		quiet[i] = 0.001
	}
	if computeRMS(quiet) >= threshold {
		t.Error("expected 0.001 amplitude signal to be below threshold (rms < 0.005)")
	}
}

func TestComputeRMSEmpty(t *testing.T) {
	if computeRMS(nil) != 0 {
		t.Error("expected nil slice to return 0")
	}
	if computeRMS([]float32{}) != 0 {
		t.Error("expected empty slice to return 0")
	}
}

func TestProcessEmpty(t *testing.T) {
	p := newAudioProcessor(48000, 2)

	if out := p.process(nil); out != nil {
		t.Errorf("expected nil for nil input, got %v", out)
	}
	if out := p.process([]float32{}); out != nil {
		t.Errorf("expected nil for empty input, got %v", out)
	}
}

func TestDownmixEmpty(t *testing.T) {
	p := newAudioProcessor(48000, 2)

	if out := p.downmix(nil); out != nil {
		t.Errorf("expected nil for nil input, got %v", out)
	}
	if out := p.downmix([]float32{}); out != nil {
		t.Errorf("expected nil for empty input, got %v", out)
	}
}

func TestProcessMonoPassthrough(t *testing.T) {
	// If input is already mono, downmix should be identity
	p := newAudioProcessor(48000, 1)

	mono := []float32{0.3, -0.7, 0.5}
	out := p.downmix(mono)

	if len(out) != 3 {
		t.Fatalf("expected 3 samples, got %d", len(out))
	}
	for i, s := range out {
		if math.Abs(float64(s)-float64(mono[i])) > 1e-6 {
			t.Errorf("out[%d] = %f, want %f", i, s, mono[i])
		}
	}
}
