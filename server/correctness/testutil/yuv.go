package testutil

import (
	"math"
	"testing"

	"github.com/stretchr/testify/require"
)

// YUVInLegalRange checks that all Y samples are in [16,235] and all Cb/Cr
// samples are in [16,240] (BT.709 limited/TV range).
func YUVInLegalRange(yuv []byte, w, h int) bool {
	yPlaneSize := w * h
	chromaW := w / 2
	chromaH := h / 2
	chromaSize := chromaW * chromaH

	// Check Y plane
	for i := 0; i < yPlaneSize; i++ {
		if yuv[i] < 16 || yuv[i] > 235 {
			return false
		}
	}

	// Check Cb plane
	cbStart := yPlaneSize
	for i := 0; i < chromaSize; i++ {
		if yuv[cbStart+i] < 16 || yuv[cbStart+i] > 240 {
			return false
		}
	}

	// Check Cr plane
	crStart := cbStart + chromaSize
	for i := 0; i < chromaSize; i++ {
		if yuv[crStart+i] < 16 || yuv[crStart+i] > 240 {
			return false
		}
	}

	return true
}

// YUVPixelAt reads a pixel from a planar YUV420p frame.
// Cb and Cr are subsampled 2x2, so each chroma sample covers a 2x2 Y block.
func YUVPixelAt(yuv []byte, w, h, x, y int) (yVal, cb, cr byte) {
	// Y plane: w*h bytes at offset 0
	yVal = yuv[y*w+x]

	// Chroma planes: (w/2)*(h/2) each
	chromaW := w / 2
	chromaX := x / 2
	chromaY := y / 2

	cbStart := w * h
	crStart := cbStart + chromaW*(h/2)

	cb = yuv[cbStart+chromaY*chromaW+chromaX]
	cr = yuv[crStart+chromaY*chromaW+chromaX]

	return
}

// YUVFrameEqual returns true if two YUV frames are byte-identical.
func YUVFrameEqual(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// YUVFramePSNR computes the Peak Signal-to-Noise Ratio on the Y plane only.
// Formula: 10 * log10(255^2 / MSE).
// Returns a very high value (999.0) for identical frames.
func YUVFramePSNR(a, b []byte, w, h int) float64 {
	yPlaneSize := w * h

	var sumSqDiff float64
	for i := 0; i < yPlaneSize; i++ {
		diff := float64(a[i]) - float64(b[i])
		sumSqDiff += diff * diff
	}

	mse := sumSqDiff / float64(yPlaneSize)
	if mse == 0 {
		return 999.0 // identical frames
	}

	return 10.0 * math.Log10(255.0*255.0/mse)
}

// ScanForBlackFrames returns true if the average Y value is <= 20.
// This detects broadcast black (Y=16) and full black (Y=0).
func ScanForBlackFrames(yuv []byte, w, h int) bool {
	yPlaneSize := w * h
	var sum uint64
	for i := 0; i < yPlaneSize; i++ {
		sum += uint64(yuv[i])
	}
	avg := float64(sum) / float64(yPlaneSize)
	return avg <= 20.0
}

// AssertYUVLegalRange is the test assertion version of YUVInLegalRange.
func AssertYUVLegalRange(t *testing.T, yuv []byte, w, h int) {
	t.Helper()
	require.True(t, YUVInLegalRange(yuv, w, h), "YUV frame contains out-of-legal-range values")
}
