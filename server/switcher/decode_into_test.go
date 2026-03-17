package switcher

import (
	"sync"
	"testing"
	"time"

	"github.com/zsiec/prism/media"
	"github.com/zsiec/switchframe/server/transition"
)

// mockDecodeIntoDecoder implements both VideoDecoder and the decodeIntoer
// interface, allowing us to test the DecodeInto optimization path.
type mockDecodeIntoDecoder struct {
	width  int
	height int
	fill   byte // fill value for generated YUV data

	// Track which method was called and whether dst was used.
	decodeIntoCalled bool
	usedDstBuffer    bool
}

func (d *mockDecodeIntoDecoder) Decode(data []byte) ([]byte, int, int, error) {
	yuvSize := d.width * d.height * 3 / 2
	result := make([]byte, yuvSize)
	for i := range result {
		result[i] = d.fill
	}
	return result, d.width, d.height, nil
}

func (d *mockDecodeIntoDecoder) DecodeInto(data []byte, dst []byte) ([]byte, int, int, error) {
	d.decodeIntoCalled = true
	yuvSize := d.width * d.height * 3 / 2
	if len(dst) >= yuvSize {
		// Write directly into dst — this is the zero-copy path.
		d.usedDstBuffer = true
		for i := 0; i < yuvSize; i++ {
			dst[i] = d.fill
		}
		return dst[:yuvSize], d.width, d.height, nil
	}
	// dst too small — fallback to allocation.
	result := make([]byte, yuvSize)
	for i := range result {
		result[i] = d.fill
	}
	return result, d.width, d.height, nil
}

func (d *mockDecodeIntoDecoder) Close() {}

// TestDecodeInto_DirectWriteSkipsCopy verifies that when a decoder supports
// DecodeInto and the pool buffer is large enough, the sourceDecoder uses the
// pool buffer directly without an extra copy.
func TestDecodeInto_DirectWriteSkipsCopy(t *testing.T) {
	width, height := 320, 240
	pool := NewFramePool(4, width, height) // exact match to source resolution

	mockDec := &mockDecodeIntoDecoder{width: width, height: height, fill: 0xAB}
	factory := func() (transition.VideoDecoder, error) {
		return mockDec, nil
	}

	var mu sync.Mutex
	var received []*ProcessingFrame
	callback := func(sourceKey string, pf *ProcessingFrame) {
		mu.Lock()
		received = append(received, pf)
		mu.Unlock()
	}

	sd := newSourceDecoder("cam-test", factory, callback, pool, nil)
	defer sd.Close()

	sd.Send(&media.VideoFrame{
		PTS: 90000, DTS: 90000, IsKeyframe: true,
		WireData: []byte{0x00, 0x00, 0x00, 0x01, 0x65, 0xAA},
		SPS:      []byte{0x67, 0x42, 0x00, 0x1e},
		PPS:      []byte{0x68, 0xce, 0x38, 0x80},
		Codec:    "h264", GroupID: 1,
	}, time.Now().UnixNano())

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

	// Verify DecodeInto was called (not Decode).
	if !mockDec.decodeIntoCalled {
		t.Error("DecodeInto was not called; expected it to be used when pool is available")
	}

	// Verify the decoder used the dst buffer directly.
	if !mockDec.usedDstBuffer {
		t.Error("DecodeInto did not use the dst buffer; expected direct write for matching resolution")
	}

	// Verify frame data is correct.
	expectedSize := width * height * 3 / 2
	if len(pf.YUV) != expectedSize {
		t.Errorf("pf.YUV len = %d, want %d", len(pf.YUV), expectedSize)
	}

	// Verify the data was filled correctly.
	for i := 0; i < expectedSize; i++ {
		if pf.YUV[i] != 0xAB {
			t.Fatalf("pf.YUV[%d] = %d, want 0xAB", i, pf.YUV[i])
		}
	}

	// Verify the frame is associated with the pool for proper release.
	if pf.pool != pool {
		t.Error("frame pool not set; zero-copy path should preserve pool association")
	}
}

