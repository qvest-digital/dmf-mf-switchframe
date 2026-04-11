package switcher

import (
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/zsiec/prism/distribution"
	"github.com/zsiec/prism/media"
	"github.com/zsiec/switchframe/server/transition"
)

func TestThumbnailCacheStoreAndGet(t *testing.T) {
	cache := NewThumbnailCache()

	jpeg := []byte{0xFF, 0xD8, 0xFF, 0xE0, 0x00, 0x10} // fake JPEG header
	now := time.Now()

	cache.Store("source-1", jpeg, 160, 90)

	info, ok := cache.Get("source-1")
	if !ok {
		t.Fatal("expected to find stored thumbnail")
	}
	if len(info.JPEG) != len(jpeg) {
		t.Fatalf("JPEG length: got %d, want %d", len(info.JPEG), len(jpeg))
	}
	for i := range jpeg {
		if info.JPEG[i] != jpeg[i] {
			t.Fatalf("JPEG byte %d: got %02x, want %02x", i, info.JPEG[i], jpeg[i])
		}
	}
	if info.Width != 160 {
		t.Fatalf("Width: got %d, want 160", info.Width)
	}
	if info.Height != 90 {
		t.Fatalf("Height: got %d, want 90", info.Height)
	}
	if info.CapturedAt.Before(now) {
		t.Fatalf("CapturedAt should be >= test start time")
	}
}

func TestThumbnailCacheGetAll(t *testing.T) {
	cache := NewThumbnailCache()

	cache.Store("src-1", []byte{0x01}, 160, 90)
	cache.Store("src-2", []byte{0x02}, 160, 90)
	cache.Store("src-3", []byte{0x03}, 160, 90)

	all := cache.GetAll()
	if len(all) != 3 {
		t.Fatalf("GetAll length: got %d, want 3", len(all))
	}

	for _, key := range []string{"src-1", "src-2", "src-3"} {
		if _, ok := all[key]; !ok {
			t.Fatalf("GetAll missing key %q", key)
		}
	}

	// Verify values match
	if all["src-1"].JPEG[0] != 0x01 {
		t.Fatalf("src-1 JPEG byte: got %02x, want 01", all["src-1"].JPEG[0])
	}
	if all["src-2"].JPEG[0] != 0x02 {
		t.Fatalf("src-2 JPEG byte: got %02x, want 02", all["src-2"].JPEG[0])
	}
}

func TestThumbnailCacheGetMissing(t *testing.T) {
	cache := NewThumbnailCache()

	_, ok := cache.Get("nonexistent")
	if ok {
		t.Fatal("expected Get for nonexistent key to return false")
	}
}

func TestThumbnailCacheRequestCapture(t *testing.T) {
	cache := NewThumbnailCache()

	if cache.ShouldCapture() {
		t.Fatal("ShouldCapture should be false initially")
	}

	cache.RequestCapture()

	if !cache.ShouldCapture() {
		t.Fatal("ShouldCapture should be true after RequestCapture")
	}
}

func TestThumbnailCacheShouldCaptureIdleTimeout(t *testing.T) {
	cache := NewThumbnailCache()

	// Set requested flag but with a lastPoll timestamp in the past (> 5s ago)
	cache.requested.Store(true)
	cache.lastPoll.Store(time.Now().Add(-6 * time.Second).UnixMilli())

	if cache.ShouldCapture() {
		t.Fatal("ShouldCapture should be false when lastPoll is > 5s ago")
	}
}

func TestThumbnailCacheClearRequest(t *testing.T) {
	cache := NewThumbnailCache()

	cache.RequestCapture()
	if !cache.ShouldCapture() {
		t.Fatal("ShouldCapture should be true after RequestCapture")
	}

	cache.ClearRequest()
	if cache.ShouldCapture() {
		t.Fatal("ShouldCapture should be false after ClearRequest")
	}
}

