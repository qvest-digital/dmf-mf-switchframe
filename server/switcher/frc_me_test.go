package switcher

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// makeGradientFrame creates a ProcessingFrame with a horizontal gradient pattern.
// Y plane: y[row*w + col] = byte(col % 256)
// Cb/Cr planes: filled with 128 (neutral chroma).
func makeGradientFrame(w, h int) *ProcessingFrame {
	ySize := w * h
	cbSize := (w / 2) * (h / 2)
	crSize := cbSize
	yuv := make([]byte, ySize+cbSize+crSize)

	// Y plane: horizontal gradient
	for row := 0; row < h; row++ {
		for col := 0; col < w; col++ {
			yuv[row*w+col] = byte(col % 256)
		}
	}

	// Cb/Cr: neutral
	for i := ySize; i < len(yuv); i++ {
		yuv[i] = 128
	}

	return &ProcessingFrame{
		YUV:    yuv,
		Width:  w,
		Height: h,
	}
}

// makeShiftedFrame creates a frame with the same gradient pattern shifted by (dx, dy).
// Pixels that shift out of bounds wrap around.
func makeShiftedFrame(w, h, dx, dy int) *ProcessingFrame {
	ySize := w * h
	cbSize := (w / 2) * (h / 2)
	crSize := cbSize
	yuv := make([]byte, ySize+cbSize+crSize)

	// Y plane: shifted gradient
	for row := 0; row < h; row++ {
		for col := 0; col < w; col++ {
			srcCol := (col - dx + w*256) % w
			srcRow := (row - dy + h*256) % h
			_ = srcRow // gradient only varies by column
			yuv[row*w+col] = byte(srcCol % 256)
		}
	}

	// Cb/Cr: neutral
	for i := ySize; i < len(yuv); i++ {
		yuv[i] = 128
	}

	return &ProcessingFrame{
		YUV:    yuv,
		Width:  w,
		Height: h,
	}
}

// makeVerticalGradientFrame creates a frame with a vertical gradient for vertical motion tests.
// Y plane: y[row*w + col] = byte(row % 256)
func makeVerticalGradientFrame(w, h int) *ProcessingFrame {
	ySize := w * h
	cbSize := (w / 2) * (h / 2)
	crSize := cbSize
	yuv := make([]byte, ySize+cbSize+crSize)

	for row := 0; row < h; row++ {
		for col := 0; col < w; col++ {
			yuv[row*w+col] = byte(row % 256)
		}
	}

	for i := ySize; i < len(yuv); i++ {
		yuv[i] = 128
	}

	return &ProcessingFrame{
		YUV:    yuv,
		Width:  w,
		Height: h,
	}
}

// makeVerticalShiftedFrame creates a vertically-shifted frame.
func makeVerticalShiftedFrame(w, h, dy int) *ProcessingFrame {
	ySize := w * h
	cbSize := (w / 2) * (h / 2)
	crSize := cbSize
	yuv := make([]byte, ySize+cbSize+crSize)

	for row := 0; row < h; row++ {
		for col := 0; col < w; col++ {
			srcRow := (row - dy + h*256) % h
			yuv[row*w+col] = byte(srcRow % 256)
		}
	}

	for i := ySize; i < len(yuv); i++ {
		yuv[i] = 128
	}

	return &ProcessingFrame{
		YUV:    yuv,
		Width:  w,
		Height: h,
	}
}

// makeCheckerGradientFrame creates a frame with strong 2D structure by combining
// horizontal and vertical gradients. Each pixel = col%256 + row%256 (clamped to byte).
// The additive combination ensures unique block content in both dimensions.
// Uses makeTranslatedFrame for shifted versions to avoid modular wrapping.
func makeCheckerGradientFrame(w, h int) *ProcessingFrame {
	ySize := w * h
	cbSize := (w / 2) * (h / 2)
	crSize := cbSize
	yuv := make([]byte, ySize+cbSize+crSize)

	for row := 0; row < h; row++ {
		for col := 0; col < w; col++ {
			// Each row has a different offset, creating vertical structure.
			// byte() naturally wraps, but with makeTranslatedFrame (no wrapping)
			// the only SAD=0 match is at the correct displacement.
			yuv[row*w+col] = byte(col) + byte(row)
		}
	}

	for i := ySize; i < len(yuv); i++ {
		yuv[i] = 128
	}

	return &ProcessingFrame{
		YUV:    yuv,
		Width:  w,
		Height: h,
	}
}

