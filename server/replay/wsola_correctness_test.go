//go:build correctness

package replay

import (
	"math"
	"testing"
)

// TestWSOLAOverlapAdd verifies that the periodic Hann window at 50% overlap
// sums to exactly 1.0 (the COLA property), which is required for artifact-free
// overlap-add reconstruction.
func TestWSOLAOverlapAdd(t *testing.T) {
	sizes := []int{256, 512, 1024, 2048, 4096}

	for _, size := range sizes {
		w := makePeriodicHannWindow(size)

		// Verify window values at endpoints
		if w[0] != 0.0 {
			t.Errorf("size %d: w[0] = %f, expected 0.0", size, w[0])
		}

		// At 50% overlap (hop = size/2), two consecutive windows overlap.
		// The COLA condition requires: w[n] + w[n + hop] = 1 for all n in [0, hop-1].
		hop := size / 2
		var maxErr float64

		for n := 0; n < hop; n++ {
			sum := w[n] + w[n+hop]
			err := math.Abs(sum - 1.0)
			if err > maxErr {
				maxErr = err
			}
		}

		t.Logf("size %d: max COLA error = %e", size, maxErr)
		if maxErr > 1e-4 {
			t.Errorf("size %d: COLA violation, max error = %e (tolerance 1e-4)", size, maxErr)
		}
	}
}

// TestWSOLAWindowSymmetry verifies that the periodic Hann window has the
// expected symmetry property: w[n] = w[N-n] for the symmetric portion.
// The periodic window wraps at N, not N-1, so w[0] != w[N-1].
func TestWSOLAWindowSymmetry(t *testing.T) {
	size := 1024
	w := makePeriodicHannWindow(size)

	// The periodic Hann window should satisfy:
	// w[n] = 0.5 * (1 - cos(2*pi*n/N))
	for i := 0; i < size; i++ {
		expected := 0.5 * (1 - math.Cos(2*math.Pi*float64(i)/float64(size)))
		err := math.Abs(w[i] - expected)
		if err > 1e-10 {
			t.Errorf("w[%d] = %.10f, expected %.10f (err=%e)", i, w[i], expected, err)
		}
	}
}

// TestWSOLAOutputLength verifies that WSOLA time-stretching produces output
// of approximately the expected length.
func TestWSOLAOutputLength(t *testing.T) {
	sampleRate := 48000
	channels := 1
	duration := 0.5 // 0.5 seconds
	totalSamples := int(duration * float64(sampleRate))

	// Generate a sine wave
	input := make([]float32, totalSamples*channels)
	for i := range input {
		input[i] = float32(math.Sin(2 * math.Pi * 440.0 * float64(i) / float64(sampleRate)))
	}

	speeds := []float64{0.5, 0.75, 0.9}
	for _, speed := range speeds {
		output := WSOLATimeStretch(input, channels, sampleRate, speed)
		if output == nil {
			t.Errorf("speed %.2f: output is nil", speed)
			continue
		}

		expectedLen := float64(totalSamples) / speed
		actualLen := float64(len(output)) / float64(channels)
		ratio := actualLen / expectedLen

		// Allow 20% tolerance since WSOLA is approximate
		if ratio < 0.8 || ratio > 1.2 {
			t.Errorf("speed %.2f: expected ~%.0f samples, got %.0f (ratio=%.3f)",
				speed, expectedLen, actualLen, ratio)
		}
	}
}

// TestWSOLAPeakNormalization verifies that the output peak does not exceed 0.95.
func TestWSOLAPeakNormalization(t *testing.T) {
	sampleRate := 48000
	channels := 1
	totalSamples := sampleRate / 2

	// Generate a full-scale sine
	input := make([]float32, totalSamples)
	for i := range input {
		input[i] = float32(math.Sin(2 * math.Pi * 440.0 * float64(i) / float64(sampleRate)))
	}

	output := WSOLATimeStretch(input, channels, sampleRate, 0.75)
	if output == nil {
		t.Fatal("output is nil")
	}

	var peak float32
	for _, s := range output {
		if s > peak {
			peak = s
		}
		if -s > peak {
			peak = -s
		}
	}

	if peak > 0.96 { // small tolerance above 0.95 for float rounding
		t.Errorf("peak %.6f exceeds 0.96 (normalizePeak should limit to 0.95)", peak)
	}
}

// TestWSOLASpeedOne verifies that speed=1.0 returns a copy of the input.
func TestWSOLASpeedOne(t *testing.T) {
	input := []float32{0.1, 0.2, 0.3, 0.4, 0.5}
	output := WSOLATimeStretch(input, 1, 48000, 1.0)

	if len(output) != len(input) {
		t.Fatalf("speed 1.0: expected length %d, got %d", len(input), len(output))
	}
	for i := range input {
		if output[i] != input[i] {
			t.Errorf("speed 1.0: output[%d]=%f != input[%d]=%f", i, output[i], i, input[i])
		}
	}
}
