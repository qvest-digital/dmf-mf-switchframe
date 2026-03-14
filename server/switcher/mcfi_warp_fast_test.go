package switcher

import (
	"fmt"
	"math"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestFastWarp_MatchesReference verifies that mcfiInterpolateFast produces output
// within +/-1 LSB of the float64 reference (mcfiInterpolateSmooth) at 320x240.
func TestFastWarp_MatchesReference(t *testing.T) {
	w, h := 320, 240
	bs := 16

	f0 := makeGradientFrame(w, h)
	f1 := makeShiftedFrame(w, h, 8, 0)

	mvf := newMotionVectorField(w, h, bs)
	n := mvf.cols * mvf.rows
	for i := 0; i < n; i++ {
		mvf.fwdX[i] = 8
		mvf.fwdY[i] = 0
		mvf.bwdX[i] = -8
		mvf.bwdY[i] = 0
	}

	frameSize := w * h * 3 / 2

	// Run reference (float64) implementation
	refDst := make([]byte, frameSize)
	mcfiInterpolateSmooth(refDst, f0.YUV, f1.YUV, w, h, mvf, 0.5)

	// Run fast (fixed-point) implementation
	fastDst := make([]byte, frameSize)
	mcfiInterpolateFast(fastDst, f0.YUV, f1.YUV, w, h, mvf, 0.5)

	// Compare all pixels: allow +/-1 LSB difference
	maxDiff := 0
	totalDiff := 0
	diffCount := 0
	for i := 0; i < frameSize; i++ {
		diff := int(fastDst[i]) - int(refDst[i])
		if diff < 0 {
			diff = -diff
		}
		if diff > maxDiff {
			maxDiff = diff
		}
		if diff > 0 {
			diffCount++
			totalDiff += diff
		}
	}

	require.LessOrEqual(t, maxDiff, 1,
		"max pixel difference should be <= 1 LSB, got %d (diffCount=%d, avgDiff=%.2f)",
		maxDiff, diffCount, float64(totalDiff)/math.Max(1, float64(diffCount)))
}

// TestFastWarp_MatchesReference_DiagonalMotion tests with diagonal MVs and
// various alpha values to exercise the full bilinear interpolation path.
func TestFastWarp_MatchesReference_DiagonalMotion(t *testing.T) {
	w, h := 320, 240
	bs := 16

	f0 := makeCheckerGradientFrame(w, h)
	f1 := makeTranslatedFrame(f0, 6, 4)

	mvf := newMotionVectorField(w, h, bs)
	n := mvf.cols * mvf.rows
	for i := 0; i < n; i++ {
		mvf.fwdX[i] = 6
		mvf.fwdY[i] = 4
		mvf.bwdX[i] = -6
		mvf.bwdY[i] = -4
	}

	frameSize := w * h * 3 / 2

	for _, alpha := range []float64{0.1, 0.25, 0.5, 0.75, 0.9} {
		t.Run("alpha="+formatAlpha(alpha), func(t *testing.T) {
			refDst := make([]byte, frameSize)
			mcfiInterpolateSmooth(refDst, f0.YUV, f1.YUV, w, h, mvf, alpha)

			fastDst := make([]byte, frameSize)
			mcfiInterpolateFast(fastDst, f0.YUV, f1.YUV, w, h, mvf, alpha)

			maxDiff := 0
			for i := 0; i < frameSize; i++ {
				diff := int(fastDst[i]) - int(refDst[i])
				if diff < 0 {
					diff = -diff
				}
				if diff > maxDiff {
					maxDiff = diff
				}
			}

			require.LessOrEqual(t, maxDiff, 1,
				"max pixel difference should be <= 1 LSB at alpha=%v, got %d", alpha, maxDiff)
		})
	}
}

// TestFastWarp_AlphaExtremes verifies that alpha=0 returns approximately srcA
// and alpha=1 returns approximately srcB.
func TestFastWarp_AlphaExtremes(t *testing.T) {
	w, h := 320, 240
	bs := 16

	f0 := makeUniformFrame(w, h, 80)
	f1 := makeUniformFrame(w, h, 200)

	mvf := newMotionVectorField(w, h, bs)
	// Zero MVs: no motion
	frameSize := w * h * 3 / 2

	t.Run("alpha=0", func(t *testing.T) {
		dst := make([]byte, frameSize)
		mcfiInterpolateFast(dst, f0.YUV, f1.YUV, w, h, mvf, 0.0)

		// With zero MVs and alpha=0:
		// sA = srcA[px, py] (no displacement), sB = srcB[px, py] (no displacement)
		// dst = sA * (1-0) + sB * 0 = sA = 80
		ySize := w * h
		for i := 0; i < ySize; i++ {
			diff := int(dst[i]) - 80
			if diff < 0 {
				diff = -diff
			}
			require.LessOrEqual(t, diff, 1,
				"alpha=0 Y pixel %d: expected ~80, got %d", i, dst[i])
		}
	})

	t.Run("alpha=1", func(t *testing.T) {
		dst := make([]byte, frameSize)
		mcfiInterpolateFast(dst, f0.YUV, f1.YUV, w, h, mvf, 1.0)

		// With zero MVs and alpha=1:
		// sA = srcA[px, py], sB = srcB[px, py]
		// dst = sA * 0 + sB * 1 = sB = 200
		ySize := w * h
		for i := 0; i < ySize; i++ {
			diff := int(dst[i]) - 200
			if diff < 0 {
				diff = -diff
			}
			require.LessOrEqual(t, diff, 1,
				"alpha=1 Y pixel %d: expected ~200, got %d", i, dst[i])
		}
	})
}

// TestFastWarp_RowParallel verifies that serial (maxGoroutines=1) produces
// identical output to parallel (maxGoroutines=0 i.e. runtime.NumCPU()).
func TestFastWarp_RowParallel(t *testing.T) {
	w, h := 320, 240
	bs := 16

	f0 := makeGradientFrame(w, h)
	f1 := makeShiftedFrame(w, h, 8, 0)

	mvf := newMotionVectorField(w, h, bs)
	n := mvf.cols * mvf.rows
	for i := 0; i < n; i++ {
		mvf.fwdX[i] = 8
		mvf.fwdY[i] = 2
		mvf.bwdX[i] = -8
		mvf.bwdY[i] = -2
	}

	frameSize := w * h * 3 / 2
	alpha := 0.4

	// Serial: Y plane with maxGoroutines=1
	serialDst := make([]byte, frameSize)
	fastWarpPlane(
		serialDst[:w*h], f0.YUV[:w*h], f1.YUV[:w*h],
		w, h, mvf, alpha, 1, 1,
	)

	// Parallel: Y plane with maxGoroutines=0 (uses NumCPU)
	parallelDst := make([]byte, frameSize)
	fastWarpPlane(
		parallelDst[:w*h], f0.YUV[:w*h], f1.YUV[:w*h],
		w, h, mvf, alpha, 1, 0,
	)

	// Must be bit-exact (same arithmetic, just different block-row split)
	require.Equal(t, serialDst[:w*h], parallelDst[:w*h],
		"serial and parallel Y plane output must be identical")
}

// TestFastWarp_WithMotion verifies that an 8px horizontal shift produces
// displaced pixels in the output.
func TestFastWarp_WithMotion(t *testing.T) {
	w, h := 320, 240
	bs := 16
	shift := 8

	f0 := makeGradientFrame(w, h)
	f1 := makeShiftedFrame(w, h, shift, 0)

	mvf := newMotionVectorField(w, h, bs)
	n := mvf.cols * mvf.rows
	for i := 0; i < n; i++ {
		mvf.fwdX[i] = int16(shift)
		mvf.fwdY[i] = 0
		mvf.bwdX[i] = int16(-shift)
		mvf.bwdY[i] = 0
	}

	frameSize := w * h * 3 / 2

	// At alpha=0.5, gather warp samples each source at half-displacement:
	// sA = srcA[px - alpha*fwd_mv] = srcA[px - 4] (sample backward from motion)
	// sB = srcB[px - invAlpha*bwd_mv] = srcB[px + 4] (sample forward from motion)
	// Both yield content at the interpolated position, blended at 50/50.
	dst := make([]byte, frameSize)
	mcfiInterpolateFast(dst, f0.YUV, f1.YUV, w, h, mvf, 0.5)

	// Expected midpoint frame: f0 shifted right by 4
	expected := makeShiftedFrame(w, h, shift/2, 0)

	// Compare interior pixels
	margin := 2 * bs
	mismatches := 0
	total := 0
	for row := margin; row < h-margin; row++ {
		for col := margin; col < w-margin; col++ {
			total++
			diff := int(dst[row*w+col]) - int(expected.YUV[row*w+col])
			if diff < 0 {
				diff = -diff
			}
			if diff > 2 {
				mismatches++
			}
		}
	}
	require.Less(t, float64(mismatches)/float64(total), 0.10,
		"fast warp midpoint should show content at half-shift offset (%.1f%% mismatch)",
		float64(mismatches)*100/float64(total))
}

// TestFastWarp_BoundsClipping verifies no panics with extreme MVs.
func TestFastWarp_BoundsClipping(t *testing.T) {
	w, h := 320, 240
	bs := 16

	f0 := makeGradientFrame(w, h)
	f1 := makeGradientFrame(w, h)

	mvf := newMotionVectorField(w, h, bs)
	n := mvf.cols * mvf.rows
	for i := 0; i < n; i++ {
		mvf.fwdX[i] = 200
		mvf.fwdY[i] = 200
		mvf.bwdX[i] = -200
		mvf.bwdY[i] = -200
	}

	frameSize := w * h * 3 / 2
	dst := make([]byte, frameSize)

	require.NotPanics(t, func() {
		mcfiInterpolateFast(dst, f0.YUV, f1.YUV, w, h, mvf, 0.5)
	}, "mcfiInterpolateFast should not panic with out-of-bounds MVs")
}

// TestFastWarp_NegativeMV verifies negative MVs work correctly.
func TestFastWarp_NegativeMV(t *testing.T) {
	w, h := 320, 240
	bs := 16

	f0 := makeGradientFrame(w, h)
	f1 := makeGradientFrame(w, h)

	mvf := newMotionVectorField(w, h, bs)
	n := mvf.cols * mvf.rows
	for i := 0; i < n; i++ {
		mvf.fwdX[i] = -6
		mvf.fwdY[i] = -3
		mvf.bwdX[i] = 6
		mvf.bwdY[i] = 3
	}

	frameSize := w * h * 3 / 2

	// Compare with reference
	refDst := make([]byte, frameSize)
	mcfiInterpolateSmooth(refDst, f0.YUV, f1.YUV, w, h, mvf, 0.5)

	fastDst := make([]byte, frameSize)
	mcfiInterpolateFast(fastDst, f0.YUV, f1.YUV, w, h, mvf, 0.5)

	maxDiff := 0
	for i := 0; i < frameSize; i++ {
		diff := int(fastDst[i]) - int(refDst[i])
		if diff < 0 {
			diff = -diff
		}
		if diff > maxDiff {
			maxDiff = diff
		}
	}
	require.LessOrEqual(t, maxDiff, 1,
		"negative MV: max diff should be <= 1 LSB, got %d", maxDiff)
}

// TestFastWarp_StepFunction validates warp displacement direction using a
// non-linear pattern (sharp edge). Unlike the gradient test, a step function
// distinguishes between correct (−) and incorrect (+) displacement signs.
//
// Setup: bright region (Y=200) at cols 50-69 in f0, shifted right by 16px in f1
// (cols 66-85). At alpha=0.5, the bright region should appear at cols 58-77.
//
// With WRONG + sign: the warp samples AWAY from the object, producing
// ghosting (intermediate values where sharp edges should be).
// With CORRECT − sign: the warp samples FROM the object, producing
// clean edges at the interpolated position.
func TestFastWarp_StepFunction(t *testing.T) {
	w, h := 320, 240
	bs := 16
	shift := 16

	// f0: bright band at cols 50-69
	f0 := makeStepFrame(w, h, 50, 70, 200, 20)
	// f1: bright band shifted right by 16 to cols 66-85
	f1 := makeStepFrame(w, h, 50+shift, 70+shift, 200, 20)

	mvf := newMotionVectorField(w, h, bs)
	n := mvf.cols * mvf.rows
	for i := 0; i < n; i++ {
		mvf.fwdX[i] = int16(shift)
		mvf.fwdY[i] = 0
		mvf.bwdX[i] = int16(-shift)
		mvf.bwdY[i] = 0
	}

	frameSize := w * h * 3 / 2
	dst := make([]byte, frameSize)
	mcfiInterpolateFast(dst, f0.YUV, f1.YUV, w, h, mvf, 0.5)

	// At alpha=0.5, bright band should be at cols 58-77 (midpoint).
	// Check interior of the bright band (cols 60-75, away from edges).
	// With correct sign: pixels should be ~200 (bright).
	// With wrong sign: pixels would be ~110 (ghosted blend of bright+dark).
	margin := 2 * bs // avoid block boundary interpolation artifacts
	brightCorrect := 0
	brightTotal := 0
	for row := margin; row < h-margin; row++ {
		for col := 62; col <= 73; col++ { // well inside the interpolated bright band
			brightTotal++
			val := dst[row*w+col]
			if val > 150 { // clearly bright, not a ghost blend
				brightCorrect++
			}
		}
	}
	pctCorrect := float64(brightCorrect) / float64(brightTotal)
	t.Logf("bright band interior: %.1f%% pixels > 150 (%d/%d)",
		pctCorrect*100, brightCorrect, brightTotal)
	require.Greater(t, pctCorrect, 0.90,
		"interior of interpolated bright band should be predominantly bright (>150), "+
			"not ghosted (~110); got %.1f%% correct", pctCorrect*100)

	// Also check the dark region well outside the band (cols 0-40).
	// Should remain dark regardless of sign, but wrong sign creates ghost trails.
	darkCorrect := 0
	darkTotal := 0
	for row := margin; row < h-margin; row++ {
		for col := 10; col <= 40; col++ {
			darkTotal++
			val := dst[row*w+col]
			if val < 50 { // clearly dark
				darkCorrect++
			}
		}
	}
	pctDark := float64(darkCorrect) / float64(darkTotal)
	t.Logf("dark region: %.1f%% pixels < 50 (%d/%d)",
		pctDark*100, darkCorrect, darkTotal)
	require.Greater(t, pctDark, 0.90,
		"dark region should stay dark; got %.1f%% correct", pctDark*100)
}

// makeStepFrame creates a frame with a bright vertical band: Y=brightVal for
// cols in [startCol, endCol), Y=darkVal elsewhere. Cb/Cr are neutral (128).
func makeStepFrame(w, h, startCol, endCol, brightVal, darkVal int) *ProcessingFrame {
	ySize := w * h
	cbSize := (w / 2) * (h / 2)
	crSize := cbSize
	yuv := make([]byte, ySize+cbSize+crSize)

	for row := 0; row < h; row++ {
		for col := 0; col < w; col++ {
			if col >= startCol && col < endCol {
				yuv[row*w+col] = byte(brightVal)
			} else {
				yuv[row*w+col] = byte(darkVal)
			}
		}
	}
	for i := ySize; i < len(yuv); i++ {
		yuv[i] = 128
	}
	return &ProcessingFrame{YUV: yuv, Width: w, Height: h}
}

// BenchmarkFastWarp_1080p benchmarks the fast fixed-point warp at 1080p.
// Target: <10ms per frame.
func BenchmarkFastWarp_1080p(b *testing.B) {
	w, h := 1920, 1072 // block-aligned
	f0 := makeGradientFrame(w, h)
	f1 := makeShiftedFrame(w, h, 8, 0)

	mvf := newMotionVectorField(w, h, 16)
	n := mvf.cols * mvf.rows
	for i := 0; i < n; i++ {
		mvf.fwdX[i] = 8
		mvf.fwdY[i] = 0
		mvf.bwdX[i] = -8
		mvf.bwdY[i] = 0
	}

	frameSize := w * h * 3 / 2
	dst := make([]byte, frameSize)

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		mcfiInterpolateFast(dst, f0.YUV, f1.YUV, w, h, mvf, 0.5)
	}
}

