package replay

import (
	"testing"

	"github.com/zsiec/ccx"
	"github.com/zsiec/prism/media"
)

func TestReplayViewer_ID(t *testing.T) {
	buf := newReplayBuffer(60, 0)
	v := newReplayViewer("cam1", buf)
	if v.ID() != "replay:cam1" {
		t.Errorf("expected ID 'replay:cam1', got %q", v.ID())
	}
}

func TestReplayViewer_SendVideo_RecordsToBuffer(t *testing.T) {
	buf := newReplayBuffer(60, 0)
	v := newReplayViewer("cam1", buf)

	frame := makeVideoFrame(0, true, 1000)
	v.SendVideo(frame)

	info := buf.Status()
	if info.FrameCount != 1 {
		t.Errorf("expected 1 frame in buffer, got %d", info.FrameCount)
	}
}

func TestReplayViewer_SendVideo_MultipleFrames(t *testing.T) {
	buf := newReplayBuffer(60, 0)
	v := newReplayViewer("cam1", buf)

	v.SendVideo(makeVideoFrame(0, true, 1000))
	v.SendVideo(makeVideoFrame(3003, false, 500))
	v.SendVideo(makeVideoFrame(6006, false, 500))

	info := buf.Status()
	if info.FrameCount != 3 {
		t.Errorf("expected 3 frames, got %d", info.FrameCount)
	}
}

func TestReplayViewer_SendAudio_Ignored(t *testing.T) {
	buf := newReplayBuffer(60, 0)
	v := newReplayViewer("cam1", buf)

	// Audio should be silently ignored (not recorded).
	v.SendAudio(&media.AudioFrame{
		PTS:  0,
		Data: make([]byte, 100),
	})

	info := buf.Status()
	if info.FrameCount != 0 {
		t.Errorf("expected 0 frames (audio ignored), got %d", info.FrameCount)
	}
}

func TestReplayViewer_SendCaptions_Ignored(t *testing.T) {
	buf := newReplayBuffer(60, 0)
	v := newReplayViewer("cam1", buf)

	v.SendCaptions(&ccx.CaptionFrame{
		PTS: 0,
	})

	info := buf.Status()
	if info.FrameCount != 0 {
		t.Errorf("expected 0 frames (captions ignored), got %d", info.FrameCount)
	}
}

func TestReplayViewer_Stats(t *testing.T) {
	buf := newReplayBuffer(60, 0)
	v := newReplayViewer("cam1", buf)

	v.SendVideo(makeVideoFrame(0, true, 1000))
	v.SendVideo(makeVideoFrame(3003, false, 500))
	v.SendAudio(&media.AudioFrame{PTS: 0, Data: make([]byte, 100)})

	stats := v.Stats()
	if stats.ID != "replay:cam1" {
		t.Errorf("expected stats ID 'replay:cam1', got %q", stats.ID)
	}
	if stats.VideoSent != 2 {
		t.Errorf("expected VideoSent=2, got %d", stats.VideoSent)
	}
	if stats.AudioSent != 1 {
		t.Errorf("expected AudioSent=1, got %d", stats.AudioSent)
	}
}

func TestReplayViewer_InterfaceCompliance(t *testing.T) {
	// Compile-time check is in viewer.go; this is a runtime sanity check.
	buf := newReplayBuffer(60, 0)
	v := newReplayViewer("cam1", buf)
	_ = v.ID()
	_ = v.Stats()
}
