package transition

import (
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
	ySize := w * h              // 16
	uvSize := (w / 2) * (h / 2) // 4
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
		src[srcYSize+i] = 100          // Cb
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
	src[0] = 10                      // top-left
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
	// Fill with test data
	for i := range src {
		src[i] = byte(i % 256)
	}

	b.ResetTimer()
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
	// Fill with test data
	for i := range src {
		src[i] = byte(i % 256)
	}

	b.ResetTimer()
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

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		ScaleYUV420Lanczos(src, srcW, srcH, dst, dstW, dstH)
	}
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

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		ScaleYUV420Lanczos(src, srcW, srcH, dst, dstW, dstH)
	}
}
