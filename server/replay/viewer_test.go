package replay

import (
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/zsiec/ccx"
	"github.com/zsiec/prism/media"
)

func TestReplayViewer_ID(t *testing.T) {
	buf := newReplayBuffer(60, 0)
	v := newReplayViewer("cam1", buf)
	require.Equal(t, "replay:cam1", v.ID())
}

func TestReplayViewer_SendVideo_RecordsToBuffer(t *testing.T) {
	buf := newReplayBuffer(60, 0)
	v := newReplayViewer("cam1", buf)

	frame := makeVideoFrame(0, true, 1000)
	v.SendVideo(frame)

	info := buf.Status()
	require.Equal(t, 1, info.FrameCount)
}

func TestReplayViewer_SendVideo_MultipleFrames(t *testing.T) {
	buf := newReplayBuffer(60, 0)
	v := newReplayViewer("cam1", buf)

	v.SendVideo(makeVideoFrame(0, true, 1000))
	v.SendVideo(makeVideoFrame(3003, false, 500))
	v.SendVideo(makeVideoFrame(6006, false, 500))

	info := buf.Status()
	require.Equal(t, 3, info.FrameCount)
}

func TestReplayViewer_SendAudio_Recorded(t *testing.T) {
	buf := newReplayBuffer(60, 0)
	v := newReplayViewer("cam1", buf)

	// Audio before any video GOP is dropped (no wall-clock reference).
	v.SendAudio(&media.AudioFrame{
		PTS:  0,
		Data: make([]byte, 100),
	})
	buf.mu.RLock()
	require.Equal(t, 0, len(buf.audioFrames), "audio before first video GOP should be dropped")
	buf.mu.RUnlock()

	// Record a video keyframe to establish a GOP.
	v.SendVideo(makeVideoFrame(0, true, 1000))

	// Now audio should be recorded.
	v.SendAudio(&media.AudioFrame{
		PTS:        1000,
		Data:       make([]byte, 100),
		SampleRate: 48000,
		Channels:   2,
	})
	buf.mu.RLock()
	require.Equal(t, 1, len(buf.audioFrames), "audio after video GOP should be recorded")
	buf.mu.RUnlock()
}

func TestReplayViewer_SendCaptions_Ignored(t *testing.T) {
	buf := newReplayBuffer(60, 0)
	v := newReplayViewer("cam1", buf)

	v.SendCaptions(&ccx.CaptionFrame{
		PTS: 0,
	})

	info := buf.Status()
	require.Equal(t, 0, info.FrameCount, "expected 0 frames (captions ignored)")
}

func TestReplayViewer_Stats(t *testing.T) {
	buf := newReplayBuffer(60, 0)
	v := newReplayViewer("cam1", buf)

	v.SendVideo(makeVideoFrame(0, true, 1000))
	v.SendVideo(makeVideoFrame(3003, false, 500))
	v.SendAudio(&media.AudioFrame{PTS: 0, Data: make([]byte, 100)})

	stats := v.Stats()
	require.Equal(t, "replay:cam1", stats.ID)
	require.Equal(t, int64(2), stats.VideoSent)
	require.Equal(t, int64(1), stats.AudioSent)
}

func TestReplayViewer_InterfaceCompliance(t *testing.T) {
	// Compile-time check is in viewer.go; this is a runtime sanity check.
	buf := newReplayBuffer(60, 0)
	v := newReplayViewer("cam1", buf)
	_ = v.ID()
	_ = v.Stats()
}
