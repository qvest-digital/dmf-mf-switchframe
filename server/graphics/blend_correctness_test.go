//go:build correctness

package graphics

import (
	"testing"
)

// TestAlphaBlendFormula verifies that AlphaBlendRGBA produces correct output
// for specific alpha values by comparing against the known blending formula.
func TestAlphaBlendFormula(t *testing.T) {
	// Test a single-pixel row at various alpha values
	alphaValues := []uint8{0, 64, 128, 192, 255}
	overlayR, overlayG, overlayB := uint8(255), uint8(0), uint8(0) // red overlay

	for _, alpha := range alphaValues {
		t.Run(testNameForAlpha(alpha), func(t *testing.T) {
			width := 16 // minimum practical width
			height := 2 // minimum for YUV420
			yuvSize := width * height * 3 / 2
			rgbaSize := width * height * 4

			// Create a gray frame (Y=128, Cb=128, Cr=128)
			yuv := make([]byte, yuvSize)
			for i := 0; i < width*height; i++ {
				yuv[i] = 128 // Y
			}
			ySize := width * height
			uvSize := (width / 2) * (height / 2)
			for i := 0; i < uvSize; i++ {
				yuv[ySize+i] = 128        // Cb
				yuv[ySize+uvSize+i] = 128 // Cr
			}

			// Create uniform RGBA overlay
			rgba := make([]byte, rgbaSize)
			for i := 0; i < width*height; i++ {
				rgba[i*4] = overlayR
				rgba[i*4+1] = overlayG
				rgba[i*4+2] = overlayB
				rgba[i*4+3] = alpha
			}

			// Save original Y values
			origY := make([]byte, width*height)
			copy(origY, yuv[:width*height])

			AlphaBlendRGBA(yuv, rgba, width, height, 1.0)

			if alpha == 0 {
				// Fully transparent: output should be unchanged
				for i := 0; i < width*height; i++ {
					if yuv[i] != origY[i] {
						t.Errorf("alpha=0: Y[%d] changed from %d to %d", i, origY[i], yuv[i])
					}
				}
				return
			}

			// For non-zero alpha, verify the Y plane matches the formula:
			// A' = A + (A >> 7)
			// a256 = (A' * alphaScale256) >> 8  (alphaScale256 = 256 for scale=1.0)
			// overlayY = 16 + ((47*R + 157*G + 16*B + 128) >> 8)
			// result = (origY*(256-a256) + overlayY*a256 + 128) >> 8
			aPrime := int(alpha) + (int(alpha) >> 7)
			a256 := (aPrime * 256) >> 8
			overlayY := 16 + ((47*int(overlayR) + 157*int(overlayG) + 16*int(overlayB) + 128) >> 8)

			for i := 0; i < width*height; i++ {
				expected := (int(origY[i])*(256-a256) + overlayY*a256 + 128) >> 8
				if expected < 0 {
					expected = 0
				}
				if expected > 255 {
					expected = 255
				}

				diff := int(yuv[i]) - expected
				if diff < -1 || diff > 1 {
					t.Errorf("alpha=%d, Y[%d]: expected %d, got %d (diff=%d)",
						alpha, i, expected, yuv[i], diff)
				}
			}
		})
	}
}

// TestAlphaBlendFullyOpaque verifies that a fully opaque overlay completely
// replaces the underlying frame at the Y plane level.
func TestAlphaBlendFullyOpaque(t *testing.T) {
	width := 16
	height := 2
	yuvSize := width * height * 3 / 2

	// Black frame
	yuv := make([]byte, yuvSize)
	for i := 0; i < width*height; i++ {
		yuv[i] = 16 // Y (black in limited range)
	}

	// White overlay, fully opaque
	rgba := make([]byte, width*height*4)
	for i := 0; i < width*height; i++ {
		rgba[i*4] = 255   // R
		rgba[i*4+1] = 255 // G
		rgba[i*4+2] = 255 // B
		rgba[i*4+3] = 255 // A
	}

	AlphaBlendRGBA(yuv, rgba, width, height, 1.0)

	// With alpha=255, the overlay should dominate.
	// overlayY = 16 + ((47*255 + 157*255 + 16*255 + 128) >> 8) = 16 + 219 = 235
	// a256 = (256 * 256) >> 8 = 256
	// result = (16*0 + 235*256 + 128) >> 8 = 235
	for i := 0; i < width*height; i++ {
		if yuv[i] < 233 || yuv[i] > 237 {
			t.Errorf("Y[%d] = %d, expected ~235 for white overlay on black", i, yuv[i])
		}
	}
}