func TestThumbnailCacheOverwrite(t *testing.T) {
	cache := NewThumbnailCache()

	cache.Store("node-1", []byte{0x01}, 160, 90)
	cache.Store("node-1", []byte{0x02, 0x03}, 320, 180)

	info, ok := cache.Get("node-1")
	if !ok {
		t.Fatal("expected to find stored thumbnail")
	}
	if len(info.JPEG) != 2 {
		t.Fatalf("JPEG length: got %d, want 2", len(info.JPEG))
	}
	if info.JPEG[0] != 0x02 || info.JPEG[1] != 0x03 {
		t.Fatalf("JPEG bytes: got %v, want [02 03]", info.JPEG)
	}
	if info.Width != 320 {
		t.Fatalf("Width: got %d, want 320", info.Width)
	}
	if info.Height != 180 {
		t.Fatalf("Height: got %d, want 180", info.Height)
	}
}

func TestThumbnailCacheConcurrent(t *testing.T) {
	cache := NewThumbnailCache()

	const goroutines = 10
	const iterations = 100

	var wg sync.WaitGroup
	wg.Add(goroutines * 2) // writers + readers

	// Writers
	for g := 0; g < goroutines; g++ {
		go func(id int) {
			defer wg.Done()
			key := fmt.Sprintf("node-%d", id)
			for i := 0; i < iterations; i++ {
				cache.Store(key, []byte{byte(i)}, 160, 90)
			}
		}(g)
	}

	// Readers
	for g := 0; g < goroutines; g++ {
		go func(id int) {
			defer wg.Done()
			key := fmt.Sprintf("node-%d", id)
			for i := 0; i < iterations; i++ {
				cache.Get(key)
				cache.GetAll()
				cache.ShouldCapture()
				cache.RequestCapture()
			}
		}(g)
	}

	wg.Wait()
}

func TestSourceDecoderThumbnailCapture(t *testing.T) {
	cache := NewThumbnailCache()
	cache.RequestCapture()

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

	sd := newSourceDecoder("cam1", factory, callback, nil, nil, nil, nil)
	sd.thumbnailCache = cache
	defer sd.Close()

	// Send a keyframe
	frame := &media.VideoFrame{
		PTS:        90000,
		DTS:        90000,
		IsKeyframe: true,
		WireData:   []byte{0x00, 0x00, 0x00, 0x01, 0x65, 0xAA},
		SPS:        []byte{0x67, 0x42, 0x00, 0x1e},
		PPS:        []byte{0x68, 0xce, 0x38, 0x80},
		Codec:      "h264",
		GroupID:    1,
	}
	sd.Send(frame, time.Now().UnixNano())

	// Wait for decode + thumbnail goroutine to complete
	deadline := time.After(3 * time.Second)
	for {
		info, ok := cache.Get("cam1")
		if ok && len(info.JPEG) > 0 {
			// Verify it's a valid JPEG (SOI marker)
			if info.JPEG[0] != 0xFF || info.JPEG[1] != 0xD8 {
				t.Fatalf("thumbnail not valid JPEG: got %02x %02x", info.JPEG[0], info.JPEG[1])
			}
			if info.Width != thumbnailWidth || info.Height != thumbnailHeight {
				t.Fatalf("thumbnail dimensions: got %dx%d, want %dx%d",
					info.Width, info.Height, thumbnailWidth, thumbnailHeight)
			}
			return
		}
		select {
		case <-deadline:
			t.Fatal("timeout waiting for thumbnail capture")
		default:
			time.Sleep(10 * time.Millisecond)
		}
	}
}

func TestSourceDecoderThumbnailNotCapturedWhenNotRequested(t *testing.T) {
	cache := NewThumbnailCache()
	// Don't call RequestCapture — ShouldCapture() returns false

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

	sd := newSourceDecoder("cam1", factory, callback, nil, nil, nil, nil)
	sd.thumbnailCache = cache
	defer sd.Close()

	// Send a keyframe
	frame := &media.VideoFrame{
		PTS:        90000,
		DTS:        90000,
		IsKeyframe: true,
		WireData:   []byte{0x00, 0x00, 0x00, 0x01, 0x65, 0xAA},
		SPS:        []byte{0x67, 0x42, 0x00, 0x1e},
		PPS:        []byte{0x68, 0xce, 0x38, 0x80},
		Codec:      "h264",
		GroupID:    1,
	}
	sd.Send(frame, time.Now().UnixNano())

	// Wait for decode to complete
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

	// Small delay to let any async thumbnail goroutine run (if it incorrectly did)
	time.Sleep(50 * time.Millisecond)

	_, ok := cache.Get("cam1")
	if ok {
		t.Fatal("thumbnail should NOT be captured when ShouldCapture() is false")
	}
}

