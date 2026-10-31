package audio

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestIngestPCM_PeakMetering(t *testing.T) {
	t.Parallel()

	m := NewMixer(MixerConfig{
		SampleRate: 48000,
		Channels:   2,
		DecoderFactory: func(sampleRate, channels int) (Decoder, error) {
			return &mockDecoder{samples: make([]float32, 4)}, nil
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

func TestIngestPCM_MutedChannelSkipped(t *testing.T) {
	t.Parallel()

	m := NewMixer(MixerConfig{
		SampleRate: 48000,
		Channels:   2,
		DecoderFactory: func(sampleRate, channels int) (Decoder, error) {
			return &mockDecoder{samples: make([]float32, 2048)}, nil
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

	m := NewMixer(MixerConfig{
		SampleRate: 48000,
		Channels:   2,
		DecoderFactory: func(sampleRate, channels int) (Decoder, error) {
			return &mockDecoder{samples: make([]float32, 2048)}, nil
		},
	})
	defer func() { _ = m.Close() }()

	m.AddChannel("mxl1")
	// Do NOT activate — channel is inactive

	pcm := make([]float32, 2048)
	m.IngestPCM("mxl1", pcm, 1000, 2)

	// Inactive channels still push to ring buffer (for AFV priming) so that
	// when the channel activates via AFV there is no silence gap.
	m.mu.RLock()
	ch := m.channels["mxl1"]
	ringLen := ch.ringBuf.Len()
	m.mu.RUnlock()
	require.Greater(t, ringLen, 0, "inactive channel should push to ring buffer for AFV priming")
}
