package audio

import (
	"math"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// generateSine creates a stereo interleaved sine wave at the given frequency.
func generateSine(freq, sampleRate float64, numSamples int) []float32 {
	pcm := make([]float32, numSamples*2) // stereo
	for i := 0; i < numSamples; i++ {
		val := float32(math.Sin(2 * math.Pi * freq * float64(i) / sampleRate))
		pcm[i*2] = val
		pcm[i*2+1] = val
	}
	return pcm
}

// rms computes the root-mean-square of a float32 slice.
func rms(pcm []float32) float64 {
	var sum float64
	for _, s := range pcm {
		sum += float64(s) * float64(s)
	}
	return math.Sqrt(sum / float64(len(pcm)))
}

func TestHPF_NewStartsBypassed(t *testing.T) {
	t.Parallel()
	h := NewHPF(48000)
	require.True(t, h.IsBypassed(), "new HPF should be bypassed")
	require.False(t, h.Enabled(), "new HPF should not be enabled")
	require.InDelta(t, 80.0, h.Frequency(), 0.01, "default frequency should be 80 Hz")
}

func TestHPF_SetParamsValidatesFrequency(t *testing.T) {
	t.Parallel()
	h := NewHPF(48000)

	// Below minimum
	err := h.SetParams(10, true)
	require.Error(t, err, "frequency below 20 Hz should be rejected")

	// Above maximum
	err = h.SetParams(600, true)
	require.Error(t, err, "frequency above 500 Hz should be rejected")

	// Valid range
	err = h.SetParams(20, true)
	require.NoError(t, err, "20 Hz should be accepted")

	err = h.SetParams(500, true)
	require.NoError(t, err, "500 Hz should be accepted")

	err = h.SetParams(100, true)
	require.NoError(t, err, "100 Hz should be accepted")
}

func TestHPF_SetParamsUpdatesState(t *testing.T) {
	t.Parallel()
	h := NewHPF(48000)

	err := h.SetParams(120, true)
	require.NoError(t, err)
	require.True(t, h.Enabled())
	require.InDelta(t, 120.0, h.Frequency(), 0.01)
	require.False(t, h.IsBypassed())

	// Disable
	err = h.SetParams(120, false)
	require.NoError(t, err)
	require.False(t, h.Enabled())
	require.True(t, h.IsBypassed())
}

func TestHPF_ProcessAttenuatesLowFrequencies(t *testing.T) {
	t.Parallel()
	h := NewHPF(48000)
	err := h.SetParams(200, true)
	require.NoError(t, err)

	// Generate a 50 Hz sine wave (well below 200 Hz cutoff)
	pcm := generateSine(50, 48000, 4096)
	original := make([]float32, len(pcm))
	copy(original, pcm)

	h.Process(pcm, 2)

	// The 50 Hz signal should be significantly attenuated
	origRMS := rms(original)
	filteredRMS := rms(pcm)

	// At 50 Hz with 200 Hz cutoff (2nd order), expect >12 dB attenuation
	// (2 octaves below cutoff * 12 dB/octave = 24 dB theoretical)
	attenuation := 20 * math.Log10(filteredRMS/origRMS)
	assert.Less(t, attenuation, -12.0,
		"50 Hz signal should be attenuated >12 dB by HPF at 200 Hz, got %.1f dB", attenuation)
}

func TestHPF_ProcessPassesHighFrequencies(t *testing.T) {
	t.Parallel()
	h := NewHPF(48000)
	err := h.SetParams(100, true)
	require.NoError(t, err)

	// Generate a 5 kHz sine wave (well above 100 Hz cutoff)
	pcm := generateSine(5000, 48000, 4096)
	original := make([]float32, len(pcm))
	copy(original, pcm)

	// Let filter settle (process several blocks to eliminate transient)
	warmup := generateSine(5000, 48000, 4096)
	h.Process(warmup, 2)

	h.Process(pcm, 2)

	// The 5 kHz signal should pass through nearly unchanged
	origRMS := rms(original)
	filteredRMS := rms(pcm)

	// Allow small deviation (< 0.5 dB)
	attenuation := math.Abs(20 * math.Log10(filteredRMS/origRMS))
	assert.Less(t, attenuation, 0.5,
		"5 kHz signal should pass through HPF at 100 Hz with <0.5 dB change, got %.2f dB", attenuation)
}

func TestHPF_IsBypassedWhenDisabled(t *testing.T) {
	t.Parallel()
	h := NewHPF(48000)

	// Enable then disable
	err := h.SetParams(100, true)
	require.NoError(t, err)
	require.False(t, h.IsBypassed())

	err = h.SetParams(100, false)
	require.NoError(t, err)
	require.True(t, h.IsBypassed())
}

func TestHPF_ProcessNoOpWhenBypassed(t *testing.T) {
	t.Parallel()
	h := NewHPF(48000)
	// Leave disabled (default)

	pcm := generateSine(50, 48000, 1024)
	original := make([]float32, len(pcm))
	copy(original, pcm)

	h.Process(pcm, 2)

	// Signal should be completely unchanged when bypassed
	for i := range pcm {
		require.Equal(t, original[i], pcm[i],
			"sample %d should be unchanged when HPF is bypassed", i)
	}
}

func TestHPF_ResetClearsFilterState(t *testing.T) {
	t.Parallel()
	h := NewHPF(48000)
	err := h.SetParams(200, true)
	require.NoError(t, err)

	// Process some audio to build up filter state
	pcm := generateSine(50, 48000, 1024)
	h.Process(pcm, 2)

	// Reset should clear internal state
	h.Reset()

	// After reset, filter state should be zeroed
	assert.Equal(t, 0.0, h.filterL.s1, "left filter s1 should be 0 after reset")
	assert.Equal(t, 0.0, h.filterL.s2, "left filter s2 should be 0 after reset")
	assert.Equal(t, 0.0, h.filterR.s1, "right filter s1 should be 0 after reset")
	assert.Equal(t, 0.0, h.filterR.s2, "right filter s2 should be 0 after reset")
}

func TestHPF_MonoProcessing(t *testing.T) {
	t.Parallel()
	h := NewHPF(48000)
	err := h.SetParams(200, true)
	require.NoError(t, err)

	// Generate mono 50 Hz sine
	numSamples := 4096
	pcm := make([]float32, numSamples)
	for i := 0; i < numSamples; i++ {
		pcm[i] = float32(math.Sin(2 * math.Pi * 50.0 * float64(i) / 48000.0))
	}
	original := make([]float32, len(pcm))
	copy(original, pcm)

	h.Process(pcm, 1)

	origRMS := rms(original)
	filteredRMS := rms(pcm)

	attenuation := 20 * math.Log10(filteredRMS/origRMS)
	assert.Less(t, attenuation, -12.0,
		"mono 50 Hz signal should be attenuated >12 dB by HPF at 200 Hz")
}

func TestHPF_StereoIndependence(t *testing.T) {
	t.Parallel()
	h := NewHPF(48000)
	err := h.SetParams(100, true)
	require.NoError(t, err)

	// Create stereo signal: left=50Hz (should be cut), right=5kHz (should pass)
	numSamples := 4096
	pcm := make([]float32, numSamples*2)
	for i := 0; i < numSamples; i++ {
		pcm[i*2] = float32(math.Sin(2 * math.Pi * 50.0 * float64(i) / 48000.0))
		pcm[i*2+1] = float32(math.Sin(2 * math.Pi * 5000.0 * float64(i) / 48000.0))
	}

	// Warmup for the right channel (5kHz) to reach steady state
	warmup := make([]float32, 4096*2)
	for i := 0; i < 4096; i++ {
		warmup[i*2] = float32(math.Sin(2 * math.Pi * 50.0 * float64(i) / 48000.0))
		warmup[i*2+1] = float32(math.Sin(2 * math.Pi * 5000.0 * float64(i) / 48000.0))
	}
	h.Process(warmup, 2)

	h.Process(pcm, 2)

	// Measure left channel (50Hz) RMS
	var leftRMS, rightRMS float64
	for i := 0; i < numSamples; i++ {
		leftRMS += float64(pcm[i*2]) * float64(pcm[i*2])
		rightRMS += float64(pcm[i*2+1]) * float64(pcm[i*2+1])
	}
	leftRMS = math.Sqrt(leftRMS / float64(numSamples))
	rightRMS = math.Sqrt(rightRMS / float64(numSamples))

	// Left (50 Hz) should be heavily attenuated, right (5 kHz) should pass
	assert.Less(t, leftRMS, 0.2, "left channel (50 Hz) should be heavily attenuated")
	assert.Greater(t, rightRMS, 0.5, "right channel (5 kHz) should pass through")
}

func TestMixer_SetHPF(t *testing.T) {
	t.Parallel()
	m := NewMixer(MixerConfig{
		SampleRate: 48000,
		Channels:   2,
	})
	defer func() { _ = m.Close() }()

	m.AddChannel("cam1")

	// Set HPF
	err := m.SetHPF("cam1", 120, true)
	require.NoError(t, err)

	// Unknown source
	err = m.SetHPF("nonexistent", 120, true)
	require.Error(t, err)

	// Invalid frequency
	err = m.SetHPF("cam1", 10, true)
	require.Error(t, err)

	// Get HPF state
	freq, enabled := m.GetHPF("cam1")
	assert.InDelta(t, 120.0, freq, 0.01)
	assert.True(t, enabled)
}
