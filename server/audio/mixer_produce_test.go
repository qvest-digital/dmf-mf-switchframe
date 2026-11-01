package audio

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newProduceTestMixer creates a Mixer for ProduceSamples testing.
func newProduceTestMixer(t *testing.T) *Mixer {
	t.Helper()
	m := NewMixer(MixerConfig{
		SampleRate: 48000,
		Channels:   2,
	})
	t.Cleanup(func() { _ = m.Close() })
	return m
}

func TestProduceSamplesBasic(t *testing.T) {
	t.Parallel()
	m := newProduceTestMixer(t)

	m.AddChannel("cam1")
	m.SetActive("cam1", true)

	// 1kHz sine, within the ring's capacity
	sine := generateSine(1000, 48000, 8192)

	tl := newTimeline()
	tl.push(m, "cam1", sine, 0)

	// Request 1601 samples (1601 * 2 channels = 3202 values)
	result := tl.produce(m, 1601)
	require.Len(t, result, 1601*2, "should return exactly 1601*2 float32 values")

	// Verify it's not silence
	r := rms(result)
	assert.Greater(t, r, 0.01, "RMS should be > 0.01 (not silence), got %f", r)
}

func TestProduceSamplesVariableCount(t *testing.T) {
	t.Parallel()
	m := newProduceTestMixer(t)

	m.AddChannel("cam1")
	m.SetActive("cam1", true)

	// Push enough data for all requests, within the ring's capacity, at
	// half scale so the master limiter leaves it as it is
	sine := generateSine(1000, 48000, 8192)
	for i := range sine {
		sine[i] *= 0.5
	}
	tl := newTimeline()
	tl.push(m, "cam1", sine, 0)

	counts := []int{1601, 1602, 1920, 800}
	read := 0
	for _, count := range counts {
		result := tl.produce(m, count)
		require.Lenf(t, result, count*2, "ProduceSamples(%d) should return %d values", count, count*2)
		// Each call continues the source where the previous one stopped.
		assert.InDeltaSlicef(t, sine[read*2:(read+count)*2], result, 1e-6,
			"ProduceSamples(%d) should return the source's next %d samples", count, count)
		read += count
	}
}

func TestProduceSamplesEmpty(t *testing.T) {
	t.Parallel()
	m := newProduceTestMixer(t)

	// No channels, no audio fed. Should return silence of correct length.
	result := newTimeline().produce(m, 1601)
	require.Len(t, result, 1601*2, "should return exactly 1601*2 values")

	// Verify all zeros (silence)
	for i, v := range result {
		assert.Equal(t, float32(0), v, "sample %d should be zero (silence)", i)
	}
}

func TestProduceSamplesCrossfade(t *testing.T) {
	t.Parallel()
	m := newProduceTestMixer(t)

	m.AddChannel("cam1")
	m.AddChannel("cam2")
	m.SetActive("cam1", true)
	m.SetActive("cam2", true)

	// cam1: 1kHz sine, cam2: silence
	sineData := generateSine(1000, 48000, 4096)
	silenceData := make([]float32, 4096*2)

	tl := newTimeline()
	tl.push(m, "cam1", sineData, 0)
	tl.push(m, "cam2", silenceData, 0)

	// Get baseline RMS with no crossfade (both channels contribute normally)
	result1 := tl.produce(m, 1024)
	rms1 := rms(result1)

	// Now push more data and set up crossfade at midpoint
	sineData2 := generateSine(1000, 48000, 4096)
	silenceData2 := make([]float32, 4096*2)
	tl.push(m, "cam1", sineData2, 4096)
	tl.push(m, "cam2", silenceData2, 4096)

	// Start a crossfade: cam1 -> cam2
	m.OnCut("cam1", "cam2")

	// Advance crossfade to midpoint
	m.mu.Lock()
	m.transCrossfadePosition = 0.5
	m.mu.Unlock()

	result2 := tl.produce(m, 1024)
	rms2 := rms(result2)

	// At midpoint of equal-power crossfade from cam1 (sine) to cam2 (silence):
	// cam1 gain = cos(0.5 * pi/2) ~= 0.707
	// cam2 gain = sin(0.5 * pi/2) ~= 0.707 (but cam2 is silence, so 0)
	// So output should be reduced compared to full cam1.
	assert.Greater(t, rms1, 0.01, "baseline RMS should be non-zero")
	assert.Greater(t, rms2, 0.01, "crossfade RMS should be non-zero")
	assert.Less(t, rms2, rms1, "crossfade at midpoint should reduce output (cam1 fading out, cam2 is silence)")
}

