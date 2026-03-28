//go:build cgo && darwin

package gpu

import (
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGPUSourceManager_IngestAndGetFrame(t *testing.T) {
	const w, h = 32, 32
	ctx, pool := setupGPU(t, w, h, 8)
	defer func() { _ = ctx.Close() }()
	defer pool.Close()

	mgr := NewGPUSourceManager(ctx, pool, nil)
	defer mgr.Close()

	// Before ingest, GetFrame should return nil.
	frame := mgr.GetFrame("cam1")
	assert.Nil(t, frame, "GetFrame before ingest should return nil")

	// Ingest a frame via auto-register path.
	yuv := createTestYUV420p(w, h, 200, 100, 150)
	mgr.IngestYUV("cam1", yuv, w, h, 42000)

	// GetFrame should now return a valid frame with correct PTS.
	frame = mgr.GetFrame("cam1")
	require.NotNil(t, frame, "GetFrame after ingest should return a frame")
	assert.Equal(t, int64(42000), frame.PTS)
	assert.Equal(t, w, frame.Width)
	assert.Equal(t, h, frame.Height)
	frame.Release()

	// Ingest a second frame — should replace the first.
	mgr.IngestYUV("cam1", yuv, w, h, 84000)
	frame = mgr.GetFrame("cam1")
	require.NotNil(t, frame)
	assert.Equal(t, int64(84000), frame.PTS, "second ingest should update PTS")
	frame.Release()

	// Download the frame and verify Y plane content survived the round-trip.
	frame = mgr.GetFrame("cam1")
	require.NotNil(t, frame)
	defer frame.Release()

	out := make([]byte, yuvFrameSize(w, h))
	err := Download(ctx, out, frame, w, h)
	require.NoError(t, err)

	// Check center Y pixel matches the input value.
	centerIdx := (h/2)*w + w/2
	assert.InDelta(t, 200, int(out[centerIdx]), 2,
		"Y center pixel should be close to 200 after upload+download round-trip")
}

func TestGPUSourceManager_CurrentFrame(t *testing.T) {
	const w, h = 32, 32
	ctx, pool := setupGPU(t, w, h, 8)
	defer func() { _ = ctx.Close() }()
	defer pool.Close()

	mgr := NewGPUSourceManager(ctx, pool, nil)
	defer mgr.Close()

	// CurrentFrame returns nil for unknown source.
	frame := mgr.CurrentFrame("nonexistent")
	assert.Nil(t, frame, "CurrentFrame for unknown source should return nil")

	// CurrentFrame returns nil before any ingest.
	frame = mgr.CurrentFrame("cam1")
	assert.Nil(t, frame, "CurrentFrame before ingest should return nil")

	// Ingest a frame via auto-register path.
	yuv := createTestYUV420p(w, h, 200, 100, 150)
	mgr.IngestYUV("cam1", yuv, w, h, 42000)

	// CurrentFrame should now return the frame (without incrementing refcount).
	frame = mgr.CurrentFrame("cam1")
	require.NotNil(t, frame, "CurrentFrame after ingest should return a frame")
	assert.Equal(t, int64(42000), frame.PTS)
	assert.Equal(t, w, frame.Width)
	// NOTE: do NOT call frame.Release() — CurrentFrame does not increment refcount.
}

func TestGPUSourceManager_RemoveSource(t *testing.T) {
	const w, h = 32, 32
	ctx, pool := setupGPU(t, w, h, 8)
	defer func() { _ = ctx.Close() }()
	defer pool.Close()

	mgr := NewGPUSourceManager(ctx, pool, nil)
	defer mgr.Close()

	// Register and ingest.
	mgr.RegisterSource("cam1", w, h, nil)
	yuv := createTestYUV420p(w, h, 180, 128, 128)
	mgr.IngestYUV("cam1", yuv, w, h, 90000)

	// Verify frame exists.
	frame := mgr.GetFrame("cam1")
	require.NotNil(t, frame)
	frame.Release()

	// Remove the source.
	mgr.RemoveSource("cam1")

	// GetFrame should now return nil.
	frame = mgr.GetFrame("cam1")
	assert.Nil(t, frame, "GetFrame after RemoveSource should return nil")

	// Re-ingest should auto-register again.
	mgr.IngestYUV("cam1", yuv, w, h, 180000)
	frame = mgr.GetFrame("cam1")
	require.NotNil(t, frame, "GetFrame after re-ingest should return a new frame")
	assert.Equal(t, int64(180000), frame.PTS)
	frame.Release()
}

func TestGPUSourceManager_STMapCorrection(t *testing.T) {
	const w, h = 32, 32
	ctx, pool := setupGPU(t, w, h, 12) // extra frames for stmap tmp buffers
	defer func() { _ = ctx.Close() }()
	defer pool.Close()

	// Create a horizontal flip ST map: each pixel reads from (w-1-x, y).
	s := make([]float32, w*h)
	tArr := make([]float32, w*h)
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			s[y*w+x] = float32(w-1-x) / float32(w-1) // flip horizontally
			tArr[y*w+x] = float32(y) / float32(h-1)
		}
	}

	provider := &mockSourceSTMapProvider{
		names: map[string]string{
			"cam1": "hflip",
		},
		arrays: map[string][2][]float32{
			"cam1": {s, tArr},
		},
	}

	mgr := NewGPUSourceManager(ctx, pool, provider)
	defer mgr.Close()

	// Create a gradient frame where the left half is bright, right half is dark.
	yuv := make([]byte, yuvFrameSize(w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			if x < w/2 {
				yuv[y*w+x] = 220 // bright left
			} else {
				yuv[y*w+x] = 30 // dark right
			}
		}
	}
	// Fill chroma with neutral.
	ySize := w * h
	for i := ySize; i < len(yuv); i++ {
		yuv[i] = 128
	}

	// Ingest with ST map correction.
	mgr.IngestYUV("cam1", yuv, w, h, 90000)

	frame := mgr.GetFrame("cam1")
	require.NotNil(t, frame)
	defer frame.Release()

	// Download and check the flip happened: left should now be dark, right bright.
	out := make([]byte, yuvFrameSize(w, h))
	err := Download(ctx, out, frame, w, h)
	require.NoError(t, err)

	// Sample pixels from the center row.
	midY := h / 2
	leftPixel := out[midY*w+2]        // was bright (220), should now be dark (~30)
	rightPixel := out[midY*w+(w-1-2)] // was dark (30), should now be bright (~220)

	// The flip should have swapped the brightness.
	assert.Less(t, leftPixel, uint8(100),
		"left pixel after hflip should be dark (was bright)")
	assert.Greater(t, rightPixel, uint8(150),
		"right pixel after hflip should be bright (was dark)")

	t.Logf("STMap correction: left=%d (expected <100), right=%d (expected >150)",
		leftPixel, rightPixel)
}

