package audio

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/zsiec/prism/media"
)

// newProduceTestMixer creates a Mixer for ProduceSamples testing.
// No encoder is needed since ProduceSamples returns raw PCM.
func newProduceTestMixer(t *testing.T) *Mixer {
	t.Helper()
	m := NewMixer(MixerConfig{
		SampleRate: 48000,
		Channels:   2,
		Output:     func(_ *media.AudioFrame) {},
	})
	t.Cleanup(func() { _ = m.Close() })
	return m
}

func TestProduceSamplesBasic(t *testing.T) {
	t.Parallel()
	m := newProduceTestMixer(t)

	m.AddChannel("cam1")
	m.SetActive("cam1", true)

	// Generate 1 second of 1kHz sine (48000 samples * 2 channels = 96000 values)
	sine := generateSine(1000, 48000, 48000)

	// Push into ring buffer
	m.mu.Lock()
	m.channels["cam1"].ringBuf.Push(sine)
	m.mu.Unlock()

	// Request 1601 samples (1601 * 2 channels = 3202 values)
	result := m.ProduceSamples(1601)
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

	// Push enough data for all requests
	sine := generateSine(1000, 48000, 48000)
	m.mu.Lock()
	m.channels["cam1"].ringBuf.Push(sine)
	m.mu.Unlock()

	counts := []int{1601, 1602, 1920, 800}
	for _, count := range counts {
		result := m.ProduceSamples(count)
		assert.Lenf(t, result, count*2, "ProduceSamples(%d) should return %d values", count, count*2)
	}
}

func TestProduceSamplesEmpty(t *testing.T) {
	t.Parallel()
	m := newProduceTestMixer(t)

	// No channels, no audio fed. Should return silence of correct length.
	result := m.ProduceSamples(1601)
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

	m.mu.Lock()
	m.channels["cam1"].ringBuf.Push(sineData)
	m.channels["cam2"].ringBuf.Push(silenceData)
	m.mu.Unlock()

	// Get baseline RMS with no crossfade (both channels contribute normally)
	result1 := m.ProduceSamples(1024)
	rms1 := rms(result1)

	// Now push more data and set up crossfade at midpoint
	sineData2 := generateSine(1000, 48000, 4096)
	silenceData2 := make([]float32, 4096*2)
	m.mu.Lock()
	m.channels["cam1"].ringBuf.Push(sineData2)
	m.channels["cam2"].ringBuf.Push(silenceData2)
	m.mu.Unlock()

	// Start a crossfade: cam1 -> cam2
	m.OnCut("cam1", "cam2")

	// Advance crossfade to midpoint
	m.mu.Lock()
	m.transCrossfadePosition = 0.5
	m.mu.Unlock()

	result2 := m.ProduceSamples(1024)
	rms2 := rms(result2)

	// At midpoint of equal-power crossfade from cam1 (sine) to cam2 (silence):
	// cam1 gain = cos(0.5 * pi/2) ~= 0.707
	// cam2 gain = sin(0.5 * pi/2) ~= 0.707 (but cam2 is silence, so 0)
	// So output should be reduced compared to full cam1.
	assert.Greater(t, rms1, 0.01, "baseline RMS should be non-zero")
	assert.Greater(t, rms2, 0.01, "crossfade RMS should be non-zero")
	assert.Less(t, rms2, rms1, "crossfade at midpoint should reduce output (cam1 fading out, cam2 is silence)")
}

func TestProduceSamplesMatchesTick(t *testing.T) {
	t.Parallel()

	// Create two identical mixers with the same audio data.
	// One uses tick(), the other uses ProduceSamples().
	// The raw PCM before AAC encoding should be identical.

	enc := &mockTickEncoder{}
	m1 := NewMixer(MixerConfig{
		SampleRate: 48000,
		Channels:   2,
		Output:     func(_ *media.AudioFrame) {},
		EncoderFactory: func(sampleRate, channels int) (Encoder, error) {
			return enc, nil
		},
	})
	t.Cleanup(func() { _ = m1.Close() })

	m2 := newProduceTestMixer(t)

	// Same channel setup
	m1.AddChannel("cam1")
	m1.SetActive("cam1", true)
	m2.AddChannel("cam1")
	m2.SetActive("cam1", true)

	// Same PCM data
	pcm := make([]float32, 1024*2)
	for i := range pcm {
		pcm[i] = 0.25
	}

	// Push identical data into both mixers' ring buffers
	m1.mu.Lock()
	m1.channels["cam1"].ringBuf.Push(pcm)
	m1.mu.Unlock()

	m2.mu.Lock()
	m2.channels["cam1"].ringBuf.Push(pcm)
	m2.mu.Unlock()

	// Call tick() on m1 to get the PCM that went to the encoder
	frame := m1.tick()
	require.NotNil(t, frame, "tick should produce output")
	tickPCM := enc.getLastInput()
	require.NotNil(t, tickPCM)
	require.Len(t, tickPCM, 1024*2)

	// Call ProduceSamples on m2 to get the same mix
	producePCM := m2.ProduceSamples(1024)
	require.Len(t, producePCM, 1024*2)

	// Compare sample by sample — should be identical
	for i := range tickPCM {
		assert.InDelta(t, tickPCM[i], producePCM[i], 1e-6,
			"sample %d: tick() PCM (%f) should match ProduceSamples() PCM (%f)",
			i, tickPCM[i], producePCM[i])
	}
}

