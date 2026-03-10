package transition

import (
	"math"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestScalePlane_Identity(t *testing.T) {
	t.Parallel()
	// Same resolution = exact copy
	src := []byte{10, 20, 30, 40, 50, 60, 70, 80, 90}
	dst := make([]byte, 9)
	scalePlane(src, 3, 3, dst, 3, 3)
	require.Equal(t, src, dst)
}

func TestScalePlane_Upscale2x(t *testing.T) {
	t.Parallel()
	// 2x2 -> 4x4
	// src:
	//   0  100
	//   200  50
	src := []byte{0, 100, 200, 50}
	dst := make([]byte, 16) // 4x4
	scalePlane(src, 2, 2, dst, 4, 4)

	// Corners must match source corners exactly
	require.Equal(t, byte(0), dst[0], "top-left corner")       // (0,0) maps to src (0,0) = 0
	require.Equal(t, byte(100), dst[3], "top-right corner")    // (3,0) maps to src (1,0) = 100
	require.Equal(t, byte(200), dst[12], "bottom-left corner") // (0,3) maps to src (0,1) = 200
	require.Equal(t, byte(50), dst[15], "bottom-right corner") // (3,3) maps to src (1,1) = 50

	// Intermediate values should be interpolated (not zero, not garbage)
	for i, v := range dst {
		require.True(t, v <= 200, "pixel %d value %d exceeds max source value", i, v)
	}
}

func TestScalePlane_Downscale(t *testing.T) {
	t.Parallel()
	// 4x4 -> 2x2
	src := []byte{
		0, 33, 66, 100,
		66, 77, 88, 100,
		133, 122, 111, 100,
		200, 166, 133, 50,
	}
	dst := make([]byte, 4) // 2x2
	scalePlane(src, 4, 4, dst, 2, 2)

	// Corners must match source corners exactly
	require.Equal(t, byte(0), dst[0], "top-left corner")      // maps to src[0] = 0
	require.Equal(t, byte(100), dst[1], "top-right corner")   // maps to src[3] = 100
	require.Equal(t, byte(200), dst[2], "bottom-left corner") // maps to src[12] = 200
	require.Equal(t, byte(50), dst[3], "bottom-right corner") // maps to src[15] = 50
}

func TestScaleYUV420_MatchedResolution(t *testing.T) {
	t.Parallel()
	// Same size = exact copy for full YUV420 frame
	w, h := 4, 4
	ySize := w * h                // 16
	uvSize := (w / 2) * (h / 2)   // 4
	totalSize := ySize + 2*uvSize // 24

	src := make([]byte, totalSize)
	for i := range src {
		src[i] = byte(i * 7 % 256) // deterministic pattern
	}
	dst := make([]byte, totalSize)

	ScaleYUV420(src, w, h, dst, w, h)
	require.Equal(t, src, dst)
}

func TestScaleYUV420_DifferentResolutions(t *testing.T) {
	t.Parallel()
	// 8x8 -> 16x16, verify non-zero output
	srcW, srcH := 8, 8
	dstW, dstH := 16, 16

	srcYSize := srcW * srcH
	srcUVSize := (srcW / 2) * (srcH / 2)
	srcTotal := srcYSize + 2*srcUVSize

	dstYSize := dstW * dstH
	dstUVSize := (dstW / 2) * (dstH / 2)
	dstTotal := dstYSize + 2*dstUVSize

	src := make([]byte, srcTotal)
	// Fill with non-zero values
	for i := 0; i < srcYSize; i++ {
		src[i] = 128 // Y
	}
	for i := 0; i < srcUVSize; i++ {
		src[srcYSize+i] = 100           // Cb
		src[srcYSize+srcUVSize+i] = 200 // Cr
	}

	dst := make([]byte, dstTotal)
	ScaleYUV420(src, srcW, srcH, dst, dstW, dstH)

	// Y plane should have non-zero values
	hasNonZeroY := false
	for i := 0; i < dstYSize; i++ {
		if dst[i] != 0 {
			hasNonZeroY = true
			break
		}
	}
	require.True(t, hasNonZeroY, "Y plane should have non-zero values after upscale")

	// Check Y values are all 128 (uniform input -> uniform output)
	for i := 0; i < dstYSize; i++ {
		require.Equal(t, byte(128), dst[i], "Y pixel %d should be 128", i)
	}

	// Check Cb values are all 100
	for i := 0; i < dstUVSize; i++ {
		require.Equal(t, byte(100), dst[dstYSize+i], "Cb pixel %d should be 100", i)
	}

	// Check Cr values are all 200
	for i := 0; i < dstUVSize; i++ {
		require.Equal(t, byte(200), dst[dstYSize+dstUVSize+i], "Cr pixel %d should be 200", i)
	}
}

func TestScaleYUV420_CornerPreservation(t *testing.T) {
	t.Parallel()
	// Scale 4x4 -> 8x8 and verify Y plane corners match
	srcW, srcH := 4, 4
	dstW, dstH := 8, 8

	srcYSize := srcW * srcH
	srcUVSize := (srcW / 2) * (srcH / 2)
	srcTotal := srcYSize + 2*srcUVSize

	dstYSize := dstW * dstH
	dstUVSize := (dstW / 2) * (dstH / 2)
	dstTotal := dstYSize + 2*dstUVSize

	src := make([]byte, srcTotal)
	// Set Y plane corners to distinct values
	src[0] = 10                     // top-left
	src[srcW-1] = 50                // top-right
	src[(srcH-1)*srcW] = 150        // bottom-left
	src[(srcH-1)*srcW+srcW-1] = 200 // bottom-right
	// Fill chroma with neutral values
	for i := 0; i < srcUVSize; i++ {
		src[srcYSize+i] = 128
		src[srcYSize+srcUVSize+i] = 128
	}

	dst := make([]byte, dstTotal)
	ScaleYUV420(src, srcW, srcH, dst, dstW, dstH)

	// Y plane corners should match exactly
	require.Equal(t, byte(10), dst[0], "top-left Y corner")
	require.Equal(t, byte(50), dst[dstW-1], "top-right Y corner")
	require.Equal(t, byte(150), dst[(dstH-1)*dstW], "bottom-left Y corner")
	require.Equal(t, byte(200), dst[(dstH-1)*dstW+dstW-1], "bottom-right Y corner")
}

func TestScalePlane_1x1(t *testing.T) {
	t.Parallel()
	// Edge case: 1x1 source
	src := []byte{42}
	dst := make([]byte, 1)
	scalePlane(src, 1, 1, dst, 1, 1)
	require.Equal(t, byte(42), dst[0])
}

func TestScalePlane_1x1_Upscale(t *testing.T) {
	t.Parallel()
	// 1x1 -> 4x4: all destination pixels should equal the source pixel
	src := []byte{99}
	dst := make([]byte, 16)
	scalePlane(src, 1, 1, dst, 4, 4)
	for i, v := range dst {
		require.Equal(t, byte(99), v, "pixel %d should be 99 for 1x1 upscale", i)
	}
}

// --- Lanczos-3 tests ---

func TestScaleYUV420Lanczos_SameDimensions(t *testing.T) {
	t.Parallel()
	w, h := 8, 8
	ySize := w * h
	uvSize := (w / 2) * (h / 2)
	totalSize := ySize + 2*uvSize

	src := make([]byte, totalSize)
	for i := range src {
		src[i] = byte(i*13%256 + 17)
	}
	dst := make([]byte, totalSize)

	ScaleYUV420Lanczos(src, w, h, dst, w, h)
	require.Equal(t, src, dst, "same dimensions should produce exact copy")
}

func TestScaleYUV420Lanczos_Downscale(t *testing.T) {
	t.Parallel()
	// 16x16 solid color -> 8x8, output should be very close to original value
	srcW, srcH := 16, 16
	dstW, dstH := 8, 8

	srcYSize := srcW * srcH
	srcUVSize := (srcW / 2) * (srcH / 2)
	srcTotal := srcYSize + 2*srcUVSize

	dstYSize := dstW * dstH
	dstUVSize := (dstW / 2) * (dstH / 2)
	dstTotal := dstYSize + 2*dstUVSize

	src := make([]byte, srcTotal)
	// Fill Y with 180, Cb with 90, Cr with 210
	for i := 0; i < srcYSize; i++ {
		src[i] = 180
	}
	for i := 0; i < srcUVSize; i++ {
		src[srcYSize+i] = 90
		src[srcYSize+srcUVSize+i] = 210
	}

	dst := make([]byte, dstTotal)
	ScaleYUV420Lanczos(src, srcW, srcH, dst, dstW, dstH)

	// Uniform input should produce uniform output (within rounding tolerance)
	for i := 0; i < dstYSize; i++ {
		diff := int(dst[i]) - 180
		if diff < 0 {
			diff = -diff
		}
		require.LessOrEqual(t, diff, 1, "Y pixel %d: expected ~180, got %d", i, dst[i])
	}
	for i := 0; i < dstUVSize; i++ {
		diffCb := int(dst[dstYSize+i]) - 90
		if diffCb < 0 {
			diffCb = -diffCb
		}
		require.LessOrEqual(t, diffCb, 1, "Cb pixel %d: expected ~90, got %d", i, dst[dstYSize+i])

		diffCr := int(dst[dstYSize+dstUVSize+i]) - 210
		if diffCr < 0 {
			diffCr = -diffCr
		}
		require.LessOrEqual(t, diffCr, 1, "Cr pixel %d: expected ~210, got %d", i, dst[dstYSize+dstUVSize+i])
	}
}

func TestScaleYUV420Lanczos_Upscale(t *testing.T) {
	t.Parallel()
	// 4x4 -> 8x8, verify no crashes and non-zero output
	srcW, srcH := 4, 4
	dstW, dstH := 8, 8

	srcYSize := srcW * srcH
	srcUVSize := (srcW / 2) * (srcH / 2)
	srcTotal := srcYSize + 2*srcUVSize

	dstYSize := dstW * dstH
	dstUVSize := (dstW / 2) * (dstH / 2)
	dstTotal := dstYSize + 2*dstUVSize

	src := make([]byte, srcTotal)
	// Set non-trivial pattern
	for i := 0; i < srcYSize; i++ {
		src[i] = byte(40 + i*12)
	}
	for i := 0; i < srcUVSize; i++ {
		src[srcYSize+i] = 128
		src[srcYSize+srcUVSize+i] = 128
	}

	dst := make([]byte, dstTotal)
	ScaleYUV420Lanczos(src, srcW, srcH, dst, dstW, dstH)

	// Should have non-zero Y values
	hasNonZero := false
	for i := 0; i < dstYSize; i++ {
		if dst[i] != 0 {
			hasNonZero = true
			break
		}
	}
	require.True(t, hasNonZero, "upscaled Y plane should have non-zero values")

	// All values should be in valid range [0, 255] (implicit from byte type,
	// but ensures the clamping logic works — no overflows)
}

func TestScaleYUV420WithQuality(t *testing.T) {
	t.Parallel()
	srcW, srcH := 8, 8
	dstW, dstH := 16, 16

	srcYSize := srcW * srcH
	srcUVSize := (srcW / 2) * (srcH / 2)
	srcTotal := srcYSize + 2*srcUVSize

	dstYSize := dstW * dstH
	dstUVSize := (dstW / 2) * (dstH / 2)
	dstTotal := dstYSize + 2*dstUVSize

	src := make([]byte, srcTotal)
	for i := 0; i < srcYSize; i++ {
		src[i] = 100
	}
	for i := 0; i < srcUVSize; i++ {
		src[srcYSize+i] = 128
		src[srcYSize+srcUVSize+i] = 128
	}

	// Test High quality (Lanczos)
	dstHigh := make([]byte, dstTotal)
	ScaleYUV420WithQuality(src, srcW, srcH, dstHigh, dstW, dstH, ScaleQualityHigh)

	// Verify Y plane has valid output
	for i := 0; i < dstYSize; i++ {
		diff := int(dstHigh[i]) - 100
		if diff < 0 {
			diff = -diff
		}
		require.LessOrEqual(t, diff, 1, "High quality Y pixel %d: expected ~100, got %d", i, dstHigh[i])
	}

	// Test Fast quality (bilinear)
	dstFast := make([]byte, dstTotal)
	ScaleYUV420WithQuality(src, srcW, srcH, dstFast, dstW, dstH, ScaleQualityFast)

	// Verify Y plane has valid output
	for i := 0; i < dstYSize; i++ {
		require.Equal(t, byte(100), dstFast[i], "Fast quality Y pixel %d should be 100", i)
	}

	// Test same dimensions — both modes should copy
	dstSame := make([]byte, srcTotal)
	ScaleYUV420WithQuality(src, srcW, srcH, dstSame, srcW, srcH, ScaleQualityHigh)
	require.Equal(t, src, dstSame, "same dimensions should produce exact copy regardless of quality")
}

func TestScaleYUV420WithQuality_SameDimsCopy(t *testing.T) {
	t.Parallel()
	w, h := 4, 4
	total := w * h * 3 / 2
	src := make([]byte, total)
	for i := range src {
		src[i] = byte(i*7 + 3)
	}

	dst := make([]byte, total)
	ScaleYUV420WithQuality(src, w, h, dst, w, h, ScaleQualityHigh)
	require.Equal(t, src, dst, "ScaleYUV420WithQuality same-dims fast path")

	dst2 := make([]byte, total)
	ScaleYUV420WithQuality(src, w, h, dst2, w, h, ScaleQualityFast)
	require.Equal(t, src, dst2, "ScaleYUV420WithQuality same-dims fast path (fast mode)")
}

func BenchmarkScaleYUV420_720pTo1080p(b *testing.B) {
	srcW, srcH := 1280, 720
	dstW, dstH := 1920, 1080

	srcSize := srcW * srcH * 3 / 2
	dstSize := dstW * dstH * 3 / 2

	src := make([]byte, srcSize)
	dst := make([]byte, dstSize)
	for i := range src {
		src[i] = byte(i % 256)
	}

	b.SetBytes(int64(dstSize))
	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		ScaleYUV420(src, srcW, srcH, dst, dstW, dstH)
	}
}