func TestGPUSourceManager_ConcurrentIngestGet(t *testing.T) {
	const w, h = 32, 32
	ctx, pool := setupGPU(t, w, h, 32) // large pool for concurrent access
	defer func() { _ = ctx.Close() }()
	defer pool.Close()

	mgr := NewGPUSourceManager(ctx, pool, nil)
	defer mgr.Close()

	sources := []string{"cam1", "cam2", "cam3", "cam4"}

	// Seed each source with one frame so readers always have something.
	yuv := createTestYUV420p(w, h, 128, 128, 128)
	for _, key := range sources {
		mgr.IngestYUV(key, yuv, w, h, 1000)
	}

	var wg sync.WaitGroup
	const iterations = 50

	// 4 goroutines ingesting frames concurrently.
	for _, key := range sources {
		wg.Add(1)
		go func(srcKey string) {
			defer wg.Done()
			localYUV := createTestYUV420p(w, h, 128, 128, 128)
			for i := 0; i < iterations; i++ {
				mgr.IngestYUV(srcKey, localYUV, w, h, int64((i+1)*3000))
			}
		}(key)
	}

	// 4 goroutines reading frames concurrently.
	var reads atomic.Int64
	for _, key := range sources {
		wg.Add(1)
		go func(srcKey string) {
			defer wg.Done()
			for i := 0; i < iterations; i++ {
				frame := mgr.GetFrame(srcKey)
				if frame != nil {
					reads.Add(1)
					frame.Release()
				}
			}
		}(key)
	}

	wg.Wait()

	// We should have gotten at least some successful reads (seeded above).
	assert.Greater(t, reads.Load(), int64(0),
		"concurrent readers should get at least some frames")

	// All sources should still be accessible after concurrent access.
	for _, key := range sources {
		frame := mgr.GetFrame(key)
		require.NotNil(t, frame, "source %s should still be accessible", key)
		frame.Release()
	}

	t.Logf("Concurrent: %d successful reads across %d goroutines",
		reads.Load(), len(sources)*2)
}

