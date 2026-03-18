package preview

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/zsiec/prism/distribution"
	"github.com/zsiec/prism/media"

	"github.com/zsiec/switchframe/server/codec"
)

// skipWithoutEncoder skips the test if no H.264 encoder is available
// (e.g., CI builds without FFmpeg/cgo).
func skipWithoutEncoder(t *testing.T) {
	t.Helper()
	enc, err := codec.NewPreviewEncoder(320, 240, 100_000, 30, 1)
	if err != nil {
		t.Skipf("no preview encoder available: %v", err)
	}
	enc.Close()
}

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
		GroupID:    frame.GroupID,
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
	skipWithoutEncoder(t)
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
	skipWithoutEncoder(t)
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

	// Use small frames -- this test validates the drop policy, not encoding.
	// Large frames (640x480 = 460KB each) cause 46MB of allocations under
	// -race + -coverprofile which can exceed the timeout on slow CI runners.
	srcW, srcH := 160, 120

	// Flood 20 frames in a tight loop -- Send must never block.
	// Channel capacity is 4, so 20 frames is 5x overflow -- plenty to test drop.
	done := make(chan struct{})
	go func() {
		for i := 0; i < 20; i++ {
			yuv := makeYUV420(srcW, srcH, byte(i))
			enc.Send(yuv, srcW, srcH, int64(i)*3000)
		}
		close(done)
	}()

	select {
	case <-done:
		// Good -- Send never blocked.
	case <-time.After(5 * time.Second):
		t.Fatal("Send blocked for >5s, expected non-blocking newest-wins drop")
	}
}

func TestEncoder_SendOwned_NoCopy(t *testing.T) {
	skipWithoutEncoder(t)
	relay := &mockRelay{}

	enc, err := NewEncoder(Config{
		SourceKey: "test-owned1",
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

	srcW, srcH := 1920, 1080
	yuv := makeYUV420(srcW, srcH, 0xAA)

	// SendOwned should accept the buffer without deep-copying it.
	// We verify by checking that FramesIn increments.
	enc.SendOwned(yuv, srcW, srcH, 3000, nil)

	time.Sleep(200 * time.Millisecond)

	stats := enc.GetStats()
	if stats.FramesIn < 1 {
		t.Fatalf("expected FramesIn >= 1 after SendOwned, got %d", stats.FramesIn)
	}
}

func TestEncoder_SendOwned_ReleaseCalledAfterProcess(t *testing.T) {
	skipWithoutEncoder(t)
	relay := &mockRelay{}

	enc, err := NewEncoder(Config{
		SourceKey: "test-owned2",
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

	var released atomic.Int32
	releaseFn := func(buf []byte) {
		released.Add(1)
	}

	// Send a frame with a release callback.
	yuv := makeYUV420(srcW, srcH, 0x55)
	enc.SendOwned(yuv, srcW, srcH, 3000, releaseFn)

	// Wait for the encode goroutine to process.
	time.Sleep(500 * time.Millisecond)

	if released.Load() != 1 {
		t.Fatalf("expected release callback to be called once, got %d", released.Load())
	}
}

func TestEncoder_SendOwned_ReleaseCalledOnDrop(t *testing.T) {
	skipWithoutEncoder(t)
	relay := &mockRelay{}

	enc, err := NewEncoder(Config{
		SourceKey: "test-owned3",
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

	// To force drops, we need to fill the channel (cap 2) and then overflow.
	// First, stop the encoder from consuming by filling with blocking work.
	// We'll send many frames rapidly with release callbacks and verify
	// all callbacks eventually fire (both processed and dropped).

	const totalFrames = 20
	var released atomic.Int32

	releaseFn := func(buf []byte) {
		released.Add(1)
	}

	// Flood frames -- channel cap is 2, so most will be dropped.
	for i := 0; i < totalFrames; i++ {
		yuv := makeYUV420(srcW, srcH, byte(i))
		enc.SendOwned(yuv, srcW, srcH, int64(i)*3000, releaseFn)
	}

	// Wait for processing to complete.
	time.Sleep(1 * time.Second)

	// Every frame's release must have been called -- whether processed or dropped.
	got := released.Load()
	if got != totalFrames {
		t.Fatalf("expected all %d release callbacks to fire, got %d", totalFrames, got)
	}
}

func TestEncoder_StopDrainsCleanly(t *testing.T) {
	skipWithoutEncoder(t)
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

func TestEncoder_FrameInterval_SkipsFrames(t *testing.T) {
	skipWithoutEncoder(t)
	relay := &mockRelay{}

	enc, err := NewEncoder(Config{
		SourceKey:     "test-skip",
		Width:         320,
		Height:        240,
		Bitrate:       100_000,
		FPSNum:        30,
		FPSDen:        1,
		Relay:         relay,
		FrameInterval: 2, // encode every 2nd frame
	})
	if err != nil {
		t.Fatal(err)
	}

	// Send 10 frames synchronously
	for i := 0; i < 10; i++ {
		yuv := makeYUV420(320, 240, byte(i))
		enc.Send(yuv, 320, 240, int64(i)*3003)
	}

	// Wait for processing
	time.Sleep(500 * time.Millisecond)
	enc.Stop()

	framesIn := enc.stats.FramesIn.Load()
	framesOut := enc.stats.FramesOut.Load()

	if framesIn != 10 {
		t.Errorf("expected 10 frames in, got %d", framesIn)
	}

	// With interval=2, roughly half should be encoded (some may be dropped by channel)
	if framesOut > 6 {
		t.Errorf("expected ≤6 frames out with interval=2 from 10 input, got %d", framesOut)
	}
	if framesOut == 0 {
		t.Error("expected at least 1 frame out")
	}
}

func TestEncoder_FrameInterval_Default(t *testing.T) {
	// FrameInterval=0 should behave as 1 (encode all frames)
	skipWithoutEncoder(t)
	relay := &mockRelay{}

	enc, err := NewEncoder(Config{
		SourceKey:     "test-default",
		Width:         320,
		Height:        240,
		Bitrate:       100_000,
		FPSNum:        30,
		FPSDen:        1,
		Relay:         relay,
		FrameInterval: 0, // should default to 1
	})
	if err != nil {
		t.Fatal(err)
	}

	if enc.frameInterval != 1 {
		t.Errorf("expected frameInterval=1 for Config.FrameInterval=0, got %d", enc.frameInterval)
	}
	enc.Stop()
}
