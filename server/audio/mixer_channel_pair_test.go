package audio

import (
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/zsiec/prism/media"
)

const pairTestFrames = 256

// numberedPCM returns interleaved PCM of srcChannels channels in which
// channel c, 1-based, holds c/100 in every frame, so each output sample
// names the source channel it came from.
func numberedPCM(srcChannels int) []float32 {
	out := make([]float32, pairTestFrames*srcChannels)
	for i := range out {
		out[i] = float32(i%srcChannels+1) / 100
	}
	return out
}

func newPairTestMixer(t *testing.T, dec Decoder) *Mixer {
	t.Helper()
	m := NewMixer(MixerConfig{
		SampleRate: 48000,
		Channels:   2,
		DecoderFactory: func(int, int) (Decoder, error) {
			return dec, nil
		},
	})
	t.Cleanup(func() { _ = m.Close() })
	return m
}

// requireProgramPair asserts the channel's buffered audio carries wantL on
// the left and wantR on the right in every frame.
func requireProgramPair(t *testing.T, m *Mixer, key string, wantL, wantR float32) {
	t.Helper()
	m.mu.RLock()
	defer m.mu.RUnlock()
	rb := m.channels[key].ringBuf
	require.Equal(t, pairTestFrames*2, rb.Len(), "ring holds stereo frames")
	got := rb.Pop(rb.Len())
	for i := 0; i < pairTestFrames; i++ {
		require.InDelta(t, wantL, got[2*i], 1e-6, "L of frame %d", i)
		require.InDelta(t, wantR, got[2*i+1], 1e-6, "R of frame %d", i)
	}
}

func ingestTestAAC(m *Mixer, key string) {
	m.IngestFrame(key, &media.AudioFrame{
		Data:       []byte{0xFF, 0xF1, 0x50, 0x80, 0x02, 0x00, 0xFC},
		SampleRate: 48000,
	})
}

// A 16-channel input set to pair 3/4 carries only channels 3 and 4 on
// program, on L and R.
func TestMixer_ChannelPair_PCM16SelectsPair(t *testing.T) {
	m := newPairTestMixer(t, nil)
	m.AddChannel("mxl:cam1")
	require.NoError(t, m.SetChannelPair("mxl:cam1", 3))

	m.IngestPCM("mxl:cam1", numberedPCM(16), 1000, 16)

	requireProgramPair(t, m, "mxl:cam1", 0.03, 0.04)
	st := m.ChannelStates()["mxl:cam1"]
	require.Equal(t, 3, st.FirstChannel)
	require.Equal(t, 16, st.SourceChannels, "the UI offers pairs up to the source's width")
}

// The last pair of a 16-channel source is reachable.
func TestMixer_ChannelPair_PCM16LastPair(t *testing.T) {
	m := newPairTestMixer(t, nil)
	m.AddChannel("mxl:cam1")
	require.NoError(t, m.SetChannelPair("mxl:cam1", 15))

	m.IngestPCM("mxl:cam1", numberedPCM(16), 1000, 16)

	requireProgramPair(t, m, "mxl:cam1", 0.15, 0.16)
}

// A decoded AAC bitstream is selected from in the decoder's channel order
// instead of being folded by its layout.
func TestMixer_ChannelPair_AAC6SelectsPair(t *testing.T) {
	m := newPairTestMixer(t, &surroundDecoder{channels: 6, samples: numberedPCM(6)})
	m.AddChannel("clip:1")
	m.SetActive("clip:1", true)
	require.NoError(t, m.SetChannelPair("clip:1", 3))

	ingestTestAAC(m, "clip:1")

	requireProgramPair(t, m, "clip:1", 0.03, 0.04)
	require.Equal(t, 6, m.ChannelStates()["clip:1"].SourceChannels)
}