func TestGPUSourceManager_PreviewEncode(t *testing.T) {
	const w, h = 64, 64
	const previewW, previewH = 32, 32
	ctx, pool := setupGPU(t, w, h, 12)
	defer func() { _ = ctx.Close() }()
	defer pool.Close()

	var previewFrames atomic.Int64
	var lastData []byte
	var mu sync.Mutex

	preview := &PreviewConfig{
		Width:   previewW,
		Height:  previewH,
		Bitrate: 200_000,
		FPSNum:  30,
		FPSDen:  1,
		OnPreview: func(data []byte, isIDR bool, pts int64) {
			previewFrames.Add(1)
			mu.Lock()
			lastData = make([]byte, len(data))
			copy(lastData, data)
			mu.Unlock()
		},
	}

	mgr := NewGPUSourceManager(ctx, pool, nil)
	defer mgr.Close()

	mgr.RegisterSource("cam1", w, h, preview)

	// Ingest frames with brief pauses so the preview goroutine has time to
	// consume from the capacity-1 channel. Hardware encoders may buffer
	// the first few frames before producing output.
	yuv := createTestYUV420p(w, h, 180, 100, 200)
	for i := 0; i < 60; i++ {
		mgr.IngestYUV("cam1", yuv, w, h, int64(i*3000))
		// Brief yield to let the preview goroutine pick up the frame.
		if i%5 == 0 {
			runtime.Gosched()
		}
	}

	// Give the preview goroutine time to finish encoding the last queued frame.
	time.Sleep(100 * time.Millisecond)

	// RemoveSource closes prevCh and blocks until preview goroutine exits.
	mgr.RemoveSource("cam1")

	count := previewFrames.Load()
	assert.Greater(t, count, int64(0),
		"preview encoder should produce at least one frame")

	mu.Lock()
	assert.NotEmpty(t, lastData, "preview callback should receive H.264 data")
	mu.Unlock()

	t.Logf("Preview encode: %d frames produced", count)
}

func TestGPUSourceManager_ReadCachedFrame(t *testing.T) {
	const w, h = 32, 32
	ctx, pool := setupGPU(t, w, h, 8)
	defer func() { _ = ctx.Close() }()
	defer pool.Close()

	mgr := NewGPUSourceManager(ctx, pool, nil)
	defer mgr.Close()

	// ReadCachedFrame returns nil for unknown source.
	yuv, rw, rh, rpts := mgr.ReadCachedFrame("nonexistent")
	assert.Nil(t, yuv, "ReadCachedFrame for unknown source should return nil")
	assert.Equal(t, 0, rw)
	assert.Equal(t, 0, rh)
	assert.Equal(t, int64(0), rpts)

	// Ingest a frame.
	inputYUV := createTestYUV420p(w, h, 200, 100, 150)
	mgr.IngestYUV("cam1", inputYUV, w, h, 42000)

	// ReadCachedFrame should now return the cached CPU copy.
	yuv, rw, rh, rpts = mgr.ReadCachedFrame("cam1")
	require.NotNil(t, yuv, "ReadCachedFrame after ingest should return data")
	assert.Equal(t, w, rw)
	assert.Equal(t, h, rh)
	assert.Equal(t, int64(42000), rpts)
	assert.Equal(t, len(inputYUV), len(yuv))

	// Verify the Y plane content matches the input.
	centerIdx := (h/2)*w + w/2
	assert.Equal(t, byte(200), yuv[centerIdx],
		"cached Y center pixel should match input")

	// Verify it's a deep copy: modifying the returned slice does not affect the cache.
	yuv[centerIdx] = 0
	yuv2, _, _, _ := mgr.ReadCachedFrame("cam1")
	assert.Equal(t, byte(200), yuv2[centerIdx],
		"ReadCachedFrame should return independent copies")

	// Ingest a second frame — ReadCachedFrame should return updated data.
	inputYUV2 := createTestYUV420p(w, h, 50, 120, 130)
	mgr.IngestYUV("cam1", inputYUV2, w, h, 84000)

	yuv, _, _, rpts = mgr.ReadCachedFrame("cam1")
	require.NotNil(t, yuv)
	assert.Equal(t, int64(84000), rpts, "PTS should update on second ingest")
	assert.Equal(t, byte(50), yuv[centerIdx],
		"cached Y should reflect second ingest")
}

