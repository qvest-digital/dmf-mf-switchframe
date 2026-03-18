package audio

import (
	"sync"
	"sync/atomic"
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

// TestClockDrivenMixer_MasterGainApplied verifies that master level gain is
// applied in the tick output path.
func TestClockDrivenMixer_MasterGainApplied(t *testing.T) {
	t.Parallel()
	m, enc := newTickTestMixer(t)

	m.AddChannel("cam1")
	m.SetActive("cam1", true)
	require.NoError(t, m.SetMasterLevel(-6.0)) // ~0.5012 linear

	pcm := make([]float32, 1024*2)
	for i := range pcm {
		pcm[i] = 1.0
	}
	m.mu.Lock()
	m.channels["cam1"].ringBuf.Push(pcm)
	m.mu.Unlock()

	frame := m.tick()
	require.NotNil(t, frame)

	lastPCM := enc.getLastInput()
	require.NotNil(t, lastPCM)
	// Fader at 0dB (1.0), master at -6dB (~0.5012). Input is 1.0.
	// After limiter (1.0 * 0.5012 = 0.5012, below threshold): ~0.5012
	assert.InDelta(t, 0.5012, lastPCM[0], 0.02, "master gain should be applied")
}

// TestClockDrivenMixer_ProgramMuteProducesSilence verifies that program mute
// zeroes the output PCM in the tick path.
func TestClockDrivenMixer_ProgramMuteProducesSilence(t *testing.T) {
	t.Parallel()
	m, enc := newTickTestMixer(t)

	m.AddChannel("cam1")
	m.SetActive("cam1", true)
	m.SetProgramMute(true)

	pcm := make([]float32, 1024*2)
	for i := range pcm {
		pcm[i] = 0.8
	}
	m.mu.Lock()
	m.channels["cam1"].ringBuf.Push(pcm)
	m.mu.Unlock()

	frame := m.tick()
	require.NotNil(t, frame)

	lastPCM := enc.getLastInput()
	require.NotNil(t, lastPCM)
	for i, v := range lastPCM {
		assert.Equal(t, float32(0), v, "sample %d should be zero when program muted", i)
	}

	// Verify metering shows silence
	peak := m.ProgramPeak()
	assert.InDelta(t, -96.0, peak[0], 0.001, "left peak should be -96 (silence)")
	assert.InDelta(t, -96.0, peak[1], 0.001, "right peak should be -96 (silence)")
}

// TestClockDrivenMixer_DipMidpointSilence verifies that at the dip midpoint
// (position 0.5), both sources produce silent output.
func TestClockDrivenMixer_DipMidpointSilence(t *testing.T) {
	t.Parallel()
	m, enc := newTickTestMixer(t)

	m.AddChannel("cam1")
	m.AddChannel("cam2")
	m.SetActive("cam1", true)
	m.SetActive("cam2", true)

	// Set up dip transition at midpoint
	m.mu.Lock()
	m.transCrossfadeActive = true
	m.transCrossfadeFrom = "cam1"
	m.transCrossfadeTo = "cam2"
	m.transCrossfadePosition = 0.5
	m.transCrossfadeMode = DipToSilence
	m.transCrossfadeAudioPos = 0.5

	pcm := make([]float32, 1024*2)
	for i := range pcm {
		pcm[i] = 1.0
	}
	m.channels["cam1"].ringBuf.Push(pcm)
	m.channels["cam2"].ringBuf.Push(pcm)
	m.mu.Unlock()

	frame := m.tick()
	require.NotNil(t, frame)

	lastPCM := enc.getLastInput()
	require.NotNil(t, lastPCM)
	// At dip midpoint, both gains are 0 -> output should be silence
	for i, s := range lastPCM {
		assert.InDelta(t, 0.0, s, 0.001, "sample %d should be silent at dip midpoint", i)
	}
}

// TestClockDrivenMixer_MonotonicPTS verifies that output PTS is monotonically
// increasing across multiple ticks.
func TestClockDrivenMixer_MonotonicPTS(t *testing.T) {
	t.Parallel()
	m, _ := newTickTestMixer(t)

	m.SeedPTSFromVideo(90000)

	var ptsList []int64
	for i := 0; i < 10; i++ {
		frame := m.tick()
		require.NotNil(t, frame)
		ptsList = append(ptsList, frame.PTS)
	}

	for i := 1; i < len(ptsList); i++ {
		assert.Greater(t, ptsList[i], ptsList[i-1],
			"PTS should be monotonically increasing: pts[%d]=%d <= pts[%d]=%d",
			i, ptsList[i], i-1, ptsList[i-1])
	}
}

// TestClockDrivenMixer_MonoToStereoUpmix verifies that mono PCM sources are
// upmixed to stereo when pushed through IngestPCM.
func TestClockDrivenMixer_MonoToStereoUpmix(t *testing.T) {
	t.Parallel()
	m, enc := newTickTestMixer(t)

	m.AddChannel("mono_src")
	m.SetActive("mono_src", true)

	// Push mono PCM via IngestPCM (channels=1)
	monoPCM := make([]float32, 1024)
	for i := range monoPCM {
		monoPCM[i] = 0.5
	}
	m.IngestPCM("mono_src", monoPCM, 1920, 1)

	// tick should read from ring buffer (which has upmixed stereo data)
	frame := m.tick()
	require.NotNil(t, frame)

	lastPCM := enc.getLastInput()
	require.NotNil(t, lastPCM)
	require.Len(t, lastPCM, 1024*2, "output should be stereo (2048 samples)")

	// Check both L and R have the value (upmixed from mono 0.5)
	assert.InDelta(t, 0.5, lastPCM[0], 0.01, "left sample should be 0.5 (upmixed)")
	assert.InDelta(t, 0.5, lastPCM[1], 0.01, "right sample should be 0.5 (upmixed)")
}

// TestClockDrivenMixer_StingerAudioOverlay verifies that stinger audio is
// additively mixed into the tick output.
func TestClockDrivenMixer_StingerAudioOverlay(t *testing.T) {
	t.Parallel()
	m, enc := newTickTestMixer(t)

	m.AddChannel("cam1")
	m.SetActive("cam1", true)

	// Set stinger audio (one frame worth)
	stingerPCM := make([]float32, 1024*2)
	for i := range stingerPCM {
		stingerPCM[i] = 0.1
	}
	m.SetStingerAudio(stingerPCM, 48000, 2)

	// Push source PCM
	pcm := make([]float32, 1024*2)
	for i := range pcm {
		pcm[i] = 0.2
	}
	m.mu.Lock()
	m.channels["cam1"].ringBuf.Push(pcm)
	m.mu.Unlock()

	frame := m.tick()
	require.NotNil(t, frame)

	lastPCM := enc.getLastInput()
	require.NotNil(t, lastPCM)
	// Source (0.2) + stinger (0.1 * fade envelope ~= 0.1 for most samples)
	// Sum should be > 0.2 due to stinger contribution
	assert.Greater(t, lastPCM[len(lastPCM)/2], float32(0.2),
		"output should include stinger audio overlay")
}

// TestClockDrivenMixer_CloseConcurrent verifies that calling Close()
// concurrently from multiple goroutines closes codecs exactly once.
func TestClockDrivenMixer_CloseConcurrent(t *testing.T) {
	t.Parallel()

	var decoderCloses atomic.Int64
	var encoderCloses atomic.Int64

	m := NewMixer(MixerConfig{
		SampleRate: 48000,
		Channels:   2,
		Output:     func(_ *media.AudioFrame) {},
		DecoderFactory: func(sampleRate, channels int) (Decoder, error) {
			return &closeCountDec{closes: &decoderCloses, samples: make([]float32, 1024*2)}, nil
		},
		EncoderFactory: func(sampleRate, channels int) (Encoder, error) {
			return &closeCountEnc{closes: &encoderCloses}, nil
		},
	})

	m.AddChannel("cam1")
	m.AddChannel("cam2")
	m.SetActive("cam1", true)
	m.SetActive("cam2", true)

	// Ingest AAC frames to trigger lazy decoder init
	aacFrame := &media.AudioFrame{PTS: 1000, Data: []byte{0xFF, 0xF1, 0x50, 0x80, 0x02, 0x00, 0xFC}, SampleRate: 48000, Channels: 2}
	m.IngestFrame("cam1", aacFrame)
	m.IngestFrame("cam2", aacFrame)

	// Wait for ticker to produce a frame (initializes encoder)
	time.Sleep(50 * time.Millisecond)

	// Close concurrently from 10 goroutines
	const goroutines = 10
	var wg sync.WaitGroup
	wg.Add(goroutines)
	for i := 0; i < goroutines; i++ {
		go func() {
			defer wg.Done()
			_ = m.Close()
		}()
	}
	wg.Wait()

	require.Equal(t, int64(2), decoderCloses.Load(),
		"each decoder should be closed exactly once (2 channels)")
	require.Equal(t, int64(1), encoderCloses.Load(),
		"encoder should be closed exactly once")
}

// closeCountDec tracks Close() calls via an atomic counter.
type closeCountDec struct {
	closes  *atomic.Int64
	samples []float32
}

func (d *closeCountDec) Decode([]byte) ([]float32, error) { return d.samples, nil }
func (d *closeCountDec) Close() error                     { d.closes.Add(1); return nil }

// closeCountEnc tracks Close() calls via an atomic counter.
type closeCountEnc struct {
	closes *atomic.Int64
}

func (e *closeCountEnc) Encode([]float32) ([]byte, error) { return []byte{0xFF}, nil }
func (e *closeCountEnc) Close() error                     { e.closes.Add(1); return nil }
