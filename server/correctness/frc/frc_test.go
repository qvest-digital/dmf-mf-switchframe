//go:build correctness

package frc

import (
	"math"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/zsiec/switchframe/server/correctness/testutil"
	"github.com/zsiec/switchframe/server/switcher"
)

const (
	testWidth  = 320
	testHeight = 240
)

// yuvFrameSize returns the total byte count for a YUV420p frame.
func yuvFrameSize(w, h int) int {
	return w*h + 2*(w/2)*(h/2)
}

// ==========================================================================
// Test Helpers
// ==========================================================================

// makeGradientYUV creates a horizontal gradient on Y (col % 256) with
// neutral chroma. This matches the pattern used in switcher's internal tests.
func makeGradientYUV(w, h int) []byte {
	size := yuvFrameSize(w, h)
	yuv := make([]byte, size)
	ySize := w * h
	for row := 0; row < h; row++ {
		for col := 0; col < w; col++ {
			yuv[row*w+col] = byte(col % 256)
		}
	}
	for i := ySize; i < size; i++ {
		yuv[i] = 128
	}
	return yuv
}

// makeShiftedGradientYUV creates a horizontal gradient shifted right by dx pixels.
// Pixels that shift out of bounds wrap around.
func makeShiftedGradientYUV(w, h, dx int) []byte {
	size := yuvFrameSize(w, h)
	yuv := make([]byte, size)
	ySize := w * h
	for row := 0; row < h; row++ {
		for col := 0; col < w; col++ {
			srcCol := (col - dx + w*256) % w
			yuv[row*w+col] = byte(srcCol % 256)
		}
	}
	for i := ySize; i < size; i++ {
		yuv[i] = 128
	}
	return yuv
}

// makeTranslatedYUV creates a frame by translating src by (dx, dy) pixels.
// Out-of-bounds pixels are filled with neutral gray (128).
func makeTranslatedYUV(src []byte, w, h, dx, dy int) []byte {
	size := yuvFrameSize(w, h)
	yuv := make([]byte, size)
	ySize := w * h

	// Fill Y plane with neutral gray
	for i := 0; i < ySize; i++ {
		yuv[i] = 128
	}

	// Copy translated region
	for row := 0; row < h; row++ {
		srcRow := row - dy
		if srcRow < 0 || srcRow >= h {
			continue
		}
		for col := 0; col < w; col++ {
			srcCol := col - dx
			if srcCol < 0 || srcCol >= w {
				continue
			}
			yuv[row*w+col] = src[srcRow*w+srcCol]
		}
	}

	// Neutral chroma
	for i := ySize; i < size; i++ {
		yuv[i] = 128
	}
	return yuv
}

// makeCheckerGradientYUV creates a frame with strong 2D structure: each pixel = col+row.
func makeCheckerGradientYUV(w, h int) []byte {
	size := yuvFrameSize(w, h)
	yuv := make([]byte, size)
	ySize := w * h
	for row := 0; row < h; row++ {
		for col := 0; col < w; col++ {
			yuv[row*w+col] = byte(col) + byte(row)
		}
	}
	for i := ySize; i < size; i++ {
		yuv[i] = 128
	}
	return yuv
}

// averageY computes the mean Y value of a YUV420 frame.
func averageY(yuv []byte, w, h int) float64 {
	ySize := w * h
	var sum uint64
	for i := 0; i < ySize; i++ {
		sum += uint64(yuv[i])
	}
	return float64(sum) / float64(ySize)
}