func BenchmarkScaleYUV420_1080pTo720p(b *testing.B) {
	srcW, srcH := 1920, 1080
	dstW, dstH := 1280, 720

	srcSize := srcW * srcH * 3 / 2
	dstSize := dstW * dstH * 3 / 2

	src := make([]byte, srcSize)
	dst := make([]byte, dstSize)
	for i := range src {
		src[i] = byte(i % 256)
	}

	b.SetBytes(int64(dstSize))
	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		ScaleYUV420(src, srcW, srcH, dst, dstW, dstH)
	}
}

func BenchmarkScaleLanczos_1080to720(b *testing.B) {
	srcW, srcH := 1920, 1080
	dstW, dstH := 1280, 720

	srcSize := srcW * srcH * 3 / 2
	dstSize := dstW * dstH * 3 / 2

	src := make([]byte, srcSize)
	dst := make([]byte, dstSize)
	for i := range src {
		src[i] = byte(i % 256)
	}

	b.SetBytes(int64(dstSize))
	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		ScaleYUV420Lanczos(src, srcW, srcH, dst, dstW, dstH)
	}
}

// --- Optimized Lanczos cross-validation tests ---

// scalePlaneLanczosReference is the original float64 implementation for cross-validation.
func scalePlaneLanczosReference(src []byte, srcW, srcH int, dst []byte, dstW, dstH int) {
	if srcW == dstW && srcH == dstH {
		copy(dst, src)
		return
	}
	if srcW == 1 && srcH == 1 {
		val := src[0]
		for i := range dst {
			dst[i] = val
		}
		return
	}

	tmp := make([]float64, dstW*srcH)
	xRatio := float64(srcW) / float64(dstW)
	hScale := 1.0
	if xRatio > 1.0 {
		hScale = xRatio
	}
	hRadius := 3.0 * hScale

	for y := 0; y < srcH; y++ {
		srcRow := y * srcW
		tmpRow := y * dstW
		for dx := 0; dx < dstW; dx++ {
			sx := (float64(dx)+0.5)*xRatio - 0.5
			minX := int(math.Floor(sx - hRadius))
			maxX := int(math.Ceil(sx + hRadius))
			var sum, wsum float64
			for ix := minX; ix <= maxX; ix++ {
				cix := ix
				if cix < 0 {
					cix = 0
				} else if cix >= srcW {
					cix = srcW - 1
				}
				w := lanczos3((float64(ix) - sx) / hScale)
				sum += w * float64(src[srcRow+cix])
				wsum += w
			}
			if wsum != 0 {
				tmp[tmpRow+dx] = sum / wsum
			}
		}
	}

	yRatio := float64(srcH) / float64(dstH)
	vScale := 1.0
	if yRatio > 1.0 {
		vScale = yRatio
	}
	vRadius := 3.0 * vScale

	for dx := 0; dx < dstW; dx++ {
		for dy := 0; dy < dstH; dy++ {
			sy := (float64(dy)+0.5)*yRatio - 0.5
			minY := int(math.Floor(sy - vRadius))
			maxY := int(math.Ceil(sy + vRadius))
			var sum, wsum float64
			for iy := minY; iy <= maxY; iy++ {
				ciy := iy
				if ciy < 0 {
					ciy = 0
				} else if ciy >= srcH {
					ciy = srcH - 1
				}
				w := lanczos3((float64(iy) - sy) / vScale)
				sum += w * tmp[ciy*dstW+dx]
				wsum += w
			}
			var val float64
			if wsum != 0 {
				val = sum / wsum
			}
			if val < 0 {
				val = 0
			} else if val > 255 {
				val = 255
			}
			dst[dy*dstW+dx] = byte(val + 0.5)
		}
	}
}

