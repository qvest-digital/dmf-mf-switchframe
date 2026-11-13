package main

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/zsiec/switchframe/server/audio"
	"github.com/zsiec/switchframe/server/clock"
)

// A clip's PCM reaches program as its AAC did: the mixer folds AAC of 3,
// 4, 5 and 8 channels to stereo with the centre and the surrounds, but
// keeps the first two channels of PCM that wide, which for 7.1 AAC are
// the outer front pair alone. Laid out as 5.1, the PCM is folded as its
// AAC was. A selected channel pair takes the channels as decoded.
func TestFilePCMFoldsAsItsAACLayout(t *testing.T) {
	const k = 0.7071067811865475
	// The mixer's AAC fold, per layout, of one frame s.
	folds := map[int]func(s []float64) (float64, float64){
		3: func(s []float64) (float64, float64) { return s[0] + k*s[2], s[1] + k*s[2] },
		4: func(s []float64) (float64, float64) { return s[0] + k*s[2] + k*s[3], s[1] + k*s[2] + k*s[3] },
		5: func(s []float64) (float64, float64) { return s[0] + k*s[2] + k*s[3], s[1] + k*s[2] + k*s[4] },
		6: func(s []float64) (float64, float64) { return s[0] + k*s[2] + k*s[4], s[1] + k*s[2] + k*s[5] },
		8: func(s []float64) (float64, float64) {
			return s[6] + k*s[2] + k*s[0] + k*s[4], s[7] + k*s[2] + k*s[1] + k*s[5]
		},
	}
	for channels, fold := range folds {
		frame := make([]float64, channels)
		for c := range frame {
			frame[c] = 0.01 * float64(c+1)
		}
		wantL, wantR := fold(frame)
		for _, pair := range []int{0, 3} {
			if pair > 0 && channels < 4 {
				continue
			}
			got := readFilePCM(t, frame, pair)
			if pair == 0 {
				require.InDelta(t, wantL, got[0], 1e-5, "%d channels: left", channels)
				require.InDelta(t, wantR, got[1], 1e-5, "%d channels: right", channels)
			} else {
				require.InDelta(t, frame[2], got[0], 1e-5, "%d channels, pair 3: left", channels)
				require.InDelta(t, frame[3], got[1], 1e-5, "%d channels, pair 3: right", channels)
			}
		}
	}
}

// readFilePCM hands a period of frame, repeated, to a stereo mixer as a
// clip's PCM, with channel pair pair selected, and returns the first
// output frame.
func readFilePCM(t *testing.T, frame []float64, pair int) []float32 {
	t.Helper()
	m := audio.NewMixer(audio.MixerConfig{SampleRate: 48000, Channels: 2})
	defer func() { _ = m.Close() }()
	const key = "clip:1"
	m.AddChannel(key)
	m.SetActive(key, true)
	if pair > 0 {
		require.NoError(t, m.SetChannelPair(key, pair))
	}
	const n = 960
	pcm := make([]float32, 0, n*len(frame))
	for range n {
		for _, v := range frame {
			pcm = append(pcm, float32(v))
		}
	}
	rate := clock.Rate{Num: 48000, Den: 1}
	at := clock.TimestampToIndex(rate, clock.TAINow())
	a := &App{mixer: m}
	a.ingestFilePCM(key, pcm, clock.IndexPTS(rate, at), len(frame), true)
	return m.ProduceSamples(at, n, 0)[:2]
}