func TestGPUSourceManager_ReadCachedFrame_NilYUV(t *testing.T) {
	const w, h = 32, 32
	ctx, pool := setupGPU(t, w, h, 8)
	defer func() { _ = ctx.Close() }()
	defer pool.Close()

	mgr := NewGPUSourceManager(ctx, pool, nil)
	defer mgr.Close()

	// Ingest a real frame first.
	inputYUV := createTestYUV420p(w, h, 200, 100, 150)
	mgr.IngestYUV("cam1", inputYUV, w, h, 42000)

	// Ingest nil YUV (no-op path) — cache should remain from previous ingest.
	mgr.IngestYUV("cam1", nil, w, h, 84000)

	yuv, _, _, pts := mgr.ReadCachedFrame("cam1")
	require.NotNil(t, yuv, "nil YUV ingest should not clear the cache")
	assert.Equal(t, int64(42000), pts, "PTS should remain from the first ingest")
}

func TestGPUSourceManager_ReadCachedFrame_ConcurrentAccess(t *testing.T) {
	const w, h = 32, 32
	ctx, pool := setupGPU(t, w, h, 32)
	defer func() { _ = ctx.Close() }()
	defer pool.Close()

	mgr := NewGPUSourceManager(ctx, pool, nil)
	defer mgr.Close()

	// Seed with an initial frame.
	inputYUV := createTestYUV420p(w, h, 128, 128, 128)
	mgr.IngestYUV("cam1", inputYUV, w, h, 1000)

	var wg sync.WaitGroup
	const iterations = 100

	// Writer goroutine: continuous ingest.
	wg.Add(1)
	go func() {
		defer wg.Done()
		localYUV := createTestYUV420p(w, h, 180, 100, 200)
		for i := 0; i < iterations; i++ {
			mgr.IngestYUV("cam1", localYUV, w, h, int64((i+1)*3000))
		}
	}()

	// Reader goroutine: continuous ReadCachedFrame.
	var reads atomic.Int64
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < iterations; i++ {
			yuv, rw, rh, _ := mgr.ReadCachedFrame("cam1")
			if yuv != nil {
				reads.Add(1)
				// Verify basic consistency: dimensions match YUV buffer size.
				expected := yuvFrameSize(rw, rh)
				assert.Equal(t, expected, len(yuv),
					"YUV buffer size should match dimensions")
			}
		}
	}()

	wg.Wait()

	assert.Greater(t, reads.Load(), int64(0),
		"concurrent reader should get at least some cached frames")
	t.Logf("ReadCachedFrame concurrent: %d successful reads out of %d", reads.Load(), iterations)
}

func TestGPUSourceManager_ReadCachedFrame_AfterRemove(t *testing.T) {
	const w, h = 32, 32
	ctx, pool := setupGPU(t, w, h, 8)
	defer func() { _ = ctx.Close() }()
	defer pool.Close()

	mgr := NewGPUSourceManager(ctx, pool, nil)
	defer mgr.Close()

	// Ingest, then remove.
	inputYUV := createTestYUV420p(w, h, 200, 100, 150)
	mgr.IngestYUV("cam1", inputYUV, w, h, 42000)
	mgr.RemoveSource("cam1")

	// ReadCachedFrame should return nil after removal.
	yuv, _, _, _ := mgr.ReadCachedFrame("cam1")
	assert.Nil(t, yuv, "ReadCachedFrame after RemoveSource should return nil")
}