// makeTranslatedFrame creates curr by copying prev's Y plane with a pixel translation of (dx, dy).
// Pixels that would come from outside prev are filled with a neutral value (128).
// This avoids wrapping artifacts and ensures the only perfect SAD match is at (dx, dy).
func makeTranslatedFrame(prev *ProcessingFrame, dx, dy int) *ProcessingFrame {
	w := prev.Width
	h := prev.Height
	ySize := w * h
	cbSize := (w / 2) * (h / 2)
	crSize := cbSize
	yuv := make([]byte, ySize+cbSize+crSize)

	prevY := prev.YUV[:ySize]

	// Fill with neutral gray (distinct from most gradient values)
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
			yuv[row*w+col] = prevY[srcRow*w+srcCol]
		}
	}

	// Cb/Cr: neutral
	for i := ySize; i < len(yuv); i++ {
		yuv[i] = 128
	}

	return &ProcessingFrame{
		YUV:    yuv,
		Width:  w,
		Height: h,
	}
}

func makeUniformFrame(w, h int, yVal byte) *ProcessingFrame {
	ySize := w * h
	cbSize := (w / 2) * (h / 2)
	crSize := cbSize
	yuv := make([]byte, ySize+cbSize+crSize)

	for i := 0; i < ySize; i++ {
		yuv[i] = yVal
	}
	for i := ySize; i < len(yuv); i++ {
		yuv[i] = 128
	}

	return &ProcessingFrame{
		YUV:    yuv,
		Width:  w,
		Height: h,
	}
}

func TestDiamondSearch_ZeroMotion(t *testing.T) {
	w, h := 320, 240
	frame := makeGradientFrame(w, h)

	mvf := newMotionVectorField(w, h, 16)
	estimateMotionField(frame, frame, mvf, 32)

	for row := 0; row < mvf.rows; row++ {
		for col := 0; col < mvf.cols; col++ {
			idx := row*mvf.cols + col
			require.Equal(t, int16(0), mvf.fwdX[idx], "fwdX at (%d,%d)", col, row)
			require.Equal(t, int16(0), mvf.fwdY[idx], "fwdY at (%d,%d)", col, row)
			require.Equal(t, uint32(0), mvf.fwdSAD[idx], "fwdSAD at (%d,%d)", col, row)
			require.Equal(t, int16(0), mvf.bwdX[idx], "bwdX at (%d,%d)", col, row)
			require.Equal(t, int16(0), mvf.bwdY[idx], "bwdY at (%d,%d)", col, row)
			require.Equal(t, uint32(0), mvf.bwdSAD[idx], "bwdSAD at (%d,%d)", col, row)
		}
	}
}

func TestDiamondSearch_UniformPan(t *testing.T) {
	w, h := 320, 240
	shift := 8

	prev := makeGradientFrame(w, h)
	curr := makeShiftedFrame(w, h, shift, 0)

	mvf := newMotionVectorField(w, h, 16)
	estimateMotionField(prev, curr, mvf, 32)

	// Interior blocks should find MV = (shift, 0).
	// Edge blocks may be affected by wrapping artifacts, so skip first/last column.
	for row := 0; row < mvf.rows; row++ {
		for col := 1; col < mvf.cols-1; col++ {
			idx := row*mvf.cols + col
			require.Equal(t, int16(shift), mvf.fwdX[idx],
				"fwdX at (%d,%d)", col, row)
			require.Equal(t, int16(0), mvf.fwdY[idx],
				"fwdY at (%d,%d)", col, row)
		}
	}
}

