package switcher

import (
	"sync"
	"testing"
	"time"

	"github.com/zsiec/prism/media"
	"github.com/zsiec/switchframe/server/transition"
)

// Fix 5: source_decoder should slice buffer to actual frame size, not pool size.
func TestSourceDecoder_YUVBufferMatchesFrameSize(t *testing.T) {
	// Pool is 1080p (bufSize=3,110,400) but decoder produces 320x240 frames.
	pool := NewFramePool(4, 1920, 1080)

	factory := func() (transition.VideoDecoder, error) {
		return transition.NewMockDecoder(320, 240), nil
	}

	var mu sync.Mutex
	var received []*ProcessingFrame
	callback := func(sourceKey string, pf *ProcessingFrame) {
		mu.Lock()
		received = append(received, pf)
		mu.Unlock()
	}

	sd := newSourceDecoder("cam1", factory, callback, pool)
	defer sd.Close()

	sd.Send(&media.VideoFrame{
		PTS: 90000, DTS: 90000, IsKeyframe: true,
		WireData: []byte{0x00, 0x00, 0x00, 0x01, 0x65, 0xAA},
		SPS:      []byte{0x67, 0x42, 0x00, 0x1e},
		PPS:      []byte{0x68, 0xce, 0x38, 0x80},
		Codec:    "h264", GroupID: 1,
	})

	deadline := time.After(2 * time.Second)
	for {
		mu.Lock()
		n := len(received)
		mu.Unlock()
		if n >= 1 {
			break
		}
		select {
		case <-deadline:
			t.Fatal("timeout waiting for callback")
		default:
			time.Sleep(5 * time.Millisecond)
		}
	}

	mu.Lock()
	defer mu.Unlock()
	pf := received[0]

	expectedSize := pf.Width * pf.Height * 3 / 2 // 320*240*3/2 = 115200
	if len(pf.YUV) != expectedSize {
		t.Errorf("pf.YUV len = %d, want %d (Width=%d, Height=%d)",
			len(pf.YUV), expectedSize, pf.Width, pf.Height)
	}
}

// Fix 6: DeepCopy should preserve source YUV slice length, not expand to pool size.
func TestDeepCopy_PreservesYUVLength(t *testing.T) {
	pool := NewFramePool(4, 1920, 1080)

	// Create a 720p frame using a pool buffer sliced to 720p.
	buf := pool.Acquire()
	yuvSize720p := 1280 * 720 * 3 / 2
	for i := 0; i < yuvSize720p; i++ {
		buf[i] = byte(i % 251)
	}
	pf := &ProcessingFrame{
		YUV:    buf[:yuvSize720p],
		Width:  1280,
		Height: 720,
		PTS:    90000,
		pool:   pool,
	}

	cp := pf.DeepCopy()

	if len(cp.YUV) != yuvSize720p {
		t.Errorf("DeepCopy YUV len = %d, want %d", len(cp.YUV), yuvSize720p)
	}

	// Verify data was copied correctly.
	for i := 0; i < yuvSize720p; i++ {
		if cp.YUV[i] != pf.YUV[i] {
			t.Fatalf("DeepCopy data mismatch at byte %d: got %d, want %d",
				i, cp.YUV[i], pf.YUV[i])
		}
	}

	// The underlying buffer should still have full pool capacity
	// (so Release works correctly).
	poolBufSize := 1920 * 1080 * 3 / 2
	if cap(cp.YUV) < poolBufSize {
		t.Errorf("DeepCopy cap = %d, want >= %d (pool buf)", cap(cp.YUV), poolBufSize)
	}
}

// Fix 6: DeepCopy without pool should also preserve length.
func TestDeepCopy_NoPool_PreservesLength(t *testing.T) {
	pf := &ProcessingFrame{
		YUV:    make([]byte, 115200), // 320x240
		Width:  320,
		Height: 240,
	}

	cp := pf.DeepCopy()

	if len(cp.YUV) != 115200 {
		t.Errorf("DeepCopy YUV len = %d, want 115200", len(cp.YUV))
	}
}

// Fix 7: broadcastProcessed should slice buffer to actual frame size.
// Tests the pattern used in broadcastProcessed: acquire pool buffer, copy
// source data, slice to actual frame dimensions.
func TestBroadcastProcessed_YUVBufferMatchesFrameSize(t *testing.T) {
	pool := NewFramePool(4, 1920, 1080)

	width, height := 1280, 720
	yuvSize720p := width * height * 3 / 2
	yuv := make([]byte, yuvSize720p)
	for i := range yuv {
		yuv[i] = byte(i % 251)
	}

	// Replicate the fixed broadcastProcessed pattern:
	// acquire pool buf, copy source data, slice to actual size.
	expectedSize := width * height * 3 / 2
	buf := pool.Acquire()
	copy(buf, yuv[:expectedSize])
	pf := &ProcessingFrame{
		YUV: buf[:expectedSize], Width: width, Height: height,
		pool: pool,
	}

	// The YUV buffer should match the frame dimensions.
	if len(pf.YUV) != expectedSize {
		t.Errorf("broadcastProcessed pf.YUV len = %d, want %d",
			len(pf.YUV), expectedSize)
	}

	// Underlying buffer should still have pool capacity for Release.
	poolBufSize := 1920 * 1080 * 3 / 2
	if cap(pf.YUV) < poolBufSize {
		t.Errorf("cap = %d, want >= %d (pool buf)", cap(pf.YUV), poolBufSize)
	}

	// Data should match source.
	for i := 0; i < expectedSize; i++ {
		if pf.YUV[i] != yuv[i] {
			t.Fatalf("data mismatch at byte %d: got %d, want %d", i, pf.YUV[i], yuv[i])
		}
	}
}

// Fix 5+6: Pool buffers should round-trip correctly through Release even when sliced.
func TestFramePool_ReleaseAcceptsSubslice(t *testing.T) {
	pool := NewFramePool(2, 1920, 1080)
	poolBufSize := 1920 * 1080 * 3 / 2

	// Acquire, slice to 720p, release.
	buf := pool.Acquire()
	smallSlice := buf[:1280*720*3/2]
	pool.Release(smallSlice)

	// Acquire again — should get a hit (buffer was returned).
	buf2 := pool.Acquire()
	if len(buf2) != poolBufSize {
		t.Errorf("re-acquired buf len = %d, want %d", len(buf2), poolBufSize)
	}

	hits, misses := pool.Stats()
	if hits != 2 || misses != 0 {
		t.Errorf("stats = hits:%d misses:%d, want hits:2 misses:0", hits, misses)
	}
}
