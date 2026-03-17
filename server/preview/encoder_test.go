package preview

import (
	"sync"
	"testing"
	"time"

	"github.com/zsiec/prism/distribution"
	"github.com/zsiec/prism/media"
)

// mockRelay captures broadcast calls for verification.
type mockRelay struct {
	mu        sync.Mutex
	videos    []*media.VideoFrame
	audios    []*media.AudioFrame
	videoInfo *distribution.VideoInfo
}

func (r *mockRelay) BroadcastVideo(frame *media.VideoFrame) {
	r.mu.Lock()
	defer r.mu.Unlock()
	// Deep copy WireData to decouple from encoder buffer reuse.
	cp := &media.VideoFrame{
		PTS:        frame.PTS,
		DTS:        frame.DTS,
		IsKeyframe: frame.IsKeyframe,
		Codec:      frame.Codec,
		GroupID:     frame.GroupID,
	}
	if frame.WireData != nil {
		cp.WireData = make([]byte, len(frame.WireData))
		copy(cp.WireData, frame.WireData)
	}
	if frame.SPS != nil {
		cp.SPS = make([]byte, len(frame.SPS))
		copy(cp.SPS, frame.SPS)
	}
	if frame.PPS != nil {
		cp.PPS = make([]byte, len(frame.PPS))
		copy(cp.PPS, frame.PPS)
	}
	r.videos = append(r.videos, cp)
}

func (r *mockRelay) BroadcastAudio(frame *media.AudioFrame) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.audios = append(r.audios, frame)
}

func (r *mockRelay) SetVideoInfo(info distribution.VideoInfo) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.videoInfo = &info
}

func (r *mockRelay) getVideos() []*media.VideoFrame {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]*media.VideoFrame, len(r.videos))
	copy(out, r.videos)
	return out
}

func (r *mockRelay) getVideoInfo() *distribution.VideoInfo {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.videoInfo
}

// makeYUV420 creates a YUV420 buffer of size w*h*3/2 filled with a
// deterministic pattern so we can verify frames aren't aliased.
func makeYUV420(w, h int, fill byte) []byte {
	size := w * h * 3 / 2
	buf := make([]byte, size)
	for i := range buf {
		buf[i] = fill
	}
	return buf
}

func TestEncoder_ProducesFrames(t *testing.T) {
	relay := &mockRelay{}

	enc, err := NewEncoder(Config{
		SourceKey: "test-cam1",
		Width:     426,
		Height:    240,
		Bitrate:   300_000,
		FPSNum:    30,
		FPSDen:    1,
		Relay:     relay,
	})
	if err != nil {
		t.Fatalf("NewEncoder failed: %v", err)
	}
	defer enc.Stop()

	// Send 10 frames of 1080p YUV.
	srcW, srcH := 1920, 1080
	for i := 0; i < 10; i++ {
		yuv := makeYUV420(srcW, srcH, byte(i+1))
		enc.Send(yuv, srcW, srcH, int64(i)*3000)
		// Small sleep to let the goroutine process.
		time.Sleep(50 * time.Millisecond)
	}

	// Wait for processing to complete.
	time.Sleep(500 * time.Millisecond)

	frames := relay.getVideos()
	if len(frames) == 0 {
		t.Fatal("expected at least one encoded frame from relay, got zero")
	}

	// Verify first frame is a keyframe (encoder produces IDR on first frame).
	if !frames[0].IsKeyframe {
		t.Error("expected first frame to be a keyframe")
	}

	// Verify WireData is non-empty (actual encoded data).
	for i, f := range frames {
		if len(f.WireData) == 0 {
			t.Errorf("frame %d has empty WireData", i)
		}
	}

	// Verify VideoInfo was set on relay (happens on first keyframe).
	info := relay.getVideoInfo()
	if info == nil {
		t.Fatal("expected VideoInfo to be set on relay")
	}
	if info.Width != 426 || info.Height != 240 {
		t.Errorf("expected VideoInfo dimensions 426x240, got %dx%d", info.Width, info.Height)
	}
}

func TestEncoder_NewestWinsDrop(t *testing.T) {
	relay := &mockRelay{}

	enc, err := NewEncoder(Config{
		SourceKey: "test-cam2",
		Width:     426,
		Height:    240,
		Bitrate:   300_000,
		FPSNum:    30,
		FPSDen:    1,
		Relay:     relay,
	})
	if err != nil {
		t.Fatalf("NewEncoder failed: %v", err)
	}
	defer enc.Stop()

	srcW, srcH := 640, 480

	// Flood 100 frames in a tight loop -- Send must never block.
	done := make(chan struct{})
	go func() {
		for i := 0; i < 100; i++ {
			yuv := makeYUV420(srcW, srcH, byte(i))
			enc.Send(yuv, srcW, srcH, int64(i)*3000)
		}
		close(done)
	}()

	select {
	case <-done:
		// Good -- Send never blocked.
	case <-time.After(2 * time.Second):
		t.Fatal("Send blocked for >2s, expected non-blocking newest-wins drop")
	}
}

func TestEncoder_StopDrainsCleanly(t *testing.T) {
	relay := &mockRelay{}

	enc, err := NewEncoder(Config{
		SourceKey: "test-cam3",
		Width:     426,
		Height:    240,
		Bitrate:   300_000,
		FPSNum:    30,
		FPSDen:    1,
		Relay:     relay,
	})
	if err != nil {
		t.Fatalf("NewEncoder failed: %v", err)
	}

	// Stop immediately -- should return promptly.
	done := make(chan struct{})
	go func() {
		enc.Stop()
		close(done)
	}()

	select {
	case <-done:
		// Good -- Stop returned promptly.
	case <-time.After(2 * time.Second):
		t.Fatal("Stop did not return within 2s")
	}
}