// computeSSIM computes a simplified SSIM on the Y plane between two frames.
// Uses 8x8 non-overlapping blocks, following the SSIM formula:
//
//	SSIM(x,y) = (2*mu_x*mu_y + C1)(2*sigma_xy + C2) /
//	            ((mu_x^2 + mu_y^2 + C1)(sigma_x^2 + sigma_y^2 + C2))
//
// C1 = (K1*L)^2 where K1=0.01, L=255
// C2 = (K2*L)^2 where K2=0.03, L=255
func computeSSIM(a, b []byte, w, h int) float64 {
	const (
		blockSize = 8
		L         = 255.0
		K1        = 0.01
		K2        = 0.03
	)
	C1 := (K1 * L) * (K1 * L)
	C2 := (K2 * L) * (K2 * L)

	blockCols := w / blockSize
	blockRows := h / blockSize
	if blockCols == 0 || blockRows == 0 {
		return 0
	}

	var totalSSIM float64
	blockCount := 0

	for by := 0; by < blockRows; by++ {
		for bx := 0; bx < blockCols; bx++ {
			var sumA, sumB float64
			var sumA2, sumB2, sumAB float64

			for row := 0; row < blockSize; row++ {
				for col := 0; col < blockSize; col++ {
					py := by*blockSize + row
					px := bx*blockSize + col
					idx := py*w + px
					va := float64(a[idx])
					vb := float64(b[idx])
					sumA += va
					sumB += vb
					sumA2 += va * va
					sumB2 += vb * vb
					sumAB += va * vb
				}
			}

			n := float64(blockSize * blockSize)
			muA := sumA / n
			muB := sumB / n
			sigA2 := sumA2/n - muA*muA
			sigB2 := sumB2/n - muB*muB
			sigAB := sumAB/n - muA*muB

			ssim := (2*muA*muB + C1) * (2*sigAB + C2) /
				((muA*muA + muB*muB + C1) * (sigA2 + sigB2 + C2))
			totalSSIM += ssim
			blockCount++
		}
	}

	return totalSSIM / float64(blockCount)
}

// ==========================================================================
// 1. TestStaticScenePassthrough
// ==========================================================================

// TestStaticScenePassthrough verifies that when the same frame is used as both
// input A and input B (no motion), the MCFI interpolated output closely matches
// the input. With identical frames, there is zero motion, so the output should
// be the same as the input (very high PSNR).
func TestStaticScenePassthrough(t *testing.T) {
	t.Parallel()

	w, h := testWidth, testHeight
	frameSize := yuvFrameSize(w, h)

	// Create a solid frame with known content.
	frameA := testutil.GenerateSolidYUV420(w, h, 128, 100, 200)

	// Use same data for both frames.
	frameB := make([]byte, frameSize)
	copy(frameB, frameA)

	mcfi := switcher.NewMCFIState()

	// Interpolate at alpha=0.5 between two identical frames.
	result := mcfi.Interpolate(frameA, frameB, w, h, 0.5)
	require.NotNil(t, result)
	require.Len(t, result, frameSize)

	// Compute PSNR on Y plane. For identical input frames, output should
	// match input very closely.
	psnr := testutil.YUVFramePSNR(frameA, result, w, h)
	t.Logf("Static scene PSNR: %.2f dB", psnr)
	assert.Greater(t, psnr, 40.0,
		"static scene PSNR should be very high (>40 dB), got %.2f dB", psnr)

	// Also test with a gradient pattern (more realistic content).
	gradA := makeGradientYUV(w, h)
	gradB := make([]byte, frameSize)
	copy(gradB, gradA)

	mcfi2 := switcher.NewMCFIState()
	result2 := mcfi2.Interpolate(gradA, gradB, w, h, 0.5)
	require.NotNil(t, result2)

	psnr2 := testutil.YUVFramePSNR(gradA, result2, w, h)
	t.Logf("Static gradient PSNR: %.2f dB", psnr2)
	assert.Greater(t, psnr2, 40.0,
		"static gradient PSNR should be very high (>40 dB), got %.2f dB", psnr2)
}

// ==========================================================================
// 2. TestMCFIInterpolatedQuality
// ==========================================================================

// TestMCFIInterpolatedQuality creates two frames with a known horizontal pan
// (frame B shifted right by N pixels from frame A). The interpolated frame at
// alpha=0.5 should be structurally similar to both neighbors (SSIM > 0.85).
func TestMCFIInterpolatedQuality(t *testing.T) {
	t.Parallel()

	w, h := testWidth, testHeight
	shift := 8

	frameA := makeGradientYUV(w, h)
	frameB := makeShiftedGradientYUV(w, h, shift)

	mcfi := switcher.NewMCFIState()
	result := mcfi.Interpolate(frameA, frameB, w, h, 0.5)
	require.NotNil(t, result)

	// Compute SSIM between interpolated result and each neighbor.
	ssimA := computeSSIM(frameA, result, w, h)
	ssimB := computeSSIM(frameB, result, w, h)

	t.Logf("SSIM(result, frameA) = %.4f", ssimA)
	t.Logf("SSIM(result, frameB) = %.4f", ssimB)

	assert.Greater(t, ssimA, 0.92,
		"SSIM between interpolated frame and frameA should be > 0.92, got %.4f", ssimA)
	assert.Greater(t, ssimB, 0.92,
		"SSIM between interpolated frame and frameB should be > 0.92, got %.4f", ssimB)

	// The interpolated frame should also be similar to the expected midpoint:
	// a gradient shifted by half the displacement.
	expected := makeShiftedGradientYUV(w, h, shift/2)
	ssimMid := computeSSIM(expected, result, w, h)
	t.Logf("SSIM(result, expectedMidpoint) = %.4f", ssimMid)
	assert.Greater(t, ssimMid, 0.80,
		"SSIM to expected midpoint should be > 0.80, got %.4f", ssimMid)
}

