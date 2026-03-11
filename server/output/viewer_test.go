package output

import (
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/zsiec/prism/media"
)

func TestOutputViewerStopIdempotent(t *testing.T) {
	t.Parallel()
	muxer := &TSMuxer{}
	v := NewViewer(muxer, func(_ *media.VideoFrame) {})
	go v.Run()
	v.Stop()
	v.Stop() // must not panic
}

func TestOutputViewerVideoPriority(t *testing.T) {
	t.Parallel()

	var videoMuxed atomic.Int64
	var audioMuxed atomic.Int64

	muxer := NewTSMuxer()
	muxer.SetOutput(func(_ []byte) {})

	v := NewViewer(muxer, func(_ *media.VideoFrame) {
		videoMuxed.Add(1)
	})

	// Fill the audio channel to near capacity before starting.
	for i := 0; i < audioChSize-1; i++ {
		v.SendAudio(&media.AudioFrame{
			PTS:        int64(i * 1000),
			Data:       []byte{0xFF, 0xF1, 0x50, 0x80, 0x02, 0x80, 0xFC, 0xAA, 0xBB},
			SampleRate: 48000,
			Channels:   2,
		})
	}

	// Send one video keyframe (needed to initialize the muxer).
	v.SendVideo(&media.VideoFrame{
		PTS:        90000,
		DTS:        90000,
		IsKeyframe: true,
		WireData:   []byte{0x00, 0x00, 0x00, 0x02, 0x65, 0x88},
		SPS:        []byte{0x67, 0x64, 0x00, 0x28},
		PPS:        []byte{0x68, 0xEE, 0x3C, 0x80},
	})

	go v.Run()

	// The video frame should be processed promptly despite the audio backlog.
	require.Eventually(t, func() bool {
		return videoMuxed.Load() >= 1
	}, 2*time.Second, 5*time.Millisecond,
		"video frame should be processed with priority over queued audio")

	v.Stop()

	require.GreaterOrEqual(t, videoMuxed.Load(), int64(1),
		"video frame must have been muxed")
	t.Logf("video muxed: %d, audio muxed: %d", videoMuxed.Load(), audioMuxed.Load())
}
