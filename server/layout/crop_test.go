package layout

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestComputeCropRect_WiderSource(t *testing.T) {
	// 16:9 source into a 9:16 slot (portrait) — horizontal crop.
	cropX, cropY, cropW, cropH := ComputeCropRect(1280, 720, 540, 960, 0.5, 0.5)
	require.Equal(t, 720, cropH, "full height used")
	// Expected crop width: 540*720/960 = 405 → even-aligned = 404
	require.Equal(t, 404, cropW)
	require.Equal(t, 0, cropY)
	// Anchor 0.5 → centered: (1280-404)*0.5 = 438 → even = 438
	require.Equal(t, 438, cropX)
}

func TestComputeCropRect_TallerSource(t *testing.T) {
	// 4:3 source into a 16:9 slot — vertical crop.
	cropX, cropY, cropW, cropH := ComputeCropRect(640, 480, 1920, 1080, 0.5, 0.5)
	require.Equal(t, 640, cropW, "full width used")
	// Expected crop height: 1080*640/1920 = 360 → even-aligned = 360
	require.Equal(t, 360, cropH)
	require.Equal(t, 0, cropX)
	// Anchor 0.5 → centered: (480-360)*0.5 = 60 → even = 60
	require.Equal(t, 60, cropY)
}

func TestComputeCropRect_MatchingAspect(t *testing.T) {
	// Same aspect ratio — no crop.
	cropX, cropY, cropW, cropH := ComputeCropRect(1920, 1080, 960, 540, 0.5, 0.5)
	require.Equal(t, 0, cropX)
	require.Equal(t, 0, cropY)
	require.Equal(t, 1920, cropW)
	require.Equal(t, 1080, cropH)
}

func TestComputeCropRect_AnchorTopLeft(t *testing.T) {
	cropX, _, _, _ := ComputeCropRect(1280, 720, 540, 960, 0.0, 0.0)
	require.Equal(t, 0, cropX, "top-left anchor should crop from origin")
}

func TestComputeCropRect_AnchorBottomRight(t *testing.T) {
	cropX, cropY, cropW, cropH := ComputeCropRect(1280, 720, 540, 960, 1.0, 1.0)
	require.Equal(t, 720, cropH)
	// 1.0 anchor → max offset
	expectedX := EvenAlign(1280 - cropW)
	require.Equal(t, expectedX, cropX)
	require.Equal(t, 0, cropY, "no vertical crop for wider source")
}

func TestComputeCropRect_EvenAlignment(t *testing.T) {
	// Odd-ish dimensions should still produce even-aligned output.
	cropX, cropY, cropW, cropH := ComputeCropRect(1281, 721, 541, 961, 0.3, 0.7)
	require.Equal(t, 0, cropX%2, "cropX must be even")
	require.Equal(t, 0, cropY%2, "cropY must be even")
	require.Equal(t, 0, cropW%2, "cropW must be even")
	require.Equal(t, 0, cropH%2, "cropH must be even")
}

func TestComputeCropRect_SideBySideMotivating(t *testing.T) {
	// The motivating case: 1280×720 (16:9) into a 958×1080 side-by-side slot.
	cropX, cropY, cropW, cropH := ComputeCropRect(1280, 720, 958, 1080, 0.5, 0.5)
	// Source is wider than slot → crop horizontally. Full height used.
	require.Equal(t, 720, cropH)
	// cropW = 958*720/1080 = 638.666 → even = 638
	require.Equal(t, 638, cropW)
	require.Equal(t, 0, cropY)
	// Center anchor: (1280-638)*0.5 = 321 → even = 320
	require.Equal(t, 320, cropX)
	// Aspect ratio of crop region should approximate slot aspect.
	cropAspect := float64(cropW) / float64(cropH)
	slotAspect := float64(958) / float64(1080)
	require.InDelta(t, slotAspect, cropAspect, 0.01)
}

func TestCropYUV420Region_Basic(t *testing.T) {
	// 8×8 source, crop 4×4 from center (2,2).
	src := makeYUV420(8, 8, 200, 100, 150)

	// Mark a unique pixel so we can verify the copy.
	src[3*8+3] = 42 // Y at (3,3)

	cropW, cropH := 4, 4
	dst := make([]byte, cropW*cropH*3/2)
	CropYUV420Region(dst, src, 8, 8, 2, 2, cropW, cropH)

	// Y at (1,1) in cropped = Y at (3,3) in source
	require.Equal(t, byte(42), dst[1*cropW+1])
	// General Y should be 200.
	require.Equal(t, byte(200), dst[0])
}

func TestCropYUV420Region_ChromaCorrectness(t *testing.T) {
	// 8×8 source with distinct Cb=100, Cr=150.
	srcW, srcH := 8, 8
	src := makeYUV420(srcW, srcH, 200, 100, 150)

	cropW, cropH := 4, 4
	dst := make([]byte, cropW*cropH*3/2)
	CropYUV420Region(dst, src, srcW, srcH, 2, 2, cropW, cropH)

	ySize := cropW * cropH
	cbSize := (cropW / 2) * (cropH / 2)
	// Cb plane
	require.Equal(t, byte(100), dst[ySize])
	// Cr plane
	require.Equal(t, byte(150), dst[ySize+cbSize])
}

func TestCropYUV420Region_FullFrame(t *testing.T) {
	// Crop of full source should be identity.
	srcW, srcH := 8, 8
	src := makeYUV420(srcW, srcH, 200, 100, 150)
	dst := make([]byte, len(src))
	CropYUV420Region(dst, src, srcW, srcH, 0, 0, srcW, srcH)
	require.Equal(t, src, dst)
}

func BenchmarkComputeCropRect(b *testing.B) {
	for i := 0; i < b.N; i++ {
		ComputeCropRect(1280, 720, 958, 1080, 0.5, 0.5)
	}
}

func BenchmarkCropYUV420Region_1080p(b *testing.B) {
	srcW, srcH := 1920, 1080
	cropW, cropH := 1280, 1080
	src := makeYUV420(srcW, srcH, 200, 100, 150)
	dst := make([]byte, cropW*cropH*3/2)

	b.SetBytes(int64(cropW * cropH * 3 / 2))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		CropYUV420Region(dst, src, srcW, srcH, 320, 0, cropW, cropH)
	}
}
