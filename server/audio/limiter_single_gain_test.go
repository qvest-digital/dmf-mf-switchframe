package audio

import (
	"math"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestLimiter_SingleGainNoOverCompression(t *testing.T) {
	t.Parallel()
	lim := NewLimiter(48000, 1)
	lim.SetCeiling(-3.0)

	samples := make([]float32, 48000)
	for i := range samples {
		samples[i] = 0.9
	}
	gr := lim.Process(samples)

	ceilLinear := float32(math.Pow(10, -3.0/20.0))
	for i := len(samples) - 1000; i < len(samples); i++ {
		absVal := float32(math.Abs(float64(samples[i])))
		require.LessOrEqual(t, absVal, ceilLinear+1e-4,
			"sample %d (%.4f) should not exceed ceiling %.4f", i, absVal, ceilLinear)
		require.Greater(t, absVal, ceilLinear-0.02,
			"sample %d (%.4f) should be at ceiling (not double-compounded below)", i, absVal)
	}

	expectedGR := 20 * math.Log10(0.9/float64(ceilLinear))
	require.InDelta(t, expectedGR, gr, 0.15,
		"GR %.3f dB should match ceiling/peak=%.3f dB, not compound", gr, expectedGR)
}

func TestLimiter_SineAt0dBFSWithMinusOneCeiling(t *testing.T) {
	t.Parallel()
	lim := NewLimiter(48000, 1)
	lim.SetCeiling(-1.0)

	const sr = 48000.0
	const freq = 1000.0
	samples := make([]float32, 4800)
	for i := range samples {
		samples[i] = float32(math.Sin(2 * math.Pi * freq * float64(i) / sr))
	}

	lim.Process(samples)

	var peak float32
	for i := 2400; i < len(samples); i++ {
		abs := float32(math.Abs(float64(samples[i])))
		if abs > peak {
			peak = abs
		}
	}
	peakDB := 20 * math.Log10(float64(peak))
	require.InDelta(t, -1.0, peakDB, 0.05,
		"1 kHz sine into -1 dBFS ceiling: peak %.3f dBFS should be -1 dBFS within 0.05 dB", peakDB)
}

func TestLimiter_TransientDoesNotDoubleCompound(t *testing.T) {
	t.Parallel()
	lim := NewLimiter(48000, 1)
	lim.SetThreshold(-1.0)
	lim.SetCeiling(-3.0)

	loudA := make([]float32, 4800)
	for i := range loudA {
		loudA[i] = 0.9
	}
	lim.Process(loudA)

	quiet := make([]float32, 9600)
	for i := range quiet {
		quiet[i] = 0.4
	}
	lim.Process(quiet)

	loudB := make([]float32, 4800)
	for i := range loudB {
		loudB[i] = 0.9
	}
	lim.Process(loudB)

	ceilLinear := float32(math.Pow(10, -3.0/20.0))
	var peak float32
	for i := 2400; i < len(loudB); i++ {
		abs := float32(math.Abs(float64(loudB[i])))
		if abs > peak {
			peak = abs
		}
	}
	require.LessOrEqual(t, peak, ceilLinear+1e-4,
		"loudB peak %.4f should not exceed ceiling %.4f", peak, ceilLinear)
	require.Greater(t, peak, ceilLinear-0.02,
		"loudB peak %.4f should be at ceiling (not compounded to %.4f)",
		peak, ceilLinear*float32(math.Pow(10, -3.0/20.0)))
}