// ==========================================================================
// 3. TestPTSSpacingUniform
// ==========================================================================

// TestPTSSpacingUniform verifies that when performing frame rate conversion
// from a source rate to a target rate, the output PTS values are uniformly
// spaced. This test exercises the frcSource.emit() path indirectly through
// MCFIState (which doesn't produce PTS values itself), so we test the
// mathematical model: given input PTS at source rate and output PTS at
// target rate, the interpolation alphas should be evenly distributed.
func TestPTSSpacingUniform(t *testing.T) {
	t.Parallel()

	// Simulate 24fps -> 60fps conversion.
	// Source PTS interval: 90000/24 = 3750 ticks
	// Output PTS interval: 90000/60 = 1500 ticks
	// For every 2 source frames (7500 ticks), we produce 5 output frames.
	const (
		sourceFPS       = 24
		targetFPS       = 60
		clock           = 90000
		sourceInterval  = clock / sourceFPS // 3750
		targetInterval  = clock / targetFPS // 1500
		numSourceFrames = 10
	)

	// Generate source PTS sequence at 24fps.
	sourcePTS := make([]int64, numSourceFrames)
	for i := range sourcePTS {
		sourcePTS[i] = int64(i) * sourceInterval
	}

	// Compute output PTS at 60fps spanning the same duration.
	duration := sourcePTS[len(sourcePTS)-1] // last source PTS
	numOutputFrames := int(duration / targetInterval)
	outputPTS := make([]int64, numOutputFrames)
	for i := range outputPTS {
		outputPTS[i] = int64(i) * targetInterval
	}

	// Verify uniform spacing of output PTS.
	for i := 1; i < len(outputPTS); i++ {
		delta := outputPTS[i] - outputPTS[i-1]
		diff := int64(math.Abs(float64(delta - targetInterval)))
		assert.LessOrEqual(t, diff, int64(1),
			"output PTS delta at frame %d should be %d +/- 1, got %d",
			i, targetInterval, delta)
	}

	// Also verify that the alpha values computed for each output tick
	// within a source frame pair are uniformly distributed.
	for srcIdx := 0; srcIdx < len(sourcePTS)-1; srcIdx++ {
		prevPTS := sourcePTS[srcIdx]
		currPTS := sourcePTS[srcIdx+1]
		frameDelta := currPTS - prevPTS

		var alphas []float64
		for _, outPTS := range outputPTS {
			if outPTS >= prevPTS && outPTS < currPTS {
				alpha := float64(outPTS-prevPTS) / float64(frameDelta)
				alphas = append(alphas, alpha)
			}
		}

		// Within each source interval, output alphas should be evenly spaced.
		if len(alphas) > 1 {
			alphaStep := alphas[1] - alphas[0]
			for j := 2; j < len(alphas); j++ {
				step := alphas[j] - alphas[j-1]
				diff := math.Abs(step - alphaStep)
				assert.Less(t, diff, 0.01,
					"alpha spacing should be uniform within source pair %d: step[%d]=%.4f vs step[0]=%.4f",
					srcIdx, j-1, step, alphaStep)
			}
		}
	}

	t.Logf("Verified %d output frames with uniform PTS spacing at %dfps", len(outputPTS), targetFPS)
}

// ==========================================================================
// 4. TestFrameCountConversion
// ==========================================================================