func TestPrecomputeKernel_WeightsNormalized(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name            string
		srcSize, dstSize int
	}{
		{"upscale_720_1080", 720, 1080},
		{"upscale_1280_1920", 1280, 1920},
		{"downscale_1920_1280", 1920, 1280},
		{"downscale_1080_720", 1080, 720},
		{"identity_1920", 1920, 1920},
		{"small_4_8", 4, 8},
		{"small_8_4", 8, 4},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			k := precomputeLanczosKernel(tt.srcSize, tt.dstSize)
			require.Equal(t, tt.dstSize, k.size)

			for d := 0; d < k.size; d++ {
				var sum float32
				wBase := d * k.maxTaps
				for t := 0; t < k.maxTaps; t++ {
					sum += k.weights[wBase+t]
				}
				diff := sum - 1.0
				if diff < 0 {
					diff = -diff
				}
				require.LessOrEqual(t, diff, float32(0.01),
					"position %d: weights sum to %f, expected ~1.0", d, sum)
			}
		})
	}
}

func TestPrecomputeKernel_OffsetsInRange(t *testing.T) {
	t.Parallel()
	k := precomputeLanczosKernel(1280, 1920)
	for d := 0; d < k.size; d++ {
		off := k.offsets[d]
		require.GreaterOrEqual(t, off, int32(0), "offset[%d] = %d, must be >= 0", d, off)
		// Offset itself must be within source
		require.Less(t, int(off), 1280, "offset[%d] = %d, must be < srcSize", d, off)
		// Any taps past the source boundary must have zero weight
		wBase := d * k.maxTaps
		for t2 := 0; t2 < k.maxTaps; t2++ {
			idx := int(off) + t2
			if idx >= 1280 && k.weights[wBase+t2] != 0 {
				t.Fatalf("offset[%d]+tap %d = %d >= srcSize, but weight = %f (should be 0)",
					d, t2, idx, k.weights[wBase+t2])
			}
		}
	}
}

