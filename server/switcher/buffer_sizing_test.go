package switcher

import (
	"sync"
	"testing"
	"time"

	"github.com/zsiec/prism/media"
	"github.com/zsiec/switchframe/server/transition"
)

// source_decoder slices buffer to actual frame dimensions, not pool buffer size.
func TestSourceDecoder_YUVBufferMatchesFrameSize(t *testing.T) {
	// Pool is 1080p (bufSize=3,110,400) but decoder produces 320x240 frames.
	pool := NewFramePool(4, 1920, 1080, ColorYUV420_8bit)

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

	sd := newSourceDecoder("cam1", factory, callback, pool, nil, nil, nil)
	defer sd.Close()

	sd.Send(&media.VideoFrame{
		PTS: 90000, DTS: 90000, IsKeyframe: true,
		WireData: []byte{0x00, 0x00, 0x00, 0x01, 0x65, 0xAA},
		SPS:      []byte{0x67, 0x42, 0x00, 0x1e},
		PPS:      []byte{0x68, 0xce, 0x38, 0x80},
		Codec:    "h264", GroupID: 1,
	}, 0)

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

	expectedSize := ColorYUV420_8bit.FrameSize(pf.Width, pf.Height) // ColorYUV420_8bit.FrameSize(320, 240) = 115200
	if len(pf.Data) != expectedSize {
		t.Errorf("pf.Data len = %d, want %d (Width=%d, Height=%d)",
			len(pf.Data), expectedSize, pf.Width, pf.Height)
	}
}

// DeepCopy preserves source YUV slice length, not pool buffer capacity.
func TestDeepCopy_PreservesYUVLength(t *testing.T) {
	pool := NewFramePool(4, 1920, 1080, ColorYUV420_8bit)

	// Create a 720p frame using a pool buffer sliced to 720p.
	buf := pool.Acquire()
	yuvSize720p := ColorYUV420_8bit.FrameSize(1280, 720)
	for i := 0; i < yuvSize720p; i++ {
		buf[i] = byte(i % 251)
	}
	pf := &ProcessingFrame{
		Data:   buf[:yuvSize720p],
		Width:  1280,
		Height: 720,
		PTS:    90000,
		pool:   pool,
	}

	cp := pf.DeepCopy()

	if len(cp.Data) != yuvSize720p {
		t.Errorf("DeepCopy YUV len = %d, want %d", len(cp.Data), yuvSize720p)
	}

	// Verify data was copied correctly.
	for i := 0; i < yuvSize720p; i++ {
		if cp.Data[i] != pf.Data[i] {
			t.Fatalf("DeepCopy data mismatch at byte %d: got %d, want %d",
				i, cp.Data[i], pf.Data[i])
		}
	}

	// The underlying buffer should still have full pool capacity
	// (so Release works correctly).
	poolBufSize := ColorYUV420_8bit.FrameSize(1920, 1080)
	if cap(cp.Data) < poolBufSize {
		t.Errorf("DeepCopy cap = %d, want >= %d (pool buf)", cap(cp.Data), poolBufSize)
	}
}

// DeepCopy without pool preserves YUV slice length.
func TestDeepCopy_NoPool_PreservesLength(t *testing.T) {
	pf := &ProcessingFrame{
		Data:   make([]byte, 115200), // 320x240
		Width:  320,
		Height: 240,
	}

	cp := pf.DeepCopy()

	if len(cp.Data) != 115200 {
		t.Errorf("DeepCopy YUV len = %d, want 115200", len(cp.Data))
	}
}

// broadcastProcessed slices buffer to actual frame dimensions.
// Tests the pattern used in broadcastProcessed: acquire pool buffer, copy
// source data, slice to actual frame dimensions.
func TestBroadcastProcessed_YUVBufferMatchesFrameSize(t *testing.T) {
	pool := NewFramePool(4, 1920, 1080, ColorYUV420_8bit)

	width, height := 1280, 720
	yuvSize720p := ColorYUV420_8bit.FrameSize(width, height)
	yuv := make([]byte, yuvSize720p)
	for i := range yuv {
		yuv[i] = byte(i % 251)
	}

	// Replicate the fixed broadcastProcessed pattern:
	// acquire pool buf, copy source data, slice to actual size.
	expectedSize := ColorYUV420_8bit.FrameSize(width, height)
	buf := pool.Acquire()
	copy(buf, yuv[:expectedSize])
	pf := &ProcessingFrame{
		Data: buf[:expectedSize], Width: width, Height: height,
		pool: pool,
	}

	// The YUV buffer should match the frame dimensions.
	if len(pf.Data) != expectedSize {
		t.Errorf("broadcastProcessed pf.Data len = %d, want %d",
			len(pf.Data), expectedSize)
	}

	// Underlying buffer should still have pool capacity for Release.
	poolBufSize := ColorYUV420_8bit.FrameSize(1920, 1080)
	if cap(pf.Data) < poolBufSize {
		t.Errorf("cap = %d, want >= %d (pool buf)", cap(pf.Data), poolBufSize)
	}

	// Data should match source.
	for i := 0; i < expectedSize; i++ {
		if pf.Data[i] != yuv[i] {
			t.Fatalf("data mismatch at byte %d: got %d, want %d", i, pf.Data[i], yuv[i])
		}
	}
}

// Pool buffers round-trip correctly through Release even when sliced.
func TestFramePool_ReleaseAcceptsSubslice(t *testing.T) {
	pool := NewFramePool(2, 1920, 1080, ColorYUV420_8bit)
	poolBufSize := ColorYUV420_8bit.FrameSize(1920, 1080)

	// Acquire, slice to 720p, release.
	buf := pool.Acquire()
	smallSlice := buf[:ColorYUV420_8bit.FrameSize(1280, 720)]
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