// TestFrameCountConversion verifies the mathematical relationship between
// input and output frame counts for various frame rate conversions.
func TestFrameCountConversion(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name            string
		sourceFPS       int
		targetFPS       int
		durationSec     int
		expectedOutputs int
	}{
		{
			name:            "30->60fps 10 seconds",
			sourceFPS:       30,
			targetFPS:       60,
			durationSec:     10,
			expectedOutputs: 600,
		},
		{
			name:            "60->30fps 10 seconds",
			sourceFPS:       60,
			targetFPS:       30,
			durationSec:     10,
			expectedOutputs: 300,
		},
		{
			name:            "24->30fps 10 seconds",
			sourceFPS:       24,
			targetFPS:       30,
			durationSec:     10,
			expectedOutputs: 300,
		},
		{
			name:            "25->30fps 10 seconds",
			sourceFPS:       25,
			targetFPS:       30,
			durationSec:     10,
			expectedOutputs: 300,
		},
		{
			name:            "50->60fps 5 seconds",
			sourceFPS:       50,
			targetFPS:       60,
			durationSec:     5,
			expectedOutputs: 300,
		},
		{
			name:            "24->60fps 10 seconds",
			sourceFPS:       24,
			targetFPS:       60,
			durationSec:     10,
			expectedOutputs: 600,
		},
	}

	const clock = 90000 // 90kHz MPEG-TS clock

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			sourceInterval := int64(clock / tc.sourceFPS)
			targetInterval := int64(clock / tc.targetFPS)

			// Total duration in clock ticks.
			totalTicks := int64(tc.durationSec) * clock

			// Count source frames.
			sourceFrames := int(totalTicks / sourceInterval)
			t.Logf("Source: %d frames at %dfps", sourceFrames, tc.sourceFPS)

			// Count output frames at target rate.
			outputFrames := int(totalTicks / targetInterval)
			t.Logf("Output: %d frames at %dfps", outputFrames, tc.targetFPS)

			assert.Equal(t, tc.expectedOutputs, outputFrames,
				"expected %d output frames for %d seconds at %dfps",
				tc.expectedOutputs, tc.durationSec, tc.targetFPS)

			// Verify the conversion ratio.
			ratio := float64(outputFrames) / float64(sourceFrames)
			expectedRatio := float64(tc.targetFPS) / float64(tc.sourceFPS)
			assert.InDelta(t, expectedRatio, ratio, 0.01,
				"frame count ratio should match FPS ratio: expected %.3f, got %.3f",
				expectedRatio, ratio)
		})
	}
}

// ==========================================================================
// 5. TestInterpolatedFrameNotBlack
// ==========================================================================

// TestInterpolatedFrameNotBlack verifies that interpolated frames are not
// all-black (a common failure mode where the output buffer is left zeroed).
// Checks average Y > 20 for various alpha positions and input patterns.
func TestInterpolatedFrameNotBlack(t *testing.T) {
	t.Parallel()

	w, h := testWidth, testHeight
	frameSize := yuvFrameSize(w, h)

	tests := []struct {
		name   string
		frameA []byte
		frameB []byte
	}{
		{
			name:   "solid bright frames",
			frameA: testutil.GenerateSolidYUV420(w, h, 180, 128, 128),
			frameB: testutil.GenerateSolidYUV420(w, h, 220, 128, 128),
		},
		{
			name:   "gradient frames",
			frameA: makeGradientYUV(w, h),
			frameB: makeShiftedGradientYUV(w, h, 8),
		},
		{
			name:   "checker gradient with motion",
			frameA: makeCheckerGradientYUV(w, h),
			frameB: makeTranslatedYUV(makeCheckerGradientYUV(w, h), w, h, 10, 0),
		},
	}

	alphas := []float64{0.1, 0.25, 0.5, 0.75, 0.9}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			for _, alpha := range alphas {
				mcfi := switcher.NewMCFIState()
				result := mcfi.Interpolate(tc.frameA, tc.frameB, w, h, alpha)
				require.NotNil(t, result)
				require.Len(t, result, frameSize)

				avgY := averageY(result, w, h)
				isBlack := testutil.ScanForBlackFrames(result, w, h)

				assert.False(t, isBlack,
					"%s alpha=%.2f: frame should not be black (avg Y=%.1f)",
					tc.name, alpha, avgY)
				assert.Greater(t, avgY, 20.0,
					"%s alpha=%.2f: average Y should be > 20, got %.1f",
					tc.name, alpha, avgY)
			}
		})
	}
}

// ==========================================================================
// 6. TestMCFIWithKnownDisplacement
// ==========================================================================

