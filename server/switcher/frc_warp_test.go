package switcher

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// makeUniformFrame creates a frame filled with a constant Y value and neutral chroma.
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

func TestWarpBlock_StaticFrame(t *testing.T) {
	w, h := 320, 240
	bs := 16

	f0 := makeGradientFrame(w, h)
	f1 := makeGradientFrame(w, h) // identical to f0

	mvf := newMotionVectorField(w, h, bs)
	// All MVs are zero (default after newMotionVectorField)

	ySize := w * h
	cbSize := (w / 2) * (h / 2)
	totalSize := ySize + 2*cbSize

	dst := make([]byte, totalSize)
	warpA := make([]byte, totalSize)
	warpB := make([]byte, totalSize)

	mcfiInterpolate(dst, warpA, warpB, f0, f1, mvf, 0.5)

	// With zero MVs, warpA should equal f0 and warpB should equal f1
	require.Equal(t, f0.YUV, warpA, "warpA should equal f0 with zero MVs")
	require.Equal(t, f1.YUV, warpB, "warpB should equal f1 with zero MVs")
}

func TestWarpBlock_HorizontalShift(t *testing.T) {
	w, h := 320, 240
	bs := 16
	shift := 8

	f0 := makeGradientFrame(w, h)
	f1 := makeGradientFrame(w, h) // content doesn't matter for warp direction test

	mvf := newMotionVectorField(w, h, bs)

	// Set all forward MVs to (shift, 0) — every block shifts right
	n := mvf.cols * mvf.rows
	for i := 0; i < n; i++ {
		mvf.fwdX[i] = int16(shift)
		mvf.fwdY[i] = 0
		mvf.bwdX[i] = int16(-shift)
		mvf.bwdY[i] = 0
	}

	ySize := w * h
	cbSize := (w / 2) * (h / 2)
	totalSize := ySize + 2*cbSize

	dst := make([]byte, totalSize)
	warpA := make([]byte, totalSize)
	warpB := make([]byte, totalSize)

	// At alpha=1.0, forward warp displaces by the full MV
	mcfiInterpolate(dst, warpA, warpB, f0, f1, mvf, 1.0)

	// Check that interior pixels in warpA have been shifted right by 'shift' pixels.
	// warpA[row*w + col] should equal f0.YUV[row*w + (col - shift)]
	// for pixels where both source and destination are in bounds.
	for row := 0; row < h; row++ {
		for col := shift + bs; col < w-bs; col++ {
			expected := f0.YUV[row*w+(col-shift)]
			got := warpA[row*w+col]
			if expected != got {
				t.Fatalf("warpA mismatch at (%d,%d): expected %d, got %d", col, row, expected, got)
			}
		}
	}
}

func TestMCFI_Alpha0(t *testing.T) {
	w, h := 320, 240
	bs := 16

	f0 := makeGradientFrame(w, h)
	f1 := makeShiftedFrame(w, h, 8, 0)

	mvf := newMotionVectorField(w, h, bs)
	// Set consistent MVs for a horizontal shift
	n := mvf.cols * mvf.rows
	for i := 0; i < n; i++ {
		mvf.fwdX[i] = 8
		mvf.fwdY[i] = 0
		mvf.bwdX[i] = -8
		mvf.bwdY[i] = 0
	}

	ySize := w * h
	cbSize := (w / 2) * (h / 2)
	totalSize := ySize + 2*cbSize

	dst := make([]byte, totalSize)
	warpA := make([]byte, totalSize)
	warpB := make([]byte, totalSize)

	// alpha=0 means forward warp displacement = MV * 0 = 0 (identity),
	// backward warp displacement = MV * 1.0 = full MV.
	// Since all blocks are occlusion=0, output is 50/50 blend of warpA and warpB.
	// warpA = f0 (no displacement), warpB = f1 warped by full backward MV.
	// For backward warp with MV = (-8, 0) * (1-0) = (-8, 0), f1 is shifted left by 8.
	// The backward warp of f1 (which is f0 shifted right by 8) shifted left by 8 = f0.
	// So warpA = f0 and warpB ~ f0, output ~ f0.
	mcfiInterpolate(dst, warpA, warpB, f0, f1, mvf, 0.0)

	// warpA should be identical to f0 (zero displacement)
	require.Equal(t, f0.YUV, warpA, "at alpha=0, warpA should equal f0")

	// Output should be very close to f0 (blend of f0 and ~f0)
	// Check Y plane interior pixels (skip edges due to clipping)
	mismatches := 0
	for row := bs; row < h-bs; row++ {
		for col := bs; col < w-bs; col++ {
			diff := int(dst[row*w+col]) - int(f0.YUV[row*w+col])
			if diff < 0 {
				diff = -diff
			}
			if diff > 1 {
				mismatches++
			}
		}
	}
	interiorPixels := (h - 2*bs) * (w - 2*bs)
	require.Less(t, float64(mismatches)/float64(interiorPixels), 0.05,
		"at alpha=0, output should closely match f0")
}

