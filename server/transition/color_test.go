package transition_test

import (
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/zsiec/switchframe/server/transition"
)

func TestYUV420ToRGBBlack(t *testing.T) {
	t.Parallel()
	// Black in YUV: Y=16, U=128, V=128 (studio range)
	// But OpenH264 outputs full-range: Y=0, U=128, V=128
	w, h := 4, 4
	yuv := make([]byte, w*h*3/2)
	// Y plane = 0 (full-range black)
	// U plane = 128 (neutral chroma)
	for i := w * h; i < w*h+w*h/4; i++ {
		yuv[i] = 128
	}
	// V plane = 128 (neutral chroma)
	for i := w*h + w*h/4; i < w*h*3/2; i++ {
		yuv[i] = 128
	}

	rgb := make([]byte, w*h*3)
	transition.YUV420ToRGB(yuv, w, h, rgb)

	// All RGB values should be 0 (black)
	for i := 0; i < len(rgb); i++ {
		require.Equal(t, byte(0), rgb[i], "pixel %d should be 0", i)
	}
}

func TestYUV420ToRGBWhite(t *testing.T) {
	t.Parallel()
	w, h := 4, 4
	yuv := make([]byte, w*h*3/2)
	// Y plane = 255 (full-range white)
	for i := 0; i < w*h; i++ {
		yuv[i] = 255
	}
	// U, V = 128 (neutral)
	for i := w * h; i < w*h*3/2; i++ {
		yuv[i] = 128
	}

	rgb := make([]byte, w*h*3)
	transition.YUV420ToRGB(yuv, w, h, rgb)

	// All RGB values should be 255 (white)
	for i := 0; i < len(rgb); i++ {
		require.Equal(t, byte(255), rgb[i], "pixel %d should be 255", i)
	}
}

func TestRoundTripYUVRGBYUV(t *testing.T) {
	t.Parallel()
	w, h := 8, 8
	// Create a gradient in YUV
	original := make([]byte, w*h*3/2)
	for i := 0; i < w*h; i++ {
		original[i] = byte(i * 3 % 256) // Y gradient
	}
	for i := w * h; i < w*h*3/2; i++ {
		original[i] = 128 // neutral chroma
	}

	rgb := make([]byte, w*h*3)
	transition.YUV420ToRGB(original, w, h, rgb)

	roundtrip := make([]byte, w*h*3/2)
	transition.RGBToYUV420(rgb, w, h, roundtrip)

	// Y plane should round-trip within +/-1 (quantization error)
	for i := 0; i < w*h; i++ {
		diff := int(original[i]) - int(roundtrip[i])
		if diff < 0 {
			diff = -diff
		}
		require.LessOrEqual(t, diff, 1, "Y[%d]: original=%d roundtrip=%d", i, original[i], roundtrip[i])
	}
}

func TestYUV420ToRGBKnownColor(t *testing.T) {
	t.Parallel()
	// Pure red in full-range YUV (BT.709): Y~54, U~99, V~255
	w, h := 2, 2
	yuv := make([]byte, w*h*3/2)
	for i := 0; i < w*h; i++ {
		yuv[i] = 54 // Y
	}
	yuv[w*h] = 99    // U (Cb)
	yuv[w*h+1] = 255 // V (Cr)

	rgb := make([]byte, w*h*3)
	transition.YUV420ToRGB(yuv, w, h, rgb)

	// Red channel should be high (~255), green and blue low (~0)
	require.InDelta(t, 255, int(rgb[0]), 10, "R should be ~255")
	require.InDelta(t, 0, int(rgb[1]), 20, "G should be ~0")
	require.InDelta(t, 0, int(rgb[2]), 20, "B should be ~0")
}

func TestBufferSizes(t *testing.T) {
	t.Parallel()
	w, h := 1920, 1080
	yuv := make([]byte, w*h*3/2)
	rgb := make([]byte, w*h*3)

	// Should not panic with correct buffer sizes
	transition.YUV420ToRGB(yuv, w, h, rgb)
	transition.RGBToYUV420(rgb, w, h, yuv)
}

// --- Limited-range BT.709 tests ---

func TestRGBToYUV_BT709Limited_Black(t *testing.T) {
	t.Parallel()
	// RGB black (0,0,0) should produce limited-range YUV black: Y=16, Cb=128, Cr=128
	y, cb, cr := transition.RGBToYUV_BT709Limited(0, 0, 0)
	require.Equal(t, uint8(16), y, "Y for black should be 16 (limited-range)")
	require.Equal(t, uint8(128), cb, "Cb for black should be 128")
	require.Equal(t, uint8(128), cr, "Cr for black should be 128")
}

func TestRGBToYUV_BT709Limited_White(t *testing.T) {
	t.Parallel()
	// RGB white (255,255,255) should produce limited-range YUV white: Y=235, Cb=128, Cr=128
	y, cb, cr := transition.RGBToYUV_BT709Limited(255, 255, 255)
	require.Equal(t, uint8(235), y, "Y for white should be 235 (limited-range)")
	require.Equal(t, uint8(128), cb, "Cb for white should be 128")
	require.Equal(t, uint8(128), cr, "Cr for white should be 128")
}