func TestScalePlaneLanczos_CrossValidate_Upscale(t *testing.T) {
	t.Parallel()
	// Clear kernel cache to avoid stale entries from other tests
	for i := range kernelCache {
		kernelCache[i].Store(nil)
	}
	srcW, srcH := 64, 48
	dstW, dstH := 128, 96

	src := make([]byte, srcW*srcH)
	for i := range src {
		src[i] = byte((i * 37) % 256)
	}

	ref := make([]byte, dstW*dstH)
	opt := make([]byte, dstW*dstH)

	scalePlaneLanczosReference(src, srcW, srcH, ref, dstW, dstH)
	scalePlaneLanczos(src, srcW, srcH, opt, dstW, dstH)

	maxDiff := 0
	maxDiffIdx := 0
	for i := range ref {
		d := int(ref[i]) - int(opt[i])
		if d < 0 {
			d = -d
		}
		if d > maxDiff {
			maxDiff = d
			maxDiffIdx = i
		}
	}
	if maxDiff > 2 {
		y := maxDiffIdx / dstW
		x := maxDiffIdx % dstW
		t.Logf("max diff at (%d,%d): ref=%d opt=%d diff=%d", x, y, ref[maxDiffIdx], opt[maxDiffIdx], maxDiff)
	}
	require.LessOrEqual(t, maxDiff, 2,
		"max pixel difference %d exceeds tolerance of ±2", maxDiff)
}

