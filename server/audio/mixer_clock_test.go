package audio

import (
	"sync"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/zsiec/prism/media"
)

// TestMixer_IngestPCMProducesOutput verifies the end-to-end flow: IngestPCM
// pushes PCM into a channel's ring buffer, and ProduceSamples reads it.
func TestMixer_IngestPCMProducesOutput(t *testing.T) {
	t.Parallel()
	m := newProduceTestMixer(t)

	m.AddChannel("cam1")
	m.SetActive("cam1", true)

	// Push several PCM frames via IngestPCM (simulates MXL/SRT source)
	pcm := make([]float32, 1024*2)
	for i := range pcm {
		pcm[i] = 0.3
	}
	tl := newTimeline()
	for i := 0; i < 3; i++ {
		m.IngestPCM("cam1", pcm, tl.pts(i*1024), 2)
	}

	for i := 0; i < 3; i++ {
		out := tl.produce(m, 1024)
		require.Len(t, out, 1024*2)
		for j, v := range out {
			require.InDelta(t, 0.3, v, 0.001, "period %d sample %d", i, j)
		}
	}
}

// TestMixer_IngestFrameProducesOutput verifies that IngestFrame (AAC path)
// decodes, processes, and pushes to the ring buffer ProduceSamples reads.
func TestMixer_IngestFrameProducesOutput(t *testing.T) {
	t.Parallel()

	// Track what the "decoder" produces
	decodedPCM := make([]float32, 1024*2)
	for i := range decodedPCM {
		decodedPCM[i] = 0.5
	}

	mockDec := &stubDecoder{pcm: decodedPCM}

	m := NewMixer(MixerConfig{
		SampleRate: 48000,
		Channels:   2,
		DecoderFactory: func(sampleRate, channels int) (Decoder, error) {
			return mockDec, nil
		},
	})
	defer func() { _ = m.Close() }()

	m.AddChannel("cam1")
	m.SetActive("cam1", true)

	// Ingest multiple frames
	tl := newTimeline()
	for i := 0; i < 3; i++ {
		m.IngestFrame("cam1", &media.AudioFrame{
			PTS:        tl.pts(i * 1024),
			Data:       []byte{0xFF, 0xF1, 0x50, 0x80, 0x02, 0x00, 0xFC},
			SampleRate: 48000,
			Channels:   2,
		})
	}

	for i := 0; i < 3; i++ {
		out := tl.produce(m, 1024)
		require.Len(t, out, 1024*2)
		for j, v := range out {
			require.InDelta(t, 0.5, v, 0.001, "period %d sample %d", i, j)
		}
	}
}

