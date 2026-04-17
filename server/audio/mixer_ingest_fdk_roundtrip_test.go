//go:build cgo

package audio

import (
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/zsiec/prism/media"
)

func TestIngestFrame_FDKRoundTrip_ContentLengthPreserved(t *testing.T) {
	t.Parallel()

	const (
		sampleRate    = 48000
		channels      = 2
		samplesPerFrm = 1024
		totalFrames   = 6
	)

	enc, err := NewFDKEncoder(sampleRate, channels)
	require.NoError(t, err)
	defer func() { _ = enc.Close() }()

	aacFrames := make([][]byte, 0, totalFrames)
	for i := 0; i < totalFrames; i++ {
		pcm := make([]float32, samplesPerFrm*channels)
		for j := range pcm {
			pcm[j] = 0.1
		}
		aac, encErr := enc.Encode(pcm)
		require.NoError(t, encErr)
		if len(aac) > 0 {
			aacFrames = append(aacFrames, aac)
		}
	}

	m := NewMixer(MixerConfig{
		SampleRate: sampleRate,
		Channels:   channels,
		Output:     func(frame *media.AudioFrame) {},
		DecoderFactory: func(sr, ch int) (Decoder, error) {
			return NewFDKDecoder(sr, ch)
		},
		EncoderFactory: func(sr, ch int) (Encoder, error) {
			return NewFDKEncoder(sr, ch)
		},
		ClockDriven: true,
	})
	defer func() { _ = m.Close() }()

	m.AddChannel("cam1")
	m.SetActive("cam1", true)

	for _, aac := range aacFrames {
		m.IngestFrame("cam1", &media.AudioFrame{
			Data:       aac,
			SampleRate: sampleRate,
			Channels:   channels,
		})
	}

	m.mu.RLock()
	ch := m.channels["cam1"]
	depth := ch.ringBuf.Len()
	m.mu.RUnlock()

	expectedFrames := len(aacFrames)
	expectedSamples := expectedFrames * samplesPerFrm * channels
	toleranceSamples := samplesPerFrm * channels

	require.InDelta(t, expectedSamples, depth, float64(toleranceSamples),
		"ring buffer depth should match input sample count within one frame — pipeline preserves content length (got %d, expected %d ±%d)",
		depth, expectedSamples, toleranceSamples)
}
