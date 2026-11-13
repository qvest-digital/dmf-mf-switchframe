//go:build cgo && !noffmpeg

package clip

import (
	"math"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/zsiec/switchframe/server/audio"
	"github.com/zsiec/switchframe/server/codec"
)

// stereoTones transcodes the fixture at path as an upload is, decodes the
// result's audio and returns the amplitude of each listed tone on its left
// and right channel.
func stereoTones(t *testing.T, path string, tones []float64) (l, r map[float64]float64) {
	t.Helper()
	out := filepath.Join(t.TempDir(), "out.ts")
	_, err := codec.TranscodeFile(path, out, "libx264", 500000)
	require.NoError(t, err)
	_, frames, err := DemuxFile(out)
	require.NoError(t, err)
	require.NotEmpty(t, frames)

	dec, err := audio.NewFDKDecoder(48000, 2)
	require.NoError(t, err)
	defer func() { _ = dec.Close() }()
	var left, right []float64
	for i, f := range frames {
		pcm, err := dec.Decode(codec.EnsureADTS(f.Data(), f.SampleRate(), f.Channels()))
		require.NoError(t, err)
		if i < 4 || len(pcm) == 0 {
			continue
		}
		require.Equal(t, 2, dec.Channels())
		for k := 0; k+1 < len(pcm); k += 2 {
			left = append(left, float64(pcm[k]))
			right = append(right, float64(pcm[k+1]))
		}
	}
	require.Greater(t, len(left), 4096)
	amp := func(x []float64, f float64) float64 {
		var s, c float64
		for i, v := range x {
			w := 2 * math.Pi * f * float64(i) / 48000
			s += v * math.Sin(w)
			c += v * math.Cos(w)
		}
		return 2 * math.Hypot(s, c) / float64(len(x))
	}
	l, r = map[float64]float64{}, map[float64]float64{}
	for _, f := range tones {
		l[f], r[f] = amp(left, f), amp(right, f)
	}
	return l, r
}

// Audio whose channels name no speakers - discrete tracks, as an 8 to 16
// channel broadcast file carries, here one tone per channel from 300 Hz
// up - was folded to stereo by the speaker layout the resampler guesses
// for its channel count, summing every program into both sides, and a
// count without such a layout, 13, failed the upload. Channel 1 now goes
// to the left, channel 2 to the right, and nothing else into either.
func TestTranscode_DiscreteChannelsKeepTheirFirstPair(t *testing.T) {
	for _, tc := range []struct {
		file     string
		channels int
	}{
		{"testdata/discrete-8.mkv", 8},
		{"testdata/discrete-13.mkv", 13},
	} {
		t.Run(filepath.Base(tc.file), func(t *testing.T) {
			tones := make([]float64, tc.channels)
			for i := range tones {
				tones[i] = float64(300 + 100*i)
			}
			l, r := stereoTones(t, tc.file, tones)
			require.Greater(t, l[300], 0.05, "channel 1 is not on the left")
			require.Greater(t, r[400], 0.05, "channel 2 is not on the right")
			require.Less(t, r[300], 0.01, "channel 1 is on the right")
			require.Less(t, l[400], 0.01, "channel 2 is on the left")
			for _, f := range tones[2:] {
				require.Less(t, l[f], 0.01, "the %v Hz channel is on the left", f)
				require.Less(t, r[f], 0.01, "the %v Hz channel is on the right", f)
			}
		})
	}
}

// A layout that names its speakers keeps the resampler's speaker matrix,
// in whatever order its channels come: the 6.1 fixture's one tone, on
// every channel, reaches both sides, and film-order 5.1 (L C R Ls Rs LFE,
// as MOV carries it; tones FL 300 Hz, FC 500, FR 400, SL 700, SR 800,
// LFE 600) folds each speaker to its side, centre to both, LFE to neither.
func TestTranscode_SpeakerLayoutIsMixedDown(t *testing.T) {
	l, r := stereoTones(t, "testdata/h264-aac-6.1.mp4", []float64{440})
	require.Greater(t, l[440], 0.05)
	require.Greater(t, r[440], 0.05)

	l, r = stereoTones(t, "testdata/h264-pcm-5.1-film-order.mov", []float64{300, 400, 500, 600, 700, 800})
	for _, f := range []float64{300, 700} {
		require.Greater(t, l[f], 0.02, "the %v Hz left speaker is not on the left", f)
		require.Less(t, r[f], 0.01, "the %v Hz left speaker is on the right", f)
	}
	for _, f := range []float64{400, 800} {
		require.Greater(t, r[f], 0.02, "the %v Hz right speaker is not on the right", f)
		require.Less(t, l[f], 0.01, "the %v Hz right speaker is on the left", f)
	}
	require.Greater(t, l[500], 0.02, "the centre is not on the left")
	require.Greater(t, r[500], 0.02, "the centre is not on the right")
	require.Less(t, l[600], 0.01, "the LFE is on the left")
	require.Less(t, r[600], 0.01, "the LFE is on the right")
}

// The demuxer plays an upload's first sound track, and the probe judges
// it, so the transcode converts that one too, not the track avformat
// would pick as best: here a mono 300 Hz track first and a default 5.1
// 400 Hz track second.
func TestTranscode_ConvertsTheFirstAudioTrack(t *testing.T) {
	l, r := stereoTones(t, "testdata/h264-two-audio-tracks.mp4", []float64{300, 400})
	require.Greater(t, l[300], 0.02, "the first track is not on the left")
	require.Greater(t, r[300], 0.02, "the first track is not on the right")
	require.Less(t, l[400], 0.01, "the second track is on the left")
	require.Less(t, r[400], 0.01, "the second track is on the right")
}