func TestScalePlaneLanczos_CrossValidate_Downscale(t *testing.T) {
	t.Parallel()
	srcW, srcH := 128, 96
	dstW, dstH := 64, 48

	src := make([]byte, srcW*srcH)
	for i := range src {
		src[i] = byte((i * 37) % 256)
	}

	ref := make([]byte, dstW*dstH)
	opt := make([]byte, dstW*dstH)

	scalePlaneLanczosReference(src, srcW, srcH, ref, dstW, dstH)
	scalePlaneLanczos(src, srcW, srcH, opt, dstW, dstH)

	maxDiff := 0
	for i := range ref {
		d := int(ref[i]) - int(opt[i])
		if d < 0 {
			d = -d
		}
		if d > maxDiff {
			maxDiff = d
		}
	}
	require.LessOrEqual(t, maxDiff, 2,
		"max pixel difference %d exceeds tolerance of ±2", maxDiff)
}

func TestScalePlaneLanczos_CrossValidate_720to1080(t *testing.T) {
	t.Parallel()
	srcW, srcH := 320, 180 // smaller than full 720p for test speed
	dstW, dstH := 480, 270

	src := make([]byte, srcW*srcH)
	for i := range src {
		src[i] = byte((i * 37) % 256)
	}

	ref := make([]byte, dstW*dstH)
	opt := make([]byte, dstW*dstH)

	scalePlaneLanczosReference(src, srcW, srcH, ref, dstW, dstH)
	scalePlaneLanczos(src, srcW, srcH, opt, dstW, dstH)

	maxDiff := 0
	for i := range ref {
		d := int(ref[i]) - int(opt[i])
		if d < 0 {
			d = -d
		}
		if d > maxDiff {
			maxDiff = d
		}
	}
	require.LessOrEqual(t, maxDiff, 2,
		"max pixel difference %d exceeds tolerance of ±2", maxDiff)
}