// TestDecodeInto_FallbackWhenPoolTooSmall verifies that when the pool buffer
// is too small for the decoded frame, the decoder falls back to allocation
// and the sourceDecoder handles it correctly.
func TestDecodeInto_FallbackWhenPoolTooSmall(t *testing.T) {
	// Decoder produces 640x480, but pool has 320x240 buffers.
	decWidth, decHeight := 640, 480
	pool := NewFramePool(4, 320, 240) // too small for 640x480

	mockDec := &mockDecodeIntoDecoder{width: decWidth, height: decHeight, fill: 0xCD}
	factory := func() (transition.VideoDecoder, error) {
		return mockDec, nil
	}

	var mu sync.Mutex
	var received []*ProcessingFrame
	callback := func(sourceKey string, pf *ProcessingFrame) {
		mu.Lock()
		received = append(received, pf)
		mu.Unlock()
	}

	sd := newSourceDecoder("cam-test-small", factory, callback, pool, nil)
	defer sd.Close()

	sd.Send(&media.VideoFrame{
		PTS: 90000, DTS: 90000, IsKeyframe: true,
		WireData: []byte{0x00, 0x00, 0x00, 0x01, 0x65, 0xAA},
		SPS:      []byte{0x67, 0x42, 0x00, 0x1e},
		PPS:      []byte{0x68, 0xce, 0x38, 0x80},
		Codec:    "h264", GroupID: 1,
	}, time.Now().UnixNano())

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

	// DecodeInto should have been called.
	if !mockDec.decodeIntoCalled {
		t.Error("DecodeInto was not called")
	}

	// The dst buffer was too small, so decoder should NOT have used it.
	if mockDec.usedDstBuffer {
		t.Error("DecodeInto should not have used the dst buffer (too small)")
	}

	// Frame data should still be correct.
	expectedSize := decWidth * decHeight * 3 / 2
	if len(pf.YUV) != expectedSize {
		t.Errorf("pf.YUV len = %d, want %d", len(pf.YUV), expectedSize)
	}
	for i := 0; i < expectedSize; i++ {
		if pf.YUV[i] != 0xCD {
			t.Fatalf("pf.YUV[%d] = %d, want 0xCD", i, pf.YUV[i])
		}
	}

	// Frame should NOT be associated with the pool (it's a heap allocation).
	if pf.pool != nil {
		t.Error("frame should not have pool association when pool buffer was too small")
	}
}

// TestDecodeInto_FallbackForNonDecodeIntoDecoder verifies that decoders that
// don't support DecodeInto still work correctly through the standard Decode path.
func TestDecodeInto_FallbackForNonDecodeIntoDecoder(t *testing.T) {
	width, height := 320, 240
	pool := NewFramePool(4, width, height)

	// Use the standard mock decoder which does NOT implement DecodeInto.
	factory := func() (transition.VideoDecoder, error) {
		return transition.NewMockDecoder(width, height), nil
	}

	var mu sync.Mutex
	var received []*ProcessingFrame
	callback := func(sourceKey string, pf *ProcessingFrame) {
		mu.Lock()
		received = append(received, pf)
		mu.Unlock()
	}

	sd := newSourceDecoder("cam-fallback", factory, callback, pool, nil)
	defer sd.Close()

	sd.Send(&media.VideoFrame{
		PTS: 90000, DTS: 90000, IsKeyframe: true,
		WireData: []byte{0x00, 0x00, 0x00, 0x01, 0x65, 0xAA},
		SPS:      []byte{0x67, 0x42, 0x00, 0x1e},
		PPS:      []byte{0x68, 0xce, 0x38, 0x80},
		Codec:    "h264", GroupID: 1,
	}, time.Now().UnixNano())

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

	// Standard mock decoder, so pool should be used via the copy path.
	expectedSize := width * height * 3 / 2
	if len(pf.YUV) != expectedSize {
		t.Errorf("pf.YUV len = %d, want %d", len(pf.YUV), expectedSize)
	}

	// Frame should be associated with the pool (copy into pool buffer).
	if pf.pool != pool {
		t.Error("frame pool not set; standard path should copy into pool buffer")
	}
}

// TestDecodeInto_NilPool verifies that when there is no pool, DecodeInto
// is not attempted and the standard Decode path is used.
func TestDecodeInto_NilPool(t *testing.T) {
	width, height := 320, 240

	mockDec := &mockDecodeIntoDecoder{width: width, height: height, fill: 0xEF}
	factory := func() (transition.VideoDecoder, error) {
		return mockDec, nil
	}

	var mu sync.Mutex
	var received []*ProcessingFrame
	callback := func(sourceKey string, pf *ProcessingFrame) {
		mu.Lock()
		received = append(received, pf)
		mu.Unlock()
	}

	// Pass nil pool — should fall back to Decode() not DecodeInto().
	sd := newSourceDecoder("cam-nopool", factory, callback, nil, nil)
	defer sd.Close()

	sd.Send(&media.VideoFrame{
		PTS: 90000, DTS: 90000, IsKeyframe: true,
		WireData: []byte{0x00, 0x00, 0x00, 0x01, 0x65, 0xAA},
		SPS:      []byte{0x67, 0x42, 0x00, 0x1e},
		PPS:      []byte{0x68, 0xce, 0x38, 0x80},
		Codec:    "h264", GroupID: 1,
	}, time.Now().UnixNano())

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

	// With nil pool, DecodeInto should NOT be called (falls to Decode path).
	if mockDec.decodeIntoCalled {
		t.Error("DecodeInto should not be called when pool is nil")
	}

	expectedSize := width * height * 3 / 2
	if len(pf.YUV) != expectedSize {
		t.Errorf("pf.YUV len = %d, want %d", len(pf.YUV), expectedSize)
	}
}
