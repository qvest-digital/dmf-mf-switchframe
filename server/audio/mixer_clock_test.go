package audio

import (
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/zsiec/prism/media"
)

// TestClockDrivenMixer_IngestPCMProducesOutput verifies the end-to-end flow:
// IngestPCM pushes PCM into a channel's ring buffer, and the outputTicker
// reads it and produces encoded output frames.
func TestClockDrivenMixer_IngestPCMProducesOutput(t *testing.T) {
	t.Parallel()

	var mu sync.Mutex
	var outputFrames []*media.AudioFrame

	enc := &mockTickEncoder{}
	m := NewMixer(MixerConfig{
		SampleRate: 48000,
		Channels:   2,
		Output: func(frame *media.AudioFrame) {
			mu.Lock()
			outputFrames = append(outputFrames, frame)
			mu.Unlock()
		},
		EncoderFactory: func(sampleRate, channels int) (Encoder, error) {
			return enc, nil
		},
	})
	defer func() { _ = m.Close() }()

	m.AddChannel("cam1")
	m.SetActive("cam1", true)

	// Push several PCM frames via IngestPCM (simulates MXL/SRT source)
	pcm := make([]float32, 1024*2)
	for i := range pcm {
		pcm[i] = 0.3
	}

	// Push 3 frames — the ticker should pick them up
	for i := 0; i < 3; i++ {
		m.IngestPCM("cam1", pcm, int64(i)*1920, 2)
	}

	// Wait for the ticker to fire a few times (~21ms per tick, wait 200ms for margin)
	deadline := time.After(200 * time.Millisecond)
	for {
		mu.Lock()
		count := len(outputFrames)
		mu.Unlock()
		if count >= 2 {
			break
		}
		select {
		case <-deadline:
			mu.Lock()
			count = len(outputFrames)
			mu.Unlock()
			require.GreaterOrEqual(t, count, 1,
				"expected at least 1 output frame within 200ms, got %d", count)
			// If we got at least 1, that's acceptable
			goto done
		case <-time.After(5 * time.Millisecond):
			// poll
		}
	}
done:

	mu.Lock()
	count := len(outputFrames)
	mu.Unlock()

	assert.GreaterOrEqual(t, count, 1, "outputTicker should have produced at least 1 frame")

	// Verify output frames have valid structure
	mu.Lock()
	for i, f := range outputFrames {
		assert.NotEmpty(t, f.Data, "frame %d should have AAC data", i)
		assert.Equal(t, 48000, f.SampleRate, "frame %d sample rate", i)
		assert.Equal(t, 2, f.Channels, "frame %d channels", i)
	}
	mu.Unlock()
}

// TestClockDrivenMixer_IngestFrameProducesOutput verifies that IngestFrame
// (AAC path) decodes, processes, and pushes to ring buffer for ticker output.
func TestClockDrivenMixer_IngestFrameProducesOutput(t *testing.T) {
	t.Parallel()

	var mu sync.Mutex
	var outputFrames []*media.AudioFrame

	// Track what the "decoder" produces
	decodedPCM := make([]float32, 1024*2)
	for i := range decodedPCM {
		decodedPCM[i] = 0.5
	}

	mockDec := &stubDecoder{pcm: decodedPCM}
	enc := &mockTickEncoder{}

	m := NewMixer(MixerConfig{
		SampleRate: 48000,
		Channels:   2,
		Output: func(frame *media.AudioFrame) {
			mu.Lock()
			outputFrames = append(outputFrames, frame)
			mu.Unlock()
		},
		DecoderFactory: func(sampleRate, channels int) (Decoder, error) {
			return mockDec, nil
		},
		EncoderFactory: func(sampleRate, channels int) (Encoder, error) {
			return enc, nil
		},
	})
	defer func() { _ = m.Close() }()

	m.AddChannel("cam1")
	m.SetActive("cam1", true)

	// Create a fake AAC frame
	aacFrame := &media.AudioFrame{
		PTS:        1920,
		Data:       []byte{0xFF, 0xF1, 0x50, 0x80, 0x02, 0x00, 0xFC},
		SampleRate: 48000,
		Channels:   2,
	}

	// Ingest multiple frames
	for i := 0; i < 3; i++ {
		m.IngestFrame("cam1", aacFrame)
	}

	// Wait for ticker to produce output
	deadline := time.After(200 * time.Millisecond)
	for {
		mu.Lock()
		count := len(outputFrames)
		mu.Unlock()
		if count >= 1 {
			break
		}
		select {
		case <-deadline:
			mu.Lock()
			count = len(outputFrames)
			mu.Unlock()
			require.GreaterOrEqual(t, count, 1,
				"expected at least 1 output frame within 200ms, got %d", count)
			goto done
		case <-time.After(5 * time.Millisecond):
		}
	}
done:

	mu.Lock()
	count := len(outputFrames)
	mu.Unlock()

	assert.GreaterOrEqual(t, count, 1,
		"outputTicker should produce frames from IngestFrame input")
}