func BenchmarkScaleLanczos_720to1080(b *testing.B) {
	srcW, srcH := 1280, 720
	dstW, dstH := 1920, 1080

	srcSize := srcW * srcH * 3 / 2
	dstSize := dstW * dstH * 3 / 2

	src := make([]byte, srcSize)
	dst := make([]byte, dstSize)
	for i := range src {
		src[i] = byte(i % 256)
	}

	b.SetBytes(int64(dstSize))
	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		ScaleYUV420Lanczos(src, srcW, srcH, dst, dstW, dstH)
	}
}

func BenchmarkScaleLanczos_1080to360(b *testing.B) {
	srcW, srcH := 1920, 1080
	dstW, dstH := 640, 360

	srcSize := srcW * srcH * 3 / 2
	dstSize := dstW * dstH * 3 / 2

	src := make([]byte, srcSize)
	dst := make([]byte, dstSize)
	for i := range src {
		src[i] = byte(i % 256)
	}

	b.SetBytes(int64(dstSize))
	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		ScaleYUV420Lanczos(src, srcW, srcH, dst, dstW, dstH)
	}
}

func TestScaleYUV420Lanczos_LargeDownscale(t *testing.T) {
	t.Parallel()
	// 3x downscale triggers box pre-shrink
	srcW, srcH := 192, 108
	dstW, dstH := 64, 36

	srcYSize := srcW * srcH
	srcUVSize := (srcW / 2) * (srcH / 2)
	srcTotal := srcYSize + 2*srcUVSize

	dstYSize := dstW * dstH
	dstUVSize := (dstW / 2) * (dstH / 2)
	dstTotal := dstYSize + 2*dstUVSize

	src := make([]byte, srcTotal)
	for i := 0; i < srcYSize; i++ {
		src[i] = 150
	}
	for i := 0; i < srcUVSize; i++ {
		src[srcYSize+i] = 80
		src[srcYSize+srcUVSize+i] = 200
	}

	dst := make([]byte, dstTotal)
	ScaleYUV420Lanczos(src, srcW, srcH, dst, dstW, dstH)

	for i := 0; i < dstYSize; i++ {
		diff := int(dst[i]) - 150
		if diff < 0 {
			diff = -diff
		}
		require.LessOrEqual(t, diff, 2, "Y pixel %d: expected ~150, got %d", i, dst[i])
	}
	for i := 0; i < dstUVSize; i++ {
		diffCb := int(dst[dstYSize+i]) - 80
		if diffCb < 0 {
			diffCb = -diffCb
		}
		require.LessOrEqual(t, diffCb, 2, "Cb pixel %d: expected ~80, got %d", i, dst[dstYSize+i])
	}
}

