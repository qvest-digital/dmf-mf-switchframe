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
// Full Pipeline Chain at 1080p — P210
// ============================================================================

// TestGPU_P210_FullChain1080p runs 30 consecutive P210 blend operations at
// 1920x1080 and verifies output consistency. A=Y:320, B=Y:640 blended at 50%
// should produce Y ~480 (the integer math midpoint).
func TestGPU_P210_FullChain1080p(t *testing.T) {
	ctx := testContext(t)

	width, height := 1920, 1080
	pool, err := NewP210FramePool(ctx, width, height, 4)
	require.NoError(t, err)
	defer pool.Close()

	dataA := makeP210TestFrame(width, height, 320, 512, 512)
	dataB := makeP210TestFrame(width, height, 640, 512, 512)

	frameA := uploadP210(t, ctx, pool, dataA, width, height)
	defer frameA.Release()
	frameB := uploadP210(t, ctx, pool, dataB, width, height)
	defer frameB.Release()

	const numFrames = 30
	results := make([]uint16, numFrames)
	durations := make([]time.Duration, numFrames)

	for i := 0; i < numFrames; i++ {
		frameDst, err := pool.Acquire()
		require.NoError(t, err, "frame %d: acquire dst", i)

		start := time.Now()
		err = BlendMixP210(ctx, frameDst, frameA, frameB, 0.5)
		elapsed := time.Since(start)
		require.NoError(t, err, "frame %d: blend", i)

		durations[i] = elapsed

		result := make([]byte, p210DownloadSize(width, height))
		err = Download10(ctx, frameDst, result, width, height)
		require.NoError(t, err)

		results[i] = readP210YSample(result, width*height/2) // sample from middle
		frameDst.Release()
	}

	// Integer midpoint of 320 and 640: (320*128 + 640*128 + 128) >> 8 = 480
	expectedY := uint16(480)
	assert.InDelta(t, float64(expectedY), float64(results[0]), 2,
		"blend(320, 640, 0.5) should produce Y~480, got %d", results[0])

	// All frames must be consistent
	for i := 1; i < numFrames; i++ {
		diff := math.Abs(float64(results[i]) - float64(results[0]))
		assert.LessOrEqual(t, diff, 1.0,
			"frame %d: Y=%d differs from frame 0: Y=%d", i, results[i], results[0])
	}
}

// ============================================================================
// Memory Stability
// ============================================================================

// TestGPU_P210_MemoryStability runs 300 P210 blend operations, releasing each
// output frame, and verifies no Y drift exceeds 4 LSB per frame (10-bit tolerance).
func TestGPU_P210_MemoryStability(t *testing.T) {
	ctx := testContext(t)

	width, height := 320, 240
	pool, err := NewP210FramePool(ctx, width, height, 6)
	require.NoError(t, err)
	defer pool.Close()

	dataA := makeP210TestFrame(width, height, 200, 500, 500)
	dataB := makeP210TestFrame(width, height, 800, 500, 500)

	frameA := uploadP210(t, ctx, pool, dataA, width, height)
	defer frameA.Release()
	frameB := uploadP210(t, ctx, pool, dataB, width, height)
	defer frameB.Release()

	const numFrames = 300
	var firstY uint16
	driftViolations := 0

	for i := 0; i < numFrames; i++ {
		frameDst, err := pool.Acquire()
		require.NoError(t, err, "frame %d: acquire", i)

		err = BlendMixP210(ctx, frameDst, frameA, frameB, 0.5)
		require.NoError(t, err, "frame %d: blend", i)

		result := make([]byte, p210DownloadSize(width, height))
		err = Download10(ctx, frameDst, result, width, height)
		require.NoError(t, err)

		gotY := readP210YSample(result, 0)

		if i == 0 {
			firstY = gotY
		} else {
			drift := math.Abs(float64(gotY) - float64(firstY))
			if drift > 4 {
				driftViolations++
				if driftViolations <= 5 {
					t.Logf("frame %d: Y drift %d (first=%d, now=%d)", i, int(drift), firstY, gotY)
				}
			}
		}

		frameDst.Release()
	}

	assert.Equal(t, 0, driftViolations,
		"no frame should drift > 4 LSB from first frame over %d frames, got %d violations", numFrames, driftViolations)
}

// ============================================================================
// Rapid Pool Cycling
// ============================================================================