// BenchmarkSmoothWarp_1080p_Reference benchmarks the float64 reference warp at 1080p.
// This is the baseline (~87ms) that the fast warp replaces.
func BenchmarkSmoothWarp_1080p_Reference(b *testing.B) {
	w, h := 1920, 1072 // block-aligned
	f0 := makeGradientFrame(w, h)
	f1 := makeShiftedFrame(w, h, 8, 0)

	mvf := newMotionVectorField(w, h, 16)
	n := mvf.cols * mvf.rows
	for i := 0; i < n; i++ {
		mvf.fwdX[i] = 8
		mvf.fwdY[i] = 0
		mvf.bwdX[i] = -8
		mvf.bwdY[i] = 0
	}

	frameSize := w * h * 3 / 2
	dst := make([]byte, frameSize)

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		mcfiInterpolateSmooth(dst, f0.YUV, f1.YUV, w, h, mvf, 0.5)
	}
}

// BenchmarkMCFI_EndToEnd_1080p benchmarks the complete MCFI pipeline at 1080p:
// hierarchical ME + median filter + consistency check + fast warp.
// This is the total per-frame cost for motion-compensated frame interpolation.
func BenchmarkMCFI_EndToEnd_1080p(b *testing.B) {
	w, h := 1920, 1072 // block-aligned
	f0 := makeGradientFrame(w, h)
	f1 := makeShiftedFrame(w, h, 8, 0)

	state := NewMCFIState()
	// Prime the state so buffers are allocated
	state.Interpolate(f0.YUV, f1.YUV, w, h, 0.5)

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		// Force new frame pair each iteration to include ME cost
		state.mvValid = false
		state.lastPtrA = nil
		state.Interpolate(f0.YUV, f1.YUV, w, h, 0.5)
	}
}

// BenchmarkMCFI_WarpOnly_1080p benchmarks just the warp phase when MVs are cached.
// This is the per-frame cost when multiple frames share the same MV field (slow-mo).
func BenchmarkMCFI_WarpOnly_1080p(b *testing.B) {
	w, h := 1920, 1072 // block-aligned
	f0 := makeGradientFrame(w, h)
	f1 := makeShiftedFrame(w, h, 8, 0)

	state := NewMCFIState()
	// Run once to compute MVs
	state.Interpolate(f0.YUV, f1.YUV, w, h, 0.5)

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		// Same frame pair = MV cache hit, only warp runs
		state.Interpolate(f0.YUV, f1.YUV, w, h, float64(i%10)/10.0)
	}
}

func formatAlpha(a float64) string {
	return fmt.Sprintf("%.2f", a)
}
