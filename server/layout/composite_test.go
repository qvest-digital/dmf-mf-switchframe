package layout

import (
	"image"
	"testing"

	"github.com/stretchr/testify/require"
)

// makeYUV420 creates a YUV420 buffer filled with a constant Y value.
func makeYUV420(w, h int, y, cb, cr byte) []byte {
	ySize := w * h
	cbSize := (w / 2) * (h / 2)
	buf := make([]byte, ySize+cbSize*2)
	for i := 0; i < ySize; i++ {
		buf[i] = y
	}
	for i := 0; i < cbSize; i++ {
		buf[ySize+i] = cb
		buf[ySize+cbSize+i] = cr
	}
	return buf
}

func TestComposePIPOpaque(t *testing.T) {
	// 8x8 destination (black), 4x4 source (white) placed at (2,2)
	dst := makeYUV420(8, 8, 16, 128, 128)  // BT.709 black
	src := makeYUV420(4, 4, 235, 128, 128) // BT.709 white
	rect := image.Rect(2, 2, 6, 6)

	ComposePIPOpaque(dst, 8, 8, src, 4, 4, rect)

	// Y plane: pixel (3,3) should be white (235), pixel (0,0) should be black (16)
	require.Equal(t, byte(235), dst[3*8+3], "Y at (3,3) should be white")
	require.Equal(t, byte(16), dst[0], "Y at (0,0) should be black")

	// Cb plane: chroma at (1,1) in half-res = center of PIP
	ySize := 8 * 8
	chromaW := 4 // 8/2
	require.Equal(t, byte(128), dst[ySize+1*chromaW+1], "Cb at chroma (1,1)")
}

func TestComposePIPOpaque_Boundaries(t *testing.T) {
	// Place PIP at top-left corner (0,0)
	dst := makeYUV420(8, 8, 16, 128, 128)
	src := makeYUV420(4, 4, 235, 128, 128)
	rect := image.Rect(0, 0, 4, 4)

	ComposePIPOpaque(dst, 8, 8, src, 4, 4, rect)
	require.Equal(t, byte(235), dst[0], "Y at (0,0) should be white")
	require.Equal(t, byte(16), dst[4], "Y at (4,0) should be black")
}

func TestDrawBorderYUV(t *testing.T) {
	dst := makeYUV420(8, 8, 16, 128, 128)
	rect := image.Rect(2, 2, 6, 6)
	color := [3]byte{235, 128, 128} // white

	DrawBorderYUV(dst, 8, 8, rect, color, 2)

	// Border pixel at (2,0) — top border, 2px thick means rows 0-1
	require.Equal(t, byte(235), dst[0*8+2], "top border Y at (2,0)")
	require.Equal(t, byte(235), dst[1*8+2], "top border Y at (2,1)")
	// Interior (3,3) should still be black
	require.Equal(t, byte(16), dst[3*8+3], "interior should be unchanged")
}

func TestBlendRegion(t *testing.T) {
	dst := makeYUV420(8, 8, 16, 128, 128)
	src := makeYUV420(4, 4, 235, 128, 128)
	rect := image.Rect(0, 0, 4, 4)

	BlendRegion(dst, 8, 8, src, 4, 4, rect, 0.5)

	// Y at (0,0) should be blended between 16 and 235 ≈ ~125
	blended := dst[0]
	require.Greater(t, blended, byte(100))
	require.Less(t, blended, byte(150))
}

func TestBlendRegion_FullAlpha(t *testing.T) {
	dst := makeYUV420(8, 8, 16, 128, 128)
	src := makeYUV420(4, 4, 235, 128, 128)
	rect := image.Rect(0, 0, 4, 4)

	BlendRegion(dst, 8, 8, src, 4, 4, rect, 1.0)

	require.Equal(t, byte(235), dst[0], "full alpha should be fully opaque")
}

func TestBlendRegion_ZeroAlpha(t *testing.T) {
	dst := makeYUV420(8, 8, 16, 128, 128)
	src := makeYUV420(4, 4, 235, 128, 128)
	rect := image.Rect(0, 0, 4, 4)

	BlendRegion(dst, 8, 8, src, 4, 4, rect, 0.0)

	require.Equal(t, byte(16), dst[0], "zero alpha should leave dst unchanged")
}