// TestGPU_P210_RapidPoolCycle performs 100 acquire-upload-download-release cycles
// on P210 frames and verifies consistent output.
func TestGPU_P210_RapidPoolCycle(t *testing.T) {
	ctx := testContext(t)

	width, height := 64, 32
	pool, err := NewP210FramePool(ctx, width, height, 4)
	require.NoError(t, err)
	defer pool.Close()

	src := makeP210TestFrame(width, height, 750, 300, 600)
	downloadBuf := make([]byte, p210DownloadSize(width, height))

	const numCycles = 100
	validDownloads := 0

	for i := 0; i < numCycles; i++ {
		frame, err := pool.Acquire()
		require.NoError(t, err, "cycle %d: acquire", i)

		err = Upload10(ctx, frame, src, width, height)
		require.NoError(t, err, "cycle %d: upload", i)

		err = Download10(ctx, frame, downloadBuf, width, height)
		require.NoError(t, err, "cycle %d: download", i)

		gotY := readP210YSample(downloadBuf, 0)
		if gotY == 750 {
			validDownloads++
		} else if i < 5 {
			t.Logf("cycle %d: Y=%d, expected 750", i, gotY)
		}

		frame.Release()
	}

	assert.Equal(t, numCycles, validDownloads,
		"all %d downloads should produce Y=750, got %d valid", numCycles, validDownloads)
}

// ============================================================================
// Blend Position Sweep at 1080p
// ============================================================================

// TestGPU_P210_BlendPositionSweep1080p sweeps blend position from 0.0 to 1.0
// at 1080p between Y=64 (black) and Y=940 (white), verifying Y increases
// monotonically.
func TestGPU_P210_BlendPositionSweep1080p(t *testing.T) {
	ctx := testContext(t)

	width, height := 1920, 1080
	pool, err := NewP210FramePool(ctx, width, height, 4)
	require.NoError(t, err)
	defer pool.Close()

	// BT.709 10-bit limited: black Y=64, white Y=940
	dataA := makeP210TestFrame(width, height, 64, 512, 512)
	dataB := makeP210TestFrame(width, height, 940, 512, 512)

	frameA := uploadP210(t, ctx, pool, dataA, width, height)
	defer frameA.Release()
	frameB := uploadP210(t, ctx, pool, dataB, width, height)
	defer frameB.Release()
	frameDst, err := pool.Acquire()
	require.NoError(t, err)
	defer frameDst.Release()

	var prevY uint16
	violations := 0

	// Sweep in 256 steps (pos256 = 0 to 256)
	for step := 0; step <= 256; step++ {
		pos := float64(step) / 256.0
		err := BlendMixP210(ctx, frameDst, frameA, frameB, pos)
		require.NoError(t, err, "step=%d pos=%.4f", step, pos)

		result := make([]byte, p210DownloadSize(width, height))
		err = Download10(ctx, frameDst, result, width, height)
		require.NoError(t, err)

		gotY := readP210YSample(result, width*height/2) // sample from middle

		if step > 0 && gotY < prevY {
			violations++
			if violations <= 5 {
				t.Logf("monotonicity violation at step %d: Y=%d < prev=%d", step, gotY, prevY)
			}
		}
		prevY = gotY

		if step%64 == 0 {
			t.Logf("step %d (pos=%.3f): Y=%d", step, pos, gotY)
		}
	}

	assert.Equal(t, 0, violations,
		"Y must monotonically increase from 64 to 940 over 256 steps, got %d violations", violations)

	// Endpoints: first step should be near 64, last near 940
	assert.InDelta(t, 940, int(prevY), 2, "final step should produce Y~940")
}

// ============================================================================
// FTB Full Range
// ============================================================================

// TestGPU_P210_FTBFullRange verifies FTB across positions:
// pos=0 -> source unchanged, pos=1.0 -> black (Y=64), pos=0.5 -> midpoint.
func TestGPU_P210_FTBFullRange(t *testing.T) {
	ctx := testContext(t)

	width, height := 640, 480
	pool, err := NewP210FramePool(ctx, width, height, 3)
	require.NoError(t, err)
	defer pool.Close()

	srcData := makeP210TestFrame(width, height, 800, 300, 700)
	frameSrc := uploadP210(t, ctx, pool, srcData, width, height)
	defer frameSrc.Release()
	frameDst, err := pool.Acquire()
	require.NoError(t, err)
	defer frameDst.Release()

	result := make([]byte, p210DownloadSize(width, height))

	// pos=0.0: source unchanged
	err = BlendFTBP210(ctx, frameDst, frameSrc, 0.0)
	require.NoError(t, err)
	err = Download10(ctx, frameDst, result, width, height)
	require.NoError(t, err)
	gotY := readP210YSample(result, 0)
	assert.InDelta(t, 800, int(gotY), 2, "FTB pos=0: Y should be source (800)")

	// pos=1.0: fully black
	err = BlendFTBP210(ctx, frameDst, frameSrc, 1.0)
	require.NoError(t, err)
	err = Download10(ctx, frameDst, result, width, height)
	require.NoError(t, err)
	gotY = readP210YSample(result, 0)
	assert.InDelta(t, 64, int(gotY), 2, "FTB pos=1: Y should be black (64)")

	// pos=0.5: midpoint between 800 and 64
	err = BlendFTBP210(ctx, frameDst, frameSrc, 0.5)
	require.NoError(t, err)
	err = Download10(ctx, frameDst, result, width, height)
	require.NoError(t, err)
	gotY = readP210YSample(result, 0)
	// (800*128 + 64*128 + 128) >> 8 = 432
	assert.InDelta(t, 432, int(gotY), 2, "FTB pos=0.5: Y should be midpoint ~432")
}

