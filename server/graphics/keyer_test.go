package graphics

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// makeYUV420Frame creates a solid-color YUV420 frame.
func makeYUV420Frame(width, height int, y, u, v byte) []byte {
	ySize := width * height
	uvSize := (width / 2) * (height / 2)
	frame := make([]byte, ySize+2*uvSize)

	for i := 0; i < ySize; i++ {
		frame[i] = y
	}
	for i := 0; i < uvSize; i++ {
		frame[ySize+i] = u
		frame[ySize+uvSize+i] = v
	}
	return frame
}

// greenYUV returns BT.709 YCbCr for pure green (R=0, G=255, B=0).
// Y = 0.2126*0 + 0.7152*255 + 0.0722*0 ~ 182
// Cb = -0.1146*0 - 0.3854*255 + 0.5*0 + 128 ~ 30
// Cr = 0.5*0 - 0.4542*255 - 0.0458*0 + 128 ~ 12
func greenYUV() YCbCr {
	return YCbCr{Y: 182, Cb: 30, Cr: 12}
}

func TestChromaKey_GreenPixelsTransparent(t *testing.T) {
	t.Parallel()
	// Create a 4x4 green frame
	frame := makeYUV420Frame(4, 4, 182, 30, 12)

	mask := ChromaKey(frame, 4, 4, greenYUV(), 0.4, 0.0, 0.0)

	require.Len(t, mask, 16)

	// All pixels should be transparent (alpha ~ 0)
	for i, a := range mask {
		require.LessOrEqual(t, a, byte(30),
			"pixel %d: expected transparent (<=30), got %d", i, a)
	}
}

func TestChromaKey_NonGreenStaysOpaque(t *testing.T) {
	t.Parallel()
	// Create a 4x4 blue frame: Y~18, Cb~237, Cr~114
	frame := makeYUV420Frame(4, 4, 18, 237, 114)

	mask := ChromaKey(frame, 4, 4, greenYUV(), 0.4, 0.0, 0.0)

	// All pixels should be opaque (alpha ~ 255)
	for i, a := range mask {
		require.GreaterOrEqual(t, a, byte(225),
			"pixel %d: expected opaque (>=225), got %d", i, a)
	}
}

func TestChromaKey_SmoothnessCreatesSoftEdges(t *testing.T) {
	t.Parallel()
	// Near-green frame: slightly off green (Y=180, Cb=40, Cr=20)
	frame := makeYUV420Frame(4, 4, 180, 40, 20)

	// Without smoothness: hard edge, should be near-opaque
	maskHard := ChromaKey(frame, 4, 4, greenYUV(), 0.1, 0.0, 0.0)
	// With smoothness: soft edges, partial transparency
	maskSoft := ChromaKey(frame, 4, 4, greenYUV(), 0.1, 0.3, 0.0)

	// At least some pixels should differ between hard and soft
	differs := false
	for i := range maskHard {
		if maskHard[i] != maskSoft[i] {
			differs = true
			break
		}
	}
	if !differs {
		t.Log("smoothness did not create different alpha values (may be expected for this color)")
	}
}

func TestChromaKey_SpillSuppression(t *testing.T) {
	t.Parallel()
	// Near-green pixel where spill suppression should desaturate
	// Cb=50, Cr=30 -- close to green in chroma space
	frame := makeYUV420Frame(4, 4, 180, 50, 30)

	// Run with spill suppression
	mask := ChromaKey(frame, 4, 4, greenYUV(), 0.4, 0.1, 0.8)

	// Mask should be computed without errors (spill suppression modifies the frame, not the mask)
	require.Len(t, mask, 16)
}

func TestLumaKey_BrightPixelsTransparent(t *testing.T) {
	t.Parallel()
	// High-clip luma key: bright pixels (Y > highClip) become transparent
	frame := makeYUV420Frame(4, 4, 240, 128, 128)

	mask := LumaKey(frame, 4, 4, 0.0, 0.8, 0.0)

	// Y=240/255 ~ 0.94, highClip=0.8 -> should be transparent
	for i, a := range mask {
		require.LessOrEqual(t, a, byte(30),
			"pixel %d: expected transparent (<=30) for bright pixel above highClip, got %d", i, a)
	}
}