// Pair 1/2 of a 5.1 source is its L and R as they are, not the Lo/Ro
// fold of the default mapping: an explicit choice of the front pair.
func TestMixer_ChannelPair_FirstPairOfSurroundIsRaw(t *testing.T) {
	m := newPairTestMixer(t, nil)
	m.AddChannel("srt:cam2")
	require.NoError(t, m.SetChannelPair("srt:cam2", 1))

	m.IngestPCM("srt:cam2", numberedPCM(6), 1000, 6)

	requireProgramPair(t, m, "srt:cam2", 0.01, 0.02)
}

// A pair beyond the source's width falls back to the default mapping,
// never to silence.
func TestMixer_ChannelPair_TooNarrowSourceFallsBack(t *testing.T) {
	m := newPairTestMixer(t, nil)
	m.AddChannel("srt:cam2")
	require.NoError(t, m.SetChannelPair("srt:cam2", 7))

	m.IngestPCM("srt:cam2", numberedPCM(6), 1000, 6)
	wantL, wantR := bs775Lo(0.01, 0.02, 0.03, 0.04, 0.05, 0.06)
	requireProgramPair(t, m, "srt:cam2", wantL, wantR)

	m.IngestPCM("srt:cam2", numberedPCM(2), 1000, 2)
	requireProgramPair(t, m, "srt:cam2", 0.01, 0.02)

	m.IngestPCM("srt:cam2", numberedPCM(1), 1000, 1)
	requireProgramPair(t, m, "srt:cam2", 0.01, 0.01)
}

// The default, and a selection set back to it, map a wide source as
// before: its first two channels on L and R.
func TestMixer_ChannelPair_DefaultUnchanged(t *testing.T) {
	m := newPairTestMixer(t, nil)
	m.AddChannel("mxl:cam1")

	m.IngestPCM("mxl:cam1", numberedPCM(16), 1000, 16)
	requireProgramPair(t, m, "mxl:cam1", 0.01, 0.02)

	require.NoError(t, m.SetChannelPair("mxl:cam1", 5))
	require.NoError(t, m.SetChannelPair("mxl:cam1", 0))
	require.Equal(t, 0, m.ChannelPair("mxl:cam1"))
	m.IngestPCM("mxl:cam1", numberedPCM(16), 1000, 16)
	requireProgramPair(t, m, "mxl:cam1", 0.01, 0.02)
	require.Zero(t, m.ChannelStates()["mxl:cam1"].FirstChannel)
}

// A selection is kept by source key: one made before the source registers,
// as the startup defaults are, holds once it does, and one for a source
// removed and added again, as a clip on every load is, holds after.
func TestMixer_ChannelPair_KeptAcrossReAdd(t *testing.T) {
	m := newPairTestMixer(t, nil)
	require.NoError(t, m.SetChannelPair("mxl:cam1", 3))
	m.AddChannel("mxl:cam1")
	m.IngestPCM("mxl:cam1", numberedPCM(16), 1000, 16)
	requireProgramPair(t, m, "mxl:cam1", 0.03, 0.04)

	m.RemoveChannel("mxl:cam1")
	m.AddChannel("mxl:cam1")
	require.Equal(t, 3, m.ChannelPair("mxl:cam1"))
	m.IngestPCM("mxl:cam1", numberedPCM(16), 1000, 16)
	requireProgramPair(t, m, "mxl:cam1", 0.03, 0.04)
}

func TestMixer_SetChannelPair_Validates(t *testing.T) {
	m := newPairTestMixer(t, nil)
	m.AddChannel("mxl:cam1")
	for _, n := range []int{0, 1, 3, 15} {
		require.NoError(t, m.SetChannelPair("mxl:cam1", n), "first channel %d", n)
	}
	for _, n := range []int{-1, 2, 4, 16, 17} {
		require.ErrorIs(t, m.SetChannelPair("mxl:cam1", n), ErrInvalidChannelPair, "first channel %d", n)
	}
	require.Equal(t, 15, m.ChannelPair("mxl:cam1"), "a rejected value leaves the selection")
}
