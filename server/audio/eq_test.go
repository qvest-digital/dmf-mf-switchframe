package audio

import (
	"math"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestEQ_FlatPassesSignalUnchanged(t *testing.T) {
	t.Parallel()
	eq := NewEQ(48000)

	// Generate a 1kHz sine wave
	samples := make([]float32, 1024)
	for i := range samples {
		samples[i] = float32(math.Sin(2 * math.Pi * 1000.0 * float64(i) / 48000.0))
	}
	original := make([]float32, len(samples))
	copy(original, samples)

	result := eq.Process(samples)

	// Flat EQ (all bands at 0dB gain) should not alter the signal
	for i := range result {
		require.InDelta(t, float64(original[i]), float64(result[i]), 1e-5,
			"sample %d should be unchanged with flat EQ", i)
	}
}

func TestEQ_IsBypassed_FlatGain(t *testing.T) {
	t.Parallel()
	eq := NewEQ(48000)
	require.True(t, eq.IsBypassed(), "new EQ with 0dB gains should be bypassed")
}

func TestEQ_IsBypassed_AllDisabled(t *testing.T) {
	t.Parallel()
	eq := NewEQ(48000)
	// Set a non-zero gain but disable the band
	err := eq.SetBand(0, 500, 6.0, 1.0, false)
	require.NoError(t, err)
	require.True(t, eq.IsBypassed(), "EQ with all bands disabled should be bypassed")
}

func TestEQ_IsBypassed_NonZeroGain(t *testing.T) {
	t.Parallel()
	eq := NewEQ(48000)
	err := eq.SetBand(0, 500, 6.0, 1.0, true)
	require.NoError(t, err)
	require.False(t, eq.IsBypassed(), "EQ with an enabled band at non-zero gain should not be bypassed")
}

func TestEQ_SingleBandBoost(t *testing.T) {
	t.Parallel()
	eq := NewEQ(48000)
	// Boost band 0 at 200Hz by +12dB
	err := eq.SetBand(0, 200, 12.0, 1.0, true)
	require.NoError(t, err)

	// Generate a 200Hz sine wave (target frequency)
	n := 4096
	samples := make([]float32, n)
	for i := range samples {
		samples[i] = float32(0.1 * math.Sin(2*math.Pi*200.0*float64(i)/48000.0))
	}

	// Measure energy before
	energyBefore := rmsEnergy(samples)

	// Process through EQ
	result := eq.Process(samples)

	// Measure energy after
	energyAfter := rmsEnergy(result)

	// Boosting at the target frequency should increase energy
	require.Greater(t, energyAfter, energyBefore*1.5,
		"boosting at target frequency should measurably increase energy")
}

func TestEQ_SingleBandCut(t *testing.T) {
	t.Parallel()
	eq := NewEQ(48000)
	// Cut band 1 at 1kHz by -12dB
	err := eq.SetBand(1, 1000, -12.0, 1.0, true)
	require.NoError(t, err)

	// Generate a 1kHz sine wave (target frequency)
	n := 4096
	samples := make([]float32, n)
	for i := range samples {
		samples[i] = float32(0.5 * math.Sin(2*math.Pi*1000.0*float64(i)/48000.0))
	}

	// Measure energy before
	energyBefore := rmsEnergy(samples)

	// Process through EQ
	result := eq.Process(samples)

	// Cutting at the target frequency should decrease energy
	require.Less(t, rmsEnergy(result), energyBefore*0.5,
		"cutting at target frequency should measurably decrease energy")
}

func TestEQ_ParameterValidation_FrequencyRanges(t *testing.T) {
	t.Parallel()
	eq := NewEQ(48000)

	// Band 0 (Low): 80-1000Hz
	require.Error(t, eq.SetBand(0, 79, 0, 1.0, true), "band 0 freq below 80 should fail")
	require.Error(t, eq.SetBand(0, 1001, 0, 1.0, true), "band 0 freq above 1000 should fail")
	require.NoError(t, eq.SetBand(0, 80, 0, 1.0, true), "band 0 freq at 80 should succeed")
	require.NoError(t, eq.SetBand(0, 1000, 0, 1.0, true), "band 0 freq at 1000 should succeed")

	// Band 1 (Mid): 200-8000Hz
	require.Error(t, eq.SetBand(1, 199, 0, 1.0, true), "band 1 freq below 200 should fail")
	require.Error(t, eq.SetBand(1, 8001, 0, 1.0, true), "band 1 freq above 8000 should fail")
	require.NoError(t, eq.SetBand(1, 200, 0, 1.0, true), "band 1 freq at 200 should succeed")
	require.NoError(t, eq.SetBand(1, 8000, 0, 1.0, true), "band 1 freq at 8000 should succeed")

	// Band 2 (High): 1000-16000Hz
	require.Error(t, eq.SetBand(2, 999, 0, 1.0, true), "band 2 freq below 1000 should fail")
	require.Error(t, eq.SetBand(2, 16001, 0, 1.0, true), "band 2 freq above 16000 should fail")
	require.NoError(t, eq.SetBand(2, 1000, 0, 1.0, true), "band 2 freq at 1000 should succeed")
	require.NoError(t, eq.SetBand(2, 16000, 0, 1.0, true), "band 2 freq at 16000 should succeed")
}

func TestEQ_ParameterValidation_GainLimits(t *testing.T) {
	t.Parallel()
	eq := NewEQ(48000)
	require.Error(t, eq.SetBand(0, 500, -13, 1.0, true), "gain below -12 should fail")
	require.Error(t, eq.SetBand(0, 500, 13, 1.0, true), "gain above +12 should fail")
	require.NoError(t, eq.SetBand(0, 500, -12, 1.0, true), "gain at -12 should succeed")
	require.NoError(t, eq.SetBand(0, 500, 12, 1.0, true), "gain at +12 should succeed")
}

func TestEQ_ParameterValidation_QLimits(t *testing.T) {
	t.Parallel()
	eq := NewEQ(48000)
	require.Error(t, eq.SetBand(0, 500, 0, 0.4, true), "Q below 0.5 should fail")
	require.Error(t, eq.SetBand(0, 500, 0, 4.1, true), "Q above 4.0 should fail")
	require.NoError(t, eq.SetBand(0, 500, 0, 0.5, true), "Q at 0.5 should succeed")
	require.NoError(t, eq.SetBand(0, 500, 0, 4.0, true), "Q at 4.0 should succeed")
}

func TestEQ_ParameterValidation_InvalidBand(t *testing.T) {
	t.Parallel()
	eq := NewEQ(48000)
	require.Error(t, eq.SetBand(-1, 500, 0, 1.0, true), "band -1 should fail")
	require.Error(t, eq.SetBand(3, 500, 0, 1.0, true), "band 3 should fail")
}

func TestEQ_CoefficientsRecalculateOnSetBand(t *testing.T) {
	t.Parallel()
	eq := NewEQ(48000)

	// Process a signal
	samples1 := make([]float32, 1024)
	for i := range samples1 {
		samples1[i] = float32(math.Sin(2 * math.Pi * 1000.0 * float64(i) / 48000.0))
	}
	result1 := eq.Process(samples1)

	// Change a band
	err := eq.SetBand(1, 1000, 6.0, 1.0, true)
	require.NoError(t, err)

	// Process same signal -- result should differ
	samples2 := make([]float32, 1024)
	for i := range samples2 {
		samples2[i] = float32(math.Sin(2 * math.Pi * 1000.0 * float64(i) / 48000.0))
	}

	// Reset the EQ's filter state for a fair comparison
	eq2 := NewEQ(48000)
	err = eq2.SetBand(1, 1000, 6.0, 1.0, true)
	require.NoError(t, err)
	result2 := eq2.Process(samples2)

	// The two results should differ (flat vs boosted)
	differ := false
	for i := range result1 {
		if math.Abs(float64(result1[i]-result2[i])) > 1e-5 {
			differ = true
			break
		}
	}
	require.True(t, differ, "output should change after SetBand modifies coefficients")
}

func TestEQ_GetBands(t *testing.T) {
	t.Parallel()
	eq := NewEQ(48000)
	err := eq.SetBand(0, 200, 3.0, 1.5, true)
	require.NoError(t, err)

	bands := eq.GetBands()
	require.Len(t, bands, 3)
	require.InDelta(t, 200.0, bands[0].Frequency, 0.01)
	require.InDelta(t, 3.0, bands[0].Gain, 0.01)
	require.InDelta(t, 1.5, bands[0].Q, 0.01)
	require.True(t, bands[0].Enabled)
}

// rmsEnergy computes the root mean square energy of a sample buffer.
func rmsEnergy(samples []float32) float64 {
	var sum float64
	for _, s := range samples {
		sum += float64(s) * float64(s)
	}
	return math.Sqrt(sum / float64(len(samples)))
}