func TestDiamondSearch_VerticalPan(t *testing.T) {
	w, h := 320, 240
	shift := 4

	prev := makeVerticalGradientFrame(w, h)
	curr := makeVerticalShiftedFrame(w, h, shift)

	mvf := newMotionVectorField(w, h, 16)
	estimateMotionField(prev, curr, mvf, 32)

	// Interior blocks should find MV = (0, shift).
	// Skip first/last row due to wrapping artifacts.
	for row := 1; row < mvf.rows-1; row++ {
		for col := 0; col < mvf.cols; col++ {
			idx := row*mvf.cols + col
			require.Equal(t, int16(0), mvf.fwdX[idx],
				"fwdX at (%d,%d)", col, row)
			require.Equal(t, int16(shift), mvf.fwdY[idx],
				"fwdY at (%d,%d)", col, row)
		}
	}
}

func TestDiamondSearch_DiagonalMotion(t *testing.T) {
	w, h := 640, 480
	dx, dy := 4, 4

	// Create prev with a pattern that has strong structure in both X and Y.
	// Use half+half gradient: upper nibble from col, lower nibble from row.
	prev := makeCheckerGradientFrame(w, h)
	curr := makeTranslatedFrame(prev, dx, dy)

	mvf := newMotionVectorField(w, h, 16)
	estimateMotionField(prev, curr, mvf, 32)

	// Interior blocks should find MV = (dx, dy).
	// Skip generous border to avoid translation boundary (filled with neutral 128).
	margin := 3 // blocks
	for row := margin; row < mvf.rows-margin; row++ {
		for col := margin; col < mvf.cols-margin; col++ {
			idx := row*mvf.cols + col
			require.Equal(t, int16(dx), mvf.fwdX[idx],
				"fwdX at (%d,%d)", col, row)
			require.Equal(t, int16(dy), mvf.fwdY[idx],
				"fwdY at (%d,%d)", col, row)
		}
	}
}

func TestDiamondSearch_TwoRegions(t *testing.T) {
	w, h := 320, 240
	bs := 16

	// Create prev: horizontal gradient
	prev := makeGradientFrame(w, h)

	// Create curr: left half shifted right by 8, right half shifted left by 8
	ySize := w * h
	cbSize := (w / 2) * (h / 2)
	crSize := cbSize
	currYUV := make([]byte, ySize+cbSize+crSize)
	halfW := w / 2

	prevY := prev.YUV[:ySize]
	for row := 0; row < h; row++ {
		for col := 0; col < w; col++ {
			var srcCol int
			if col < halfW {
				// Left half: content shifted right by 8 (source is col-8)
				srcCol = col - 8
			} else {
				// Right half: content shifted left by 8 (source is col+8)
				srcCol = col + 8
			}
			if srcCol >= 0 && srcCol < w {
				currYUV[row*w+col] = prevY[row*w+srcCol]
			} else {
				currYUV[row*w+col] = 0
			}
		}
	}
	for i := ySize; i < len(currYUV); i++ {
		currYUV[i] = 128
	}

	curr := &ProcessingFrame{YUV: currYUV, Width: w, Height: h}
	mvf := newMotionVectorField(w, h, bs)
	estimateMotionField(prev, curr, mvf, 32)

	// Left interior blocks should have MV ~(8, 0)
	leftCols := halfW / bs
	for row := 0; row < mvf.rows; row++ {
		for col := 1; col < leftCols-1; col++ {
			idx := row*mvf.cols + col
			require.Equal(t, int16(8), mvf.fwdX[idx],
				"left region fwdX at (%d,%d)", col, row)
			require.Equal(t, int16(0), mvf.fwdY[idx],
				"left region fwdY at (%d,%d)", col, row)
		}
	}

	// Right interior blocks should have MV ~(-8, 0)
	for row := 0; row < mvf.rows; row++ {
		for col := leftCols + 1; col < mvf.cols-1; col++ {
			idx := row*mvf.cols + col
			require.Equal(t, int16(-8), mvf.fwdX[idx],
				"right region fwdX at (%d,%d)", col, row)
			require.Equal(t, int16(0), mvf.fwdY[idx],
				"right region fwdY at (%d,%d)", col, row)
		}
	}
}

