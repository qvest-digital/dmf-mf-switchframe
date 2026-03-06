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