func TestExistingTickerStillWorks(t *testing.T) {
	t.Parallel()

	// Verify that the refactored tick() still produces correct output
	// by running the same scenario as TestTick_SingleChannel_ProducesOutput
	// and TestTick_TwoChannels_SumsOutput.

	t.Run("single_channel", func(t *testing.T) {
		t.Parallel()
		m, enc := newTickTestMixer(t)

		m.AddChannel("cam1")
		m.SetActive("cam1", true)

		pcm := make([]float32, 1024*2)
		for i := range pcm {
			pcm[i] = 0.25
		}

		m.mu.Lock()
		m.channels["cam1"].ringBuf.Push(pcm)
		m.mu.Unlock()

		frame := m.tick()
		require.NotNil(t, frame, "tick should produce output")
		require.NotEmpty(t, frame.Data)
		assert.Equal(t, 48000, frame.SampleRate)
		assert.Equal(t, 2, frame.Channels)

		// Encoder called: 1 priming + 1 tick
		assert.Equal(t, 2, enc.getCalls())

		lastPCM := enc.getLastInput()
		require.NotNil(t, lastPCM)
		require.Len(t, lastPCM, 1024*2)
		assert.InDelta(t, 0.25, lastPCM[0], 0.01)
	})

	t.Run("two_channels_sum", func(t *testing.T) {
		t.Parallel()
		m, enc := newTickTestMixer(t)

		m.AddChannel("cam1")
		m.AddChannel("cam2")
		m.SetActive("cam1", true)
		m.SetActive("cam2", true)

		pcm1 := make([]float32, 1024*2)
		pcm2 := make([]float32, 1024*2)
		for i := range pcm1 {
			pcm1[i] = 0.3
			pcm2[i] = 0.2
		}

		m.mu.Lock()
		m.channels["cam1"].ringBuf.Push(pcm1)
		m.channels["cam2"].ringBuf.Push(pcm2)
		m.mu.Unlock()

		frame := m.tick()
		require.NotNil(t, frame)
		require.NotEmpty(t, frame.Data)

		lastPCM := enc.getLastInput()
		require.NotNil(t, lastPCM)
		require.Len(t, lastPCM, 1024*2)
		assert.InDelta(t, 0.5, lastPCM[0], 0.01, "0.3 + 0.2 = 0.5")
	})

	t.Run("no_encoder_advances_crossfade", func(t *testing.T) {
		t.Parallel()
		// Mixer with no encoder factory — tick should still advance crossfade
		m := NewMixer(MixerConfig{
			SampleRate: 48000,
			Channels:   2,
			Output:     func(_ *media.AudioFrame) {},
		})
		t.Cleanup(func() { _ = m.Close() })

		m.AddChannel("cam1")
		m.AddChannel("cam2")
		m.SetActive("cam1", true)

		m.OnCut("cam1", "cam2")

		m.mu.RLock()
		assert.Equal(t, 4, m.cutFramesRemaining)
		m.mu.RUnlock()

		// tick with no encoder should still advance the crossfade
		frame := m.tick()
		assert.Nil(t, frame, "no encoder means nil frame")

		m.mu.RLock()
		assert.Equal(t, 3, m.cutFramesRemaining, "crossfade should advance even without encoder")
		m.mu.RUnlock()
	})

	t.Run("muted_channel_excluded", func(t *testing.T) {
		t.Parallel()
		m, enc := newTickTestMixer(t)

		m.AddChannel("cam1")
		m.SetActive("cam1", true)
		_ = m.SetMuted("cam1", true)

		pcm := make([]float32, 1024*2)
		for i := range pcm {
			pcm[i] = 0.9
		}
		m.mu.Lock()
		m.channels["cam1"].ringBuf.Push(pcm)
		m.mu.Unlock()

		frame := m.tick()
		require.NotNil(t, frame)

		lastPCM := enc.getLastInput()
		require.NotNil(t, lastPCM)
		for _, v := range lastPCM {
			assert.Equal(t, float32(0), v, "muted channel should produce silence")
		}
	})
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

	// AdvanceCrossfade should advance by one tick
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
