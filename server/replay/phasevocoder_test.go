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

	peaks := findSpectralPeaksInto(mag, nil)
	require.Contains(t, peaks, 10)
	require.Contains(t, peaks, 50)
	require.NotContains(t, peaks, 9)
	require.NotContains(t, peaks, 11)
	require.NotContains(t, peaks, 49)
}

func TestBuildNearestPeakMap(t *testing.T) {
	peaks := []int{10, 50, 80}
	nearest := buildNearestPeakMap(peaks, 100, make([]int, 100))
	assert.Equal(t, 10, nearest[8])
	assert.Equal(t, 10, nearest[12])
	assert.Equal(t, 50, nearest[45])
	assert.Equal(t, 80, nearest[70])
	assert.Equal(t, 80, nearest[90])
	// Peak bins map to themselves
	assert.Equal(t, 10, nearest[10])
	assert.Equal(t, 50, nearest[50])
	assert.Equal(t, 80, nearest[80])
}

func TestFindSpectralPeaks_Empty(t *testing.T) {
	// Flat spectrum: no local maxima
	mag := make([]float32, 10)
	for i := range mag {
		mag[i] = 1.0
	}
	peaks := findSpectralPeaksInto(mag, nil)
	assert.Empty(t, peaks)
}

func TestBuildNearestPeakMap_NoPeaks(t *testing.T) {
	// No peaks — every bin maps to itself
	nearest := buildNearestPeakMap(nil, 10, make([]int, 10))
	for i := 0; i < 10; i++ {
		assert.Equal(t, i, nearest[i])
	}
}

func TestSpectralFlux_SteadyState(t *testing.T) {
	mag := make([]float32, 100)
	for i := range mag {
		mag[i] = float32(i) * 0.01
	}
	flux := spectralFlux(mag, mag)
	assert.InDelta(t, 0.0, flux, 1e-6)
}

func TestSpectralFlux_Increase(t *testing.T) {
	prevMag := make([]float32, 100)
	curMag := make([]float32, 100)
	for i := range curMag {
		curMag[i] = 10.0
	}
	flux := spectralFlux(prevMag, curMag)
	assert.InDelta(t, 1000.0, flux, 1e-3) // 100 bins * 10.0
}

func TestTransientDetector_SteadyState(t *testing.T) {
	td := newTransientDetector(100, 20)
	steady := make([]float32, 100)
	for i := range steady {
		steady[i] = 1.0
	}
	// Feed steady frames — should never be transient after warmup
	for i := 0; i < 20; i++ {
		assert.False(t, td.isTransient(steady), "frame %d should not be transient", i)
	}
}

func TestTransientDetector_DetectsTransient(t *testing.T) {
	td := newTransientDetector(100, 20)
	steady := make([]float32, 100)
	for i := range steady {
		steady[i] = 1.0
	}
	// Warmup with steady frames
	for i := 0; i < 20; i++ {
		td.isTransient(steady)
	}
	// Sudden large increase
	transient := make([]float32, 100)
	for i := range transient {
		transient[i] = 50.0
	}
	assert.True(t, td.isTransient(transient))
}

func TestPhaseVocoderTimeStretch_HalfSpeed(t *testing.T) {
	input := make([]float32, 48000)
	for i := range input {
		input[i] = float32(math.Sin(2 * math.Pi * 440 * float64(i) / 48000))
	}
	output := PhaseVocoderTimeStretch(input, 1, 48000, 0.5)
	require.NotNil(t, output)
	expectedLen := len(input) * 2
	assert.InDelta(t, expectedLen, len(output), float64(expectedLen)*0.1)
}

func TestPhaseVocoderTimeStretch_QuarterSpeed(t *testing.T) {
	input := make([]float32, 48000)
	for i := range input {
		input[i] = float32(math.Sin(2 * math.Pi * 440 * float64(i) / 48000))
	}
	output := PhaseVocoderTimeStretch(input, 1, 48000, 0.25)
	require.NotNil(t, output)
	expectedLen := len(input) * 4
	assert.InDelta(t, expectedLen, len(output), float64(expectedLen)*0.15)
}

func TestPhaseVocoderTimeStretch_Stereo(t *testing.T) {
	input := make([]float32, 48000*2)
	for i := 0; i < 48000; i++ {
		v := float32(math.Sin(2 * math.Pi * 440 * float64(i) / 48000))
		input[i*2] = v
		input[i*2+1] = v * 0.5
	}
	output := PhaseVocoderTimeStretch(input, 2, 48000, 0.5)
	require.NotNil(t, output)
	expectedLen := len(input) * 2
	assert.InDelta(t, expectedLen, len(output), float64(expectedLen)*0.1)
	assert.Equal(t, 0, len(output)%2, "output length must be even for stereo")
}

func TestPhaseVocoderTimeStretch_Passthrough(t *testing.T) {
	input := make([]float32, 48000)
	for i := range input {
		input[i] = float32(math.Sin(2 * math.Pi * 440 * float64(i) / 48000))
	}
	output := PhaseVocoderTimeStretch(input, 1, 48000, 1.0)
	require.Len(t, output, len(input))
	for i := range input {
		assert.InDelta(t, input[i], output[i], 1e-6)
	}
}

func TestPhaseVocoderTimeStretch_Empty(t *testing.T) {
	output := PhaseVocoderTimeStretch(nil, 1, 48000, 0.5)
	assert.Nil(t, output)
	output = PhaseVocoderTimeStretch([]float32{}, 1, 48000, 0.5)
	assert.Nil(t, output)
}

func TestPhaseVocoderTimeStretch_SpeedClamping(t *testing.T) {
	input := make([]float32, 48000)
	for i := range input {
		input[i] = float32(math.Sin(2 * math.Pi * 440 * float64(i) / 48000))
	}
	// Speed < 0.1 should be clamped
	output := PhaseVocoderTimeStretch(input, 1, 48000, 0.01)
	require.NotNil(t, output)
	require.Greater(t, len(output), len(input))
}

func TestPhaseVocoderTimeStretch_ShortInput(t *testing.T) {
	// Input shorter than FFT window — should return nil (too short)
	input := make([]float32, 100)
	for i := range input {
		input[i] = 0.5
	}
	output := PhaseVocoderTimeStretch(input, 1, 48000, 0.5)
	assert.Nil(t, output)
}

func TestPhaseVocoderTimeStretch_OutputNotClipped(t *testing.T) {
	input := make([]float32, 48000)
	for i := range input {
		input[i] = float32(math.Sin(2*math.Pi*440*float64(i)/48000) +
			0.5*math.Sin(2*math.Pi*880*float64(i)/48000))
	}
	output := PhaseVocoderTimeStretch(input, 1, 48000, 0.5)
	require.NotNil(t, output)
	for i, s := range output {
		assert.LessOrEqual(t, s, float32(0.96), "sample %d clipping positive", i)
		assert.GreaterOrEqual(t, s, float32(-0.96), "sample %d clipping negative", i)
	}
}