func TestDiamondSearch_BoundaryClipping(t *testing.T) {
	w, h := 320, 240
	bs := 16

	// Create a frame with distinct content near the bottom-right corner
	prev := makeGradientFrame(w, h)

	// Create curr by shifting right by 20 pixels. Bottom-right corner blocks
	// can't match fully because the reference block would extend beyond frame.
	curr := makeShiftedFrame(w, h, 20, 0)

	mvf := newMotionVectorField(w, h, bs)
	estimateMotionField(prev, curr, mvf, 32)

	// The last column of blocks starts at x = (cols-1)*16 = 304.
	// With MV=20, reference block would start at 324 which exceeds width 320.
	// Search should clip and find best available match.
	lastCol := mvf.cols - 1
	for row := 0; row < mvf.rows; row++ {
		idx := row*mvf.cols + lastCol
		// The motion vector for the boundary block should be within valid range
		refX := lastCol*bs + int(mvf.fwdX[idx])
		require.GreaterOrEqual(t, refX, 0, "refX should be >= 0")
		require.LessOrEqual(t, refX+bs, w, "refX+bs should be <= width")
	}
}

func TestDiamondSearch_LargeMotion(t *testing.T) {
	w, h := 640, 480
	shift := 30 // Near search range limit of 32

	prev := makeGradientFrame(w, h)
	curr := makeTranslatedFrame(prev, shift, 0)

	mvf := newMotionVectorField(w, h, 16)
	estimateMotionField(prev, curr, mvf, 32)

	// Interior blocks should find MV = (30, 0).
	// Skip generous border: shift=30 means blocks near edges have fill content.
	for row := 2; row < mvf.rows-2; row++ {
		for col := 3; col < mvf.cols-3; col++ {
			idx := row*mvf.cols + col
			require.Equal(t, int16(shift), mvf.fwdX[idx],
				"fwdX at (%d,%d)", col, row)
			require.Equal(t, int16(0), mvf.fwdY[idx],
				"fwdY at (%d,%d)", col, row)
		}
	}
}

func TestMedianFilterMV_SmoothsOutlier(t *testing.T) {
	w, h := 160, 160
	bs := 16
	mvf := newMotionVectorField(w, h, bs)

	// Fill with uniform (4, 0) vectors
	n := mvf.cols * mvf.rows
	for i := 0; i < n; i++ {
		mvf.fwdX[i] = 4
		mvf.fwdY[i] = 0
		mvf.bwdX[i] = -4
		mvf.bwdY[i] = 0
	}

	// Place an outlier at center
	centerRow := mvf.rows / 2
	centerCol := mvf.cols / 2
	centerIdx := centerRow*mvf.cols + centerCol
	mvf.fwdX[centerIdx] = 100
	mvf.fwdY[centerIdx] = 100

	medianFilterMVField(mvf)

	// After median filtering, the outlier should be smoothed to (4, 0)
	require.Equal(t, int16(4), mvf.fwdX[centerIdx],
		"outlier fwdX should be smoothed to 4")
	require.Equal(t, int16(0), mvf.fwdY[centerIdx],
		"outlier fwdY should be smoothed to 0")
}

func TestMedianFilterMV_PreservesUniform(t *testing.T) {
	w, h := 160, 160
	bs := 16
	mvf := newMotionVectorField(w, h, bs)

	// Fill with uniform (6, -2) vectors
	n := mvf.cols * mvf.rows
	for i := 0; i < n; i++ {
		mvf.fwdX[i] = 6
		mvf.fwdY[i] = -2
		mvf.bwdX[i] = -6
		mvf.bwdY[i] = 2
	}

	medianFilterMVField(mvf)

	// All vectors should remain unchanged
	for i := 0; i < n; i++ {
		require.Equal(t, int16(6), mvf.fwdX[i], "fwdX[%d]", i)
		require.Equal(t, int16(-2), mvf.fwdY[i], "fwdY[%d]", i)
		require.Equal(t, int16(-6), mvf.bwdX[i], "bwdX[%d]", i)
		require.Equal(t, int16(2), mvf.bwdY[i], "bwdY[%d]", i)
	}
}

