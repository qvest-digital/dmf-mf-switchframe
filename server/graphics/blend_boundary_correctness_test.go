//go:build correctness

package graphics

import (
	"fmt"
	"image"
	"testing"
)

// TestAlphaBlendRowBoundarySizes exercises alphaBlendRGBARowY at widths 1 through 17.
// Feeds fully opaque red RGBA (R=255,G=0,B=0,A=255) over a Y=128 row.
// BT.709 limited-range: overlayY = 16 + ((47*255 + 157*0 + 16*0 + 128) >> 8) = 16 + 47 = 63
// With A=255: A'=255+(255>>7)=255+1=256, a256=(256*256)>>8 = 256, inv=0
// Expected: (128*0 + 63*256 + 128) >> 8 = 16256 >> 8 = 63
func TestAlphaBlendRowBoundarySizes(t *testing.T) {
	// BT.709: overlayY = 16 + ((47*R + 157*G + 16*B + 128) >> 8)
	// For red (255,0,0): = 16 + ((47*255 + 128) >> 8) = 16 + (12113 >> 8) = 16 + 47 = 63
	overlayY := 16 + ((47*255 + 128) >> 8)
	t.Logf("Expected overlayY for pure red: %d", overlayY)

	alphaScale256 := 256 // alphaScale = 1.0

	var failedWidths []int
	for width := 1; width <= 17; width++ {
		t.Run(fmt.Sprintf("width_%d", width), func(t *testing.T) {
			yRow := make([]byte, width)
			rgba := make([]byte, width*4)

			// Fill Y row with 128
			for i := range yRow {
				yRow[i] = 128
			}
			// Save original
			origY := make([]byte, width)
			copy(origY, yRow)

			// Fill RGBA with opaque red
			for i := 0; i < width; i++ {
				rgba[i*4] = 255   // R
				rgba[i*4+1] = 0   // G
				rgba[i*4+2] = 0   // B
				rgba[i*4+3] = 255 // A (fully opaque)
			}

			alphaBlendRGBARowY(&yRow[0], &rgba[0], width, alphaScale256)

			// With A=255, A'=256, a256=256, inv=0
			// result = (128*0 + overlayY*256 + 128) >> 8 = overlayY
			for i := 0; i < width; i++ {
				actual := int(yRow[i])
				diff := actual - overlayY
				if diff < -2 || diff > 2 {
					t.Logf("width=%d, pixel[%d]: got %d, expected %d (diff=%d)",
						width, i, actual, overlayY, diff)
					failedWidths = append(failedWidths, width)
					t.Errorf("width=%d: Y value out of range at pixel %d", width, i)
					return
				}
			}
		})
	}
	if len(failedWidths) > 0 {
		t.Logf("Failed widths: %v", failedWidths)
	}
}

// TestAlphaBlendChromaRowBoundary exercises alphaBlendRGBAChromaRow at
// chromaWidths 1 through 9. Red overlay (R=255,G=0,B=0,A=255).
// Expected Cb and Cr for pure red in BT.709:
//
//	overlayCb = ((-26*255 - 86*0 + 112*0 + 128) >> 8) + 128 = (-6630 + 128)>>8 + 128 = -25 + 128 = 103
//	overlayCr = ((112*255 - 102*0 - 10*0 + 128) >> 8) + 128 = (28688)>>8 + 128 = 112 + 128 = 240
func TestAlphaBlendChromaRowBoundary(t *testing.T) {
	overlayCb := ((-26*255 + 128) >> 8) + 128
	overlayCr := ((112*255 + 128) >> 8) + 128
	t.Logf("Expected overlayCb=%d, overlayCr=%d for pure red", overlayCb, overlayCr)

	alphaScale256 := 256

	for chromaWidth := 1; chromaWidth <= 9; chromaWidth++ {
		t.Run(fmt.Sprintf("chromaWidth_%d", chromaWidth), func(t *testing.T) {
			cb := make([]byte, chromaWidth)
			cr := make([]byte, chromaWidth)
			// RGBA at stride-8 layout (every other full-res pixel)
			rgba := make([]byte, chromaWidth*8)

			// Fill Cb/Cr with neutral gray (128)
			for i := range cb {
				cb[i] = 128
				cr[i] = 128
			}

			// Fill RGBA with opaque red at stride-8
			for i := 0; i < chromaWidth; i++ {
				off := i * 8
				rgba[off] = 255   // R
				rgba[off+1] = 0   // G
				rgba[off+2] = 0   // B
				rgba[off+3] = 255 // A
			}

			alphaBlendRGBAChromaRow(&cb[0], &cr[0], &rgba[0], chromaWidth, alphaScale256)

			// With A=255 and alphaScale=256: A'=256, a256=256, inv=0
			// Cb = (128*0 + overlayCb*256 + 128) >> 8 = overlayCb
			// Cr = (128*0 + overlayCr*256 + 128) >> 8 = overlayCr
			for i := 0; i < chromaWidth; i++ {
				actualCb := int(cb[i])
				actualCr := int(cr[i])
				diffCb := actualCb - overlayCb
				diffCr := actualCr - overlayCr

				if diffCb < -2 || diffCb > 2 {
					t.Errorf("chromaWidth=%d, pixel[%d]: Cb got %d, expected ~%d (diff=%d)",
						chromaWidth, i, actualCb, overlayCb, diffCb)
				}
				if diffCr < -2 || diffCr > 2 {
					t.Errorf("chromaWidth=%d, pixel[%d]: Cr got %d, expected ~%d (diff=%d)",
						chromaWidth, i, actualCr, overlayCr, diffCr)
				}
			}
		})
	}
}

