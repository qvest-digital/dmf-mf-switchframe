//go:build correctness && ((cgo && cuda) || (cgo && darwin))

package gpu

import (
	"math"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ============================================================================
// Invalid Dimension Recovery
// ============================================================================

// TestGPUInvalidDimensionRecovery verifies the GPU context recovers after
// processing a frame with invalid (0x0 or 1x1) dimensions.
func TestGPUInvalidDimensionRecovery(t *testing.T) {
	ctx := testContext(t)

	// Step 1: Process valid 320x240 frame — should succeed.
	width, height := 320, 240
	pool, err := NewFramePool(ctx, width, height, 4)
	require.NoError(t, err)
	defer pool.Close()

	frame1 := makeTestFrame(t, ctx, pool, width, height, 128, 128, 128)
	defer frame1.Release()
	yAvg1, _, _ := avgPlanes(t, ctx, frame1, width, height)
	t.Logf("MEASURE: valid frame1 Y=%.1f (expected 128)", yAvg1)
	assert.InDelta(t, 128.0, yAvg1, 1.0, "valid frame should upload/download correctly")

	// Step 2: Try to create a pool with 0x0 dimensions — expect error or degenerate behavior.
	var invalidErr error
	var invalidPoolErr error
	func() {
		defer func() {
			if r := recover(); r != nil {
				t.Logf("MEASURE: 0x0 pool creation panicked: %v", r)
				invalidErr = nil // recovered from panic
			}
		}()
		_, invalidPoolErr = NewFramePool(ctx, 0, 0, 1)
	}()
	t.Logf("MEASURE: 0x0 pool creation error=%v", invalidPoolErr)

	// Also try 1x1 dimensions — odd dimensions may cause chroma plane issues.
	var smallPoolErr error
	var smallPool *FramePool
	func() {
		defer func() {
			if r := recover(); r != nil {
				t.Logf("MEASURE: 1x1 pool creation panicked: %v", r)
			}
		}()
		smallPool, smallPoolErr = NewFramePool(ctx, 1, 1, 1)
	}()
	t.Logf("MEASURE: 1x1 pool creation error=%v", smallPoolErr)
	if smallPool != nil {
		// Try to upload to 1x1 — chroma plane is (0 * 0) = 0 bytes
		func() {
			defer func() {
				if r := recover(); r != nil {
					t.Logf("MEASURE: 1x1 upload panicked: %v", r)
				}
			}()
			f, acqErr := smallPool.Acquire()
			t.Logf("MEASURE: 1x1 acquire error=%v", acqErr)
			if acqErr == nil && f != nil {
				// 1x1 YUV420: Y=1 byte, Cb=0 bytes (0.5*0.5 rounds to 0), Cr=0 bytes
				// This is degenerate. Upload expects width/2 * height/2 chroma.
				yuv := []byte{128}
				uploadErr := Upload(ctx, f, yuv, 1, 1)
				t.Logf("MEASURE: 1x1 upload error=%v", uploadErr)
				f.Release()
			}
		}()
		smallPool.Close()
	}

	// Step 3: Process valid 320x240 frame again — should succeed.
	frame2 := makeTestFrame(t, ctx, pool, width, height, 200, 100, 150)
	defer frame2.Release()
	yAvg2, cbAvg2, crAvg2 := avgPlanes(t, ctx, frame2, width, height)
	t.Logf("MEASURE: recovery frame Y=%.1f, Cb=%.1f, Cr=%.1f (expected 200, 100, 150)", yAvg2, cbAvg2, crAvg2)

	// Step 4: Assert GPU recovered from the invalid input.
	assert.InDelta(t, 200.0, yAvg2, 1.0, "GPU should recover: Y after invalid input")
	assert.InDelta(t, 100.0, cbAvg2, 1.0, "GPU should recover: Cb after invalid input")
	assert.InDelta(t, 150.0, crAvg2, 1.0, "GPU should recover: Cr after invalid input")

	_ = invalidErr
}

// ============================================================================
// Pool Exhaustion Under Load
// ============================================================================

// TestGPUPoolExhaustionUnderLoad verifies pool behavior when all frames are
// acquired and a 5th acquire attempts to exceed capacity.
func TestGPUPoolExhaustionUnderLoad(t *testing.T) {
	ctx := testContext(t)

	width, height := 320, 240
	poolCap := 4
	pool, err := NewFramePool(ctx, width, height, poolCap)
	require.NoError(t, err)
	defer pool.Close()

	// Step 1: Acquire all 4 frames.
	frames := make([]*GPUFrame, 0, poolCap)
	for i := 0; i < poolCap; i++ {
		f, acqErr := pool.Acquire()
		require.NoError(t, acqErr, "acquire frame %d", i)
		require.NotNil(t, f, "frame %d should not be nil", i)
		frames = append(frames, f)
	}
	t.Logf("MEASURE: acquired %d frames successfully", len(frames))

	// Step 2: Try to acquire 5th frame — should get heap fallback (pool miss).
	fifth, fifthErr := pool.Acquire()
	t.Logf("MEASURE: pool capacity=%d, 5th acquire result=%v, frame=%v", poolCap, fifthErr, fifth != nil)

	// The pool should allocate a new frame (heap fallback), not error.
	// This is the expected behavior based on the Acquire() implementation.
	if fifthErr != nil {
		t.Logf("MEASURE: 5th acquire returned error (unexpected): %v", fifthErr)
	} else {
		require.NotNil(t, fifth, "5th frame from heap fallback should not be nil")
		fifth.Release() // Release the overflow frame
	}

	// Step 3: Release all 4 pool frames.
	for _, f := range frames {
		f.Release()
	}

	// Step 4: Pool should be healthy — acquire 4 more successfully.
	hits1, misses1 := pool.Stats()
	t.Logf("MEASURE: pool stats before re-acquire: hits=%d, misses=%d", hits1, misses1)

	for i := 0; i < poolCap; i++ {
		f, acqErr := pool.Acquire()
		require.NoError(t, acqErr, "re-acquire frame %d", i)
		require.NotNil(t, f, "re-acquired frame %d should not be nil", i)
		// Verify we can actually use the frame.
		uploadErr := Upload(ctx, f, make([]byte, width*height*3/2), width, height)
		require.NoError(t, uploadErr, "upload to re-acquired frame %d", i)
		f.Release()
	}

	hits2, misses2 := pool.Stats()
	t.Logf("MEASURE: pool stats after re-acquire: hits=%d, misses=%d", hits2, misses2)

	// After releasing 4 and re-acquiring 4, we should have 4 more hits.
	assert.Equal(t, hits1+uint64(poolCap), hits2,
		"re-acquires should all be pool hits (LIFO reuse)")
}

// ============================================================================
// Frame Content After Pool Recycle
// ============================================================================

// TestGPUFrameContentAfterPoolRecycle verifies that a recycled frame does not
// carry stale data from its previous use.
func TestGPUFrameContentAfterPoolRecycle(t *testing.T) {
	ctx := testContext(t)

	width, height := 320, 240
	pool, err := NewFramePool(ctx, width, height, 2)
	require.NoError(t, err)
	defer pool.Close()

	// Step 1: Acquire frame, fill with Y=100, upload, release.
	frame1, err := pool.Acquire()
	require.NoError(t, err)
	ySize := width * height
	cbSize := (width / 2) * (height / 2)
	crSize := cbSize
	totalSize := ySize + cbSize + crSize
	yuv1 := make([]byte, totalSize)
	for i := 0; i < ySize; i++ {
		yuv1[i] = 100
	}
	for i := 0; i < cbSize; i++ {
		yuv1[ySize+i] = 128
	}
	for i := 0; i < crSize; i++ {
		yuv1[ySize+cbSize+i] = 128
	}
	require.NoError(t, Upload(ctx, frame1, yuv1, width, height))

	// Verify upload worked.
	yAvg1, _, _ := avgPlanes(t, ctx, frame1, width, height)
	t.Logf("MEASURE: first upload Y=%.1f (expected 100)", yAvg1)
	assert.InDelta(t, 100.0, yAvg1, 1.0)

	frame1.Release()

	// Step 2: Acquire again — should get same buffer from LIFO pool.
	frame2, err := pool.Acquire()
	require.NoError(t, err)

	// Before uploading new data, read what's in the recycled frame.
	// The GPU buffer still has old NV12 data — downloading should show ~Y=100.
	staleYUV := make([]byte, totalSize)
	require.NoError(t, Download(ctx, staleYUV, frame2, width, height))
	staleYAvg := 0.0
	for i := 0; i < ySize; i++ {
		staleYAvg += float64(staleYUV[i])
	}
	staleYAvg /= float64(ySize)
	t.Logf("MEASURE: recycled frame before new upload, stale Y=%.1f (was 100)", staleYAvg)

	// Step 3: Fill with Y=200, upload, blend at pos=1.0, download.
	yuv2 := make([]byte, totalSize)
	for i := 0; i < ySize; i++ {
		yuv2[i] = 200
	}
	for i := 0; i < cbSize; i++ {
		yuv2[ySize+i] = 128
	}
	for i := 0; i < crSize; i++ {
		yuv2[ySize+cbSize+i] = 128
	}
	require.NoError(t, Upload(ctx, frame2, yuv2, width, height))

	// Blend at pos=1.0: output = 100% of B (frame2). Need a dummy A frame.
	dummyA := makeTestFrame(t, ctx, pool, width, height, 50, 128, 128)

	dst, err := pool.Acquire()
	require.NoError(t, err)

	require.NoError(t, BlendMix(ctx, dst, dummyA, frame2, 1.0))

	yAvg, _, _ := avgPlanes(t, ctx, dst, width, height)
	t.Logf("MEASURE: after recycle, downloaded Y=%.1f (expected 200)", yAvg)

	// Step 4: Verify output is Y=200, not Y=100 (no stale data).
	assert.InDelta(t, 200.0, yAvg, 1.0,
		"recycled frame should contain new data (Y=200), not stale (Y=100)")

	dst.Release()
	dummyA.Release()
	frame2.Release()
}

// ============================================================================
// Double Release
// ============================================================================

// TestGPUDoubleRelease verifies that double-releasing a GPU frame does not
// panic and does not cause pool corruption (aliasing).
func TestGPUDoubleRelease(t *testing.T) {
	ctx := testContext(t)

	width, height := 320, 240
	pool, err := NewFramePool(ctx, width, height, 2)
	require.NoError(t, err)
	defer pool.Close()

	// Step 1: Acquire frame from pool.
	frame, err := pool.Acquire()
	require.NoError(t, err)
	require.NotNil(t, frame)

	// Fill with known data so we can check for aliasing later.
	ySize := width * height
	cbSize := (width / 2) * (height / 2)
	crSize := cbSize
	totalSize := ySize + cbSize + crSize
	yuv := make([]byte, totalSize)
	for i := 0; i < ySize; i++ {
		yuv[i] = 42
	}
	for i := 0; i < cbSize+crSize; i++ {
		yuv[ySize+i] = 128
	}
	require.NoError(t, Upload(ctx, frame, yuv, width, height))

	// Step 2: Release it.
	frame.Release()

	// Step 3: Release it again (double release).
	// The implementation logs an error but should NOT panic.
	didPanic := false
	func() {
		defer func() {
			if r := recover(); r != nil {
				didPanic = true
				t.Logf("MEASURE: double release panicked: %v", r)
			}
		}()
		frame.Release()
	}()
	t.Logf("MEASURE: double release panic=%v", didPanic)
	assert.False(t, didPanic, "double release should not panic")

	// Step 4: Acquire twice — check if both are valid (no aliasing).
	// After double release, the frame may have been returned to the pool twice,
	// which would mean two Acquire() calls return the SAME pointer — aliasing bug.
	f1, err := pool.Acquire()
	require.NoError(t, err)
	require.NotNil(t, f1)

	f2, err := pool.Acquire()
	require.NoError(t, err)
	require.NotNil(t, f2)

	// Check for aliasing: if double release returned the frame twice,
	// f1 and f2 would be the same pointer.
	aliased := f1 == f2
	t.Logf("MEASURE: double release behavior, f1==f2 (aliased)=%v", aliased)
	t.Logf("MEASURE: double release behavior, f1.DevPtr=%v, f2.DevPtr=%v", f1.DevPtr, f2.DevPtr)

	// Verify both frames are independently usable.
	yuv1 := make([]byte, totalSize)
	for i := 0; i < ySize; i++ {
		yuv1[i] = 100
	}
	for i := 0; i < cbSize+crSize; i++ {
		yuv1[ySize+i] = 128
	}
	err1 := Upload(ctx, f1, yuv1, width, height)
	t.Logf("MEASURE: double release behavior, f1 upload err=%v", err1)

	yuv2 := make([]byte, totalSize)
	for i := 0; i < ySize; i++ {
		yuv2[i] = 200
	}
	for i := 0; i < cbSize+crSize; i++ {
		yuv2[ySize+i] = 128
	}
	err2 := Upload(ctx, f2, yuv2, width, height)
	t.Logf("MEASURE: double release behavior, f2 upload err=%v", err2)

	// If aliased, uploading to f2 overwrites f1's data.
	if err1 == nil && err2 == nil {
		y1, _, _ := avgPlanes(t, ctx, f1, width, height)
		y2, _, _ := avgPlanes(t, ctx, f2, width, height)
		t.Logf("MEASURE: f1 Y=%.1f (expected 100), f2 Y=%.1f (expected 200)", y1, y2)

		f1Valid := math.Abs(y1-100.0) < 2.0
		f2Valid := math.Abs(y2-200.0) < 2.0
		t.Logf("MEASURE: double release behavior, second acquire valid=%v", f1Valid && f2Valid)

		if aliased {
			// If they're aliased, f1 read after f2 write will show f2's data.
			// This is the BUG we're looking for.
			assert.False(t, aliased,
				"BUG: double release caused pool aliasing — two Acquire() calls returned the same frame")
		}

		assert.InDelta(t, 100.0, y1, 2.0, "f1 should have Y=100")
		assert.InDelta(t, 200.0, y2, 2.0, "f2 should have Y=200")
	}

	f1.Release()
	f2.Release()
}

// ============================================================================
// Concurrent Acquire/Release
// ============================================================================

// TestGPUConcurrentAcquireRelease runs concurrent acquire→upload→download→release
// cycles to detect data races under the race detector.
func TestGPUConcurrentAcquireRelease(t *testing.T) {
	ctx := testContext(t)

	width, height := 320, 240
	poolSize := 8
	pool, err := NewFramePool(ctx, width, height, poolSize)
	require.NoError(t, err)
	defer pool.Close()

	goroutines := 4
	cyclesPerGoroutine := 100

	var totalCycles atomic.Int64
	var totalErrors atomic.Int64

	ySize := width * height
	cbSize := (width / 2) * (height / 2)
	crSize := cbSize
	totalSize := ySize + cbSize + crSize

	var wg sync.WaitGroup
	wg.Add(goroutines)

	for g := 0; g < goroutines; g++ {
		go func(gID int) {
			defer wg.Done()
			yVal := byte(50 + gID*50) // each goroutine uses a distinct Y value

			for c := 0; c < cyclesPerGoroutine; c++ {
				// Acquire
				frame, acqErr := pool.Acquire()
				if acqErr != nil {
					totalErrors.Add(1)
					continue
				}

				// Upload
				yuv := make([]byte, totalSize)
				for i := 0; i < ySize; i++ {
					yuv[i] = yVal
				}
				for i := 0; i < cbSize+crSize; i++ {
					yuv[ySize+i] = 128
				}
				if uploadErr := Upload(ctx, frame, yuv, width, height); uploadErr != nil {
					totalErrors.Add(1)
					frame.Release()
					continue
				}

				// Download
				result := make([]byte, totalSize)
				if dlErr := Download(ctx, result, frame, width, height); dlErr != nil {
					totalErrors.Add(1)
					frame.Release()
					continue
				}

				// Verify our Y value survived the round-trip.
				// Sample a few pixels rather than checking all.
				sampleOk := true
				for _, idx := range []int{0, ySize / 4, ySize / 2, ySize - 1} {
					diff := int(result[idx]) - int(yVal)
					if diff < -1 || diff > 1 {
						sampleOk = false
						break
					}
				}
				if !sampleOk {
					totalErrors.Add(1)
				}

				// Release
				frame.Release()
				totalCycles.Add(1)
			}
		}(g)
	}

	wg.Wait()

	cycles := totalCycles.Load()
	errors := totalErrors.Load()
	t.Logf("MEASURE: total cycles=%d, errors=%d", cycles, errors)

	hits, misses := pool.Stats()
	t.Logf("MEASURE: pool stats: hits=%d, misses=%d", hits, misses)

	assert.Equal(t, int64(0), errors, "concurrent acquire/release should have no errors")
	assert.Equal(t, int64(goroutines*cyclesPerGoroutine), cycles,
		"all cycles should complete")
}