func TestCheckConsistency_NoOcclusion(t *testing.T) {
	w, h := 160, 160
	bs := 16
	mvf := newMotionVectorField(w, h, bs)

	// Set perfectly consistent forward/backward vectors.
	// Forward = (0, 0) everywhere, backward = (0, 0) everywhere.
	// These are perfectly consistent.
	n := mvf.cols * mvf.rows
	for i := 0; i < n; i++ {
		mvf.fwdX[i] = 0
		mvf.fwdY[i] = 0
		mvf.bwdX[i] = 0
		mvf.bwdY[i] = 0
	}

	checkConsistency(mvf, 4)

	for i := 0; i < n; i++ {
		require.Equal(t, byte(0), mvf.occlusion[i],
			"occlusion[%d] should be 0 (no occlusion)", i)
	}
}

func TestCheckConsistency_MarksOcclusion(t *testing.T) {
	w, h := 160, 160
	bs := 16
	mvf := newMotionVectorField(w, h, bs)

	n := mvf.cols * mvf.rows

	// Set consistent vectors for most blocks
	for i := 0; i < n; i++ {
		mvf.fwdX[i] = 0
		mvf.fwdY[i] = 0
		mvf.bwdX[i] = 0
		mvf.bwdY[i] = 0
	}

	// Create inconsistency at center block:
	// Forward says (16, 0) — block maps to next column
	// But backward at destination says (16, 0) instead of (-16, 0)
	centerRow := mvf.rows / 2
	centerCol := mvf.cols / 2
	centerIdx := centerRow*mvf.cols + centerCol
	mvf.fwdX[centerIdx] = 16 // maps to (centerCol+1, centerRow)

	// At the destination block, backward should be (-16, 0) for consistency.
	// Instead, set it to (16, 0) which is inconsistent.
	dstIdx := centerRow*mvf.cols + centerCol + 1
	mvf.bwdX[dstIdx] = 16 // inconsistent: fwd(16) + bwd(16) = 32 > threshold(4)

	checkConsistency(mvf, 4)

	// Center block should be forward-occluded (bit 0)
	require.NotEqual(t, byte(0), mvf.occlusion[centerIdx]&1,
		"center block should be forward-occluded")

	// Verify some non-occluded blocks remain clean
	cornerIdx := 0
	require.Equal(t, byte(0), mvf.occlusion[cornerIdx]&1,
		"corner block should not be forward-occluded")
}

func TestEstimateMotionField_FullFrame(t *testing.T) {
	w, h := 320, 240
	shift := 8

	prev := makeGradientFrame(w, h)
	curr := makeShiftedFrame(w, h, shift, 0)

	mvf := newMotionVectorField(w, h, 16)
	estimateMotionField(prev, curr, mvf, 32)

	// Verify forward vectors for interior blocks
	correctFwd := 0
	totalInterior := 0
	for row := 0; row < mvf.rows; row++ {
		for col := 1; col < mvf.cols-1; col++ {
			totalInterior++
			idx := row*mvf.cols + col
			if mvf.fwdX[idx] == int16(shift) && mvf.fwdY[idx] == 0 {
				correctFwd++
			}
		}
	}
	require.Greater(t, float64(correctFwd)/float64(totalInterior), 0.9,
		"at least 90%% of interior blocks should have correct forward MV")

	// Verify backward vectors for interior blocks
	correctBwd := 0
	totalInterior = 0
	for row := 0; row < mvf.rows; row++ {
		for col := 1; col < mvf.cols-1; col++ {
			totalInterior++
			idx := row*mvf.cols + col
			if mvf.bwdX[idx] == int16(-shift) && mvf.bwdY[idx] == 0 {
				correctBwd++
			}
		}
	}
	require.Greater(t, float64(correctBwd)/float64(totalInterior), 0.9,
		"at least 90%% of interior blocks should have correct backward MV")

	// After median filter, consistency check should show minimal occlusion
	medianFilterMVField(mvf)
	checkConsistency(mvf, 4)

	occluded := 0
	n := mvf.cols * mvf.rows
	for i := 0; i < n; i++ {
		if mvf.occlusion[i] != 0 {
			occluded++
		}
	}
	require.Less(t, float64(occluded)/float64(n), 0.3,
		"less than 30%% of blocks should be occluded for simple translation")
}

