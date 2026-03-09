package replay

import (
	"math"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCartesianToPolar_Known(t *testing.T) {
	re := []float32{3.0, 1.0, 0.0}
	im := []float32{4.0, 0.0, -1.0}
	mag := make([]float32, 3)
	phase := make([]float32, 3)
	cartesianToPolar(re, im, mag, phase, 3)

	assert.InDelta(t, 5.0, mag[0], 1e-5)
	assert.InDelta(t, math.Atan2(4, 3), float64(phase[0]), 1e-5)

	assert.InDelta(t, 1.0, mag[1], 1e-5)
	assert.InDelta(t, 0.0, phase[1], 1e-5)

	assert.InDelta(t, 1.0, mag[2], 1e-5)
	assert.InDelta(t, -math.Pi/2, float64(phase[2]), 1e-5)
}

func TestPolarToCartesian_Known(t *testing.T) {
	mag := []float32{5.0, 1.0}
	phase := []float32{float32(math.Atan2(4, 3)), 0.0}
	re := make([]float32, 2)
	im := make([]float32, 2)
	polarToCartesian(mag, phase, re, im, 2)

	assert.InDelta(t, 3.0, re[0], 1e-4)
	assert.InDelta(t, 4.0, im[0], 1e-4)
	assert.InDelta(t, 1.0, re[1], 1e-5)
	assert.InDelta(t, 0.0, im[1], 1e-5)
}

func TestPolarRoundtrip(t *testing.T) {
	n := 2049
	re := make([]float32, n)
	im := make([]float32, n)
	for i := range re {
		re[i] = float32(math.Sin(float64(i) * 0.3))
		im[i] = float32(math.Cos(float64(i) * 0.7))
	}
	mag := make([]float32, n)
	phase := make([]float32, n)
	cartesianToPolar(re, im, mag, phase, n)

	reOut := make([]float32, n)
	imOut := make([]float32, n)
	polarToCartesian(mag, phase, reOut, imOut, n)

	for i := 0; i < n; i++ {
		assert.InDelta(t, re[i], reOut[i], 1e-4, "re[%d]", i)
		assert.InDelta(t, im[i], imOut[i], 1e-4, "im[%d]", i)
	}
}

func TestMakeHannWindowF32(t *testing.T) {
	w := makeHannWindowF32(4)
	require.Len(t, w, 4)
	assert.InDelta(t, 0.0, w[0], 1e-7)
	assert.Greater(t, w[1], float32(0))
	assert.Greater(t, w[2], float32(0))
	assert.Greater(t, w[3], float32(0)) // periodic: last != 0
}

func TestSTFTAnalysis_PeakBin(t *testing.T) {
	N := pvFFTSize
	hop := N / pvHopDivisor
	pv := newPhaseVocoder(N, hop, 48000)

	// 1000 Hz sine
	signal := make([]float32, N)
	for i := range signal {
		signal[i] = float32(math.Sin(2 * math.Pi * 1000 * float64(i) / 48000))
	}

	mag, _ := pv.analyzeFrame(signal)
	require.Len(t, mag, pvNumBins)

	// Find peak
	peakBin := 0
	peakMag := float32(0)
	for i, m := range mag {
		if m > peakMag {
			peakMag = m
			peakBin = i
		}
	}

	expectedBin := int(math.Round(1000.0 * float64(N) / 48000.0))
	assert.InDelta(t, expectedBin, peakBin, 1, "peak bin for 1000Hz")
}

func TestSTFT_AnalysisSynthesis_Roundtrip(t *testing.T) {
	N := pvFFTSize
	hop := N / pvHopDivisor
	pv := newPhaseVocoder(N, hop, 48000)

	// Create a test signal
	signal := make([]float32, N)
	for i := range signal {
		signal[i] = float32(math.Sin(2*math.Pi*440*float64(i)/48000) +
			0.5*math.Sin(2*math.Pi*880*float64(i)/48000))
	}

	mag, phase := pv.analyzeFrame(signal)
	// Copy since they reference internal buffers
	magCopy := make([]float32, len(mag))
	phaseCopy := make([]float32, len(phase))
	copy(magCopy, mag)
	copy(phaseCopy, phase)

	output := pv.synthesizeFrame(magCopy, phaseCopy)
	require.Len(t, output, N)

	// The output should resemble the input (with windowing applied twice)
	// Just verify it's not all zeros and has reasonable energy
	var energy float64
	for _, s := range output {
		energy += float64(s) * float64(s)
	}
	assert.Greater(t, energy, 1.0, "output should have energy")
}

func TestInstantaneousFrequency_PureTone(t *testing.T) {
	N := pvFFTSize
	hop := N / pvHopDivisor
	pv := newPhaseVocoder(N, hop, 48000)

	freq := 440.0
	frame1 := make([]float32, N)
	frame2 := make([]float32, N)
	for i := 0; i < N; i++ {
		frame1[i] = float32(math.Sin(2 * math.Pi * freq * float64(i) / 48000))
		frame2[i] = float32(math.Sin(2 * math.Pi * freq * float64(i+hop) / 48000))
	}

	_, phase1 := pv.analyzeFrame(frame1)
	phase1Copy := make([]float32, len(phase1))
	copy(phase1Copy, phase1)

	_, phase2 := pv.analyzeFrame(frame2)

	// Expected bin for 440 Hz
	binIdx := int(math.Round(freq * float64(N) / 48000))

	instFreq := pv.instantaneousFrequency(phase1Copy[binIdx], phase2[binIdx], binIdx)
	assert.InDelta(t, freq, instFreq, 5.0, "instantaneous frequency")
}

func TestFindSpectralPeaks(t *testing.T) {
	mag := make([]float32, 100)
	mag[10] = 5.0
	mag[9] = 2.0
	mag[11] = 3.0
	mag[50] = 8.0
	mag[49] = 4.0
	mag[51] = 4.0

	peaks := findSpectralPeaks(mag)
	require.Contains(t, peaks, 10)
	require.Contains(t, peaks, 50)
	require.NotContains(t, peaks, 9)
	require.NotContains(t, peaks, 11)
	require.NotContains(t, peaks, 49)
}

func TestNearestPeak(t *testing.T) {
	peaks := []int{10, 50, 80}
	assert.Equal(t, 10, nearestPeak(peaks, 8))
	assert.Equal(t, 10, nearestPeak(peaks, 12))
	assert.Equal(t, 50, nearestPeak(peaks, 45))
	assert.Equal(t, 80, nearestPeak(peaks, 70))
	assert.Equal(t, 80, nearestPeak(peaks, 90))
}

func TestFindSpectralPeaks_Empty(t *testing.T) {
	// Flat spectrum: no local maxima
	mag := make([]float32, 10)
	for i := range mag {
		mag[i] = 1.0
	}
	peaks := findSpectralPeaks(mag)
	assert.Empty(t, peaks)
}

func TestNearestPeak_NoPeaks(t *testing.T) {
	// No peaks, should return self
	result := nearestPeak(nil, 5)
	assert.Equal(t, 5, result)
}