func TestMCFI_Alpha1(t *testing.T) {
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

	ySize := w * h
	cbSize := (w / 2) * (h / 2)
	totalSize := ySize + 2*cbSize

	dst := make([]byte, totalSize)
	warpA := make([]byte, totalSize)
	warpB := make([]byte, totalSize)

	// alpha=1 means backward warp displacement = MV * 0 = 0 (identity for warpB),
	// forward warp displacement = MV * 1.0 = full MV.
	// warpB = f1 (no displacement). warpA = f0 warped by full forward MV.
	// Forward warp of f0 with MV = (8, 0) * 1.0 shifts f0 right by 8, which ~ f1.
	// So warpA ~ f1 and warpB = f1, output ~ f1.
	mcfiInterpolate(dst, warpA, warpB, f0, f1, mvf, 1.0)

	// warpB should be identical to f1 (zero displacement)
	require.Equal(t, f1.YUV, warpB, "at alpha=1, warpB should equal f1")

	// Output should be very close to f1
	mismatches := 0
	for row := bs; row < h-bs; row++ {
		for col := bs; col < w-bs; col++ {
			diff := int(dst[row*w+col]) - int(f1.YUV[row*w+col])
			if diff < 0 {
				diff = -diff
			}
			if diff > 1 {
				mismatches++
			}
		}
	}
	interiorPixels := (h - 2*bs) * (w - 2*bs)
	require.Less(t, float64(mismatches)/float64(interiorPixels), 0.05,
		"at alpha=1, output should closely match f1")
}

func TestMCFI_Midpoint_Static(t *testing.T) {
	w, h := 320, 240
	bs := 16

	// Static scene: both frames identical, MVs all zero
	f0 := makeGradientFrame(w, h)
	f1 := makeGradientFrame(w, h) // identical

	mvf := newMotionVectorField(w, h, bs)
	// All MVs default to zero

	ySize := w * h
	cbSize := (w / 2) * (h / 2)
	totalSize := ySize + 2*cbSize

	dst := make([]byte, totalSize)
	warpA := make([]byte, totalSize)
	warpB := make([]byte, totalSize)

	mcfiInterpolate(dst, warpA, warpB, f0, f1, mvf, 0.5)

	// With zero MVs and identical frames, the 50/50 blend should produce
	// output identical to f0 (within rounding of 1 LSB).
	for row := 0; row < h; row++ {
		for col := 0; col < w; col++ {
			diff := int(dst[row*w+col]) - int(f0.YUV[row*w+col])
			if diff < 0 {
				diff = -diff
			}
			require.LessOrEqual(t, diff, 1,
				"midpoint static: pixel (%d,%d) diff=%d", col, row, diff)
		}
	}
}

func TestMCFI_Midpoint_Uniform(t *testing.T) {
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

	ySize := w * h
	cbSize := (w / 2) * (h / 2)
	totalSize := ySize + 2*cbSize

	dst := make([]byte, totalSize)
	warpA := make([]byte, totalSize)
	warpB := make([]byte, totalSize)

	// alpha=0.5 with uniform horizontal shift of 8px:
	// Forward warp moves f0 right by 4px, backward warp moves f1 left by 4px.
	// Both warpA and warpB should show content at 4px offset.
	mcfiInterpolate(dst, warpA, warpB, f0, f1, mvf, 0.5)

	// The expected midpoint frame should be f0 shifted right by 4 pixels.
	// Create the expected frame for comparison.
	expected := makeShiftedFrame(w, h, shift/2, 0)

	// Compare interior pixels (generous margin to avoid edge effects)
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
			if diff > 2 { // allow rounding tolerance of 2 (blend + shift)
				mismatches++
			}
		}
	}
	require.Less(t, float64(mismatches)/float64(total), 0.05,
		"midpoint uniform pan: output should show content at half-shift offset")
}

