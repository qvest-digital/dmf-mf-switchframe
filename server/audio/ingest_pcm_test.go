package audio

import (
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/zsiec/prism/media"
)

func TestIngestPCM_PeakMetering(t *testing.T) {
	t.Parallel()

	m := NewMixer(MixerConfig{
		SampleRate: 48000,
		Channels:   2,
		Output:     func(frame *media.AudioFrame) {},
		DecoderFactory: func(sampleRate, channels int) (Decoder, error) {
			return &mockDecoder{samples: make([]float32, 4)}, nil
		},
		EncoderFactory: func(sampleRate, channels int) (Encoder, error) {
			return &mockEncoder{}, nil
		},
	})
	defer func() { _ = m.Close() }()

	m.AddChannel("mxl1")
	m.SetActive("mxl1", true)

	// PCM with known peak values: L=0.75, R=0.5 (interleaved stereo)
	pcm := []float32{0.75, 0.5, 0.3, 0.4}
	m.IngestPCM("mxl1", pcm, 1000, 2)

	m.mu.RLock()
	ch := m.channels["mxl1"]
	peakL := ch.peakL
	peakR := ch.peakR
	m.mu.RUnlock()

	require.InDelta(t, 0.75, peakL, 0.001, "peakL should reflect left channel peak")
	require.InDelta(t, 0.5, peakR, 0.001, "peakR should reflect right channel peak")
}

func TestIngestPCM_StoresForCrossfade(t *testing.T) {
	t.Parallel()

	m := NewMixer(MixerConfig{
		SampleRate: 48000,
		Channels:   2,
		Output:     func(frame *media.AudioFrame) {},
		DecoderFactory: func(sampleRate, channels int) (Decoder, error) {
			return &mockDecoder{samples: make([]float32, 4)}, nil
		},
		EncoderFactory: func(sampleRate, channels int) (Encoder, error) {
			return &mockEncoder{}, nil
		},
	})
	defer func() { _ = m.Close() }()

	m.AddChannel("mxl1")
	m.SetActive("mxl1", true)

	pcm := []float32{0.1, 0.2, 0.3, 0.4}
	m.IngestPCM("mxl1", pcm, 1000, 2)

	m.mu.RLock()
	ch := m.channels["mxl1"]
	storedBuf := ch.storedBuf
	lastDecoded := m.lastDecodedPCM["mxl1"]
	m.mu.RUnlock()

	require.Equal(t, 4, len(storedBuf), "storedBuf should have PCM data")
	require.InDelta(t, 0.1, storedBuf[0], 0.001)
	require.InDelta(t, 0.2, storedBuf[1], 0.001)
	require.InDelta(t, 0.3, storedBuf[2], 0.001)
	require.InDelta(t, 0.4, storedBuf[3], 0.001)

	require.Equal(t, 4, len(lastDecoded), "lastDecodedPCM should have PCM data")
	// storedBuf and lastDecodedPCM should reference the same backing data
	require.Equal(t, storedBuf, lastDecoded)
}

func TestIngestPCM_MutedChannelSkipped(t *testing.T) {
	t.Parallel()

	var mu sync.Mutex
	var outputFrames []*media.AudioFrame

	m := NewMixer(MixerConfig{
		SampleRate: 48000,
		Channels:   2,
		Output: func(frame *media.AudioFrame) {
			mu.Lock()
			outputFrames = append(outputFrames, frame)
			mu.Unlock()
		},
		DecoderFactory: func(sampleRate, channels int) (Decoder, error) {
			return &mockDecoder{samples: make([]float32, 2048)}, nil
		},
		EncoderFactory: func(sampleRate, channels int) (Encoder, error) {
			return &mockEncoder{}, nil
		},
	})
	defer func() { _ = m.Close() }()

	m.AddChannel("mxl1")
	m.SetActive("mxl1", true)
	_ = m.SetMuted("mxl1", true)

	pcm := make([]float32, 2048)
	m.IngestPCM("mxl1", pcm, 1000, 2)

	// Muted channel should not push to ring buffer (no direct output)
	m.mu.RLock()
	ch := m.channels["mxl1"]
	ringLen := ch.ringBuf.Len()
	m.mu.RUnlock()
	require.Equal(t, 0, ringLen, "muted channel should not push to ring buffer")
}

func TestIngestPCM_InactiveChannelSkipped(t *testing.T) {
	t.Parallel()

	var mu sync.Mutex
	var outputFrames []*media.AudioFrame

	m := NewMixer(MixerConfig{
		SampleRate: 48000,
		Channels:   2,
		Output: func(frame *media.AudioFrame) {
			mu.Lock()
			outputFrames = append(outputFrames, frame)
			mu.Unlock()
		},
		DecoderFactory: func(sampleRate, channels int) (Decoder, error) {
			return &mockDecoder{samples: make([]float32, 2048)}, nil
		},
		EncoderFactory: func(sampleRate, channels int) (Encoder, error) {
			return &mockEncoder{}, nil
		},
	})
	defer func() { _ = m.Close() }()

	m.AddChannel("mxl1")
	// Do NOT activate — channel is inactive

	pcm := make([]float32, 2048)
	m.IngestPCM("mxl1", pcm, 1000, 2)

	// Inactive channel should not push to ring buffer
	m.mu.RLock()
	ch := m.channels["mxl1"]
	ringLen := ch.ringBuf.Len()
	m.mu.RUnlock()
	require.Equal(t, 0, ringLen, "inactive channel should not push to ring buffer")
}