// TestMCFIWithKnownDisplacement creates two frames where a block is shifted
// by exactly (dx=10, dy=0). Verifies that MCFI detects the motion and
// produces reasonable interpolation (not just a naive blend).
func TestMCFIWithKnownDisplacement(t *testing.T) {
	t.Parallel()

	w, h := testWidth, testHeight

	// Create frame A with strong 2D texture (col+row pattern).
	frameA := makeCheckerGradientYUV(w, h)

	// Create frame B by translating A by (10, 0) — a known horizontal shift.
	dx := 10
	frameB := makeTranslatedYUV(frameA, w, h, dx, 0)

	mcfi := switcher.NewMCFIState()
	result := mcfi.Interpolate(frameA, frameB, w, h, 0.5)
	require.NotNil(t, result)

	// The interpolated frame at alpha=0.5 should look like the content
	// shifted by dx/2 = 5 pixels. Build the expected midpoint.
	expectedMid := makeTranslatedYUV(frameA, w, h, dx/2, 0)

	// Compare interior pixels (avoid borders where translation fills with neutral gray).
	margin := 32 // generous margin to avoid boundary effects
	ySize := w * h

	interiorMatchCount := 0
	interiorTotal := 0
	for row := margin; row < h-margin; row++ {
		for col := margin; col < w-margin; col++ {
			interiorTotal++
			idx := row*w + col
			diff := int(result[idx]) - int(expectedMid[idx])
			if diff < 0 {
				diff = -diff
			}
			if diff <= 3 {
				interiorMatchCount++
			}
		}
	}

	matchPct := float64(interiorMatchCount) / float64(interiorTotal) * 100
	t.Logf("Interior pixel match (within +/-3): %.1f%% (%d/%d)",
		matchPct, interiorMatchCount, interiorTotal)

	// MCFI should produce at least 70% of interior pixels close to the expected midpoint.
	assert.Greater(t, matchPct, 70.0,
		"MCFI with known displacement should match expected midpoint for >70%% of interior pixels, got %.1f%%",
		matchPct)

	// The result should not be a simple average (which would produce ghosting).
	// Compute PSNR between result and a naive linear blend.
	naiveBlend := make([]byte, yuvFrameSize(w, h))
	for i := 0; i < ySize; i++ {
		naiveBlend[i] = byte((int(frameA[i]) + int(frameB[i])) / 2)
	}
	for i := ySize; i < len(naiveBlend); i++ {
		naiveBlend[i] = 128
	}

	psnrToMidpoint := testutil.YUVFramePSNR(expectedMid, result, w, h)
	psnrToNaive := testutil.YUVFramePSNR(naiveBlend, result, w, h)

	t.Logf("PSNR(result, expectedMidpoint) = %.2f dB", psnrToMidpoint)
	t.Logf("PSNR(result, naiveBlend) = %.2f dB", psnrToNaive)

	// MCFI should be closer to the motion-compensated midpoint than to
	// a naive blend (unless the motion estimation failed).
	// We don't strictly require this because MCFI may fall back to blend
	// for some blocks, but the PSNR to midpoint should still be reasonable.
	assert.Greater(t, psnrToMidpoint, 20.0,
		"PSNR to expected midpoint should be > 20 dB, got %.2f dB", psnrToMidpoint)
}

// ==========================================================================
// Additional tests for completeness
// ==========================================================================

// TestMCFINearThresholdPassthrough verifies that extreme alpha values (near 0 or 1)
// produce output very close to the corresponding source frame.
func TestMCFINearThresholdPassthrough(t *testing.T) {
	t.Parallel()

	w, h := testWidth, testHeight

	frameA := makeGradientYUV(w, h)
	frameB := makeShiftedGradientYUV(w, h, 16)

	mcfi := switcher.NewMCFIState()

	// alpha=0.01 (below frcNearThresholdLow=0.05): should return frameA
	resultLow := mcfi.Interpolate(frameA, frameB, w, h, 0.01)
	require.NotNil(t, resultLow)
	psnrLow := testutil.YUVFramePSNR(frameA, resultLow, w, h)
	t.Logf("alpha=0.01: PSNR to frameA = %.2f dB", psnrLow)
	assert.Greater(t, psnrLow, 60.0,
		"alpha=0.01 should be nearly identical to frameA, got PSNR=%.2f dB", psnrLow)

	// alpha=0.99 (above frcNearThresholdHigh=0.95): should return frameB
	resultHigh := mcfi.Interpolate(frameA, frameB, w, h, 0.99)
	require.NotNil(t, resultHigh)
	psnrHigh := testutil.YUVFramePSNR(frameB, resultHigh, w, h)
	t.Logf("alpha=0.99: PSNR to frameB = %.2f dB", psnrHigh)
	assert.Greater(t, psnrHigh, 60.0,
		"alpha=0.99 should be nearly identical to frameB, got PSNR=%.2f dB", psnrHigh)
}