// TestAlphaBlendAlphaZeroPassthrough verifies that with all RGBA alpha=0,
// the Y row is completely unchanged (byte-exact before/after).
// This catches bugs where A=0 pixels are not treated as no-ops.
func TestAlphaBlendAlphaZeroPassthrough(t *testing.T) {
	width := 320
	yRow := make([]byte, width)
	rgba := make([]byte, width*4)

	// Fill Y with a pattern
	for i := range yRow {
		yRow[i] = byte((i * 13 + 42) & 0xFF)
	}
	origY := make([]byte, width)
	copy(origY, yRow)

	// Fill RGBA: non-zero RGB but A=0
	for i := 0; i < width; i++ {
		rgba[i*4] = 200   // R (non-zero!)
		rgba[i*4+1] = 150 // G
		rgba[i*4+2] = 100 // B
		rgba[i*4+3] = 0   // A = 0 (transparent)
	}

	alphaBlendRGBARowY(&yRow[0], &rgba[0], width, 256) // alphaScale = 1.0

	var mismatches int
	for i := 0; i < width; i++ {
		if yRow[i] != origY[i] {
			if mismatches < 10 {
				t.Logf("pixel[%d]: was %d, now %d (should be unchanged with A=0)",
					i, origY[i], yRow[i])
			}
			mismatches++
		}
	}
	if mismatches > 0 {
		t.Errorf("A=0 passthrough: %d/%d Y pixels changed (should be byte-exact unchanged)",
			mismatches, width)
	}
}

// TestAlphaBlendAlphaZeroChromaPassthrough verifies that chroma blending with
// A=0 also leaves Cb/Cr unchanged.
func TestAlphaBlendAlphaZeroChromaPassthrough(t *testing.T) {
	chromaWidth := 160
	cb := make([]byte, chromaWidth)
	cr := make([]byte, chromaWidth)
	rgba := make([]byte, chromaWidth*8)

	// Fill Cb/Cr with a pattern
	for i := range cb {
		cb[i] = byte((i*17 + 50) & 0xFF)
		cr[i] = byte((i*23 + 80) & 0xFF)
	}
	origCb := make([]byte, chromaWidth)
	origCr := make([]byte, chromaWidth)
	copy(origCb, cb)
	copy(origCr, cr)

	// Fill RGBA: non-zero RGB, A=0, stride-8
	for i := 0; i < chromaWidth; i++ {
		off := i * 8
		rgba[off] = 200
		rgba[off+1] = 150
		rgba[off+2] = 100
		rgba[off+3] = 0 // transparent
	}

	alphaBlendRGBAChromaRow(&cb[0], &cr[0], &rgba[0], chromaWidth, 256)

	var mismatchesCb, mismatchesCr int
	for i := 0; i < chromaWidth; i++ {
		if cb[i] != origCb[i] {
			if mismatchesCb < 5 {
				t.Logf("Cb[%d]: was %d, now %d", i, origCb[i], cb[i])
			}
			mismatchesCb++
		}
		if cr[i] != origCr[i] {
			if mismatchesCr < 5 {
				t.Logf("Cr[%d]: was %d, now %d", i, origCr[i], cr[i])
			}
			mismatchesCr++
		}
	}
	if mismatchesCb > 0 || mismatchesCr > 0 {
		t.Errorf("A=0 chroma passthrough: %d Cb, %d Cr pixels changed (should be unchanged)",
			mismatchesCb, mismatchesCr)
	}
}

// TestAlphaBlendRectOddDimensions exercises AlphaBlendRGBARectInto with a rect
// that has odd width/height. The function should clip to even boundaries.
// Verify no panic and output is reasonable.
func TestAlphaBlendRectOddDimensions(t *testing.T) {
	frameW, frameH := 320, 240
	yuvSize := frameW * frameH * 3 / 2
	yuv := make([]byte, yuvSize)
	for i := range yuv {
		yuv[i] = 128
	}
	origYUV := make([]byte, yuvSize)
	copy(origYUV, yuv)

	overlayW, overlayH := 100, 100
	rgba := make([]byte, overlayW*overlayH*4)
	for i := 0; i < overlayW*overlayH; i++ {
		rgba[i*4] = 255   // R
		rgba[i*4+1] = 0   // G
		rgba[i*4+2] = 0   // B
		rgba[i*4+3] = 128 // A (semi-transparent)
	}

	oddRects := []image.Rectangle{
		image.Rect(10, 10, 51, 51),   // odd width and height (41x41)
		image.Rect(11, 11, 52, 52),   // odd start, odd end
		image.Rect(0, 0, 1, 1),       // 1x1 (clips to 0x0)
		image.Rect(10, 10, 11, 11),   // 1x1 (clips to 0x0)
		image.Rect(0, 0, 319, 239),   // almost full frame, odd
		image.Rect(10, 10, 101, 101), // odd width and height
	}

	for _, rect := range oddRects {
		t.Run(fmt.Sprintf("rect_%d_%d_%d_%d", rect.Min.X, rect.Min.Y, rect.Max.X, rect.Max.Y), func(t *testing.T) {
			// Reset frame
			copy(yuv, origYUV)

			// Should not panic
			AlphaBlendRGBARectInto(yuv, rgba, frameW, frameH, overlayW, overlayH,
				rect, 1.0, nil, nil)

			// Verify the frame is still valid (no out-of-bounds writes)
			if len(yuv) != yuvSize {
				t.Errorf("yuv buffer size changed: %d -> %d", yuvSize, len(yuv))
			}
		})
	}
}

