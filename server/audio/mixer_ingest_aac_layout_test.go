package audio

import (
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/zsiec/prism/media"
)

// AAC bitstreams of three to eight channels decode in the layout their
// channel configuration fixes. Keeping their first two channels, as the
// fallback for an unknown layout does, drops the centre, where the
// dialogue of a 3.0, 4.0 or 5.0 mix sits, and of 7.1 keeps only the side
// pair. Each channel is folded into its side, centre and surrounds at
// -3 dB.
func TestMixer_IngestFrame_FoldsAACLayoutsToStereo(t *testing.T) {
	t.Parallel()

	const k = float32(0.7071067811865475)
	cases := []struct {
		name         string
		in           []float32 // one sample frame, FDK WAV order
		wantL, wantR float32
	}{
		{"3.0", []float32{0.1, 0.2, 0.4}, 0.1 + k*0.4, 0.2 + k*0.4},
		{"4.0", []float32{0.1, 0.2, 0.4, 0.3}, 0.1 + k*0.4 + k*0.3, 0.2 + k*0.4 + k*0.3},
		{"5.0", []float32{0.1, 0.2, 0.4, 0.3, 0.05}, 0.1 + k*0.4 + k*0.3, 0.2 + k*0.4 + k*0.05},
		{"7.1", []float32{0.01, 0.02, 0.4, 0.9, 0.03, 0.04, 0.1, 0.2},
			0.1 + k*0.4 + k*0.01 + k*0.03, 0.2 + k*0.4 + k*0.02 + k*0.04},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			const frames = 1024
			n := len(tc.in)
			samples := make([]float32, frames*n)
			for i := 0; i < frames; i++ {
				copy(samples[i*n:], tc.in)
			}
			dec := &surroundDecoder{channels: n, samples: samples}
			m := NewMixer(MixerConfig{
				SampleRate: 48000,
				Channels:   2,
				DecoderFactory: func(int, int) (Decoder, error) {
					return dec, nil
				},
			})
			defer func() { _ = m.Close() }()
			m.AddChannel("clip:1")
			m.SetActive("clip:1", true)
			m.IngestFrame("clip:1", &media.AudioFrame{
				Data:       []byte{0xFF, 0xF1, 0x50, 0x80, 0x02, 0x00, 0xFC},
				SampleRate: 48000,
				Channels:   n,
			})

			m.mu.RLock()
			ch := m.channels["clip:1"]
			ringLen := ch.ringBuf.Len()
			popped := ch.ringBuf.Pop(2)
			m.mu.RUnlock()

			require.Equal(t, frames*2, ringLen)
			require.InDelta(t, float64(tc.wantL), float64(popped[0]), 1e-5, "L")
			require.InDelta(t, float64(tc.wantR), float64(popped[1]), 1e-5, "R")
		})
	}
}
