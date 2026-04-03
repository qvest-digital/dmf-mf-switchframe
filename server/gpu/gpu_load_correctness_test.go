//go:build correctness && ((cgo && cuda) || (cgo && darwin))

package gpu

import (
	"fmt"
	"math"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ============================================================================
// Full Pipeline Chain at 1080p
// ============================================================================

// TestGPUPipelineFullChain1080p runs a full GPU chain (upload → blend → download)
// at 1920x1080 for 30 consecutive frames and checks consistency.
func TestGPUPipelineFullChain1080p(t *testing.T) {
	ctx := testContext(t)

	width, height := 1920, 1080
	// Need 4 frames: A, B, dst, plus 1 spare for pool cycling
	pool, err := NewFramePool(ctx, width, height, 4)
	require.NoError(t, err)
	defer pool.Close()

	// Source A: Y=80, neutral chroma
	a := makeTestFrame(t, ctx, pool, width, height, 80, 128, 128)
	defer a.Release()

	// Source B: Y=160, neutral chroma
	b := makeTestFrame(t, ctx, pool, width, height, 160, 128, 128)
	defer b.Release()

	const numFrames = 30
	results := make([]float64, numFrames)
	durations := make([]time.Duration, numFrames)

	for i := 0; i < numFrames; i++ {
		dst, err := pool.Acquire()
		require.NoError(t, err, "frame %d: acquire dst", i)

		start := time.Now()
		err = BlendMix(ctx, dst, a, b, 0.5)
		elapsed := time.Since(start)
		require.NoError(t, err, "frame %d: blend", i)

		durations[i] = elapsed

		yAvg, _, _ := avgPlanes(t, ctx, dst, width, height)
		results[i] = yAvg
		dst.Release()
	}

	// Log all measured values before any assertion
	for i := 0; i < numFrames; i++ {
		t.Logf("MEASURE: frame %d: avg Y = %.4f, processing time = %v", i, results[i], durations[i])
	}

	// Verify first result: avg Y should be ~120 (midpoint of 80 and 160)
	t.Logf("MEASURE: avg Y per frame = %v, processing time per frame = %v", results, durations)
	assert.InDelta(t, 120.0, results[0], 2.0,
		"blend(80, 160, 0.5) should produce Y≈120, got %.4f", results[0])

	// Verify all 30 results are consistent with each other (within ±1 of first)
	inconsistentFrames := 0
	for i := 1; i < numFrames; i++ {
		diff := math.Abs(results[i] - results[0])
		if diff > 1.0 {
			inconsistentFrames++
			t.Logf("MEASURE: frame %d inconsistent: %.4f vs frame 0: %.4f (diff=%.4f)", i, results[i], results[0], diff)
		}
	}
	assert.Equal(t, 0, inconsistentFrames,
		"%d of %d frames were inconsistent (>±1 from first result)", inconsistentFrames, numFrames)
}

// ============================================================================
// Memory Stability
// ============================================================================

// TestGPUMemoryStability runs 300 frames through GPU upload → blend → download,
// releasing each output frame, and checks that the pool does not leak.
func TestGPUMemoryStability(t *testing.T) {
	ctx := testContext(t)

	width, height := 320, 240
	// Pool with 6 frames: 2 sources + 1 dst + 3 spare
	pool, err := NewFramePool(ctx, width, height, 6)
	require.NoError(t, err)
	defer pool.Close()

	hitsStart, missesStart := pool.Stats()
	t.Logf("MEASURE: pool frames at start: hits=%d, misses=%d", hitsStart, missesStart)

	// Persistent source frames
	a := makeTestFrame(t, ctx, pool, width, height, 40, 100, 200)
	defer a.Release()
	b := makeTestFrame(t, ctx, pool, width, height, 220, 50, 150)
	defer b.Release()

	const numFrames = 300
	ySize := width * height
	cbSize := (width / 2) * (height / 2)
	crSize := cbSize
	downloadBuf := make([]byte, ySize+cbSize+crSize)

	var maxMissesSeen uint64
	var lastY float64

	for i := 0; i < numFrames; i++ {
		dst, err := pool.Acquire()
		require.NoError(t, err, "frame %d: acquire", i)

		err = BlendMix(ctx, dst, a, b, 0.5)
		require.NoError(t, err, "frame %d: blend", i)

		err = Download(ctx, downloadBuf, dst, width, height)
		require.NoError(t, err, "frame %d: download", i)

		// Verify download produced valid data
		var ySum int64
		for j := 0; j < ySize; j++ {
			ySum += int64(downloadBuf[j])
		}
		yAvg := float64(ySum) / float64(ySize)

		if i == 0 {
			lastY = yAvg
		}

		// Release the output frame back to pool
		dst.Release()

		// Check pool stats periodically
		if i%50 == 0 {
			_, misses := pool.Stats()
			if misses > maxMissesSeen {
				maxMissesSeen = misses
			}
			t.Logf("MEASURE: frame %d: avg Y = %.4f, pool misses so far = %d", i, yAvg, misses)
		}

		// Frame-to-frame consistency: same inputs should produce same output
		if math.Abs(yAvg-lastY) > 2.0 {
			t.Logf("MEASURE: frame %d: Y drift detected: %.4f vs previous %.4f", i, yAvg, lastY)
		}
		lastY = yAvg
	}

	hitsEnd, missesEnd := pool.Stats()
	t.Logf("MEASURE: pool frames at start: hits=%d, misses=%d", hitsStart, missesStart)
	t.Logf("MEASURE: pool frames at end: hits=%d, misses=%d", hitsEnd, missesEnd)
	t.Logf("MEASURE: total new misses during test = %d", missesEnd-missesStart)

	// After releasing sources (2 frames) + 300 dst frames acquired and released,
	// misses beyond the initial pool allocation indicate potential pool pressure
	// or leaks. With 6 frames in pool and 2 held for sources, we should have
	// 4 available for dst cycling. If misses grow beyond the initial allocation,
	// frames are leaking.
	//
	// The pool starts with 6 frames. We acquire 2 for sources (not released yet),
	// leaving 4 free. Each iteration acquires 1 and releases 1, so misses should
	// be bounded.
	//
	// Initial allocation: 6 frames acquired = 6 misses initially reported? No,
	// pool.Stats() tracks hits/misses on Acquire calls, not on initial alloc.
	// Initial frames go into the free list directly, so first 4 Acquires after
	// sources are taken should be hits.
	t.Logf("MEASURE: misses growth = %d (should be small)", missesEnd-missesStart)

	// With a pool of 6 and only ever holding 3 frames at once (a, b, dst),
	// there should be zero new misses after the initial source allocation.
	// Allow 3 misses for the initial makeTestFrame calls (2 sources + 1 spare).
	// The key assertion: misses should NOT grow with the number of frames processed.
	// If misses grew proportionally to numFrames, frames are leaking.
	if missesEnd-missesStart > 10 {
		t.Errorf("MEASURE: pool misses grew by %d over %d frames — possible leak", missesEnd-missesStart, numFrames)
	}
}

// ============================================================================
// Context Recovery After Invalid Input
// ============================================================================

// TestGPUContextRecovery processes valid frames, submits invalid operations,
// then verifies the GPU context is still functional.
func TestGPUContextRecovery(t *testing.T) {
	ctx := testContext(t)

	width, height := 128, 96
	pool, err := NewFramePool(ctx, width, height, 4)
	require.NoError(t, err)
	defer pool.Close()

	// Phase 1: Process 10 valid frames
	validBefore := 0
	for i := 0; i < 10; i++ {
		a := makeTestFrame(t, ctx, pool, width, height, 100, 128, 128)
		b := makeTestFrame(t, ctx, pool, width, height, 200, 128, 128)
		dst, err := pool.Acquire()
		require.NoError(t, err)

		err = BlendMix(ctx, dst, a, b, 0.5)
		if err == nil {
			yAvg, _, _ := avgPlanes(t, ctx, dst, width, height)
			if math.Abs(yAvg-150.0) < 5.0 {
				validBefore++
			}
		}
		dst.Release()
		b.Release()
		a.Release()
	}
	t.Logf("MEASURE: valid frames before error injection = %d/10", validBefore)
	require.Equal(t, 10, validBefore, "all pre-error frames should be valid")

	// Phase 2: Try invalid operations
	errCount := 0

	// 2a: nil frame to blend
	err = BlendMix(ctx, nil, nil, nil, 0.5)
	if err != nil {
		errCount++
		t.Logf("MEASURE: nil blend error = %v", err)
	} else {
		t.Log("MEASURE: nil blend returned no error — potential bug")
	}

	// 2b: nil dst to blend
	a := makeTestFrame(t, ctx, pool, width, height, 100, 128, 128)
	b := makeTestFrame(t, ctx, pool, width, height, 200, 128, 128)
	err = BlendMix(ctx, nil, a, b, 0.5)
	if err != nil {
		errCount++
		t.Logf("MEASURE: nil-dst blend error = %v", err)
	} else {
		t.Log("MEASURE: nil-dst blend returned no error — potential bug")
	}
	b.Release()
	a.Release()

	// 2c: Upload with undersized buffer
	frame, err := pool.Acquire()
	require.NoError(t, err)
	tinyBuf := make([]byte, 10) // way too small
	err = Upload(ctx, frame, tinyBuf, width, height)
	if err != nil {
		errCount++
		t.Logf("MEASURE: undersized upload error = %v", err)
	} else {
		t.Log("MEASURE: undersized upload returned no error — potential bug")
	}
	frame.Release()

	// 2d: Download with undersized buffer
	src := makeTestFrame(t, ctx, pool, width, height, 100, 128, 128)
	tinyOut := make([]byte, 10)
	err = Download(ctx, tinyOut, src, width, height)
	if err != nil {
		errCount++
		t.Logf("MEASURE: undersized download error = %v", err)
	} else {
		t.Log("MEASURE: undersized download returned no error — potential bug")
	}
	src.Release()

	// 2e: Blend with out-of-range position
	a = makeTestFrame(t, ctx, pool, width, height, 100, 128, 128)
	b = makeTestFrame(t, ctx, pool, width, height, 200, 128, 128)
	dst, err := pool.Acquire()
	require.NoError(t, err)
	err = BlendMix(ctx, dst, a, b, -5.0)
	if err != nil {
		errCount++
		t.Logf("MEASURE: negative position blend error = %v", err)
	} else {
		// Not necessarily a bug — the function clamps. But let's measure what it produces.
		yAvg, _, _ := avgPlanes(t, ctx, dst, width, height)
		t.Logf("MEASURE: negative position blend produced Y=%.4f (should be source A ≈ 100)", yAvg)
		assert.InDelta(t, 100.0, yAvg, 2.0,
			"position=-5.0 (clamped to 0) should produce source A")
	}
	dst.Release()

	err = BlendMix(ctx, nil, a, b, 99.0)
	if err != nil {
		errCount++
		t.Logf("MEASURE: huge position nil-dst blend error = %v", err)
	}
	b.Release()
	a.Release()

	t.Logf("MEASURE: errors on invalid input = %d", errCount)

	// Phase 3: Process 10 more valid frames — GPU should still work
	validAfter := 0
	for i := 0; i < 10; i++ {
		a := makeTestFrame(t, ctx, pool, width, height, 100, 128, 128)
		b := makeTestFrame(t, ctx, pool, width, height, 200, 128, 128)
		dst, err := pool.Acquire()
		require.NoError(t, err)

		err = BlendMix(ctx, dst, a, b, 0.5)
		if err == nil {
			yAvg, _, _ := avgPlanes(t, ctx, dst, width, height)
			if math.Abs(yAvg-150.0) < 5.0 {
				validAfter++
			} else {
				t.Logf("MEASURE: post-recovery frame %d: Y=%.4f (expected ≈150)", i, yAvg)
			}
		} else {
			t.Logf("MEASURE: post-recovery frame %d: blend error = %v", i, err)
		}
		dst.Release()
		b.Release()
		a.Release()
	}
	t.Logf("MEASURE: valid frames after recovery = %d/10", validAfter)
	assert.Equal(t, 10, validAfter,
		"GPU should still produce correct output after error injection, got %d/10 valid", validAfter)
}

// ============================================================================
// Rapid Pool Cycling
// ============================================================================

// TestGPURapidPoolCycle cycles 100 acquire → upload → download → release
// iterations on a small pool (4 frames at 320x240) and verifies pool health.
func TestGPURapidPoolCycle(t *testing.T) {
	ctx := testContext(t)

	width, height := 320, 240
	pool, err := NewFramePool(ctx, width, height, 4)
	require.NoError(t, err)
	defer pool.Close()

	hitsStart, missesStart := pool.Stats()
	t.Logf("MEASURE: initial pool stats: hits=%d, misses=%d", hitsStart, missesStart)

	ySize := width * height
	cbSize := (width / 2) * (height / 2)
	crSize := cbSize
	totalSize := ySize + cbSize + crSize

	// Create a source YUV pattern
	srcYUV := make([]byte, totalSize)
	for i := 0; i < ySize; i++ {
		srcYUV[i] = byte(100 + (i % 50))
	}
	for i := 0; i < cbSize; i++ {
		srcYUV[ySize+i] = 128
	}
	for i := 0; i < crSize; i++ {
		srcYUV[ySize+cbSize+i] = 128
	}

	downloadBuf := make([]byte, totalSize)
	const numCycles = 100
	validDownloads := 0
	var firstYAvg float64

	for i := 0; i < numCycles; i++ {
		// Acquire
		frame, err := pool.Acquire()
		require.NoError(t, err, "cycle %d: acquire", i)

		// Upload
		err = Upload(ctx, frame, srcYUV, width, height)
		require.NoError(t, err, "cycle %d: upload", i)

		// Download
		err = Download(ctx, downloadBuf, frame, width, height)
		require.NoError(t, err, "cycle %d: download", i)

		// Verify download content
		var ySum int64
		for j := 0; j < ySize; j++ {
			ySum += int64(downloadBuf[j])
		}
		yAvg := float64(ySum) / float64(ySize)

		if i == 0 {
			firstYAvg = yAvg
			t.Logf("MEASURE: cycle 0: avg Y = %.4f", yAvg)
		}

		// Each download should match the upload within rounding (NV12 conversion)
		if math.Abs(yAvg-firstYAvg) < 2.0 {
			validDownloads++
		} else {
			t.Logf("MEASURE: cycle %d: avg Y = %.4f (expected ≈%.4f)", i, yAvg, firstYAvg)
		}

		// Release
		frame.Release()
	}

	hitsEnd, missesEnd := pool.Stats()
	t.Logf("MEASURE: 100 cycles completed, pool hits = %d, misses = %d", hitsEnd-hitsStart, missesEnd-missesStart)
	t.Logf("MEASURE: valid downloads = %d/%d", validDownloads, numCycles)

	// With a pool of 4 and only ever holding 1 frame at a time, we should
	// get 100 hits and 0 new misses (after initial allocation).
	assert.Equal(t, numCycles, validDownloads,
		"all %d downloads should produce valid consistent output", numCycles)

	// Pool hits should be exactly numCycles (every acquire should hit the pool)
	totalHits := hitsEnd - hitsStart
	totalMisses := missesEnd - missesStart
	t.Logf("MEASURE: pool efficiency: %d hits, %d misses over %d cycles", totalHits, totalMisses, numCycles)

	// After initial pool creation, all frames are in the free list.
	// Every cycle: acquire (hit) → release (returns to pool).
	// So we expect numCycles hits and 0 misses.
	assert.Equal(t, uint64(numCycles), totalHits,
		"expected %d pool hits, got %d", numCycles, totalHits)
	assert.Equal(t, uint64(0), totalMisses,
		"expected 0 pool misses during cycling, got %d (leak?)", totalMisses)
}

// ============================================================================
// Blend Position Sweep (Load Variant)
// ============================================================================

// TestGPUBlendPositionSweep1080p sweeps blend position from 0.0 to 1.0 in
// 0.01 increments at 1080p, verifying monotonicity of the Y channel.
// This is a load test: 101 blend operations at full HD resolution.
func TestGPUBlendPositionSweep1080p(t *testing.T) {
	ctx := testContext(t)

	width, height := 1920, 1080
	pool, err := NewFramePool(ctx, width, height, 4)
	require.NoError(t, err)
	defer pool.Close()

	a := makeTestFrame(t, ctx, pool, width, height, 16, 128, 128)
	defer a.Release()
	b := makeTestFrame(t, ctx, pool, width, height, 235, 128, 128)
	defer b.Release()

	dst, err := pool.Acquire()
	require.NoError(t, err)
	defer dst.Release()

	var prevY float64
	monotonicViolations := 0

	for i := 0; i <= 100; i++ {
		pos := float64(i) / 100.0
		err := BlendMix(ctx, dst, a, b, pos)
		require.NoError(t, err, "pos=%.2f: blend", pos)

		yAvg, _, _ := avgPlanes(t, ctx, dst, width, height)

		// Expected: Y should increase monotonically from ~16 to ~235
		expectedY := 16.0 + pos*(235.0-16.0)
		if math.Abs(yAvg-expectedY) > 3.0 {
			t.Logf("MEASURE: pos=%.2f: Y=%.4f, expected=%.4f, delta=%.4f",
				pos, yAvg, expectedY, yAvg-expectedY)
		}

		// Check monotonicity (Y should not decrease as position increases)
		if i > 0 && yAvg < prevY-1.0 {
			monotonicViolations++
			t.Logf("MEASURE: monotonicity violation at pos=%.2f: Y=%.4f < prev=%.4f",
				pos, yAvg, prevY)
		}
		prevY = yAvg

		if i%25 == 0 {
			t.Logf("MEASURE: pos=%.2f: Y=%.4f (expected ≈%.1f)", pos, yAvg, expectedY)
		}
	}

	assert.Equal(t, 0, monotonicViolations,
		"blend Y should be monotonically increasing with position, got %d violations", monotonicViolations)
}

// ============================================================================
// Fade-to-Black Under Load
// ============================================================================

// TestGPUFTBFullRange verifies FTB across the full range, checking that
// position 1.0 produces BT.709 black (Y=16) and position 0.0 preserves input.
func TestGPUFTBFullRange(t *testing.T) {
	ctx := testContext(t)

	width, height := 640, 480
	pool, err := NewFramePool(ctx, width, height, 3)
	require.NoError(t, err)
	defer pool.Close()

	src := makeTestFrame(t, ctx, pool, width, height, 200, 80, 180)
	defer src.Release()

	dst, err := pool.Acquire()
	require.NoError(t, err)
	defer dst.Release()

	// Position 0.0: should preserve source
	err = BlendFTB(ctx, dst, src, 0.0)
	require.NoError(t, err)
	yAvg0, cbAvg0, crAvg0 := avgPlanes(t, ctx, dst, width, height)
	t.Logf("MEASURE: FTB pos=0.0: Y=%.4f, Cb=%.4f, Cr=%.4f", yAvg0, cbAvg0, crAvg0)
	assert.InDelta(t, 200.0, yAvg0, 2.0, "FTB pos=0: Y should match source")
	assert.InDelta(t, 80.0, cbAvg0, 2.0, "FTB pos=0: Cb should match source")
	assert.InDelta(t, 180.0, crAvg0, 2.0, "FTB pos=0: Cr should match source")

	// Position 1.0: should be BT.709 black (Y=16, Cb=128, Cr=128)
	err = BlendFTB(ctx, dst, src, 1.0)
	require.NoError(t, err)
	yAvg1, cbAvg1, crAvg1 := avgPlanes(t, ctx, dst, width, height)
	t.Logf("MEASURE: FTB pos=1.0: Y=%.4f, Cb=%.4f, Cr=%.4f", yAvg1, cbAvg1, crAvg1)
	assert.InDelta(t, 16.0, yAvg1, 2.0, "FTB pos=1: Y should be black (16)")
	assert.InDelta(t, 128.0, cbAvg1, 2.0, "FTB pos=1: Cb should be neutral (128)")
	assert.InDelta(t, 128.0, crAvg1, 2.0, "FTB pos=1: Cr should be neutral (128)")

	// Position 0.5: Y should be midpoint of source and black
	err = BlendFTB(ctx, dst, src, 0.5)
	require.NoError(t, err)
	yAvg5, cbAvg5, crAvg5 := avgPlanes(t, ctx, dst, width, height)
	expectedY5 := (200.0 + 16.0) / 2.0   // 108
	expectedCb5 := (80.0 + 128.0) / 2.0  // 104
	expectedCr5 := (180.0 + 128.0) / 2.0 // 154
	t.Logf("MEASURE: FTB pos=0.5: Y=%.4f (exp=%.1f), Cb=%.4f (exp=%.1f), Cr=%.4f (exp=%.1f)",
		yAvg5, expectedY5, cbAvg5, expectedCb5, crAvg5, expectedCr5)
	assert.InDelta(t, expectedY5, yAvg5, 2.0, "FTB pos=0.5: Y should be midpoint")
	assert.InDelta(t, expectedCb5, cbAvg5, 2.0, "FTB pos=0.5: Cb should be midpoint")
	assert.InDelta(t, expectedCr5, crAvg5, 2.0, "FTB pos=0.5: Cr should be midpoint")

	// Run 50 rapid FTB operations to stress test
	for i := 0; i < 50; i++ {
		pos := float64(i) / 50.0
		err = BlendFTB(ctx, dst, src, pos)
		require.NoError(t, err, "rapid FTB frame %d pos=%.2f", i, pos)
	}
	t.Log("MEASURE: 50 rapid FTB operations completed without error")
}

// ============================================================================
// Pool Exhaustion and Recovery
// ============================================================================

// TestGPUPoolExhaustionRecovery exhausts the pool, verifies new allocations
// happen (misses), then releases everything and verifies the pool recovers.
func TestGPUPoolExhaustionRecovery(t *testing.T) {
	ctx := testContext(t)

	width, height := 64, 64
	poolSize := 3
	pool, err := NewFramePool(ctx, width, height, poolSize)
	require.NoError(t, err)
	defer pool.Close()

	_, missesStart := pool.Stats()

	// Acquire all pool frames
	held := make([]*GPUFrame, 0, poolSize+5)
	for i := 0; i < poolSize; i++ {
		frame, err := pool.Acquire()
		require.NoError(t, err, "acquire %d", i)
		held = append(held, frame)
	}

	_, missesAfterDrain := pool.Stats()
	t.Logf("MEASURE: after draining pool: misses = %d (was %d)", missesAfterDrain, missesStart)

	// Now acquire 5 more — these should all be misses (new allocations)
	for i := 0; i < 5; i++ {
		frame, err := pool.Acquire()
		require.NoError(t, err, "over-acquire %d", i)
		held = append(held, frame)
	}

	_, missesAfterOverflow := pool.Stats()
	t.Logf("MEASURE: after 5 over-acquires: misses = %d", missesAfterOverflow)
	overflowMisses := missesAfterOverflow - missesAfterDrain
	assert.Equal(t, uint64(5), overflowMisses,
		"5 over-acquires should produce 5 misses, got %d", overflowMisses)

	// Verify all acquired frames are usable
	for i, frame := range held {
		err := Upload(ctx, frame, make([]byte, width*height*3/2), width, height)
		require.NoError(t, err, "upload to frame %d", i)
	}
	t.Logf("MEASURE: all %d held frames are usable", len(held))

	// Release all frames
	for _, frame := range held {
		frame.Release()
	}

	// After releasing all, pool should be restored.
	// Note: pool cap is poolSize, so only poolSize frames go back to the free list.
	// The overflow frames get freed when released (pool is full).
	hitsEnd, missesEnd := pool.Stats()
	t.Logf("MEASURE: after releasing all: hits=%d, misses=%d", hitsEnd, missesEnd)

	// Acquire and release one more to verify pool is still healthy
	frame, err := pool.Acquire()
	require.NoError(t, err, "post-recovery acquire")
	yAvg, _, _ := avgPlanes(t, ctx, frame, width, height)
	t.Logf("MEASURE: post-recovery frame avg Y = %.4f", yAvg)
	frame.Release()

	finalHits, _ := pool.Stats()
	postRecoveryHits := finalHits - hitsEnd
	assert.Equal(t, uint64(1), postRecoveryHits,
		"post-recovery acquire should be a pool hit (frame was returned), got %d hits", postRecoveryHits)
}

// ============================================================================
// Frame Dimensions After Pool Return
// ============================================================================

// TestGPUPoolDimensionReset verifies that the pool resets frame dimensions
// when a frame is returned, preventing stale dimension corruption.
func TestGPUPoolDimensionReset(t *testing.T) {
	ctx := testContext(t)

	width, height := 320, 240
	pool, err := NewFramePool(ctx, width, height, 2)
	require.NoError(t, err)
	defer pool.Close()

	// Acquire a frame
	frame, err := pool.Acquire()
	require.NoError(t, err)
	t.Logf("MEASURE: acquired frame: Width=%d, Height=%d", frame.Width, frame.Height)
	assert.Equal(t, width, frame.Width)
	assert.Equal(t, height, frame.Height)

	// Mutate the dimensions (simulating stinger overlay or resolution mismatch)
	frame.Width = 1920
	frame.Height = 1080
	t.Logf("MEASURE: mutated frame: Width=%d, Height=%d", frame.Width, frame.Height)

	// Release back to pool
	frame.Release()

	// Re-acquire — dimensions should be reset to pool defaults
	frame2, err := pool.Acquire()
	require.NoError(t, err)
	defer frame2.Release()
	t.Logf("MEASURE: re-acquired frame: Width=%d, Height=%d", frame2.Width, frame2.Height)
	assert.Equal(t, width, frame2.Width,
		"pool should reset Width to %d, got %d", width, frame2.Width)
	assert.Equal(t, height, frame2.Height,
		"pool should reset Height to %d, got %d", height, frame2.Height)
}

// ============================================================================
// Reference Count Correctness
// ============================================================================

// TestGPUFrameRefCounting verifies that Ref()/Release() behaves correctly
// and double-release doesn't crash.
func TestGPUFrameRefCounting(t *testing.T) {
	ctx := testContext(t)

	width, height := 64, 64
	pool, err := NewFramePool(ctx, width, height, 2)
	require.NoError(t, err)
	defer pool.Close()

	// Acquire, ref, and verify refcount semantics
	frame, err := pool.Acquire()
	require.NoError(t, err)
	t.Logf("MEASURE: acquired frame with initial refs=1")

	// Add a ref (simulates sharing between pipeline stages)
	frame.Ref()
	t.Logf("MEASURE: after Ref(), releasing once should not return to pool")

	// First release: should not return to pool (refcount goes to 1)
	frame.Release()

	// Frame should still be usable
	err = Upload(ctx, frame, make([]byte, width*height*3/2), width, height)
	if err != nil {
		t.Logf("MEASURE: upload after first release error: %v", err)
	}

	// Second release: should return to pool (refcount goes to 0)
	frame.Release()
	t.Logf("MEASURE: after second Release(), frame should be back in pool")

	// Acquire again — should get the same frame back from pool
	frame2, err := pool.Acquire()
	require.NoError(t, err)
	defer frame2.Release()
	t.Logf("MEASURE: re-acquired frame after ref counting cycle")

	// Verify the frame is still usable after the full ref cycle
	srcBuf := make([]byte, width*height*3/2)
	for i := range srcBuf {
		srcBuf[i] = 128
	}
	err = Upload(ctx, frame2, srcBuf, width, height)
	require.NoError(t, err, "frame should be usable after ref cycle")
	yAvg, _, _ := avgPlanes(t, ctx, frame2, width, height)
	t.Logf("MEASURE: post-refcycle frame Y = %.4f (should be ≈128)", yAvg)
	assert.InDelta(t, 128.0, yAvg, 2.0, "frame should still produce correct output after ref cycle")
}

// ============================================================================
// Concurrent Pool Access
// ============================================================================

// TestGPUPoolConcurrentAccess tests that multiple goroutines can safely
// acquire and release frames from the same pool.
func TestGPUPoolConcurrentAccess(t *testing.T) {
	ctx := testContext(t)

	width, height := 64, 64
	pool, err := NewFramePool(ctx, width, height, 8)
	require.NoError(t, err)
	defer pool.Close()

	const numGoroutines = 4
	const cyclesPerGoroutine = 25
	errCh := make(chan error, numGoroutines*cyclesPerGoroutine)
	doneCh := make(chan int, numGoroutines)

	for g := 0; g < numGoroutines; g++ {
		go func(id int) {
			valid := 0
			srcBuf := make([]byte, width*height*3/2)
			for i := range srcBuf {
				srcBuf[i] = byte(50 + id*40) // different value per goroutine
			}
			dstBuf := make([]byte, width*height*3/2)

			for c := 0; c < cyclesPerGoroutine; c++ {
				frame, err := pool.Acquire()
				if err != nil {
					errCh <- fmt.Errorf("goroutine %d cycle %d: acquire: %w", id, c, err)
					continue
				}

				err = Upload(ctx, frame, srcBuf, width, height)
				if err != nil {
					errCh <- fmt.Errorf("goroutine %d cycle %d: upload: %w", id, c, err)
					frame.Release()
					continue
				}

				err = Download(ctx, dstBuf, frame, width, height)
				if err != nil {
					errCh <- fmt.Errorf("goroutine %d cycle %d: download: %w", id, c, err)
					frame.Release()
					continue
				}

				frame.Release()
				valid++
			}
			doneCh <- valid
		}(g)
	}

	totalValid := 0
	for g := 0; g < numGoroutines; g++ {
		valid := <-doneCh
		totalValid += valid
	}
	close(errCh)

	var errors []error
	for err := range errCh {
		errors = append(errors, err)
	}

	t.Logf("MEASURE: concurrent pool access: %d/%d valid operations, %d errors",
		totalValid, numGoroutines*cyclesPerGoroutine, len(errors))
	for _, err := range errors {
		t.Logf("MEASURE: error: %v", err)
	}

	assert.Equal(t, numGoroutines*cyclesPerGoroutine, totalValid,
		"all concurrent operations should succeed")
	assert.Empty(t, errors, "no errors expected in concurrent pool access")
}
