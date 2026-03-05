package audio

import (
	"math"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestLimiter_BelowThreshold_Passthrough(t *testing.T) {
	lim := NewLimiter(48000)

	// -6 dBFS = ~0.5 linear, well below -1 dBFS threshold (~0.891)
	samples := make([]float32, 1024)
	for i := range samples {
		samples[i] = 0.5
	}
	original := make([]float32, len(samples))
	copy(original, samples)

	gr := lim.Process(samples)

	// Samples should pass through unchanged
	for i := range samples {
		require.InDelta(t, float64(original[i]), float64(samples[i]), 1e-6,
			"sample %d should be unchanged below threshold", i)
	}
	// Gain reduction should be 0 (no limiting)
	require.InDelta(t, 0.0, gr, 0.01, "GR should be 0 dB below threshold")
}

func TestLimiter_AboveThreshold_Limiting(t *testing.T) {
	lim := NewLimiter(48000)

	// +6 dBFS = ~2.0 linear, well above -1 dBFS threshold
	samples := make([]float32, 1024)
	for i := range samples {
		samples[i] = 2.0
	}

	gr := lim.Process(samples)

	// Brickwall: no sample should exceed the threshold
	threshold := float32(math.Pow(10, -1.0/20.0)) // -1 dBFS
	for i := range samples {
		absVal := float32(math.Abs(float64(samples[i])))
		require.LessOrEqual(t, absVal, threshold+1e-6,
			"sample %d should not exceed threshold after limiting", i)
	}
	// GR should be positive (indicating gain reduction occurred)
	require.Greater(t, gr, 0.0, "GR should be > 0 when limiting")
}

func TestLimiter_AttackTime(t *testing.T) {
	lim := NewLimiter(48000)

	// Feed a burst of loud signal. At 48kHz, 0.1ms = ~4.8 samples.
	// After processing a short burst, the envelope should have engaged quickly.
	// We verify fast attack by checking that GR is already significant after
	// just 1ms of loud signal (48 samples at 48kHz).
	samples := make([]float32, 48) // 1ms worth
	for i := range samples {
		samples[i] = 2.0 // +6 dBFS
	}

	gr := lim.Process(samples)

	// After 1ms (10x the 0.1ms attack time), the envelope should have mostly
	// caught up to the 2.0 input level. GR should be near the theoretical
	// maximum of 20*log10(2.0/0.891) ≈ 7.0 dB.
	require.Greater(t, gr, 5.0,
		"GR should be > 5 dB after 1ms of +6 dBFS signal (fast 0.1ms attack)")
}

func TestLimiter_ReleaseTime(t *testing.T) {
	lim := NewLimiter(48000)

	// First: engage the limiter with a loud burst
	loud := make([]float32, 480) // 10ms of loud signal
	for i := range loud {
		loud[i] = 2.0
	}
	lim.Process(loud)

	grAfterLoud := lim.GainReduction()
	require.Greater(t, grAfterLoud, 0.0, "GR should be active after loud signal")

	// Then: feed quiet signal. Release is 50ms = 2400 samples @ 48kHz.
	// After 50ms, GR should have mostly recovered.
	quiet := make([]float32, 4800) // 100ms of quiet signal
	for i := range quiet {
		quiet[i] = 0.1 // well below threshold
	}
	lim.Process(quiet)

	grAfterRelease := lim.GainReduction()
	// After 100ms (2x release time), GR should be near zero
	require.Less(t, grAfterRelease, grAfterLoud*0.1,
		"GR should have mostly recovered after 2x release time")
}

func TestLimiter_GainReductionReporting(t *testing.T) {
	lim := NewLimiter(48000)

	// Initially, GR should be 0
	require.InDelta(t, 0.0, lim.GainReduction(), 0.001, "initial GR should be 0")

	// Process loud signal
	samples := make([]float32, 1024)
	for i := range samples {
		samples[i] = 2.0
	}
	grFromProcess := lim.Process(samples)

	// GainReduction() should return the same value as Process() returned
	grFromGetter := lim.GainReduction()
	require.InDelta(t, grFromProcess, grFromGetter, 0.001,
		"GainReduction() should match Process() return value")
	require.Greater(t, grFromGetter, 0.0, "GR should be positive after limiting")
}

func TestLimiter_Silence(t *testing.T) {
	lim := NewLimiter(48000)

	// Process silence
	samples := make([]float32, 1024)
	gr := lim.Process(samples)

	// Silence should pass through unchanged
	for i := range samples {
		require.Equal(t, float32(0), samples[i],
			"silent sample %d should remain zero", i)
	}
	require.InDelta(t, 0.0, gr, 0.001, "GR should be 0 for silence")
}

func TestLimiter_ExactlyAtThreshold(t *testing.T) {
	lim := NewLimiter(48000)

	// Samples exactly at -1 dBFS threshold
	threshold := float32(math.Pow(10, -1.0/20.0)) // ~0.891
	samples := make([]float32, 1024)
	for i := range samples {
		samples[i] = threshold
	}
	original := make([]float32, len(samples))
	copy(original, samples)

	gr := lim.Process(samples)

	// Samples at exactly the threshold should pass through (or be very close)
	for i := range samples {
		require.InDelta(t, float64(original[i]), float64(samples[i]), 0.01,
			"sample %d at threshold should pass through or be barely affected", i)
	}
	require.Less(t, gr, 0.5, "GR should be negligible at exactly threshold")
}