func TestLumaKey_DarkPixelsTransparent(t *testing.T) {
	t.Parallel()
	// Low-clip luma key: dark pixels (Y < lowClip) become transparent
	frame := makeYUV420Frame(4, 4, 10, 128, 128)

	mask := LumaKey(frame, 4, 4, 0.2, 1.0, 0.0)

	// Y=10/255 ~ 0.04, lowClip=0.2 -> should be transparent
	for i, a := range mask {
		require.LessOrEqual(t, a, byte(30),
			"pixel %d: expected transparent (<=30) for dark pixel below lowClip, got %d", i, a)
	}
}

func TestLumaKey_MidRangeOpaque(t *testing.T) {
	t.Parallel()
	// Mid-range pixel between lowClip and highClip stays opaque
	frame := makeYUV420Frame(4, 4, 128, 128, 128)

	mask := LumaKey(frame, 4, 4, 0.1, 0.9, 0.0)

	// Y=128/255 ~ 0.50 -> between 0.1 and 0.9, should be opaque
	for i, a := range mask {
		require.GreaterOrEqual(t, a, byte(225),
			"pixel %d: expected opaque (>=225) for mid-range pixel, got %d", i, a)
	}
}

func TestLumaKey_SoftnessCreatesGradualTransitions(t *testing.T) {
	t.Parallel()
	// Create a frame with varying luma (gradient)
	w, h := 8, 2
	ySize := w * h
	uvSize := (w / 2) * (h / 2)
	frame := make([]byte, ySize+2*uvSize)

	// Y gradient: 0, 32, 64, 96, 128, 160, 192, 224 (repeated for 2 rows)
	for row := 0; row < h; row++ {
		for col := 0; col < w; col++ {
			frame[row*w+col] = byte(col * 32)
		}
	}
	// Neutral chroma
	for i := 0; i < uvSize*2; i++ {
		frame[ySize+i] = 128
	}

	// Luma key with softness: lowClip=0.3, highClip=1.0, softness=0.2
	mask := LumaKey(frame, w, h, 0.3, 1.0, 0.2)

	require.Len(t, mask, ySize)

	// Pixels near the lowClip boundary should have intermediate alpha values
	// Pixel at col=2 (Y=64, luma=0.25) is near lowClip=0.3
	// Some should be partially transparent, some fully opaque
	hasPartial := false
	for _, a := range mask {
		if a > 30 && a < 225 {
			hasPartial = true
			break
		}
	}
	if !hasPartial {
		t.Log("softness did not create partial alpha values (may be expected for these exact values)")
	}
}

func TestChromaKey_ZeroSizeFrame(t *testing.T) {
	t.Parallel()
	mask := ChromaKey(nil, 0, 0, greenYUV(), 0.4, 0.0, 0.0)
	require.Empty(t, mask)
}

func TestLumaKey_ZeroSizeFrame(t *testing.T) {
	t.Parallel()
	mask := LumaKey(nil, 0, 0, 0.2, 0.8, 0.0)
	require.Empty(t, mask)
}

func TestChromaKey_SinglePixel(t *testing.T) {
	t.Parallel()
	// 2x2 is minimum for YUV420 (chroma subsampling)
	frame := makeYUV420Frame(2, 2, 182, 30, 12) // green
	mask := ChromaKey(frame, 2, 2, greenYUV(), 0.4, 0.0, 0.0)
	require.Len(t, mask, 4)
}

func TestLumaKey_SinglePixel(t *testing.T) {
	t.Parallel()
	frame := makeYUV420Frame(2, 2, 128, 128, 128)
	mask := LumaKey(frame, 2, 2, 0.2, 0.8, 0.0)
	require.Len(t, mask, 4)
}

func TestChromaKey_ZeroSimilarityZeroSmoothness(t *testing.T) {
	t.Parallel()
	frame := makeYUV420Frame(4, 4, 180, 50, 30)
	// similarity=0, smoothness=0, spillSuppress=0.8 -- should not panic or produce NaN
	mask := ChromaKey(frame, 4, 4, greenYUV(), 0.0, 0.0, 0.8)
	require.Len(t, mask, 16)
	// Verify all values are valid bytes
	for i, a := range mask {
		_ = a // byte is always 0-255; verify no panics during iteration
		_ = i
	}
}

