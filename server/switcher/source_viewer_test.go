package switcher

import (
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
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
	require.Equal(t, "switchframe:camera1", sv.ID())
}

func TestSourceViewer_IDPrecomputed(t *testing.T) {
	handler := &mockFrameHandler{}
	sv := newSourceViewer("cam42", handler)

	// The id field should be set at construction time.
	require.Equal(t, "switchframe:cam42", sv.id, "id field should be pre-computed")

	// ID() should return the same pre-computed value.
	require.Equal(t, sv.id, sv.ID(), "ID() should return pre-computed field")
}

func TestSourceViewerForwardsVideo(t *testing.T) {
	handler := &mockFrameHandler{}
	sv := newSourceViewer("camera1", handler)
	frame := &media.VideoFrame{PTS: 1000, IsKeyframe: true}
	sv.SendVideo(frame)
	handler.mu.Lock()
	defer handler.mu.Unlock()
	require.Equal(t, 1, len(handler.videos), "video frame count")
	require.Equal(t, "camera1", handler.videos[0].sourceKey)
	require.Equal(t, frame, handler.videos[0].frame, "frame pointer mismatch")
}

func TestSourceViewerForwardsAudio(t *testing.T) {
	handler := &mockFrameHandler{}
	sv := newSourceViewer("camera1", handler)
	frame := &media.AudioFrame{PTS: 2000, Data: []byte{0x01}}
	sv.SendAudio(frame)
	handler.mu.Lock()
	defer handler.mu.Unlock()
	require.Equal(t, 1, len(handler.audios), "audio frame count")
	require.Equal(t, "camera1", handler.audios[0].sourceKey)
}

func TestSourceViewerStats(t *testing.T) {
	handler := &mockFrameHandler{}
	sv := newSourceViewer("camera1", handler)
	stats := sv.Stats()
	require.Equal(t, "switchframe:camera1", stats.ID)
}

// TestSourceViewer_ConcurrentFrameSyncToggle verifies that concurrently toggling
// frameSync and delayBuffer while sending frames does not trigger the race detector.
// This test exposes the data race that existed when these fields were bare pointers
// accessed without synchronization.
func TestSourceViewer_ConcurrentFrameSyncToggle(t *testing.T) {
	handler := &mockFrameHandler{}
	sv := newSourceViewer("camera1", handler)

	// Build a minimal DelayBuffer and FrameSynchronizer to use as toggle targets.
	db := NewDelayBuffer(handler)

	videoOut := func(key string, f media.VideoFrame) {}
	audioOut := func(key string, f media.AudioFrame) {}
	fs := NewFrameSynchronizer(33333*time.Microsecond, videoOut, audioOut)
	fs.AddSource("camera1")
	fs.Start()
	defer fs.Stop()

	const numFrames = 2000
	var wg sync.WaitGroup

	// Goroutine 1: continuously send video frames.
	wg.Add(1)
	go func() {
		defer wg.Done()
		frame := &media.VideoFrame{PTS: 1000, IsKeyframe: true}
		for i := 0; i < numFrames; i++ {
			sv.SendVideo(frame)
		}
	}()

	// Goroutine 2: continuously send audio frames.
	wg.Add(1)
	go func() {
		defer wg.Done()
		frame := &media.AudioFrame{PTS: 2000, Data: []byte{0x01}}
		for i := 0; i < numFrames; i++ {
			sv.SendAudio(frame)
		}
	}()

	// Goroutine 3: continuously send caption frames.
	wg.Add(1)
	go func() {
		defer wg.Done()
		frame := &ccx.CaptionFrame{}
		for i := 0; i < numFrames; i++ {
			sv.SendCaptions(frame)
		}
	}()

	// Goroutine 4: toggle frameSync on/off while frames are flowing.
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 200; i++ {
			if i%2 == 0 {
				sv.frameSync.Store(fs)
				sv.delayBuffer.Store(nil)
			} else {
				sv.frameSync.Store(nil)
				sv.delayBuffer.Store(db)
			}
		}
	}()

	wg.Wait()
}
