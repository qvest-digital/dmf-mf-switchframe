package playout

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestSliceIteratorBasic(t *testing.T) {
	frames := fakeFrames(3)
	iter := NewSliceIterator(frames)

	for i := 0; i < 3; i++ {
		f, err := iter.Next()
		if err != nil {
			t.Fatalf("Next() error on frame %d: %v", i, err)
		}
		if f == nil {
			t.Fatalf("Next() returned nil on frame %d, expected frame", i)
		}
		if f.PTS != int64(i)*3000 {
			t.Errorf("frame %d PTS = %d, want %d", i, f.PTS, int64(i)*3000)
		}
	}

	// End of stream.
	f, err := iter.Next()
	if err != nil {
		t.Fatalf("Next() error at end: %v", err)
	}
	if f != nil {
		t.Errorf("Next() at end returned non-nil frame")
	}
}

func TestSliceIteratorEstimateFPS(t *testing.T) {
	// 30fps PTS spacing: 3000 ticks per frame at 90kHz.
	frames := fakeFrames(10)
	iter := NewSliceIterator(frames)

	fps := iter.EstimateFPS()
	if fps < 29.5 || fps > 30.5 {
		t.Errorf("EstimateFPS() = %f, want ~30.0", fps)
	}
}

func TestSliceIteratorTotalFrames(t *testing.T) {
	frames := fakeFrames(7)
	iter := NewSliceIterator(frames)

	if iter.TotalFrames() != 7 {
		t.Errorf("TotalFrames() = %d, want 7", iter.TotalFrames())
	}
}

func TestTrimIteratorInOut(t *testing.T) {
	// 5 frames at 30fps: PTS = 0, 3000, 6000, 9000, 12000
	// 3000 PTS = 33.33ms
	// In=33ms → skip frame 0 (PTS 0 < 33*90=2970... wait, frame 0 PTS=0 < 2970, frame 1 PTS=3000 >= 2970)
	// Out=100ms → 100*90=9000, frame at PTS 9000 is >= out, so stop before it
	// Expected: frames 1, 2 (PTS 3000, 6000)
	frames := fakeFrames(5)
	inner := NewSliceIterator(frames)
	iter := NewTrimIterator(inner, 33, 100)

	var got []int64
	for {
		f, err := iter.Next()
		if err != nil {
			t.Fatalf("Next() error: %v", err)
		}
		if f == nil {
			break
		}
		got = append(got, f.PTS)
	}

	if len(got) != 2 {
		t.Fatalf("got %d frames, want 2; PTS values: %v", len(got), got)
	}
	if got[0] != 3000 {
		t.Errorf("first frame PTS = %d, want 3000", got[0])
	}
	if got[1] != 6000 {
		t.Errorf("second frame PTS = %d, want 6000", got[1])
	}
}

func TestTrimIteratorInOnly(t *testing.T) {
	// 5 frames at 30fps: PTS = 0, 3000, 6000, 9000, 12000
	// In=67ms → 67*90=6030. Frame at PTS 6000 < 6030, frame at PTS 9000 >= 6030.
	// Expected: frames 3, 4 (PTS 9000, 12000)
	frames := fakeFrames(5)
	inner := NewSliceIterator(frames)
	iter := NewTrimIterator(inner, 67, 0)

	var got []int64
	for {
		f, err := iter.Next()
		if err != nil {
			t.Fatalf("Next() error: %v", err)
		}
		if f == nil {
			break
		}
		got = append(got, f.PTS)
	}

	if len(got) != 2 {
		t.Fatalf("got %d frames, want 2; PTS values: %v", len(got), got)
	}
	if got[0] != 9000 {
		t.Errorf("first frame PTS = %d, want 9000", got[0])
	}
	if got[1] != 12000 {
		t.Errorf("second frame PTS = %d, want 12000", got[1])
	}
}

func TestTrimIteratorOutOnly(t *testing.T) {
	// 5 frames at 30fps: PTS = 0, 3000, 6000, 9000, 12000
	// Out=67ms → 67*90=6030. Frame at PTS 6000 < 6030 (included), PTS 9000 >= 6030 (excluded).
	// Expected: frames 0, 1, 2 (PTS 0, 3000, 6000)
	frames := fakeFrames(5)
	inner := NewSliceIterator(frames)
	iter := NewTrimIterator(inner, 0, 67)

	var got []int64
	for {
		f, err := iter.Next()
		if err != nil {
			t.Fatalf("Next() error: %v", err)
		}
		if f == nil {
			break
		}
		got = append(got, f.PTS)
	}

	if len(got) != 3 {
		t.Fatalf("got %d frames, want 3; PTS values: %v", len(got), got)
	}
	if got[0] != 0 {
		t.Errorf("first frame PTS = %d, want 0", got[0])
	}
	if got[2] != 6000 {
		t.Errorf("last frame PTS = %d, want 6000", got[2])
	}
}

