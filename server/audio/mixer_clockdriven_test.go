package audio

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/zsiec/prism/media"
)

// TestClockDrivenMixerNoTicker verifies that creating a mixer with
// ClockDriven: true does NOT start the internal ticker goroutine.
func TestClockDrivenMixerNoTicker(t *testing.T) {
	t.Parallel()

	var outputCount int
	enc := &mockTickEncoder{}
	m := NewMixer(MixerConfig{
		SampleRate:  48000,
		Channels:    2,
		ClockDriven: true,
		Output: func(frame *media.AudioFrame) {
			outputCount++
		},
		EncoderFactory: func(sampleRate, channels int) (Encoder, error) {
			return enc, nil
		},
	})
	defer func() { _ = m.Close() }()

	// stopTicker should be nil when ClockDriven is true
	assert.Nil(t, m.stopTicker, "stopTicker channel should be nil for ClockDriven mixer")

	// Wait long enough that a ticker would have fired (~4 ticks at ~21.3ms)
	time.Sleep(100 * time.Millisecond)

	// No output should have been produced by the ticker
	assert.Equal(t, 0, outputCount, "ClockDriven mixer should not produce output via ticker")

	// But ProduceSamples should still work
	m.AddChannel("cam1")
	m.SetActive("cam1", true)

	pcm := make([]float32, 1024*2)
	for i := range pcm {
		pcm[i] = 0.4
	}
	m.mu.Lock()
	m.channels["cam1"].ringBuf.Push(pcm)
	m.mu.Unlock()

	result := m.ProduceSamples(1024)
	require.Len(t, result, 1024*2, "ProduceSamples should return correct length")

	// Verify non-silence
	hasNonZero := false
	for _, v := range result {
		if v != 0 {
			hasNonZero = true
			break
		}
	}
	assert.True(t, hasNonZero, "ProduceSamples should return non-silent output from ring buffer")
}

// TestClockDrivenMixerProduceSamples verifies the full ProduceSamples path
// on a ClockDriven mixer: feed audio, produce samples, get non-silent output.
func TestClockDrivenMixerProduceSamples(t *testing.T) {
	t.Parallel()

	m := NewMixer(MixerConfig{
		SampleRate:  48000,
		Channels:    2,
		ClockDriven: true,
		Output:      func(_ *media.AudioFrame) {},
	})
	defer func() { _ = m.Close() }()

	m.AddChannel("cam1")
	m.SetActive("cam1", true)

	// Push audio via IngestPCM
	pcm := make([]float32, 1601*2)
	for i := range pcm {
		pcm[i] = 0.35
	}
	m.IngestPCM("cam1", pcm, 0, 2)

	// Request exactly 1601 samples
	result := m.ProduceSamples(1601)
	require.Len(t, result, 1601*2, "should return exactly 1601*2 float32 values")

	// Verify non-silence
	r := rms(result)
	assert.Greater(t, r, 0.01, "RMS should be > 0.01 (not silence), got %f", r)
}

// TestNonClockDrivenMixerStillWorks verifies that the default (non-ClockDriven)
// mixer still starts the ticker and produces output automatically.
func TestNonClockDrivenMixerStillWorks(t *testing.T) {
	t.Parallel()

	var outputCount int
	enc := &mockTickEncoder{}
	m := NewMixer(MixerConfig{
		SampleRate: 48000,
		Channels:   2,
		// ClockDriven defaults to false
		Output: func(frame *media.AudioFrame) {
			outputCount++
		},
		EncoderFactory: func(sampleRate, channels int) (Encoder, error) {
			return enc, nil
		},
	})

	// stopTicker should be non-nil (ticker was started)
	assert.NotNil(t, m.stopTicker, "stopTicker channel should exist for non-ClockDriven mixer")

	// Wait for ticker to fire
	time.Sleep(100 * time.Millisecond)
	_ = m.Close()

	assert.GreaterOrEqual(t, outputCount, 2,
		"non-ClockDriven mixer should produce output via ticker (got %d)", outputCount)
}

// TestClockDrivenMixerClose verifies that closing a ClockDriven mixer
// does not hang (no ticker goroutine to wait for).
func TestClockDrivenMixerClose(t *testing.T) {
	t.Parallel()

	m := NewMixer(MixerConfig{
		SampleRate:  48000,
		Channels:    2,
		ClockDriven: true,
		Output:      func(_ *media.AudioFrame) {},
	})

	// Close should return immediately without hanging
	done := make(chan struct{})
	go func() {
		_ = m.Close()
		close(done)
	}()

	select {
	case <-done:
		// success
	case <-time.After(1 * time.Second):
		t.Fatal("Close() on ClockDriven mixer should not hang")
	}

	// Close again should be safe (idempotent)
	require.NoError(t, m.Close(), "second Close must not panic")
}

// TestClockDrivenMixerSeedPTSIgnored verifies that SeedPTSFromVideo and
// ExpectVideoSeed are no-ops when ClockDriven is true.
func TestClockDrivenMixerSeedPTSIgnored(t *testing.T) {
	t.Parallel()

	m := NewMixer(MixerConfig{
		SampleRate:  48000,
		Channels:    2,
		ClockDriven: true,
		Output:      func(_ *media.AudioFrame) {},
	})
	defer func() { _ = m.Close() }()

	// These should be no-ops (no panic, no state change)
	m.ExpectVideoSeed()
	m.SeedPTSFromVideo(90000)

	// Verify no PTS state was set
	m.mu.RLock()
	assert.False(t, m.videoSeedExpected, "ExpectVideoSeed should be ignored for ClockDriven mixer")
	assert.False(t, m.outputPTSInited, "SeedPTSFromVideo should be ignored for ClockDriven mixer")
	assert.Equal(t, int64(0), m.outputPTS, "outputPTS should remain 0 for ClockDriven mixer")
	m.mu.RUnlock()
}

// TestClockDrivenMixerCrossfadeWorks verifies that crossfade state works
// correctly with ProduceSamples + AdvanceCrossfade on a ClockDriven mixer.
func TestClockDrivenMixerCrossfadeWorks(t *testing.T) {
	t.Parallel()

	m := NewMixer(MixerConfig{
		SampleRate:  48000,
		Channels:    2,
		ClockDriven: true,
		Output:      func(_ *media.AudioFrame) {},
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
	m.mu.Lock()
	m.channels["cam1"].ringBuf.Push(pcm)
	m.channels["cam2"].ringBuf.Push(pcm)
	m.mu.Unlock()

	// Trigger cut crossfade
	m.OnCut("cam1", "cam2")

	m.mu.RLock()
	assert.Equal(t, 4, m.cutFramesRemaining)
	assert.True(t, m.transCrossfadeActive)
	m.mu.RUnlock()

	// ProduceSamples + AdvanceCrossfade should advance the crossfade
	for i := 0; i < 4; i++ {
		_ = m.ProduceSamples(1024)
		m.AdvanceCrossfade()
	}

	m.mu.RLock()
	assert.False(t, m.transCrossfadeActive, "crossfade should be complete after 4 cycles")
	assert.Equal(t, 0, m.cutFramesRemaining)
	m.mu.RUnlock()
}