func TestSourceDecoderThumbnailNilCacheIsNoop(t *testing.T) {
	// Ensure no panic when thumbnailCache is nil
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

	sd := newSourceDecoder("cam1", factory, callback, nil, nil, nil, nil)
	// thumbnailCache is nil by default
	defer sd.Close()

	frame := &media.VideoFrame{
		PTS:        90000,
		DTS:        90000,
		IsKeyframe: true,
		WireData:   []byte{0x00, 0x00, 0x00, 0x01, 0x65, 0xAA},
		SPS:        []byte{0x67, 0x42, 0x00, 0x1e},
		PPS:        []byte{0x68, 0xce, 0x38, 0x80},
		Codec:      "h264",
		GroupID:    1,
	}
	sd.Send(frame, time.Now().UnixNano())

	// Wait for decode to complete
	deadline := time.After(2 * time.Second)
	for {
		mu.Lock()
		n := len(received)
		mu.Unlock()
		if n >= 1 {
			return // success — no panic
		}
		select {
		case <-deadline:
			t.Fatal("timeout waiting for callback")
		default:
			time.Sleep(5 * time.Millisecond)
		}
	}
}

func TestSwitcherThumbnailCacheAccessor(t *testing.T) {
	relay := distribution.NewRelay()
	sw := New(relay)
	defer sw.Close()

	tc := sw.ThumbnailCache()
	if tc == nil {
		t.Fatal("ThumbnailCache() returned nil")
	}

	// Verify it's functional
	tc.Store("test", []byte{0xFF}, 1, 1)
	info, ok := tc.Get("test")
	if !ok {
		t.Fatal("stored thumbnail not retrievable")
	}
	if len(info.JPEG) != 1 {
		t.Fatalf("JPEG length: got %d, want 1", len(info.JPEG))
	}
}

func TestSwitcherThumbnailCachePassedToDecoders(t *testing.T) {
	relay := distribution.NewRelay()
	sw := New(relay)
	defer sw.Close()

	// Set up a decoder factory so RegisterSource creates sourceDecoders
	sw.SetSourceDecoderFactory(func() (transition.VideoDecoder, error) {
		return transition.NewMockDecoder(320, 240), nil
	})

	sourceRelay := distribution.NewRelay()
	sw.RegisterSource("cam1", sourceRelay)

	// Verify the decoder got the thumbnail cache
	sw.mu.RLock()
	ss, ok := sw.sources["cam1"]
	sw.mu.RUnlock()
	if !ok || ss == nil {
		t.Fatal("source not registered")
	}
	if ss.viewer == nil {
		t.Fatal("viewer not set")
	}
	dec := ss.viewer.srcDecoder.Load()
	if dec == nil {
		t.Fatal("sourceDecoder not created")
	}
	if dec.thumbnailCache == nil {
		t.Fatal("sourceDecoder.thumbnailCache should be set")
	}
	if dec.thumbnailCache != sw.thumbnailCache {
		t.Fatal("sourceDecoder.thumbnailCache should match switcher's cache")
	}
}