// ============================================================================
// Pool Exhaustion Recovery
// ============================================================================

// TestGPU_P210_PoolExhaustionRecovery exhausts the P210 pool, verifies fallback
// allocation works, then verifies recovery after release.
func TestGPU_P210_PoolExhaustionRecovery(t *testing.T) {
	ctx := testContext(t)

	width, height := 64, 64
	poolSize := 3
	pool, err := NewP210FramePool(ctx, width, height, poolSize)
	require.NoError(t, err)
	defer pool.Close()

	// Drain pool
	held := make([]*GPUFrame, 0, poolSize+3)
	for i := 0; i < poolSize; i++ {
		frame, err := pool.Acquire()
		require.NoError(t, err, "acquire %d", i)
		held = append(held, frame)
	}

	// Over-acquire: should allocate new frames
	for i := 0; i < 3; i++ {
		frame, err := pool.Acquire()
		require.NoError(t, err, "over-acquire %d", i)
		assert.Equal(t, GPUFormatP210, frame.Format, "over-acquired frame should be P210")
		held = append(held, frame)
	}

	// All frames should be usable
	for i, frame := range held {
		data := makeP210TestFrame(width, height, 500, 512, 512)
		err := Upload10(ctx, frame, data, width, height)
		require.NoError(t, err, "upload to frame %d", i)
	}
	t.Logf("all %d held P210 frames are usable", len(held))

	// Release all
	for _, frame := range held {
		frame.Release()
	}

	// Post-recovery: pool should still work
	frame, err := pool.Acquire()
	require.NoError(t, err, "post-recovery acquire")
	assert.Equal(t, GPUFormatP210, frame.Format)
	frame.Release()
}

// ============================================================================
// Concurrent Access
// ============================================================================

// TestGPU_P210_ConcurrentAccess tests 4 goroutines each doing 25 upload/download
// cycles with P210 frames. No panics, no races, all outputs valid.
func TestGPU_P210_ConcurrentAccess(t *testing.T) {
	ctx := testContext(t)

	width, height := 64, 64
	pool, err := NewP210FramePool(ctx, width, height, 8)
	require.NoError(t, err)
	defer pool.Close()

	const numGoroutines = 4
	const cyclesPerGoroutine = 25
	errCh := make(chan error, numGoroutines*cyclesPerGoroutine)
	doneCh := make(chan int, numGoroutines)

	for g := 0; g < numGoroutines; g++ {
		go func(id int) {
			valid := 0
			yVal := uint16(100 + id*200) // different per goroutine

			for c := 0; c < cyclesPerGoroutine; c++ {
				frame, err := pool.Acquire()
				if err != nil {
					errCh <- fmt.Errorf("goroutine %d cycle %d: acquire: %w", id, c, err)
					continue
				}

				data := makeP210TestFrame(width, height, yVal, 512, 512)
				err = Upload10(ctx, frame, data, width, height)
				if err != nil {
					errCh <- fmt.Errorf("goroutine %d cycle %d: upload: %w", id, c, err)
					frame.Release()
					continue
				}

				result := make([]byte, p210DownloadSize(width, height))
				err = Download10(ctx, frame, result, width, height)
				if err != nil {
					errCh <- fmt.Errorf("goroutine %d cycle %d: download: %w", id, c, err)
					frame.Release()
					continue
				}

				gotY := readP210YSample(result, 0)
				if gotY == yVal {
					valid++
				}

				frame.Release()
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

	assert.Equal(t, numGoroutines*cyclesPerGoroutine, totalValid,
		"all concurrent P210 operations should succeed")
	assert.Empty(t, errors, "no errors expected in concurrent P210 pool access")
}
