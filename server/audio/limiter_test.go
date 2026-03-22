package audio

import (
	"math"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/zsiec/prism/media"
)

func TestLimiter_BelowThreshold_Passthrough(t *testing.T) {
	t.Parallel()
	lim := NewLimiter(48000, 2)

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
	t.Parallel()
	lim := NewLimiter(48000, 2)

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
	t.Parallel()
	lim := NewLimiter(48000, 2)

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
	// maximum of 20*log10(2.0/0.891) ~ 7.0 dB.
	require.Greater(t, gr, 5.0,
		"GR should be > 5 dB after 1ms of +6 dBFS signal (fast 0.1ms attack)")
}

func TestLimiter_ReleaseTime(t *testing.T) {
	t.Parallel()
	lim := NewLimiter(48000, 2)

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
	t.Parallel()
	lim := NewLimiter(48000, 2)

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
	t.Parallel()
	lim := NewLimiter(48000, 2)

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

func TestLimiter_Reset(t *testing.T) {
	t.Parallel()
	lim := NewLimiter(48000, 2)

	// Process loud signal to build up envelope
	loud := make([]float32, 1024)
	for i := range loud {
		loud[i] = 2.0 // well above threshold
	}
	lim.Process(loud)

	require.Greater(t, lim.GainReduction(), 0.0, "GR should be positive after limiting")

	// Reset should clear the envelope state
	lim.Reset()

	require.InDelta(t, 0.0, lim.GainReduction(), 0.001, "GR should be 0 after Reset")
}

func TestLimiter_ExactlyAtThreshold(t *testing.T) {
	t.Parallel()
	lim := NewLimiter(48000, 2)

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

func TestLimiter_StereoLinkedEnvelope(t *testing.T) {
	t.Parallel()
	lim := NewLimiter(48000, 2)

	// Interleaved stereo: loud L, quiet R
	samples := make([]float32, 2048)
	for i := 0; i < len(samples); i += 2 {
		samples[i] = 2.0   // L: above threshold
		samples[i+1] = 0.1 // R: well below threshold
	}

	lim.Process(samples)

	// Both channels should receive the same gain. Check the last 50 stereo pairs.
	for i := len(samples) - 100; i < len(samples); i += 2 {
		ratioL := float64(samples[i]) / 2.0
		ratioR := float64(samples[i+1]) / 0.1
		require.InDelta(t, ratioL, ratioR, 0.02,
			"L and R at index %d should have same gain ratio", i)
	}
}

func TestLimiter_MonoStillWorks(t *testing.T) {
	t.Parallel()
	lim := NewLimiter(48000, 1)

	// Mono signal above threshold
	samples := make([]float32, 1024)
	for i := range samples {
		samples[i] = 2.0
	}

	gr := lim.Process(samples)

	threshold := float32(math.Pow(10, -1.0/20.0))
	for i := range samples {
		absVal := float32(math.Abs(float64(samples[i])))
		require.LessOrEqual(t, absVal, threshold+1e-6,
			"mono sample %d should not exceed threshold", i)
	}
	require.Greater(t, gr, 0.0, "GR should be positive when limiting")
}

func TestLimiterDenormalProtection(t *testing.T) {
	t.Parallel()
	lim := NewLimiter(48000, 2)

	// Drive the envelope above threshold to prime it
	loud := make([]float32, 1024)
	for i := range loud {
		loud[i] = 2.0
	}
	lim.Process(loud)

	// Process enough silence for the envelope to fully decay.
	// 50ms release at 48kHz: after ~10 seconds the exponential envelope
	// would reach denormal territory without the floor clamp.
	for i := 0; i < 20; i++ {
		silence := make([]float32, 48000*2)
		lim.Process(silence)
	}

	lim.mu.Lock()
	env := lim.envelope
	lim.mu.Unlock()

	require.Equal(t, float64(0), env, "limiter envelope should be exactly 0 after processing silence, not a denormal") //nolint:govet // test inspects internal state
}

func TestLimiter_DefaultThreshold(t *testing.T) {
	t.Parallel()
	lim := NewLimiter(48000, 2)
	require.InDelta(t, -1.0, lim.ThresholdDB(), 0.01, "default threshold should be -1 dBFS")
}

func TestLimiter_DefaultCeiling(t *testing.T) {
	t.Parallel()
	lim := NewLimiter(48000, 2)
	require.InDelta(t, 0.0, lim.CeilingDB(), 0.01, "default ceiling should be 0 dBFS")
}

func TestLimiter_SetThreshold(t *testing.T) {
	t.Parallel()
	lim := NewLimiter(48000, 2)

	// Lower the threshold to -6 dBFS
	lim.SetThreshold(-6.0)
	require.InDelta(t, -6.0, lim.ThresholdDB(), 0.01)

	// A signal at -3 dBFS (~0.707) is above -6 dBFS threshold, so should be limited
	samples := make([]float32, 1024)
	for i := range samples {
		samples[i] = 0.707 // ~-3 dBFS
	}
	gr := lim.Process(samples)
	require.Greater(t, gr, 0.0, "GR should be positive when signal exceeds lowered threshold")

	// No sample should exceed the new threshold level
	threshLinear := float32(math.Pow(10, -6.0/20.0)) // ~0.501
	for i := range samples {
		absVal := float32(math.Abs(float64(samples[i])))
		require.LessOrEqual(t, absVal, threshLinear+0.01,
			"sample %d should not exceed -6 dBFS threshold after limiting", i)
	}
}

func TestLimiter_SetThreshold_MoreGRWithLowerThreshold(t *testing.T) {
	t.Parallel()

	// Process the same signal with two different thresholds
	signal := make([]float32, 1024)
	for i := range signal {
		signal[i] = 0.8 // -1.94 dBFS, above -6 dBFS threshold but near -1 dBFS
	}

	// Default threshold (-1 dBFS): signal near threshold, minimal GR
	limDefault := NewLimiter(48000, 2)
	samplesDefault := make([]float32, len(signal))
	copy(samplesDefault, signal)
	grDefault := limDefault.Process(samplesDefault)

	// Lower threshold (-6 dBFS): same signal is well above, more GR
	limLow := NewLimiter(48000, 2)
	limLow.SetThreshold(-6.0)
	samplesLow := make([]float32, len(signal))
	copy(samplesLow, signal)
	grLow := limLow.Process(samplesLow)

	require.Greater(t, grLow, grDefault,
		"lower threshold should produce more gain reduction on same signal")
}

func TestLimiter_SetCeiling(t *testing.T) {
	t.Parallel()
	lim := NewLimiter(48000, 2)

	// Set ceiling to -3 dBFS
	lim.SetCeiling(-3.0)
	require.InDelta(t, -3.0, lim.CeilingDB(), 0.01)

	// Feed signal at 0 dBFS (1.0 linear) — below the -1 dBFS threshold
	// so no gain reduction, but ceiling should cap the output
	samples := make([]float32, 1024)
	for i := range samples {
		samples[i] = 0.8 // below -1 dBFS threshold (~0.891)
	}
	lim.Process(samples)

	// All samples should be at or below ceiling level
	ceilLinear := float32(math.Pow(10, -3.0/20.0)) // ~0.708
	for i := range samples {
		absVal := float32(math.Abs(float64(samples[i])))
		require.LessOrEqual(t, absVal, ceilLinear+1e-6,
			"sample %d should not exceed -3 dBFS ceiling", i)
	}
}

func TestLimiter_CeilingZero_NoEffect(t *testing.T) {
	t.Parallel()
	lim := NewLimiter(48000, 2)

	// Ceiling at 0 dBFS (default) should have no effect
	samples := make([]float32, 1024)
	for i := range samples {
		samples[i] = 0.5
	}
	original := make([]float32, len(samples))
	copy(original, samples)

	lim.Process(samples)

	for i := range samples {
		require.InDelta(t, float64(original[i]), float64(samples[i]), 1e-6,
			"sample %d should be unchanged with 0 dBFS ceiling", i)
	}
}

func TestLimiter_ThresholdDBReadback(t *testing.T) {
	t.Parallel()
	lim := NewLimiter(48000, 2)

	for _, db := range []float64{0, -1, -3, -6, -12} {
		lim.SetThreshold(db)
		require.InDelta(t, db, lim.ThresholdDB(), 0.01,
			"ThresholdDB should round-trip for %f dBFS", db)
	}
}

func TestLimiter_CeilingDBReadback(t *testing.T) {
	t.Parallel()
	lim := NewLimiter(48000, 2)

	for _, db := range []float64{0, -0.5, -1, -2, -3} {
		lim.SetCeiling(db)
		require.InDelta(t, db, lim.CeilingDB(), 0.01,
			"CeilingDB should round-trip for %f dBFS", db)
	}
}

// --- Mixer-level limiter method tests ---

func newTestMixerForLimiter(t *testing.T) *Mixer {
	t.Helper()
	return NewMixer(MixerConfig{
		SampleRate: 48000,
		Channels:   2,
		Output:     func(_ *media.AudioFrame) {},
	})
}

func TestMixer_SetLimiterThreshold_ValidRange(t *testing.T) {
	t.Parallel()
	m := newTestMixerForLimiter(t)
	defer m.Close()

	require.NoError(t, m.SetLimiterThreshold(-6.0))
	threshold, _, _ := m.LimiterSettings()
	require.InDelta(t, -6.0, threshold, 0.01)
}

func TestMixer_SetLimiterThreshold_OutOfRange(t *testing.T) {
	t.Parallel()
	m := newTestMixerForLimiter(t)
	defer m.Close()

	require.Error(t, m.SetLimiterThreshold(-13.0), "below -12 should error")
	require.Error(t, m.SetLimiterThreshold(1.0), "above 0 should error")
}

func TestMixer_SetLimiterCeiling_ValidRange(t *testing.T) {
	t.Parallel()
	m := newTestMixerForLimiter(t)
	defer m.Close()

	require.NoError(t, m.SetLimiterCeiling(-1.5))
	_, ceiling, _ := m.LimiterSettings()
	require.InDelta(t, -1.5, ceiling, 0.01)
}

func TestMixer_SetLimiterCeiling_OutOfRange(t *testing.T) {
	t.Parallel()
	m := newTestMixerForLimiter(t)
	defer m.Close()

	require.Error(t, m.SetLimiterCeiling(-4.0), "below -3 should error")
	require.Error(t, m.SetLimiterCeiling(1.0), "above 0 should error")
}

func TestMixer_LimiterSettings_Defaults(t *testing.T) {
	t.Parallel()
	m := newTestMixerForLimiter(t)
	defer m.Close()

	threshold, ceiling, gr := m.LimiterSettings()
	require.InDelta(t, -1.0, threshold, 0.01, "default threshold should be -1 dBFS")
	require.InDelta(t, 0.0, ceiling, 0.01, "default ceiling should be 0 dBFS")
	require.InDelta(t, 0.0, gr, 0.01, "initial GR should be 0")
}
