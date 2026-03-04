package switcher

import (
	"sync"
	"testing"

	"github.com/zsiec/ccx"
	"github.com/zsiec/prism/distribution"
	"github.com/zsiec/prism/media"
)

type mockFrameHandler struct {
	mu       sync.Mutex
	videos   []videoFrameWithSource
	audios   []audioFrameWithSource
	captions []captionFrameWithSource
}

type captionFrameWithSource struct {
	sourceKey string
	frame     *ccx.CaptionFrame
}

type videoFrameWithSource struct {
	sourceKey string
	frame     *media.VideoFrame
}

type audioFrameWithSource struct {
	sourceKey string
	frame     *media.AudioFrame
}

func (m *mockFrameHandler) handleVideoFrame(sourceKey string, frame *media.VideoFrame) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.videos = append(m.videos, videoFrameWithSource{sourceKey, frame})
}

func (m *mockFrameHandler) handleAudioFrame(sourceKey string, frame *media.AudioFrame) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.audios = append(m.audios, audioFrameWithSource{sourceKey, frame})
}

func (m *mockFrameHandler) handleCaptionFrame(sourceKey string, frame *ccx.CaptionFrame) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.captions = append(m.captions, captionFrameWithSource{sourceKey, frame})
}

func TestSourceViewerImplementsViewer(t *testing.T) {
	handler := &mockFrameHandler{}
	sv := newSourceViewer("camera1", handler)
	var _ distribution.Viewer = sv
	if sv.ID() != "switchframe:camera1" {
		t.Errorf("ID() = %q, want %q", sv.ID(), "switchframe:camera1")
	}
}

func TestSourceViewerForwardsVideo(t *testing.T) {
	handler := &mockFrameHandler{}
	sv := newSourceViewer("camera1", handler)
	frame := &media.VideoFrame{PTS: 1000, IsKeyframe: true}
	sv.SendVideo(frame)
	handler.mu.Lock()
	defer handler.mu.Unlock()
	if len(handler.videos) != 1 {
		t.Fatalf("got %d video frames, want 1", len(handler.videos))
	}
	if handler.videos[0].sourceKey != "camera1" {
		t.Errorf("sourceKey = %q, want %q", handler.videos[0].sourceKey, "camera1")
	}
	if handler.videos[0].frame != frame {
		t.Error("frame pointer mismatch")
	}
}

func TestSourceViewerForwardsAudio(t *testing.T) {
	handler := &mockFrameHandler{}
	sv := newSourceViewer("camera1", handler)
	frame := &media.AudioFrame{PTS: 2000, Data: []byte{0x01}}
	sv.SendAudio(frame)
	handler.mu.Lock()
	defer handler.mu.Unlock()
	if len(handler.audios) != 1 {
		t.Fatalf("got %d audio frames, want 1", len(handler.audios))
	}
	if handler.audios[0].sourceKey != "camera1" {
		t.Errorf("sourceKey = %q, want %q", handler.audios[0].sourceKey, "camera1")
	}
}

func TestSourceViewerStats(t *testing.T) {
	handler := &mockFrameHandler{}
	sv := newSourceViewer("camera1", handler)
	stats := sv.Stats()
	if stats.ID != "switchframe:camera1" {
		t.Errorf("Stats().ID = %q, want %q", stats.ID, "switchframe:camera1")
	}
}