// TestDownsampleY2x verifies 2x box-filter downsampling of Y plane.
func TestDownsampleY2x(t *testing.T) {
	w, h := 320, 240
	src := make([]byte, w*h)
	for i := range src {
		src[i] = byte(i % 256)
	}

	dstW, dstH := w/2, h/2
	dst := make([]byte, dstW*dstH)
	downsampleY2x(dst, src, w, h)

	// Verify a sample of pixels: each output pixel is the average of a 2x2 block
	for row := 0; row < 10; row++ {
		for col := 0; col < 10; col++ {
			// Source 2x2 block
			a := int(src[(row*2)*w+col*2])
			b := int(src[(row*2)*w+col*2+1])
			c := int(src[(row*2+1)*w+col*2])
			d := int(src[(row*2+1)*w+col*2+1])
			expected := byte((a + b + c + d + 2) / 4)
			require.Equal(t, expected, dst[row*dstW+col],
				"pixel (%d,%d)", col, row)
		}
	}
}

// TestHierarchicalME_LargeMotion verifies that hierarchical ME can find
// large motion (64px) that exceeds the single-level search range (±32px).
func TestHierarchicalME_LargeMotion(t *testing.T) {
	w, h := 640, 480
	shift := 64 // Beyond single-level ±32px range

	prev := makeGradientFrame(w, h)
	curr := makeTranslatedFrame(prev, shift, 0)

	mvf := newMotionVectorField(w, h, 16)

	// Single-level ME with ±32px absolute range should fail to find 64px motion.
	// The absolute range cap prevents neighbor propagation from chaining beyond 32px.
	estimateMotionField(prev, curr, mvf, 32)
	singleLevelMiss := 0
	total := 0
	for row := 5; row < mvf.rows-5; row++ {
		for col := 5; col < mvf.cols-5; col++ {
			total++
			idx := row*mvf.cols + col
			diff := int(mvf.fwdX[idx]) - shift
			if diff < 0 {
				diff = -diff
			}
			if diff > 4 {
				singleLevelMiss++
			}
		}
	}
	require.Greater(t, float64(singleLevelMiss)/float64(total), 0.5,
		"single-level ME should miss most 64px motions (got %.1f%% miss)",
		float64(singleLevelMiss)*100/float64(total))

	// Hierarchical ME extends range via pyramid — should find them
	hme := newHierarchicalME()
	hme.estimate(prev, curr, mvf, 32)

	// After hme.estimate, MVs are in half-pel units (subPel=2)
	shiftHP := shift * mvf.subPel
	hierMiss := 0
	for row := 5; row < mvf.rows-5; row++ {
		for col := 5; col < mvf.cols-5; col++ {
			idx := row*mvf.cols + col
			diff := int(mvf.fwdX[idx]) - shiftHP
			if diff < 0 {
				diff = -diff
			}
			if diff > 4*mvf.subPel {
				hierMiss++
			}
		}
	}
	require.Less(t, float64(hierMiss)/float64(total), 0.15,
		"hierarchical ME should find most 64px motions (got %.1f%% miss)",
		float64(hierMiss)*100/float64(total))
}

// TestHierarchicalME_DiagonalLargeMotion tests large diagonal motion.
func TestHierarchicalME_DiagonalLargeMotion(t *testing.T) {
	w, h := 640, 480
	dx, dy := 40, 30 // Diagonal motion, within ±128px hierarchical range

	prev := makeCheckerGradientFrame(w, h)
	curr := makeTranslatedFrame(prev, dx, dy)

	mvf := newMotionVectorField(w, h, 16)
	hme := newHierarchicalME()
	hme.estimate(prev, curr, mvf, 32)

	// Interior blocks should approximate (40, 30) in half-pel units
	sp := mvf.subPel
	dxHP := dx * sp
	dyHP := dy * sp
	hits := 0
	total := 0
	for row := 5; row < mvf.rows-5; row++ {
		for col := 5; col < mvf.cols-5; col++ {
			total++
			idx := row*mvf.cols + col
			diffX := int(mvf.fwdX[idx]) - dxHP
			diffY := int(mvf.fwdY[idx]) - dyHP
			if diffX < 0 {
				diffX = -diffX
			}
			if diffY < 0 {
				diffY = -diffY
			}
			if diffX <= 4*sp && diffY <= 4*sp {
				hits++
			}
		}
	}
	require.Greater(t, float64(hits)/float64(total), 0.70,
		"hierarchical ME should find 70%%+ of diagonal (40,30) motions (got %.1f%%)",
		float64(hits)*100/float64(total))
}