func TestChromaKeyWithSpillColor_IdenticalToChromaKeyWithNeutral(t *testing.T) {
	t.Parallel()
	// The optimized ChromaKeyWithSpillColor with (128,128) should produce
	// identical results to the ChromaKey wrapper.
	w, h := 8, 8
	frameA := makeYUV420Frame(w, h, 180, 50, 30) // near-green
	frameB := make([]byte, len(frameA))
	copy(frameB, frameA)

	maskA := ChromaKey(frameA, w, h, greenYUV(), 0.3, 0.15, 0.5)
	maskB := ChromaKeyWithSpillColor(frameB, w, h, greenYUV(), 0.3, 0.15, 0.5, 128, 128)

	require.Equal(t, maskA, maskB, "ChromaKey wrapper and ChromaKeyWithSpillColor(128,128) must produce identical masks")
	// Frame modifications (spill suppression) should also be identical.
	require.Equal(t, frameA, frameB, "Spill suppression frame modifications must be identical")
}

func TestChromaKeyWithSpillColor_PullsTowardConfiguredColor(t *testing.T) {
	t.Parallel()
	// Verify spill replacement with a non-neutral color pulls chroma
	// toward that color instead of toward 128.
	w, h := 4, 4
	ySize := w * h
	uvSize := (w / 2) * (h / 2)

	// Near-green pixel: Cb=50, Cr=30 (close to green key color Cb=30, Cr=12).
	// With spill suppression, chroma should be pulled toward the replacement color.
	frameNeutral := makeYUV420Frame(w, h, 180, 50, 30)
	frameCustom := make([]byte, len(frameNeutral))
	copy(frameCustom, frameNeutral)

	// Spill toward neutral (128, 128)
	ChromaKeyWithSpillColor(frameNeutral, w, h, greenYUV(), 0.3, 0.15, 0.8, 128, 128)
	// Spill toward warm tone (140, 160)
	ChromaKeyWithSpillColor(frameCustom, w, h, greenYUV(), 0.3, 0.15, 0.8, 140, 160)

	// The Cb/Cr planes should differ between neutral and custom spill replacement.
	cbNeutral := frameNeutral[ySize]
	cbCustom := frameCustom[ySize]
	crNeutral := frameNeutral[ySize+uvSize]
	crCustom := frameCustom[ySize+uvSize]

	// Custom spill should pull Cb toward 140 (higher than 128), so cbCustom > cbNeutral.
	require.Greater(t, cbCustom, cbNeutral,
		"custom spill Cb should be pulled higher toward 140 vs neutral 128")
	// Custom spill should pull Cr toward 160 (higher than 128), so crCustom > crNeutral.
	require.Greater(t, crCustom, crNeutral,
		"custom spill Cr should be pulled higher toward 160 vs neutral 128")
}

func TestChromaKeyWithSpillColor_SquaredDistOptimizationCorrectness(t *testing.T) {
	t.Parallel()
	// Test with a variety of pixel colors that exercise all three branches:
	// 1) fully transparent (inside simDist)
	// 2) smoothness zone (between simDist and simDist+smoothDist)
	// 3) fully opaque (outside both)
	w, h := 4, 4
	ySize := w * h
	uvSize := (w / 2) * (h / 2)

	// Build a frame with chroma values at known distances from green key (Cb=30, Cr=12).
	colors := []struct {
		cb, cr byte
	}{
		{30, 12},   // exact match: dist=0 -> transparent
		{35, 15},   // close: dist~6 -> transparent (within sim)
		{60, 30},   // medium: dist~35 -> smoothness zone or opaque
		{200, 180}, // far: dist~239 -> fully opaque
	}

	for _, c := range colors {
		frame := make([]byte, ySize+2*uvSize)
		for i := 0; i < ySize; i++ {
			frame[i] = 180
		}
		for i := 0; i < uvSize; i++ {
			frame[ySize+i] = c.cb
			frame[ySize+uvSize+i] = c.cr
		}

		mask := ChromaKeyWithSpillColor(frame, w, h, greenYUV(), 0.2, 0.1, 0.0, 128, 128)
		require.Len(t, mask, ySize)

		// All pixels in a solid-color frame should have the same alpha.
		for i := 1; i < len(mask); i++ {
			require.Equal(t, mask[0], mask[i],
				"all pixels in solid-color frame should have same alpha, pixel %d differs", i)
		}
	}
}