func TestAdvanceCrossfade(t *testing.T) {
	t.Parallel()
	m := newProduceTestMixer(t)

	m.AddChannel("cam1")
	m.AddChannel("cam2")
	m.SetActive("cam1", true)

	// Set up a cut crossfade
	m.OnCut("cam1", "cam2")

	m.mu.RLock()
	assert.Equal(t, 4, m.cutFramesRemaining)
	assert.True(t, m.transCrossfadeActive)
	m.mu.RUnlock()

	// AdvanceCrossfade should advance by one period
	m.AdvanceCrossfade()

	m.mu.RLock()
	assert.Equal(t, 3, m.cutFramesRemaining)
	assert.InDelta(t, 0.25, m.transCrossfadePosition, 0.01)
	assert.True(t, m.transCrossfadeActive)
	m.mu.RUnlock()

	// Advance 3 more times to complete
	for i := 0; i < 3; i++ {
		m.AdvanceCrossfade()
	}

	m.mu.RLock()
	assert.Equal(t, 0, m.cutFramesRemaining)
	assert.False(t, m.transCrossfadeActive, "crossfade should be complete after 4 advances")
	m.mu.RUnlock()
}

// TestMixerProduceSamples_IngestPCM verifies the full ProduceSamples path:
// feed audio, produce samples, get non-silent output.
func TestMixerProduceSamples_IngestPCM(t *testing.T) {
	t.Parallel()

	m := NewMixer(MixerConfig{
		SampleRate: 48000,
		Channels:   2,
	})
	defer func() { _ = m.Close() }()

	m.AddChannel("cam1")
	m.SetActive("cam1", true)

	// Push audio via IngestPCM
	pcm := make([]float32, 1601*2)
	for i := range pcm {
		pcm[i] = 0.35
	}
	tl := newTimeline()
	m.IngestPCM("cam1", pcm, tl.pts(0), 2)

	// Request exactly 1601 samples
	result := tl.produce(m, 1601)
	require.Len(t, result, 1601*2, "should return exactly 1601*2 float32 values")

	// Verify non-silence
	r := rms(result)
	assert.Greater(t, r, 0.01, "RMS should be > 0.01 (not silence), got %f", r)
}

// TestMixerCrossfade_ProduceSamplesAndAdvance verifies that crossfade state
// works correctly with ProduceSamples + AdvanceCrossfade.
func TestMixerCrossfade_ProduceSamplesAndAdvance(t *testing.T) {
	t.Parallel()

	m := NewMixer(MixerConfig{
		SampleRate: 48000,
		Channels:   2,
	})
	defer func() { _ = m.Close() }()

	m.AddChannel("cam1")
	m.AddChannel("cam2")
	m.SetActive("cam1", true)

	// Push audio for both channels
	pcm := make([]float32, 4096*2)
	for i := range pcm {
		pcm[i] = 0.5
	}
	tl := newTimeline()
	tl.push(m, "cam1", pcm, 0)
	tl.push(m, "cam2", pcm, 0)

	// Trigger cut crossfade
	m.OnCut("cam1", "cam2")

	m.mu.RLock()
	assert.Equal(t, 4, m.cutFramesRemaining)
	assert.True(t, m.transCrossfadeActive)
	m.mu.RUnlock()

	// ProduceSamples + AdvanceCrossfade should advance the crossfade
	for i := 0; i < 4; i++ {
		_ = tl.produce(m, 1024)
		m.AdvanceCrossfade()
	}

	m.mu.RLock()
	assert.False(t, m.transCrossfadeActive, "crossfade should be complete after 4 cycles")
	assert.Equal(t, 0, m.cutFramesRemaining)
	m.mu.RUnlock()
}