// TestClockDrivenMixer_IngestPCMUpdatesMetering verifies that IngestPCM
// updates channel peak levels even for inactive/muted channels.
func TestClockDrivenMixer_IngestPCMUpdatesMetering(t *testing.T) {
	t.Parallel()

	enc := &mockTickEncoder{}
	m := NewMixer(MixerConfig{
		SampleRate: 48000,
		Channels:   2,
		Output:     func(frame *media.AudioFrame) {},
		EncoderFactory: func(sampleRate, channels int) (Encoder, error) {
			return enc, nil
		},
	})
	defer func() { _ = m.Close() }()

	m.AddChannel("cam1")
	// Do NOT activate — test metering on inactive channel

	pcm := make([]float32, 1024*2)
	for i := 0; i < len(pcm); i += 2 {
		pcm[i] = 0.8   // left
		pcm[i+1] = 0.6 // right
	}

	m.IngestPCM("cam1", pcm, 1920, 2)

	// Check metering was updated
	m.mu.RLock()
	ch := m.channels["cam1"]
	peakL := ch.peakL
	peakR := ch.peakR
	m.mu.RUnlock()

	assert.InDelta(t, 0.8, peakL, 0.01, "left peak should reflect input")
	assert.InDelta(t, 0.6, peakR, 0.01, "right peak should reflect input")
}

// TestClockDrivenMixer_IngestPCMPopulatesRingBuffer verifies that IngestPCM
// pushes processed PCM into the channel's ring buffer (direct inspection).
func TestClockDrivenMixer_IngestPCMPopulatesRingBuffer(t *testing.T) {
	t.Parallel()

	enc := &mockTickEncoder{}
	m := NewMixer(MixerConfig{
		SampleRate: 48000,
		Channels:   2,
		Output:     func(frame *media.AudioFrame) {},
		EncoderFactory: func(sampleRate, channels int) (Encoder, error) {
			return enc, nil
		},
	})
	defer func() { _ = m.Close() }()

	m.AddChannel("cam1")
	m.SetActive("cam1", true)

	pcm := make([]float32, 1024*2)
	for i := range pcm {
		pcm[i] = 0.25
	}

	m.IngestPCM("cam1", pcm, 1920, 2)

	// Verify the ring buffer has one frame
	m.mu.Lock()
	ch := m.channels["cam1"]
	require.NotNil(t, ch.ringBuf, "ring buffer should exist")
	assert.Equal(t, 1, ch.ringBuf.Len(), "ring buffer should have 1 frame after IngestPCM")

	// Pop and verify content
	popped := ch.ringBuf.Pop()
	m.mu.Unlock()

	require.NotNil(t, popped)
	require.Len(t, popped, 1024*2)
	// With trim at 0dB (trimLinear=1.0), the value should be preserved
	assert.InDelta(t, 0.25, popped[0], 0.001, "ring buffer PCM should match input after trim")
}

// TestClockDrivenMixer_NoPassthrough verifies that passthrough is always
// disabled in the clock-driven mixer.
func TestClockDrivenMixer_NoPassthrough(t *testing.T) {
	t.Parallel()

	m := NewMixer(MixerConfig{
		SampleRate: 48000,
		Channels:   2,
		Output:     func(frame *media.AudioFrame) {},
	})
	defer func() { _ = m.Close() }()

	assert.False(t, m.IsPassthrough(), "clock-driven mixer should never be in passthrough mode")

	// Add a single channel at unity — old logic would have enabled passthrough
	m.AddChannel("cam1")
	m.SetActive("cam1", true)
	assert.False(t, m.IsPassthrough(), "passthrough should stay disabled even with single 0dB channel")
}

// TestClockDrivenMixer_TickerStartedByNewMixer verifies that NewMixer
// starts the outputTicker (not the old mixDeadlineTicker), confirming
// that the mixer produces output at a fixed cadence without any ingest calls.
func TestClockDrivenMixer_TickerStartedByNewMixer(t *testing.T) {
	t.Parallel()

	var mu sync.Mutex
	var outputCount int

	enc := &mockTickEncoder{}
	m := NewMixer(MixerConfig{
		SampleRate: 48000,
		Channels:   2,
		Output: func(frame *media.AudioFrame) {
			mu.Lock()
			outputCount++
			mu.Unlock()
		},
		EncoderFactory: func(sampleRate, channels int) (Encoder, error) {
			return enc, nil
		},
	})

	// Wait ~100ms for ticker to fire (~4-5 ticks at ~21.3ms)
	time.Sleep(100 * time.Millisecond)
	_ = m.Close()

	mu.Lock()
	count := outputCount
	mu.Unlock()

	// The ticker should have produced frames even without any IngestPCM/IngestFrame calls
	assert.GreaterOrEqual(t, count, 2, "outputTicker should produce frames autonomously")
	assert.LessOrEqual(t, count, 10, "should not produce excessive frames in 100ms")
}

// stubDecoder is a mock AAC decoder that always returns the same PCM.
type stubDecoder struct {
	pcm []float32
}

func (d *stubDecoder) Decode(data []byte) ([]float32, error) {
	out := make([]float32, len(d.pcm))
	copy(out, d.pcm)
	return out, nil
}

func (d *stubDecoder) Close() error { return nil }
