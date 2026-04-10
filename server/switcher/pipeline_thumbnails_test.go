package switcher

import (
	"fmt"
	"sync"
	"testing"
	"time"
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
