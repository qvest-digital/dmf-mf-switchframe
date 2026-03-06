package audio

import (
	"math"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestCompressor_BelowThreshold_Passthrough(t *testing.T) {
	t.Parallel()
	c := NewCompressor(48000)
	// Default threshold is 0 dBFS, ratio 1:1
	// Set threshold to -10 dBFS, ratio 4:1
	err := c.SetParams(-10, 4.0, 5.0, 100.0, 0)
	require.NoError(t, err)

	// Signal at -20 dBFS (well below -10 dBFS threshold)
	level := float32(math.Pow(10, -20.0/20.0)) // ~0.1
	samples := make([]float32, 1024)
	for i := range samples {
		samples[i] = level
	}
	original := make([]float32, len(samples))
	copy(original, samples)

	result := c.Process(samples)

	// Signal below threshold should pass through essentially unchanged
	for i := range result {
		require.InDelta(t, float64(original[i]), float64(result[i]), 0.01,
			"sample %d should be essentially unchanged below threshold", i)
	}
}

func TestCompressor_AboveThreshold_Reduced(t *testing.T) {
	t.Parallel()
	c := NewCompressor(48000)
	// Threshold -6 dBFS, ratio 4:1, fast attack
	err := c.SetParams(-6, 4.0, 0.1, 100.0, 0)
	require.NoError(t, err)

	// Signal at 0 dBFS (6dB above threshold)
	samples := make([]float32, 4096)
	for i := range samples {
		samples[i] = 1.0
	}

	result := c.Process(samples)

	// After sufficient attack time, the signal should be reduced
	// 6dB above threshold at 4:1 ratio = 6/4 = 1.5dB above threshold
	// So output should be around -4.5 dBFS = 10^(-4.5/20) ~ 0.596
	// Check the last samples (after attack has settled)
	avgLast := float64(0)
	n := 100
	for i := len(result) - n; i < len(result); i++ {
		avgLast += float64(result[i])
	}
	avgLast /= float64(n)

	// The output should be significantly less than 1.0
	require.Less(t, avgLast, 0.8, "signal above threshold should be reduced")
	require.Greater(t, avgLast, 0.2, "signal should not be reduced to near-zero")
}

func TestCompressor_AttackReleaseTiming(t *testing.T) {
	t.Parallel()
	c := NewCompressor(48000)
	// 10ms attack, 100ms release
	err := c.SetParams(-6, 4.0, 10.0, 100.0, 0)
	require.NoError(t, err)

	// Phase 1: Loud signal for 50ms (well past attack time)
	loud := make([]float32, 2400) // 50ms at 48kHz
	for i := range loud {
		loud[i] = 1.0 // 0 dBFS
	}
	c.Process(loud)

	grAfterAttack := c.GainReduction()
	require.Greater(t, grAfterAttack, 1.0,
		"GR should be > 1 dB after attack period with 0 dBFS signal")

	// Phase 2: Quiet signal for 500ms (well past release time)
	quiet := make([]float32, 24000) // 500ms at 48kHz
	for i := range quiet {
		quiet[i] = 0.01 // very quiet
	}
	c.Process(quiet)

	grAfterRelease := c.GainReduction()
	require.Less(t, grAfterRelease, grAfterAttack*0.2,
		"GR should be mostly released after 5x release time")
}

func TestCompressor_MakeupGain(t *testing.T) {
	t.Parallel()
	c := NewCompressor(48000)
	// Threshold -6, ratio 4:1, fast attack, 6dB makeup
	err := c.SetParams(-6, 4.0, 0.1, 100.0, 6.0)
	require.NoError(t, err)

	// Signal at -20 dBFS (below threshold -- no compression applied)
	level := float32(math.Pow(10, -20.0/20.0)) // ~0.1
	samples := make([]float32, 4096)
	for i := range samples {
		samples[i] = level
	}

	result := c.Process(samples)

	// Makeup gain should boost the signal by ~6dB (x2)
	// Check later samples to let filter settle
	avgLast := float64(0)
	n := 100
	for i := len(result) - n; i < len(result); i++ {
		avgLast += float64(result[i])
	}
	avgLast /= float64(n)

	expected := float64(level) * math.Pow(10, 6.0/20.0) // level * ~2.0
	require.InDelta(t, expected, avgLast, expected*0.15,
		"makeup gain should boost signal by ~6dB")
}

func TestCompressor_IsBypassed(t *testing.T) {
	t.Parallel()
	c := NewCompressor(48000)
	require.True(t, c.IsBypassed(), "new compressor with ratio 1.0 should be bypassed")

	err := c.SetParams(-10, 4.0, 5.0, 100.0, 0)
	require.NoError(t, err)
	require.False(t, c.IsBypassed(), "compressor with ratio > 1 should not be bypassed")

	err = c.SetParams(-10, 1.0, 5.0, 100.0, 0)
	require.NoError(t, err)
	require.True(t, c.IsBypassed(), "compressor with ratio <= 1 should be bypassed")
}

func TestCompressor_GainReduction_ReportsValue(t *testing.T) {
	t.Parallel()
	c := NewCompressor(48000)
	require.InDelta(t, 0.0, c.GainReduction(), 0.01, "initial GR should be 0")

	// Threshold -6, ratio 4:1, fast attack
	err := c.SetParams(-6, 4.0, 0.1, 100.0, 0)
	require.NoError(t, err)

	// Process loud signal
	samples := make([]float32, 4096)
	for i := range samples {
		samples[i] = 1.0
	}
	c.Process(samples)

	gr := c.GainReduction()
	require.Greater(t, gr, 0.0, "GR should be positive after compressing")
}

func TestCompressor_NotBypassedWithMakeupGain(t *testing.T) {
	t.Parallel()
	c := NewCompressor(48000)
	// ratio=1.0 (default), but set makeup gain
	err := c.SetParams(0, 1.0, 10, 100, 6.0)
	require.NoError(t, err)
	require.False(t, c.IsBypassed(), "compressor with makeup gain should not be bypassed")
}

func TestCompressor_BypassedWhenDefault(t *testing.T) {
	t.Parallel()
	c := NewCompressor(48000)
	require.True(t, c.IsBypassed(), "default compressor should be bypassed")
}

func TestCompressor_Reset(t *testing.T) {
	t.Parallel()
	c := NewCompressor(48000)
	// Threshold -6, ratio 4:1, fast attack
	require.NoError(t, c.SetParams(-6, 4.0, 0.1, 100.0, 0))

	// Process loud signal to build up envelope
	loud := make([]float32, 4096)
	for i := range loud {
		loud[i] = 1.0
	}
	c.Process(loud)

	require.Greater(t, c.GainReduction(), 0.0, "GR should be positive after compressing")

	// Reset should clear the envelope state
	c.Reset()

	require.InDelta(t, 0.0, c.GainReduction(), 0.001, "GR should be 0 after Reset")
}

func TestCompressor_ParameterValidation(t *testing.T) {
	t.Parallel()
	c := NewCompressor(48000)

	// Threshold: -40 to 0
	require.Error(t, c.SetParams(-41, 4.0, 5.0, 100.0, 0), "threshold below -40 should fail")
	require.Error(t, c.SetParams(1, 4.0, 5.0, 100.0, 0), "threshold above 0 should fail")
	require.NoError(t, c.SetParams(-40, 4.0, 5.0, 100.0, 0))
	require.NoError(t, c.SetParams(0, 4.0, 5.0, 100.0, 0))

	// Ratio: 1.0 to 20.0
	require.Error(t, c.SetParams(-10, 0.5, 5.0, 100.0, 0), "ratio below 1 should fail")
	require.Error(t, c.SetParams(-10, 21.0, 5.0, 100.0, 0), "ratio above 20 should fail")
	require.NoError(t, c.SetParams(-10, 1.0, 5.0, 100.0, 0))
	require.NoError(t, c.SetParams(-10, 20.0, 5.0, 100.0, 0))

	// Attack: 0.1 to 100 ms
	require.Error(t, c.SetParams(-10, 4.0, 0.05, 100.0, 0), "attack below 0.1ms should fail")
	require.Error(t, c.SetParams(-10, 4.0, 101.0, 100.0, 0), "attack above 100ms should fail")
	require.NoError(t, c.SetParams(-10, 4.0, 0.1, 100.0, 0))
	require.NoError(t, c.SetParams(-10, 4.0, 100.0, 100.0, 0))

	// Release: 10 to 1000 ms
	require.Error(t, c.SetParams(-10, 4.0, 5.0, 9.0, 0), "release below 10ms should fail")
	require.Error(t, c.SetParams(-10, 4.0, 5.0, 1001.0, 0), "release above 1000ms should fail")
	require.NoError(t, c.SetParams(-10, 4.0, 5.0, 10.0, 0))
	require.NoError(t, c.SetParams(-10, 4.0, 5.0, 1000.0, 0))

	// Makeup gain: 0 to 24 dB
	require.Error(t, c.SetParams(-10, 4.0, 5.0, 100.0, -1), "makeup below 0 should fail")
	require.Error(t, c.SetParams(-10, 4.0, 5.0, 100.0, 25), "makeup above 24 should fail")
	require.NoError(t, c.SetParams(-10, 4.0, 5.0, 100.0, 0))
	require.NoError(t, c.SetParams(-10, 4.0, 5.0, 100.0, 24))
}