func TestMCFI_OcclusionFallback(t *testing.T) {
	w, h := 320, 240
	bs := 16

	// Use uniform frames with distinct Y values to verify fallback selection
	f0 := makeUniformFrame(w, h, 100)
	f1 := makeUniformFrame(w, h, 200)

	mvf := newMotionVectorField(w, h, bs)
	// All MVs zero (no motion)

	ySize := w * h
	cbSize := (w / 2) * (h / 2)
	totalSize := ySize + 2*cbSize

	// Test occlusion=1 (forward-occluded: use warpB = f1)
	mvf.reset()
	centerRow := mvf.rows / 2
	centerCol := mvf.cols / 2
	centerIdx := centerRow*mvf.cols + centerCol
	mvf.occlusion[centerIdx] = 1

	dst := make([]byte, totalSize)
	warpA := make([]byte, totalSize)
	warpB := make([]byte, totalSize)

	mcfiInterpolate(dst, warpA, warpB, f0, f1, mvf, 0.5)

	// Center block Y pixel should be 200 (from f1/warpB)
	cy := centerRow*bs + bs/2
	cx := centerCol*bs + bs/2
	require.Equal(t, byte(200), dst[cy*w+cx],
		"occlusion=1 should use warpB (f1 value)")

	// Non-occluded block should be ~150 (50/50 blend of 100 and 200)
	otherRow := 0
	otherCol := 0
	oy := otherRow*bs + bs/2
	ox := otherCol*bs + bs/2
	val := dst[oy*w+ox]
	diff := int(val) - 150
	if diff < 0 {
		diff = -diff
	}
	require.LessOrEqual(t, diff, 1,
		"non-occluded block should be ~150 (blend), got %d", val)

	// Test occlusion=2 (backward-occluded: use warpA = f0)
	mvf.reset()
	mvf.occlusion[centerIdx] = 2

	mcfiInterpolate(dst, warpA, warpB, f0, f1, mvf, 0.5)

	require.Equal(t, byte(100), dst[cy*w+cx],
		"occlusion=2 should use warpA (f0 value)")

	// Test occlusion=3 with alpha < 0.5 (use f0)
	mvf.reset()
	mvf.occlusion[centerIdx] = 3

	mcfiInterpolate(dst, warpA, warpB, f0, f1, mvf, 0.3)

	require.Equal(t, byte(100), dst[cy*w+cx],
		"occlusion=3 with alpha<0.5 should use f0")

	// Test occlusion=3 with alpha >= 0.5 (use f1)
	mcfiInterpolate(dst, warpA, warpB, f0, f1, mvf, 0.7)

	require.Equal(t, byte(200), dst[cy*w+cx],
		"occlusion=3 with alpha>=0.5 should use f1")
}

func TestMCFI_ChromaPlanes(t *testing.T) {
	w, h := 320, 240
	bs := 16
	shift := 8

	// Create frames with distinct chroma values to verify chroma warping
	ySize := w * h
	cbSize := (w / 2) * (h / 2)
	crSize := cbSize
	totalSize := ySize + cbSize + crSize

	// f0: Cb has horizontal gradient, Cr neutral
	f0YUV := make([]byte, totalSize)
	for i := 0; i < ySize; i++ {
		f0YUV[i] = 128 // neutral Y
	}
	chromaW := w / 2
	chromaH := h / 2
	for row := 0; row < chromaH; row++ {
		for col := 0; col < chromaW; col++ {
			f0YUV[ySize+row*chromaW+col] = byte(col % 256)   // Cb gradient
			f0YUV[ySize+cbSize+row*chromaW+col] = byte(128)   // Cr neutral
		}
	}
	f0 := &ProcessingFrame{YUV: f0YUV, Width: w, Height: h}

	// f1: same but Cb shifted by shift/2 in chroma space (= shift in luma space)
	f1YUV := make([]byte, totalSize)
	copy(f1YUV, f0YUV)
	for row := 0; row < chromaH; row++ {
		for col := 0; col < chromaW; col++ {
			srcCol := (col - shift/2 + chromaW*256) % chromaW
			f1YUV[ySize+row*chromaW+col] = byte(srcCol % 256) // shifted Cb
		}
	}
	f1 := &ProcessingFrame{YUV: f1YUV, Width: w, Height: h}

	mvf := newMotionVectorField(w, h, bs)
	n := mvf.cols * mvf.rows
	for i := 0; i < n; i++ {
		mvf.fwdX[i] = int16(shift)
		mvf.fwdY[i] = 0
		mvf.bwdX[i] = int16(-shift)
		mvf.bwdY[i] = 0
	}

	dst := make([]byte, totalSize)
	warpA := make([]byte, totalSize)
	warpB := make([]byte, totalSize)

	mcfiInterpolate(dst, warpA, warpB, f0, f1, mvf, 1.0)

	// At alpha=1.0, forward warp moves f0 Cb by MV/2 = shift/2 = 4 chroma pixels.
	// warpA's Cb should be f0's Cb shifted right by 4 chroma pixels.
	chromaMargin := bs / 2 // margin in chroma pixels
	for row := chromaMargin; row < chromaH-chromaMargin; row++ {
		for col := shift/2 + chromaMargin; col < chromaW-chromaMargin; col++ {
			expected := f0YUV[ySize+(row*chromaW+(col-shift/2))]
			got := warpA[ySize+(row*chromaW+col)]
			diff := int(got) - int(expected)
			if diff < 0 {
				diff = -diff
			}
			if diff > 0 {
				t.Fatalf("chroma warp mismatch at chroma (%d,%d): expected %d, got %d",
					col, row, expected, got)
			}
		}
	}
}