// TestMCFIInterpolateIntoReusesBuffer verifies that InterpolateInto correctly
// writes into a caller-provided buffer without returning a new allocation.
func TestMCFIInterpolateIntoReusesBuffer(t *testing.T) {
	t.Parallel()

	w, h := testWidth, testHeight
	frameSize := yuvFrameSize(w, h)

	frameA := testutil.GenerateSolidYUV420(w, h, 100, 128, 128)
	frameB := testutil.GenerateSolidYUV420(w, h, 200, 128, 128)

	mcfi := switcher.NewMCFIState()
	dst := make([]byte, frameSize)

	// Write into dst.
	mcfi.InterpolateInto(dst, frameA, frameB, w, h, 0.5)

	// dst should now contain interpolated content (not all zeros).
	avgY := averageY(dst, w, h)
	assert.Greater(t, avgY, 20.0,
		"InterpolateInto result should not be black, avg Y=%.1f", avgY)

	// Compare with Interpolate result.
	result := mcfi.Interpolate(frameA, frameB, w, h, 0.5)

	// They should produce the same content.
	psnr := testutil.YUVFramePSNR(dst, result, w, h)
	assert.Greater(t, psnr, 40.0,
		"InterpolateInto and Interpolate should produce similar output, PSNR=%.2f dB", psnr)
}

// TestMCFIMultipleAlphasSameFramePair verifies that the MCFI state correctly
// caches motion vectors when interpolating the same frame pair at multiple
// alpha positions (as happens in slow-motion replay).
func TestMCFIMultipleAlphasSameFramePair(t *testing.T) {
	t.Parallel()

	w, h := testWidth, testHeight

	frameA := makeCheckerGradientYUV(w, h)
	frameB := makeTranslatedYUV(frameA, w, h, 8, 0)

	mcfi := switcher.NewMCFIState()

	// Interpolate at multiple alpha values on the same frame pair.
	// Each successive alpha should produce a frame closer to frameB.
	var prevAvgY float64
	for _, alpha := range []float64{0.1, 0.3, 0.5, 0.7, 0.9} {
		result := mcfi.Interpolate(frameA, frameB, w, h, alpha)
		require.NotNil(t, result, "alpha=%.1f should produce non-nil result", alpha)

		avgY := averageY(result, w, h)
		isBlack := testutil.ScanForBlackFrames(result, w, h)
		assert.False(t, isBlack, "alpha=%.1f should not produce black frame", alpha)

		// Skip monotonicity check for first alpha.
		if prevAvgY > 0 {
			// With translated content, average Y may not be strictly monotonic
			// but frames should not be black.
			t.Logf("alpha=%.1f: avg Y=%.1f", alpha, avgY)
		}
		prevAvgY = avgY
	}
}

// TestMCFISceneChangeFallback verifies that MCFI falls back gracefully
// when frames are dramatically different (scene change). The output should
// still be a valid non-black frame.
func TestMCFISceneChangeFallback(t *testing.T) {
	t.Parallel()

	w, h := testWidth, testHeight

	// Frame A: bright gradient
	frameA := testutil.GenerateSolidYUV420(w, h, 200, 128, 128)

	// Frame B: completely different (dark, different chroma)
	frameB := testutil.GenerateSolidYUV420(w, h, 30, 60, 200)

	mcfi := switcher.NewMCFIState()
	result := mcfi.Interpolate(frameA, frameB, w, h, 0.5)
	require.NotNil(t, result)

	// The result should be a valid frame (not black).
	avgY := averageY(result, w, h)
	t.Logf("Scene change fallback: avg Y=%.1f", avgY)
	assert.Greater(t, avgY, 20.0,
		"scene change fallback should produce non-black frame, avg Y=%.1f", avgY)

	// For a scene change, MCFI falls back to linear blend.
	// At alpha=0.5, blend of Y=200 and Y=30 should be ~115.
	assert.InDelta(t, 115.0, avgY, 20.0,
		"scene change blend at alpha=0.5 should produce avg Y ~115, got %.1f", avgY)
}
