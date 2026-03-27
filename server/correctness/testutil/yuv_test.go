package testutil

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestYUVPixelAt(t *testing.T) {
	w, h := 8, 8
	frame := GenerateSolidYUV420(w, h, 100, 150, 200)

	yVal, cb, cr := YUVPixelAt(frame, w, h, 0, 0)
	require.Equal(t, byte(100), yVal)
	require.Equal(t, byte(150), cb)
	require.Equal(t, byte(200), cr)

	// Check last pixel
	yVal, cb, cr = YUVPixelAt(frame, w, h, w-1, h-1)
	require.Equal(t, byte(100), yVal)
	require.Equal(t, byte(150), cb)
	require.Equal(t, byte(200), cr)
}

func TestYUVPixelAt_ChromaSubsampling(t *testing.T) {
	// Create a frame where we manually set different chroma for different 2x2 blocks
	w, h := 4, 4
	frame := make([]byte, w*h*3/2)

	// Y plane: all 128
	for i := 0; i < w*h; i++ {
		frame[i] = 128
	}

	chromaW := w / 2
	// Cb plane starts at w*h
	cbStart := w * h
	// Cr plane starts at w*h + chromaW*chromaH
	crStart := w*h + chromaW*(h/2)

	// Set top-left 2x2 chroma block
	frame[cbStart+0] = 50
	frame[crStart+0] = 60

	// Set top-right 2x2 chroma block
	frame[cbStart+1] = 70
	frame[crStart+1] = 80

	// Pixel (0,0) and (1,1) share same chroma (top-left block)
	_, cb00, cr00 := YUVPixelAt(frame, w, h, 0, 0)
	require.Equal(t, byte(50), cb00)
	require.Equal(t, byte(60), cr00)

	_, cb11, cr11 := YUVPixelAt(frame, w, h, 1, 1)
	require.Equal(t, byte(50), cb11)
	require.Equal(t, byte(60), cr11)

	// Pixel (2,0) is in the top-right chroma block
	_, cb20, cr20 := YUVPixelAt(frame, w, h, 2, 0)
	require.Equal(t, byte(70), cb20)
	require.Equal(t, byte(80), cr20)
}

func TestYUVInLegalRange(t *testing.T) {
	w, h := 8, 8

	// Legal range: Y in [16,235], Cb/Cr in [16,240]
	legalFrame := GenerateSolidYUV420(w, h, 128, 128, 128)
	require.True(t, YUVInLegalRange(legalFrame, w, h))

	// Y=0 is out of legal range
	illegalFrame := GenerateSolidYUV420(w, h, 0, 128, 128)
	require.False(t, YUVInLegalRange(illegalFrame, w, h))

	// Y=255 is out of legal range
	illegalFrame2 := GenerateSolidYUV420(w, h, 255, 128, 128)
	require.False(t, YUVInLegalRange(illegalFrame2, w, h))

	// Edge cases: Y=16 and Y=235 should be legal
	edgeFrame := GenerateSolidYUV420(w, h, 16, 16, 16)
	require.True(t, YUVInLegalRange(edgeFrame, w, h))

	edgeFrame2 := GenerateSolidYUV420(w, h, 235, 240, 240)
	require.True(t, YUVInLegalRange(edgeFrame2, w, h))

	// Cb=0 out of range
	illegalCb := GenerateSolidYUV420(w, h, 128, 0, 128)
	require.False(t, YUVInLegalRange(illegalCb, w, h))

	// Cr=255 out of range
	illegalCr := GenerateSolidYUV420(w, h, 128, 128, 255)
	require.False(t, YUVInLegalRange(illegalCr, w, h))
}

func TestYUVFrameEqual(t *testing.T) {
	w, h := 16, 16

	a := GenerateSolidYUV420(w, h, 100, 128, 128)
	b := GenerateSolidYUV420(w, h, 100, 128, 128)
	c := GenerateSolidYUV420(w, h, 101, 128, 128)

	require.True(t, YUVFrameEqual(a, b))
	require.False(t, YUVFrameEqual(a, c))

	// Different lengths
	short := make([]byte, 10)
	require.False(t, YUVFrameEqual(a, short))
}

func TestYUVFramePSNR(t *testing.T) {
	w, h := 64, 48

	// Identical frames should have infinite PSNR
	a := GenerateSolidYUV420(w, h, 128, 128, 128)
	psnr := YUVFramePSNR(a, a, w, h)
	require.True(t, psnr > 100, "identical frames should have very high PSNR, got %f", psnr)

	// Slightly different frames should have high but finite PSNR
	b := make([]byte, len(a))
	copy(b, a)
	b[0] = 129 // one pixel off by 1
	psnr2 := YUVFramePSNR(a, b, w, h)
	require.True(t, psnr2 > 30, "one pixel off by 1 should still have high PSNR, got %f", psnr2)
	require.True(t, psnr2 < 100, "one pixel off should not be infinite PSNR")

	// Very different frames
	c := GenerateSolidYUV420(w, h, 16, 128, 128)
	d := GenerateSolidYUV420(w, h, 235, 128, 128)
	psnr3 := YUVFramePSNR(c, d, w, h)
	require.True(t, psnr3 < 20, "very different frames should have low PSNR, got %f", psnr3)
}

func TestScanForBlackFrames(t *testing.T) {
	w, h := 64, 48

	// Black frame (Y=16, broadcast black)
	black := GenerateSolidYUV420(w, h, 16, 128, 128)
	require.True(t, ScanForBlackFrames(black, w, h))

	// Full black (Y=0)
	fullBlack := GenerateSolidYUV420(w, h, 0, 128, 128)
	require.True(t, ScanForBlackFrames(fullBlack, w, h))

	// Not black (Y=128)
	notBlack := GenerateSolidYUV420(w, h, 128, 128, 128)
	require.False(t, ScanForBlackFrames(notBlack, w, h))

	// Edge case: Y=20 should be considered black (average Y <= 20)
	edgeBlack := GenerateSolidYUV420(w, h, 20, 128, 128)
	require.True(t, ScanForBlackFrames(edgeBlack, w, h))

	// Y=21 should not be black
	notQuiteBlack := GenerateSolidYUV420(w, h, 21, 128, 128)
	require.False(t, ScanForBlackFrames(notQuiteBlack, w, h))
}

func TestAssertYUVLegalRange(t *testing.T) {
	w, h := 8, 8
	legal := GenerateSolidYUV420(w, h, 128, 128, 128)

	// Should not fail
	AssertYUVLegalRange(t, legal, w, h)
}