func TestYUVToRGB_BT709Limited_Black(t *testing.T) {
	t.Parallel()
	// Limited-range black Y=16, Cb=128, Cr=128 should produce RGB (0,0,0)
	r, g, b := transition.YUVToRGB_BT709Limited(16, 128, 128)
	require.Equal(t, uint8(0), r, "R for limited-range black should be 0")
	require.Equal(t, uint8(0), g, "G for limited-range black should be 0")
	require.Equal(t, uint8(0), b, "B for limited-range black should be 0")
}

func TestYUVToRGB_BT709Limited_White(t *testing.T) {
	t.Parallel()
	// Limited-range white Y=235, Cb=128, Cr=128 should produce RGB (~255,~255,~255)
	r, g, b := transition.YUVToRGB_BT709Limited(235, 128, 128)
	require.InDelta(t, 255, int(r), 1, "R for limited-range white should be ~255")
	require.InDelta(t, 255, int(g), 1, "G for limited-range white should be ~255")
	require.InDelta(t, 255, int(b), 1, "B for limited-range white should be ~255")
}

func TestYUVToRGB_BT709Limited_FullRangeWhiteIsNotValid(t *testing.T) {
	t.Parallel()
	// Full-range white (Y=255) is above the limited-range maximum (Y=235).
	// The function should still clamp the output to valid RGB [0,255],
	// but Y=255 should produce values above 255 pre-clamp, resulting in R=G=B=255.
	// The key point: Y=255 is NOT "white" in limited-range — it's super-white.
	// Contrast this with Y=235 which IS white.
	r, g, b := transition.YUVToRGB_BT709Limited(255, 128, 128)
	require.Equal(t, uint8(255), r) // clamped
	require.Equal(t, uint8(255), g) // clamped
	require.Equal(t, uint8(255), b) // clamped

	// But converting back should NOT give us Y=255 — it should give Y=235 (clamped)
	yBack, _, _ := transition.RGBToYUV_BT709Limited(r, g, b)
	require.Equal(t, uint8(235), yBack, "round-trip of super-white should clamp to Y=235")
}

func TestBT709Limited_RoundTrip(t *testing.T) {
	t.Parallel()
	// Test round-trip: RGB → limited-range YUV → RGB should preserve values within ±1
	testColors := [][3]uint8{
		{0, 0, 0},       // black
		{255, 255, 255}, // white
		{255, 0, 0},     // red
		{0, 255, 0},     // green
		{0, 0, 255},     // blue
		{128, 128, 128}, // mid-gray
		{64, 128, 192},  // arbitrary
	}

	for _, c := range testColors {
		y, cb, cr := transition.RGBToYUV_BT709Limited(c[0], c[1], c[2])
		r, g, b := transition.YUVToRGB_BT709Limited(y, cb, cr)

		require.InDelta(t, int(c[0]), int(r), 2, "R round-trip for (%d,%d,%d)", c[0], c[1], c[2])
		require.InDelta(t, int(c[1]), int(g), 2, "G round-trip for (%d,%d,%d)", c[0], c[1], c[2])
		require.InDelta(t, int(c[2]), int(b), 2, "B round-trip for (%d,%d,%d)", c[0], c[1], c[2])
	}
}

func TestBT709Limited_YRange(t *testing.T) {
	t.Parallel()
	// Y output from RGBToYUV_BT709Limited should always be in [16, 235]
	for r := 0; r < 256; r += 17 {
		for g := 0; g < 256; g += 17 {
			for b := 0; b < 256; b += 17 {
				y, cb, cr := transition.RGBToYUV_BT709Limited(uint8(r), uint8(g), uint8(b))
				require.GreaterOrEqual(t, y, uint8(16), "Y should be >= 16 for RGB(%d,%d,%d)", r, g, b)
				require.LessOrEqual(t, y, uint8(235), "Y should be <= 235 for RGB(%d,%d,%d)", r, g, b)
				require.GreaterOrEqual(t, cb, uint8(16), "Cb should be >= 16 for RGB(%d,%d,%d)", r, g, b)
				require.LessOrEqual(t, cb, uint8(240), "Cb should be <= 240 for RGB(%d,%d,%d)", r, g, b)
				require.GreaterOrEqual(t, cr, uint8(16), "Cr should be >= 16 for RGB(%d,%d,%d)", r, g, b)
				require.LessOrEqual(t, cr, uint8(240), "Cr should be <= 240 for RGB(%d,%d,%d)", r, g, b)
			}
		}
	}
}

func TestBT709Limited_KnownRed(t *testing.T) {
	t.Parallel()
	// Pure red (255,0,0) in BT.709 limited-range:
	// Y  = 16 + 219 * (0.2126*1 + 0.7152*0 + 0.0722*0) = 16 + 219*0.2126 ≈ 62.6 ≈ 63
	// Cb = 128 + 224 * (-0.2126/1.8556) * 1           ≈ 128 + 224 * (-0.1146) ≈ 128 - 25.7 ≈ 102
	// Cr = 128 + 224 * (0.7874/1.5748) * 1             ≈ 128 + 224 * 0.5000   ≈ 128 + 112  = 240
	y, cb, cr := transition.RGBToYUV_BT709Limited(255, 0, 0)
	require.InDelta(t, 63, int(y), 1, "Y for red")
	require.InDelta(t, 102, int(cb), 2, "Cb for red")
	require.InDelta(t, 240, int(cr), 1, "Cr for red")
}