func TestTrimIteratorTotalFrames(t *testing.T) {
	frames := fakeFrames(5)
	inner := NewSliceIterator(frames)
	iter := NewTrimIterator(inner, 33, 100)

	if iter.TotalFrames() != -1 {
		t.Errorf("TotalFrames() = %d, want -1", iter.TotalFrames())
	}
}

func TestFakeAssetProvider(t *testing.T) {
	frames := fakeFrames(4)
	provider := NewFakeAssetProvider(frames)

	iter, err := provider.Open("local:///clips/test.ts")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer iter.Close()

	if iter.TotalFrames() != 4 {
		t.Errorf("TotalFrames() = %d, want 4", iter.TotalFrames())
	}

	count := 0
	for {
		f, err := iter.Next()
		if err != nil {
			t.Fatalf("Next() error: %v", err)
		}
		if f == nil {
			break
		}
		count++
	}

	if count != 4 {
		t.Errorf("iterated %d frames, want 4", count)
	}
}

func TestCacheAssetProvider_Open(t *testing.T) {
	dir := t.TempDir()
	cache, err := NewCache(CacheConfig{Dir: dir, MaxBytes: 1 << 30})
	if err != nil {
		t.Fatalf("NewCache: %v", err)
	}

	// Write a fake file to simulate cached content.
	assetPath := filepath.Join(dir, "assets", "test.ts")
	if err := os.MkdirAll(filepath.Dir(assetPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(assetPath, []byte("fake"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Put a cached entry.
	_ = cache.Put(&CacheEntry{
		AssetURI:     "local:///media/test.ts",
		LocalPath:    assetPath,
		State:        AssetCached,
		ByteSize:     4,
		LastAccessed: time.Now(),
	})

	// Create provider with a mock demux that returns 3 frames as a FrameIterator.
	demuxFn := func(path string) (FrameIterator, error) {
		return NewSliceIterator(fakeFrames(3)), nil
	}
	provider := NewCacheAssetProvider(cache, demuxFn)

	iter, err := provider.Open("local:///media/test.ts")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer iter.Close()

	if iter.TotalFrames() != 3 {
		t.Errorf("TotalFrames() = %d, want 3", iter.TotalFrames())
	}
}

func TestCacheAssetProvider_NotFound(t *testing.T) {
	dir := t.TempDir()
	cache, err := NewCache(CacheConfig{Dir: dir, MaxBytes: 1 << 30})
	if err != nil {
		t.Fatalf("NewCache: %v", err)
	}

	provider := NewCacheAssetProvider(cache, nil)
	_, err = provider.Open("local:///nonexistent.ts")
	if !errors.Is(err, ErrAssetNotFound) {
		t.Errorf("expected ErrAssetNotFound, got: %v", err)
	}
}

func TestCacheAssetProvider_NotReady(t *testing.T) {
	dir := t.TempDir()
	cache, err := NewCache(CacheConfig{Dir: dir, MaxBytes: 1 << 30})
	if err != nil {
		t.Fatalf("NewCache: %v", err)
	}

	assetPath := filepath.Join(dir, "assets", "test.ts")
	if err := os.MkdirAll(filepath.Dir(assetPath), 0o755); err != nil {
		t.Fatal(err)
	}

	// Put entry in fetching state (not yet cached).
	_ = cache.Put(&CacheEntry{
		AssetURI:     "local:///media/test.ts",
		LocalPath:    assetPath,
		State:        AssetFetching,
		LastAccessed: time.Now(),
	})

	provider := NewCacheAssetProvider(cache, nil)
	_, err = provider.Open("local:///media/test.ts")
	if !errors.Is(err, ErrAssetNotReady) {
		t.Errorf("expected ErrAssetNotReady, got: %v", err)
	}
}

func TestCacheAssetProvider_DirectFileFallback(t *testing.T) {
	dir := t.TempDir()
	cache, err := NewCache(CacheConfig{Dir: dir, MaxBytes: 1 << 30})
	if err != nil {
		t.Fatalf("NewCache: %v", err)
	}

	// Write a real file that is NOT in the cache.
	clipDir := t.TempDir()
	clipPath := filepath.Join(clipDir, "uploaded.ts")
	if err := os.WriteFile(clipPath, []byte("clip-data"), 0o644); err != nil {
		t.Fatal(err)
	}

	demuxCalled := false
	demuxFn := func(path string) (FrameIterator, error) {
		demuxCalled = true
		if path != clipPath {
			t.Errorf("demuxFn called with path %q, want %q", path, clipPath)
		}
		return NewSliceIterator(fakeFrames(5)), nil
	}

	provider := NewCacheAssetProvider(cache, demuxFn)

	// Open with the direct file path as asset URI — not in cache, but file exists.
	iter, err := provider.Open(clipPath)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer iter.Close()

	if !demuxCalled {
		t.Error("expected demuxFn to be called for direct file fallback")
	}
	if iter.TotalFrames() != 5 {
		t.Errorf("TotalFrames() = %d, want 5", iter.TotalFrames())
	}
}

func TestCacheAssetProvider_CachePreferred(t *testing.T) {
	dir := t.TempDir()
	cache, err := NewCache(CacheConfig{Dir: dir, MaxBytes: 1 << 30})
	if err != nil {
		t.Fatalf("NewCache: %v", err)
	}

	// Create a file that also happens to exist on disk.
	clipDir := t.TempDir()
	clipPath := filepath.Join(clipDir, "test.ts")
	if err := os.WriteFile(clipPath, []byte("original"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Also create a cached version at a different path.
	cachedPath := filepath.Join(dir, "assets", "cached-test.ts")
	if err := os.MkdirAll(filepath.Dir(cachedPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cachedPath, []byte("cached-version"), 0o644); err != nil {
		t.Fatal(err)
	}

	_ = cache.Put(&CacheEntry{
		AssetURI:     clipPath,
		LocalPath:    cachedPath,
		State:        AssetCached,
		ByteSize:     14,
		LastAccessed: time.Now(),
	})

	var demuxedPath string
	demuxFn := func(path string) (FrameIterator, error) {
		demuxedPath = path
		return NewSliceIterator(fakeFrames(2)), nil
	}

	provider := NewCacheAssetProvider(cache, demuxFn)

	// Open — should use the cached path, not the original file path.
	iter, err := provider.Open(clipPath)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer iter.Close()

	if demuxedPath != cachedPath {
		t.Errorf("demuxFn received path %q, want cached path %q", demuxedPath, cachedPath)
	}
}

func TestCacheAssetProvider_DirectFileFallback_NoFile(t *testing.T) {
	dir := t.TempDir()
	cache, err := NewCache(CacheConfig{Dir: dir, MaxBytes: 1 << 30})
	if err != nil {
		t.Fatalf("NewCache: %v", err)
	}

	provider := NewCacheAssetProvider(cache, func(path string) (FrameIterator, error) {
		return NewSliceIterator(fakeFrames(1)), nil
	})

	// Open with a path that doesn't exist on disk and isn't in the cache.
	_, err = provider.Open("/nonexistent/path/clip.ts")
	if !errors.Is(err, ErrAssetNotFound) {
		t.Errorf("expected ErrAssetNotFound, got: %v", err)
	}
}

func TestCacheAssetProvider_NilDemuxFn(t *testing.T) {
	dir := t.TempDir()
	cache, err := NewCache(CacheConfig{Dir: dir, MaxBytes: 1 << 30})
	if err != nil {
		t.Fatalf("NewCache: %v", err)
	}

	assetPath := filepath.Join(dir, "assets", "test.ts")
	if err := os.MkdirAll(filepath.Dir(assetPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(assetPath, []byte("fake"), 0o644); err != nil {
		t.Fatal(err)
	}

	_ = cache.Put(&CacheEntry{
		AssetURI:     "local:///media/test.ts",
		LocalPath:    assetPath,
		State:        AssetCached,
		ByteSize:     4,
		LastAccessed: time.Now(),
	})

	provider := NewCacheAssetProvider(cache, nil)
	_, err = provider.Open("local:///media/test.ts")
	if err == nil {
		t.Fatal("expected error for nil demuxFn, got nil")
	}
}

// TestTrimIterator_Seek verifies Seek positions on the trimmed timeline
// (frame 0 = in-point), keeps the in/out-point math anchored to the true
// stream start, and surfaces an error for non-seekable inner iterators.
func TestTrimIterator_Seek(t *testing.T) {
	// 120 frames @ 30fps; in=1000ms out=3000ms → trimmed frames 30..89.
	frames := fakeFramesSized(120, 64, 48, 30.0)

	t.Run("seeks past in-point", func(t *testing.T) {
		tr := NewTrimIterator(&seekableSliceIterator{frames: frames}, 1000, 3000)
		if err := tr.Seek(15); err != nil { // 500ms into trimmed timeline
			t.Fatalf("Seek: %v", err)
		}
		f, err := tr.Next()
		if err != nil {
			t.Fatalf("Next: %v", err)
		}
		if f == nil {
			t.Fatal("Next returned nil after seek")
		}
		if f.PTS != 135000 { // absolute frame 45
			t.Errorf("PTS after Seek(15) = %d, want 135000", f.PTS)
		}
	})

	t.Run("out point honored after seek", func(t *testing.T) {
		tr := NewTrimIterator(&seekableSliceIterator{frames: frames}, 1000, 3000)
		if err := tr.Seek(55); err != nil {
			t.Fatalf("Seek: %v", err)
		}
		var last *DecodedFrame
		for {
			f, err := tr.Next()
			if err != nil {
				t.Fatalf("Next: %v", err)
			}
			if f == nil {
				break
			}
			last = f
		}
		if last == nil || last.PTS != 267000 { // absolute frame 89
			var pts int64 = -1
			if last != nil {
				pts = last.PTS
			}
			t.Errorf("last PTS = %d, want 267000 (out point)", pts)
		}
	})

	t.Run("non-seekable inner returns error", func(t *testing.T) {
		tr := NewTrimIterator(NewSliceIterator(frames), 1000, 3000)
		if err := tr.Seek(15); err == nil {
			t.Error("Seek on non-seekable inner iterator should return an error")
		}
	})
}
