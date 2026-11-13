//go:build cgo

package audio

import (
	"math"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/zsiec/prism/media"

	"github.com/zsiec/switchframe/server/clip"
)

// toneAmp is the amplitude of the f Hz component of x at 48 kHz.
func toneAmp(x []float32, f float64) float64 {
	var s, c float64
	for i, v := range x {
		w := 2 * math.Pi * f * float64(i) / 48000
		s += float64(v) * math.Sin(w)
		c += float64(v) * math.Cos(w)
	}
	return 2 * math.Hypot(s, c) / float64(len(x))
}

// An MP4 clip's AAC frames carry no header; the mixer frames each in ADTS
// before the FDK decoder sees it. ADTS has three bits for the channel
// configuration, and 7.1 (configuration 7 or 12, eight channels) went into
// them as 8, read back as 0: a layout the stream would have to describe
// itself. Every frame failed to decode and the clip aired silent. The
// fixture is 7.1 with a tone per channel: FL 300 Hz, FR 400, FC 500,
// LFE 600, BL 700, BR 800, SL 900, SR 1000.
func TestMixer_IngestFrame_DecodesSevenOneAAC(t *testing.T) {
	t.Parallel()

	_, frames, err := clip.DemuxFile("testdata/aac-7.1.mp4")
	require.NoError(t, err)
	require.NotEmpty(t, frames)
	require.Equal(t, 8, frames[0].Channels())

	m := NewMixer(MixerConfig{
		SampleRate: 48000,
		Channels:   2,
		DecoderFactory: func(sr, ch int) (Decoder, error) {
			return NewFDKDecoder(sr, ch)
		},
	})
	defer func() { _ = m.Close() }()
	m.AddChannel("clip:1")
	m.SetActive("clip:1", true)

	for _, f := range frames {
		m.IngestFrame("clip:1", &media.AudioFrame{
			Data:       f.Data(),
			SampleRate: f.SampleRate(),
			Channels:   f.Channels(),
		})
	}
	require.Zero(t, m.decodeErrors.Load(), "7.1 AAC frames failed to decode")

	m.mu.RLock()
	ch := m.channels["clip:1"]
	pcm := ch.ringBuf.Pop(ch.ringBuf.Len())
	m.mu.RUnlock()
	require.Greater(t, len(pcm), 2*4096, "the clip's audio did not reach the ring")

	// Skip the decoder's start-up, then split the channels.
	pcm = pcm[2*2048:]
	l := make([]float32, len(pcm)/2)
	r := make([]float32, len(pcm)/2)
	for i := range l {
		l[i], r[i] = pcm[2*i], pcm[2*i+1]
	}
	for f := 300.0; f <= 1000; f += 100 {
		t.Logf("%v Hz: L %.3f R %.3f", f, toneAmp(l, f), toneAmp(r, f))
	}
	for _, f := range []float64{300, 700, 900} {
		require.Greater(t, toneAmp(l, f), 0.02, "left lacks the %v Hz channel", f)
		require.Less(t, toneAmp(r, f), 0.01, "right carries the %v Hz left channel", f)
	}
	for _, f := range []float64{400, 800, 1000} {
		require.Greater(t, toneAmp(r, f), 0.02, "right lacks the %v Hz channel", f)
		require.Less(t, toneAmp(l, f), 0.01, "left carries the %v Hz right channel", f)
	}
	require.Less(t, toneAmp(l, 600), 0.01, "left carries the LFE")
	require.Less(t, toneAmp(r, 600), 0.01, "right carries the LFE")
	require.Greater(t, toneAmp(l, 500), 0.02, "left lacks the centre")
	require.Greater(t, toneAmp(r, 500), 0.02, "right lacks the centre")
}
