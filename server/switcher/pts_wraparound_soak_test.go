//go:build correctness

package switcher

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/zsiec/prism/media"
)

func TestPTSWraparound_NinetySecondSoak(t *testing.T) {
	handler := &syncTestHandler{}
	fs := NewFrameSynchronizer(33*time.Millisecond, handler.onVideo, handler.onAudio)
	fs.AddSource("cam1")

	const (
		simSeconds   = 90
		videoFPS     = 30
		videoTickPTS = int64(90000 / videoFPS)
		audioTickPTS = int64(1920) // 1024 AAC samples at 48 kHz in 90 kHz clock
	)
	// Seed 60 seconds of PTS before the 2^33 wrap so the soak traverses the
	// boundary and runs ~30s past it.
	startVideoPTS := (int64(1) << 33) - int64(60*90000)
	startAudioPTS := startVideoPTS

	numVideoFrames := simSeconds * videoFPS
	videoPTS := startVideoPTS
	audioPTS := startAudioPTS

	for i := 0; i < numVideoFrames; i++ {
		vf := &media.VideoFrame{
			PTS:        videoPTS & ptsMask33,
			WireData:   []byte{byte(i)},
			IsKeyframe: (i % 30) == 0,
		}
		fs.IngestVideo("cam1", vf)

		af := &media.AudioFrame{
			PTS:        audioPTS & ptsMask33,
			Data:       []byte{byte(i)},
			SampleRate: 48000,
			Channels:   2,
		}
		fs.IngestAudio("cam1", af)
		audioPTS += audioTickPTS

		fs.releaseTick()
		videoPTS += videoTickPTS
	}

	videos := handler.getVideos()
	require.NotEmpty(t, videos, "no video frames released")

	inversions := 0
	wraps := 0
	for i := 1; i < len(videos); i++ {
		prev := videos[i-1].frame.PTS
		cur := videos[i].frame.PTS
		if !ptsAfter(cur, prev) {
			if cur == prev {
				continue
			}
			inversions++
			if inversions <= 5 {
				t.Logf("video frame[%d] PTS inversion: prev=%d cur=%d", i, prev, cur)
			}
		}
		if cur < prev {
			wraps++
		}
	}
	require.Zero(t, inversions, "video PTS inversions across wrap boundary")
	require.GreaterOrEqual(t, wraps, 1, "soak should cross the 33-bit wrap at least once")

	audios := handler.getAudios()
	require.NotEmpty(t, audios, "no audio frames released")
	audioInversions := 0
	for i := 1; i < len(audios); i++ {
		prev := audios[i-1].frame.PTS
		cur := audios[i].frame.PTS
		if !ptsAfter(cur, prev) {
			if cur == prev {
				continue
			}
			audioInversions++
			if audioInversions <= 5 {
				t.Logf("audio frame[%d] PTS inversion: prev=%d cur=%d", i, prev, cur)
			}
		}
	}
	require.Zero(t, audioInversions, "audio PTS inversions across wrap boundary")

	for i, v := range videos {
		require.LessOrEqual(t, v.frame.PTS, ptsMask33, "video[%d] PTS exceeds 33-bit range", i)
		require.GreaterOrEqual(t, v.frame.PTS, int64(0), "video[%d] PTS negative", i)
	}
	for i, a := range audios {
		require.LessOrEqual(t, a.frame.PTS, ptsMask33, "audio[%d] PTS exceeds 33-bit range", i)
		require.GreaterOrEqual(t, a.frame.PTS, int64(0), "audio[%d] PTS negative", i)
	}
}
