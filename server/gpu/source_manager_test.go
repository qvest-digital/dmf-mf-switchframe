//go:build darwin

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
	defer ctx.Close()
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

	out := make([]byte, w*h*3/2)
	err := Download(ctx, out, frame, w, h)
	require.NoError(t, err)

	// Check center Y pixel matches the input value.
	centerIdx := (h/2)*w + w/2
	assert.InDelta(t, 200, int(out[centerIdx]), 2,
		"Y center pixel should be close to 200 after upload+download round-trip")
}

func TestGPUSourceManager_RemoveSource(t *testing.T) {
	const w, h = 32, 32
	ctx, pool := setupGPU(t, w, h, 8)
	defer ctx.Close()
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
	defer ctx.Close()
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
	yuv := make([]byte, w*h*3/2)
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
	out := make([]byte, w*h*3/2)
	err := Download(ctx, out, frame, w, h)
	require.NoError(t, err)

	// Sample pixels from the center row.
	midY := h / 2
	leftPixel := out[midY*w+2]                // was bright (220), should now be dark (~30)
	rightPixel := out[midY*w+(w-1-2)]          // was dark (30), should now be bright (~220)

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
	defer ctx.Close()
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
	defer ctx.Close()
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