// TestAlphaBlendScaleZero verifies that alphaScale=0.0 leaves the frame unchanged.
func TestAlphaBlendScaleZero(t *testing.T) {
	width := 16
	height := 2
	yuvSize := width * height * 3 / 2

	yuv := make([]byte, yuvSize)
	for i := range yuv {
		yuv[i] = byte(i & 0xFF)
	}
	origYUV := make([]byte, len(yuv))
	copy(origYUV, yuv)

	// Fully opaque red overlay
	rgba := make([]byte, width*height*4)
	for i := 0; i < width*height; i++ {
		rgba[i*4] = 255
		rgba[i*4+3] = 255
	}

	AlphaBlendRGBA(yuv, rgba, width, height, 0.0)

	for i := range yuv {
		if yuv[i] != origYUV[i] {
			t.Errorf("alphaScale=0: byte[%d] changed from %d to %d", i, origYUV[i], yuv[i])
		}
	}
}

// TestAlphaBlendTransparentOverlay verifies that a fully transparent overlay
// leaves the frame unchanged regardless of RGB values.
func TestAlphaBlendTransparentOverlay(t *testing.T) {
	width := 16
	height := 2
	yuvSize := width * height * 3 / 2

	yuv := make([]byte, yuvSize)
	for i := range yuv {
		yuv[i] = byte((i * 37) & 0xFF)
	}
	origYUV := make([]byte, len(yuv))
	copy(origYUV, yuv)

	// Non-zero RGB but alpha=0
	rgba := make([]byte, width*height*4)
	for i := 0; i < width*height; i++ {
		rgba[i*4] = 200
		rgba[i*4+1] = 100
		rgba[i*4+2] = 50
		rgba[i*4+3] = 0 // transparent
	}

	AlphaBlendRGBA(yuv, rgba, width, height, 1.0)

	for i := range yuv {
		if yuv[i] != origYUV[i] {
			t.Errorf("transparent overlay: byte[%d] changed from %d to %d", i, origYUV[i], yuv[i])
		}
	}
}

// TestAlphaBlendChromaFormula verifies the chroma blending formula by checking
// a single chroma row against the reference calculation.
func TestAlphaBlendChromaFormula(t *testing.T) {
	chromaWidth := 8
	alpha := uint8(128)

	// Gray background (Cb=128, Cr=128)
	cb := make([]byte, chromaWidth)
	cr := make([]byte, chromaWidth)
	for i := range cb {
		cb[i] = 128
		cr[i] = 128
	}

	// Red overlay (R=255, G=0, B=0) at stride-8 layout
	rgba := make([]byte, chromaWidth*8)
	for i := 0; i < chromaWidth; i++ {
		off := i * 8
		rgba[off] = 255     // R
		rgba[off+1] = 0     // G
		rgba[off+2] = 0     // B
		rgba[off+3] = alpha // A
	}

	alphaScale256 := 256 // scale = 1.0
	alphaBlendRGBAChromaRow(&cb[0], &cr[0], &rgba[0], chromaWidth, alphaScale256)

	// Reference calculation for red (R=255, G=0, B=0):
	// overlayCb = ((-26*255 - 86*0 + 112*0 + 128) >> 8) + 128 = (-6630 + 128)>>8 + 128 = -25 + 128 = 103
	// overlayCr = ((112*255 - 102*0 - 10*0 + 128) >> 8) + 128 = (28560 + 128)>>8 + 128 = 112 + 128 = 240
	overlayCb := ((-26*255 + 128) >> 8) + 128
	overlayCr := ((112*255 + 128) >> 8) + 128

	aPrime := int(alpha) + (int(alpha) >> 7)
	a256 := (aPrime * alphaScale256) >> 8
	inv := 256 - a256

	expectedCb := (128*inv + overlayCb*a256 + 128) >> 8
	expectedCr := (128*inv + overlayCr*a256 + 128) >> 8

	for i := 0; i < chromaWidth; i++ {
		diffCb := int(cb[i]) - expectedCb
		diffCr := int(cr[i]) - expectedCr
		if diffCb < -1 || diffCb > 1 {
			t.Errorf("Cb[%d] = %d, expected %d (diff=%d)", i, cb[i], expectedCb, diffCb)
		}
		if diffCr < -1 || diffCr > 1 {
			t.Errorf("Cr[%d] = %d, expected %d (diff=%d)", i, cr[i], expectedCr, diffCr)
		}
	}
}

func testNameForAlpha(a uint8) string {
	switch a {
	case 0:
		return "alpha_0"
	case 64:
		return "alpha_64"
	case 128:
		return "alpha_128"
	case 192:
		return "alpha_192"
	case 255:
		return "alpha_255"
	default:
		return "alpha_other"
	}
}