func TestProgramOutputThumbnailCapture(t *testing.T) {
	relay := distribution.NewRelay()
	sw := New(relay)
	defer sw.Close()

	tc := sw.ThumbnailCache()
	tc.RequestCapture()

	// Create a synthetic YUV420 frame and push it through the video processing
	// channel to simulate the program output path.
	w, h := 320, 240
	yuvSize := w * h * 3 / 2
	yuv := make([]byte, yuvSize)
	for i := 0; i < w*h; i++ {
		yuv[i] = 128 // gray Y
	}
	for i := w * h; i < yuvSize; i++ {
		yuv[i] = 128 // neutral chroma
	}

	pf := &ProcessingFrame{
		Data:   yuv,
		Format: ColorYUV420_8bit,
		Width:  w,
		Height: h,
		PTS:    90000,
	}
	pf.SetRefs(1)

	// Enqueue to video processing channel
	sw.videoProcCh <- videoProcWork{
		yuvFrame:    pf,
		epoch:       sw.programEpoch.Load(),
		enqueueNano: time.Now().UnixNano(),
	}

	// Wait for the program output thumbnail to appear
	deadline := time.After(3 * time.Second)
	for {
		info, ok := tc.Get("program_output")
		if ok && len(info.JPEG) > 0 {
			if info.JPEG[0] != 0xFF || info.JPEG[1] != 0xD8 {
				t.Fatalf("program thumbnail not valid JPEG: got %02x %02x", info.JPEG[0], info.JPEG[1])
			}
			if info.Width != thumbnailWidth || info.Height != thumbnailHeight {
				t.Fatalf("program thumbnail dimensions: got %dx%d, want %dx%d",
					info.Width, info.Height, thumbnailWidth, thumbnailHeight)
			}
			return
		}
		select {
		case <-deadline:
			t.Fatal("timeout waiting for program output thumbnail")
		default:
			time.Sleep(10 * time.Millisecond)
		}
	}
}

func TestProgramOutputThumbnailNotCapturedWhenNotRequested(t *testing.T) {
	relay := distribution.NewRelay()
	sw := New(relay)
	defer sw.Close()

	tc := sw.ThumbnailCache()
	// Don't call RequestCapture

	w, h := 320, 240
	yuvSize := w * h * 3 / 2
	yuv := make([]byte, yuvSize)
	for i := range yuv {
		yuv[i] = 128
	}

	pf := &ProcessingFrame{
		Data:   yuv,
		Format: ColorYUV420_8bit,
		Width:  w,
		Height: h,
		PTS:    90000,
	}
	pf.SetRefs(1)

	sw.videoProcCh <- videoProcWork{
		yuvFrame:    pf,
		epoch:       sw.programEpoch.Load(),
		enqueueNano: time.Now().UnixNano(),
	}

	// Give the processing loop time to handle the frame
	time.Sleep(100 * time.Millisecond)

	_, ok := tc.Get("program_output")
	if ok {
		t.Fatal("program output thumbnail should NOT be captured when ShouldCapture() is false")
	}
}

func TestScaleAndEncodeJPEG(t *testing.T) {
	// Create a synthetic 320x180 YUV420 frame (gray: Y=128, U=128, V=128)
	srcW, srcH := 320, 180
	ySize := srcW * srcH
	uvSize := (srcW / 2) * (srcH / 2)
	frameSize := ySize + 2*uvSize
	yuv := make([]byte, frameSize)

	// Fill Y plane with 128 (mid-gray)
	for i := 0; i < ySize; i++ {
		yuv[i] = 128
	}
	// Fill U and V planes with 128 (neutral chroma)
	for i := ySize; i < frameSize; i++ {
		yuv[i] = 128
	}

	jpeg, err := ScaleAndEncodeJPEG(yuv, srcW, srcH, thumbnailWidth, thumbnailHeight)
	if err != nil {
		t.Fatalf("ScaleAndEncodeJPEG error: %v", err)
	}

	// Check JPEG magic bytes (SOI marker)
	if len(jpeg) < 2 {
		t.Fatalf("JPEG too short: %d bytes", len(jpeg))
	}
	if jpeg[0] != 0xFF || jpeg[1] != 0xD8 {
		t.Fatalf("JPEG header: got %02x %02x, want FF D8", jpeg[0], jpeg[1])
	}

	// Reasonable size check (a 160x90 JPEG at quality 60 should be < 10KB)
	if len(jpeg) > 10*1024 {
		t.Fatalf("JPEG unexpectedly large: %d bytes", len(jpeg))
	}
	if len(jpeg) < 100 {
		t.Fatalf("JPEG unexpectedly small: %d bytes", len(jpeg))
	}
}