func TestMCFI_BoundsClipping(t *testing.T) {
	w, h := 320, 240
	bs := 16

	f0 := makeGradientFrame(w, h)
	f1 := makeGradientFrame(w, h)

	mvf := newMotionVectorField(w, h, bs)

	// Set very large MVs that would push blocks far out of bounds
	n := mvf.cols * mvf.rows
	for i := 0; i < n; i++ {
		mvf.fwdX[i] = 200  // way beyond frame width
		mvf.fwdY[i] = 200  // way beyond frame height
		mvf.bwdX[i] = -200
		mvf.bwdY[i] = -200
	}

	ySize := w * h
	cbSize := (w / 2) * (h / 2)
	totalSize := ySize + 2*cbSize

	dst := make([]byte, totalSize)
	warpA := make([]byte, totalSize)
	warpB := make([]byte, totalSize)

	// This must not panic — bounds clipping should prevent out-of-range access
	require.NotPanics(t, func() {
		mcfiInterpolate(dst, warpA, warpB, f0, f1, mvf, 0.5)
	}, "mcfiInterpolate should not panic with out-of-bounds MVs")

	// Verify output has valid pixel values (not zero-initialized garbage)
	// With extreme MVs, most blocks will use the fill-from-source fallback.
	// The initial copy(warpA, f0.YUV) ensures all pixels have valid values.
	nonZero := 0
	for i := 0; i < ySize; i++ {
		if dst[i] != 0 {
			nonZero++
		}
	}
	require.Greater(t, nonZero, ySize/2,
		"output should have mostly non-zero pixels from source fill")
}

func TestMCFI_NegativeMV(t *testing.T) {
	w, h := 320, 240
	bs := 16

	f0 := makeGradientFrame(w, h)
	f1 := makeGradientFrame(w, h)

	mvf := newMotionVectorField(w, h, bs)

	// Negative MVs should work and not crash
	n := mvf.cols * mvf.rows
	for i := 0; i < n; i++ {
		mvf.fwdX[i] = -4
		mvf.fwdY[i] = -4
		mvf.bwdX[i] = 4
		mvf.bwdY[i] = 4
	}

	ySize := w * h
	cbSize := (w / 2) * (h / 2)
	totalSize := ySize + 2*cbSize

	dst := make([]byte, totalSize)
	warpA := make([]byte, totalSize)
	warpB := make([]byte, totalSize)

	require.NotPanics(t, func() {
		mcfiInterpolate(dst, warpA, warpB, f0, f1, mvf, 0.5)
	}, "negative MVs should not panic")
}

func BenchmarkMCFI_Warp1080p(b *testing.B) {
	w, h := 1920, 1080
	// Round down to block-aligned dimensions
	w = (w / 16) * 16  // 1920
	h = (h / 16) * 16  // 1072

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

	ySize := w * h
	cbSize := (w / 2) * (h / 2)
	totalSize := ySize + 2*cbSize

	dst := make([]byte, totalSize)
	warpA := make([]byte, totalSize)
	warpB := make([]byte, totalSize)

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		mcfiInterpolate(dst, warpA, warpB, f0, f1, mvf, 0.5)
	}
}