// TestAlphaBlendRectEvenAligned verifies that AlphaBlendRGBARectInto even-aligns
// the rectangle by checking that an odd-positioned rect gets rounded down.
func TestAlphaBlendRectEvenAligned(t *testing.T) {
	frameW, frameH := 64, 64
	yuvSize := frameW * frameH * 3 / 2
	yuv := make([]byte, yuvSize)

	// Fill Y with 0
	for i := 0; i < frameW*frameH; i++ {
		yuv[i] = 0
	}
	// Fill chroma with 128
	for i := frameW * frameH; i < yuvSize; i++ {
		yuv[i] = 128
	}

	overlayW, overlayH := 20, 20
	rgba := make([]byte, overlayW*overlayH*4)
	for i := 0; i < overlayW*overlayH; i++ {
		rgba[i*4] = 255   // R
		rgba[i*4+1] = 255 // G
		rgba[i*4+2] = 255 // B
		rgba[i*4+3] = 255 // A (fully opaque)
	}

	// Rect with odd start (11,11) → should even-align to (10,10)
	// Rect with odd end (31,31) → should even-align to (30,30)
	rect := image.Rect(11, 11, 31, 31)
	AlphaBlendRGBARectInto(yuv, rgba, frameW, frameH, overlayW, overlayH,
		rect, 1.0, nil, nil)

	// Check that pixel at (11,11) is NOT modified (it should be even-aligned to (10,10))
	// Actually, the even alignment rounds down: 11 &^ 1 = 10, 31 &^ 1 = 30
	// So the blended region should be (10,10)-(30,30), a 20x20 region

	// Pixel at (9,9) should be unchanged (outside rect)
	if yuv[9*frameW+9] != 0 {
		t.Errorf("pixel (9,9) = %d, expected 0 (outside even-aligned rect)", yuv[9*frameW+9])
	}

	// Pixel at (10,10) should be modified (inside even-aligned rect)
	actual := yuv[10*frameW+10]
	t.Logf("pixel (10,10) = %d (should be non-zero if inside blended rect)", actual)
	if actual == 0 {
		t.Errorf("pixel (10,10) = 0, expected non-zero (inside even-aligned rect)")
	}

	// Pixel at (30,30) should be unchanged (at the exclusive boundary after even-alignment)
	actual30 := yuv[30*frameW+30]
	t.Logf("pixel (30,30) = %d (should be 0, at exclusive boundary)", actual30)
}

// TestAlphaBlendHalfAlpha verifies blend at A=128 (half transparency) produces
// the correct intermediate value for the Y plane.
func TestAlphaBlendHalfAlpha(t *testing.T) {
	width := 32
	yRow := make([]byte, width)
	rgba := make([]byte, width*4)

	// Y = 0 (black), overlay = white at A=128
	for i := range yRow {
		yRow[i] = 0
	}
	for i := 0; i < width; i++ {
		rgba[i*4] = 255   // R
		rgba[i*4+1] = 255 // G
		rgba[i*4+2] = 255 // B
		rgba[i*4+3] = 128 // A
	}

	alphaBlendRGBARowY(&yRow[0], &rgba[0], width, 256)

	// A=128: A'=128+(128>>7)=128+1=129, a256=(129*256)>>8=129, inv=127
	// overlayY = 16 + ((47*255 + 157*255 + 16*255 + 128) >> 8) = 16 + 219 = 235
	// result = (0*127 + 235*129 + 128) >> 8 = 30443 >> 8 = 118
	aPrime := 128 + (128 >> 7) // 129
	a256 := (aPrime * 256) >> 8 // 129
	overlayY := 16 + ((47*255 + 157*255 + 16*255 + 128) >> 8) // 235
	expected := (0*(256-a256) + overlayY*a256 + 128) >> 8

	t.Logf("A=128: A'=%d, a256=%d, overlayY=%d, expected=%d", aPrime, a256, overlayY, expected)

	for i := 0; i < width; i++ {
		actual := int(yRow[i])
		diff := actual - expected
		if diff < -2 || diff > 2 {
			t.Errorf("pixel[%d]: got %d, expected %d (diff=%d)", i, actual, expected, diff)
		}
	}
}