func TestGPUSourceManager_AliasSource(t *testing.T) {
	const w, h = 32, 32
	ctx, pool := setupGPU(t, w, h, 8)
	defer func() { _ = ctx.Close() }()
	defer pool.Close()

	mgr := NewGPUSourceManager(ctx, pool, nil)
	defer mgr.Close()

	// Register both sources.
	mgr.RegisterSource("cam1", w, h, nil)
	mgr.RegisterSource("playout:1", w, h, nil)

	// Ingest a frame into cam1.
	yuv := createTestYUV420p(w, h, 200, 100, 150)
	mgr.IngestYUV("cam1", yuv, w, h, 42000)

	// Before alias, playout:1 should have no frame.
	frame := mgr.CurrentFrame("playout:1")
	assert.Nil(t, frame, "playout:1 should have no frame before alias")

	// Alias playout:1 → cam1.
	ok := mgr.AliasSource("playout:1", "cam1")
	assert.True(t, ok, "AliasSource should succeed")

	// After alias, playout:1 should have cam1's frame.
	frame = mgr.CurrentFrame("playout:1")
	require.NotNil(t, frame, "playout:1 should have cam1's frame after alias")
	assert.Equal(t, int64(42000), frame.PTS, "aliased frame should have cam1's PTS")

	// The CPU cache should also be aliased.
	yuv2, rw, rh, rpts := mgr.ReadCachedFrame("playout:1")
	assert.NotNil(t, yuv2, "playout:1 should have CPU cache after alias")
	assert.Equal(t, w, rw)
	assert.Equal(t, h, rh)
	assert.Equal(t, int64(42000), rpts)
}

func TestGPUSourceManager_AliasSource_MissingSrc(t *testing.T) {
	const w, h = 32, 32
	ctx, pool := setupGPU(t, w, h, 8)
	defer func() { _ = ctx.Close() }()
	defer pool.Close()

	mgr := NewGPUSourceManager(ctx, pool, nil)
	defer mgr.Close()

	// Register only destination.
	mgr.RegisterSource("playout:1", w, h, nil)

	// Alias to non-existent source should return false.
	ok := mgr.AliasSource("playout:1", "nonexistent")
	assert.False(t, ok, "AliasSource with missing src should return false")

	// Alias from non-existent destination should also return false.
	ok = mgr.AliasSource("nonexistent", "playout:1")
	assert.False(t, ok, "AliasSource with missing dst should return false")
}

func TestGPUSourceManager_AliasSource_NoFrame(t *testing.T) {
	const w, h = 32, 32
	ctx, pool := setupGPU(t, w, h, 8)
	defer func() { _ = ctx.Close() }()
	defer pool.Close()

	mgr := NewGPUSourceManager(ctx, pool, nil)
	defer mgr.Close()

	// Register both but don't ingest any frames.
	mgr.RegisterSource("cam1", w, h, nil)
	mgr.RegisterSource("playout:1", w, h, nil)

	ok := mgr.AliasSource("playout:1", "cam1")
	assert.False(t, ok, "AliasSource should fail when src has no frame")
}

func TestGPUSourceManager_AliasSource_RefCounting(t *testing.T) {
	const w, h = 32, 32
	ctx, pool := setupGPU(t, w, h, 8)
	defer func() { _ = ctx.Close() }()
	defer pool.Close()

	mgr := NewGPUSourceManager(ctx, pool, nil)
	defer mgr.Close()

	mgr.RegisterSource("cam1", w, h, nil)
	mgr.RegisterSource("playout:1", w, h, nil)

	// Ingest a frame.
	yuv := createTestYUV420p(w, h, 200, 100, 150)
	mgr.IngestYUV("cam1", yuv, w, h, 42000)

	// Get a ref to cam1's frame.
	srcFrame := mgr.GetFrame("cam1")
	require.NotNil(t, srcFrame)

	// Alias playout:1 → cam1.
	ok := mgr.AliasSource("playout:1", "cam1")
	require.True(t, ok)

	// Get a ref to playout:1's frame (same underlying GPU frame).
	dstFrame := mgr.GetFrame("playout:1")
	require.NotNil(t, dstFrame)

	// Both should point to the same GPU frame.
	assert.Equal(t, srcFrame.PTS, dstFrame.PTS)

	// Release both refs — should not panic or double-free.
	srcFrame.Release()
	dstFrame.Release()

	// Ingest a new frame into cam1 — should replace without issues.
	mgr.IngestYUV("cam1", yuv, w, h, 84000)
	f := mgr.GetFrame("cam1")
	require.NotNil(t, f)
	assert.Equal(t, int64(84000), f.PTS)
	f.Release()
}

// --- Mock SourceSTMapProvider ---

type mockSourceSTMapProvider struct {
	names  map[string]string
	arrays map[string][2][]float32 // sourceKey -> [s, t]
}

func (m *mockSourceSTMapProvider) SourceMapName(key string) string {
	return m.names[key]
}

func (m *mockSourceSTMapProvider) SourceSTArrays(key string) ([]float32, []float32) {
	if a, ok := m.arrays[key]; ok {
		return a[0], a[1]
	}
	return nil, nil
}