// TestMixer_IngestPCMUpdatesMetering verifies that IngestPCM
// updates channel peak levels even for inactive/muted channels.
func TestMixer_IngestPCMUpdatesMetering(t *testing.T) {
	t.Parallel()

	m := NewMixer(MixerConfig{
		SampleRate: 48000,
		Channels:   2,
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

// TestMixer_IngestPCMPopulatesRingBuffer verifies that IngestPCM
// pushes processed PCM into the channel's ring buffer (direct inspection).
func TestMixer_IngestPCMPopulatesRingBuffer(t *testing.T) {
	t.Parallel()

	m := NewMixer(MixerConfig{
		SampleRate: 48000,
		Channels:   2,
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
	assert.Greater(t, ch.ringBuf.Len(), 0, "ring buffer should have samples after IngestPCM")

	// Pop and verify content
	popped := ch.ringBuf.Pop(2048)
	m.mu.Unlock()

	require.NotNil(t, popped)
	require.Len(t, popped, 1024*2)
	// With trim at 0dB (trimLinear=1.0), the value should be preserved
	assert.InDelta(t, 0.25, popped[0], 0.001, "ring buffer PCM should match input after trim")
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

func (d *stubDecoder) Channels() int { return 2 }

func (d *stubDecoder) Close() error { return nil }

// TestMixer_MasterGainApplied verifies that master level gain is applied to
// the ProduceSamples output.
func TestMixer_MasterGainApplied(t *testing.T) {
	t.Parallel()
	m := newProduceTestMixer(t)

	m.AddChannel("cam1")
	m.SetActive("cam1", true)
	require.NoError(t, m.SetMasterLevel(-6.0)) // ~0.5012 linear

	pcm := make([]float32, 1024*2)
	for i := range pcm {
		pcm[i] = 1.0
	}
	tl := newTimeline()
	tl.push(m, "cam1", pcm, 0)

	lastPCM := tl.produce(m, 1024)
	// Fader at 0dB (1.0), master at -6dB (~0.5012). Input is 1.0.
	// After limiter (1.0 * 0.5012 = 0.5012, below threshold): ~0.5012
	assert.InDelta(t, 0.5012, lastPCM[0], 0.02, "master gain should be applied")
}

// TestMixer_ProgramMuteProducesSilence verifies that program mute zeroes the
// ProduceSamples output.
func TestMixer_ProgramMuteProducesSilence(t *testing.T) {
	t.Parallel()
	m := newProduceTestMixer(t)

	m.AddChannel("cam1")
	m.SetActive("cam1", true)
	m.SetProgramMute(true)

	pcm := make([]float32, 1024*2)
	for i := range pcm {
		pcm[i] = 0.8
	}
	tl := newTimeline()
	tl.push(m, "cam1", pcm, 0)

	lastPCM := tl.produce(m, 1024)
	for i, v := range lastPCM {
		assert.Equal(t, float32(0), v, "sample %d should be zero when program muted", i)
	}

	// Verify metering shows silence
	peak := m.ProgramPeak()
	assert.InDelta(t, -96.0, peak[0], 0.001, "left peak should be -96 (silence)")
	assert.InDelta(t, -96.0, peak[1], 0.001, "right peak should be -96 (silence)")
}

// TestMixer_DipMidpointSilence verifies that at the dip midpoint
// (position 0.5), both sources produce silent output.
func TestMixer_DipMidpointSilence(t *testing.T) {
	t.Parallel()
	m := newProduceTestMixer(t)

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

	m.mu.Unlock()

	pcm := make([]float32, 1024*2)
	for i := range pcm {
		pcm[i] = 1.0
	}
	tl := newTimeline()
	tl.push(m, "cam1", pcm, 0)
	tl.push(m, "cam2", pcm, 0)

	lastPCM := tl.produce(m, 1024)
	// At dip midpoint, both gains are 0 -> output should be silence
	for i, s := range lastPCM {
		assert.InDelta(t, 0.0, s, 0.001, "sample %d should be silent at dip midpoint", i)
	}
}

// TestMixer_MonoToStereoUpmix verifies that mono PCM sources are upmixed to
// stereo when pushed through IngestPCM.
func TestMixer_MonoToStereoUpmix(t *testing.T) {
	t.Parallel()
	m := newProduceTestMixer(t)

	m.AddChannel("mono_src")
	m.SetActive("mono_src", true)

	// Push mono PCM via IngestPCM (channels=1)
	monoPCM := make([]float32, 1024)
	for i := range monoPCM {
		monoPCM[i] = 0.5
	}
	tl := newTimeline()
	m.IngestPCM("mono_src", monoPCM, tl.pts(0), 1)

	// ProduceSamples reads the ring buffer, which has upmixed stereo data
	lastPCM := tl.produce(m, 1024)
	require.Len(t, lastPCM, 1024*2, "output should be stereo (2048 samples)")

	// Check both L and R have the value (upmixed from mono 0.5)
	assert.InDelta(t, 0.5, lastPCM[0], 0.01, "left sample should be 0.5 (upmixed)")
	assert.InDelta(t, 0.5, lastPCM[1], 0.01, "right sample should be 0.5 (upmixed)")
}

// TestMixer_StingerAudioOverlay verifies that stinger audio is additively
// mixed into the ProduceSamples output.
func TestMixer_StingerAudioOverlay(t *testing.T) {
	t.Parallel()
	m := newProduceTestMixer(t)

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
	tl := newTimeline()
	tl.push(m, "cam1", pcm, 0)

	lastPCM := tl.produce(m, 1024)
	// Source (0.2) + stinger (0.1 * fade envelope ~= 0.1 for most samples)
	// Sum should be > 0.2 due to stinger contribution
	assert.Greater(t, lastPCM[len(lastPCM)/2], float32(0.2),
		"output should include stinger audio overlay")
}

// TestMixer_CloseConcurrent verifies that calling Close() concurrently from
// multiple goroutines closes decoders exactly once.
func TestMixer_CloseConcurrent(t *testing.T) {
	t.Parallel()

	var decoderCloses atomic.Int64

	m := NewMixer(MixerConfig{
		SampleRate: 48000,
		Channels:   2,
		DecoderFactory: func(sampleRate, channels int) (Decoder, error) {
			return &closeCountDec{closes: &decoderCloses, samples: make([]float32, 1024*2)}, nil
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
}

// closeCountDec tracks Close() calls via an atomic counter.
type closeCountDec struct {
	closes  *atomic.Int64
	samples []float32
}

func (d *closeCountDec) Decode([]byte) ([]float32, error) { return d.samples, nil }
func (d *closeCountDec) Channels() int                    { return 2 }
func (d *closeCountDec) Close() error                     { d.closes.Add(1); return nil }