// TestHierarchicalME_SmallMotion verifies hierarchical ME doesn't degrade
// small motion accuracy compared to single-level.
func TestHierarchicalME_SmallMotion(t *testing.T) {
	w, h := 640, 480
	shift := 8 // Well within single-level range

	prev := makeGradientFrame(w, h)
	curr := makeTranslatedFrame(prev, shift, 0)

	mvf := newMotionVectorField(w, h, 16)
	hme := newHierarchicalME()
	hme.estimate(prev, curr, mvf, 32)

	// Interior blocks should still find exact motion (in half-pel units)
	sp := mvf.subPel
	shiftHP := shift * sp
	for row := 2; row < mvf.rows-2; row++ {
		for col := 2; col < mvf.cols-2; col++ {
			idx := row*mvf.cols + col
			diff := int(mvf.fwdX[idx]) - shiftHP
			if diff < 0 {
				diff = -diff
			}
			require.LessOrEqual(t, diff, 2*sp,
				"hierarchical ME should find small motion at (%d,%d): got %d, expected ~%d",
				col, row, mvf.fwdX[idx], shiftHP)
		}
	}
}

// TestTemporalMVPrediction verifies that temporal prediction improves
// ME quality across consecutive frame pairs with consistent motion.
func TestTemporalMVPrediction(t *testing.T) {
	w, h := 320, 240
	shift := 10

	// Create 3 frames with consistent 10px horizontal motion
	f0 := makeGradientFrame(w, h)
	f1 := makeTranslatedFrame(f0, shift, 0)
	f2 := makeTranslatedFrame(f1, shift, 0)

	mvf := newMotionVectorField(w, h, 16)
	hme := newHierarchicalME()

	// First pair: no temporal history
	hme.estimate(f0, f1, mvf, 32)

	// Second pair: should benefit from temporal prediction
	hme.estimate(f1, f2, mvf, 32)

	// The temporal predictor from f0→f1 (MV≈10) should help f1→f2 converge.
	// Interior blocks should find MV ≈ (10, 0) in half-pel units.
	sp := mvf.subPel
	shiftHP := shift * sp
	for row := 2; row < mvf.rows-2; row++ {
		for col := 2; col < mvf.cols-2; col++ {
			idx := row*mvf.cols + col
			diff := int(mvf.fwdX[idx]) - shiftHP
			if diff < 0 {
				diff = -diff
			}
			require.LessOrEqual(t, diff, 2*sp,
				"temporal prediction should help find MV at (%d,%d): got %d, expected ~%d",
				col, row, mvf.fwdX[idx], shiftHP)
		}
	}
}

// TestTemporalMV_SceneChange verifies temporal prediction doesn't cause
// problems when motion changes abruptly (e.g., scene cut within clip).
func TestTemporalMV_SceneChange(t *testing.T) {
	w, h := 320, 240

	f0 := makeGradientFrame(w, h)
	f1 := makeTranslatedFrame(f0, 20, 0) // frame pair 1: rightward motion

	mvf := newMotionVectorField(w, h, 16)
	hme := newHierarchicalME()

	// Build temporal history with rightward motion
	hme.estimate(f0, f1, mvf, 32)

	// Second pair: completely different content (upward motion)
	f2 := makeCheckerGradientFrame(w, h)
	f3 := makeTranslatedFrame(f2, 0, 5) // vertical motion

	hme.estimate(f2, f3, mvf, 32)

	// Should still work — temporal prediction is just one candidate among
	// parent seed and neighbor predictors. Should not crash or produce
	// wildly wrong results.
	sp := int(mvf.subPel)
	if sp < 1 {
		sp = 1
	}
	for row := 3; row < mvf.rows-3; row++ {
		for col := 3; col < mvf.cols-3; col++ {
			idx := row*mvf.cols + col
			diffY := int(mvf.fwdY[idx]) - 5*sp
			if diffY < 0 {
				diffY = -diffY
			}
			// Tolerance is wider since content changed completely
			require.LessOrEqual(t, diffY, 6*sp,
				"scene change: fwdY at (%d,%d) = %d, expected ~%d",
				col, row, mvf.fwdY[idx], 5*sp)
		}
	}
}