func TestBoxShrinkPlane_Basic(t *testing.T) {
	t.Parallel()
	// 8x8 uniform → 4x4 with factor 2
	src := make([]byte, 64)
	for i := range src {
		src[i] = 100
	}
	dst := make([]byte, 16)
	boxShrinkPlane(src, 8, 8, dst, 4, 4, 2, 2)
	for i, v := range dst {
		require.Equal(t, byte(100), v, "pixel %d should be 100", i)
	}
}

func TestBoxShrinkPlane_Gradient(t *testing.T) {
	t.Parallel()
	// 4x4 with 2x2 blocks → 2x2, each block averages
	src := []byte{
		10, 20, 30, 40,
		10, 20, 30, 40,
		50, 60, 70, 80,
		50, 60, 70, 80,
	}
	dst := make([]byte, 4)
	boxShrinkPlane(src, 4, 4, dst, 2, 2, 2, 2)
	require.Equal(t, byte(15), dst[0])  // avg(10,20,10,20)
	require.Equal(t, byte(35), dst[1])  // avg(30,40,30,40)
	require.Equal(t, byte(55), dst[2])  // avg(50,60,50,60)
	require.Equal(t, byte(75), dst[3])  // avg(70,80,70,80)
}

func TestBoxShrinkPlane_Rounding(t *testing.T) {
	t.Parallel()
	// When the average is not an exact integer, boxShrinkPlane should round
	// to the nearest value rather than truncating toward zero.
	// Block {10, 21, 10, 21}: sum=62, count=4, avg=15.5 → should round to 16.
	// Block {30, 41, 30, 41}: sum=142, count=4, avg=35.5 → should round to 36.
	src := []byte{
		10, 21, 30, 41,
		10, 21, 30, 41,
		50, 61, 70, 81,
		50, 61, 70, 81,
	}
	dst := make([]byte, 4)
	boxShrinkPlane(src, 4, 4, dst, 2, 2, 2, 2)
	require.Equal(t, byte(16), dst[0], "avg(10,21,10,21)=15.5 should round to 16")
	require.Equal(t, byte(36), dst[1], "avg(30,41,30,41)=35.5 should round to 36")
	require.Equal(t, byte(56), dst[2], "avg(50,61,50,61)=55.5 should round to 56")
	require.Equal(t, byte(76), dst[3], "avg(70,81,70,81)=75.5 should round to 76")
}