// TestHalfPelRefinement verifies that half-pel refinement produces MVs in
// half-pel units and that subPel is set to 2.
func TestHalfPelRefinement(t *testing.T) {
	w, h := 320, 240
	shift := 8

	prev := makeGradientFrame(w, h)
	curr := makeTranslatedFrame(prev, shift, 0)

	mvf := newMotionVectorField(w, h, 16)
	hme := newHierarchicalME()
	hme.estimate(prev, curr, mvf, 32)

	// After hierarchical ME with half-pel, subPel should be 2
	require.Equal(t, 2, mvf.subPel, "subPel should be 2 after half-pel refinement")

	// Interior MVs should be close to shift*2 in half-pel units (= shift in pixels)
	for row := 2; row < mvf.rows-2; row++ {
		for col := 2; col < mvf.cols-2; col++ {
			idx := row*mvf.cols + col
			// In half-pel units, an 8px motion = 16 half-pels
			expectedHP := shift * 2
			diff := int(mvf.fwdX[idx]) - expectedHP
			if diff < 0 {
				diff = -diff
			}
			// Allow ±1 half-pel tolerance (±0.5 pixels)
			require.LessOrEqual(t, diff, 1,
				"half-pel fwdX at (%d,%d): got %d, expected ~%d (= %dpx)",
				col, row, mvf.fwdX[idx], expectedHP, shift)
		}
	}
}

// TestHalfPelWarp verifies that the fast warp produces correct output with
// half-pel MVs (subPel=2).
func TestHalfPelWarp(t *testing.T) {
	w, h := 320, 240
	bs := 16

	f0 := makeGradientFrame(w, h)
	f1 := makeShiftedFrame(w, h, 8, 0)

	mvf := newMotionVectorField(w, h, bs)
	n := mvf.cols * mvf.rows
	// Set half-pel MVs: 8px = 16 half-pels
	mvf.subPel = 2
	for i := 0; i < n; i++ {
		mvf.fwdX[i] = 16 // 8px in half-pel units
		mvf.fwdY[i] = 0
		mvf.bwdX[i] = -16
		mvf.bwdY[i] = 0
	}

	frameSize := w * h * 3 / 2

	// Run both reference and fast warp
	refDst := make([]byte, frameSize)
	mcfiInterpolateSmooth(refDst, f0.YUV, f1.YUV, w, h, mvf, 0.5)

	fastDst := make([]byte, frameSize)
	mcfiInterpolateFast(fastDst, f0.YUV, f1.YUV, w, h, mvf, 0.5)

	// Fast should match reference within ±1 LSB
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
		"half-pel warp: max diff should be <= 1 LSB, got %d", maxDiff)
}

func BenchmarkHierarchicalME_1080p(b *testing.B) {
	w, h := 1920, 1080
	shift := 8

	prev := makeGradientFrame(w, h)
	curr := makeShiftedFrame(w, h, shift, 0)
	mvf := newMotionVectorField(w, h, 16)
	hme := newHierarchicalME()

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		hme.estimate(prev, curr, mvf, 32)
	}
}

func BenchmarkDiamondSearch_1080p(b *testing.B) {
	w, h := 1920, 1080
	shift := 8

	prev := makeGradientFrame(w, h)
	curr := makeShiftedFrame(w, h, shift, 0)
	mvf := newMotionVectorField(w, h, 16)

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		estimateMotionField(prev, curr, mvf, 32)
	}
}

func BenchmarkDiamondSearch_SingleBlock(b *testing.B) {
	w, h := 320, 240

	prev := makeGradientFrame(w, h)
	curr := makeShiftedFrame(w, h, 8, 0)

	prevY := prev.YUV[:w*h]
	currY := curr.YUV[:w*h]

	// Test block at center of frame
	bx := (w/2/16)*16 - 16
	by := (h/2/16)*16 - 16

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		diamondSearch(prevY, w, currY, w, bx, by, w, h, 32, 16, 0, 0)
	}
}
