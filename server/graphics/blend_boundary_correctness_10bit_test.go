//go:build correctness

package graphics

import (
	"encoding/binary"
	"fmt"
	"testing"
)

// make10bitTestFrame creates a YUV422P10LE frame filled with uniform Y/Cb/Cr values.
// Layout: Y plane (w*h*2) + Cb plane (w/2*h*2) + Cr plane (w/2*h*2) = w*h*4 bytes.
func make10bitTestFrame(w, h int, y, cb, cr uint16) []byte {
	buf := make([]byte, w*h*4)
	ySize := w * h
	chromaW := w / 2
	chromaSize := chromaW * h

	for i := 0; i < ySize; i++ {
		binary.LittleEndian.PutUint16(buf[i*2:], y)
	}
	cbOff := ySize * 2
	for i := 0; i < chromaSize; i++ {
		binary.LittleEndian.PutUint16(buf[cbOff+i*2:], cb)
	}
	crOff := cbOff + chromaSize*2
	for i := 0; i < chromaSize; i++ {
		binary.LittleEndian.PutUint16(buf[crOff+i*2:], cr)
	}
	return buf
}

// readY10At reads the Y sample at luma pixel position (x,y).
func readY10At(buf []byte, x, y, w int) uint16 {
	return binary.LittleEndian.Uint16(buf[(y*w+x)*2:])
}

// readCb10At reads the Cb sample at chroma position (cx,cy).
func readCb10At(buf []byte, cx, cy, w, h int) uint16 {
	ySize := w * h * 2
	chromaW := w / 2
	return binary.LittleEndian.Uint16(buf[ySize+(cy*chromaW+cx)*2:])
}

// readCr10At reads the Cr sample at chroma position (cx,cy).
func readCr10At(buf []byte, cx, cy, w, h int) uint16 {
	ySize := w * h * 2
	chromaW := w / 2
	chromaSize := chromaW * h
	return binary.LittleEndian.Uint16(buf[ySize+chromaSize*2+(cy*chromaW+cx)*2:])
}

// ---------------------------------------------------------------------------
// TestAlphaBlendRGBA10_BoundarySizes
//
// Mirror of TestAlphaBlendRowBoundarySizes for 10-bit.
// Alpha blend at even widths 2,4,6,8,16,32 with an opaque red overlay on
// a Y=512 background. Verify Y changes to expected BT.709 10-bit red Y value.
// ---------------------------------------------------------------------------

func TestAlphaBlendRGBA10_BoundarySizes(t *testing.T) {
	// BT.709: Y8 = (66*255 + 129*0 + 25*0 + 128) >> 8 + 16 = (16958) >> 8 + 16 = 66 + 16 - 1 ≈ 82
	// Actually: (66*255 + 128) >> 8 + 16 = (16958) >> 8 + 16 = 66 + 16 = 82
	// Wait: (66*255) = 16830, +128 = 16958, >> 8 = 66 (integer), + 16 = 82
	// But: 66*255 = 16830, not 16958. 16830+128=16958. 16958>>8 = 66. 66+16 = 82.
	// Hmm that doesn't match. Let me recalculate:
	// y8 = (66*R + 129*G + 25*B + 128) >> 8 + 16
	// For red (R=255, G=0, B=0): y8 = (66*255 + 0 + 0 + 128) >> 8 + 16 = 16958 >> 8 + 16 = 66 + 16 = 82
	// Hmm, but RGBToYUV_BT709Limited gives Y≈63 for red. That's a different formula.
	// The AlphaBlendRGBA10 code uses:
	//   y8 = (66*R + 129*G + 25*B + 128) >> 8 + 16 → Y10 = y8 * 4
	// For red: y8 = (16830 + 128) >> 8 + 16 = 66 + 16 = 82, Y10 = 328
	// But wait — with full alpha=255: result = (overlayY*255 + existingY*0 + 128) >> 8
	// = (328*255 + 128) >> 8 = 83768 >> 8 = 327
	// The /255 approximation causes a slight error.

	// For this test, use white overlay (255,255,255) A=255 for clearer math.
	// White: y8 = (66*255+129*255+25*255+128)>>8+16 = (56100+128)>>8+16 = 219+16 = 235
	// Y10 = 235*4 = 940
	// Result: (940*255 + existingY*0 + 128) >> 8 = (239700+128) >> 8 = 239828 >> 8 = 936
	// Hmm, /255 approximation: 940*255/255 = 940, but (940*255+128)>>8 = 936.
	// This is the well-known /256 vs /255 issue.

	// Use alpha=255: the existing code uses inv = 255 - a = 0
	// result = (overlayY * a + existingY * inv + 128) >> 8
	//        = (overlayY * 255 + 0 + 128) >> 8

	var failedWidths []int
	for _, width := range []int{2, 4, 6, 8, 16, 32} {
		t.Run(fmt.Sprintf("width_%d", width), func(t *testing.T) {
			h := 2
			yuv := make10bitTestFrame(width, h, 512, 512, 512) // Y=512, neutral chroma

			// Create opaque white RGBA overlay
			rgbaSize := width * h * 4
			rgba := make([]byte, rgbaSize)
			for i := 0; i < width*h; i++ {
				rgba[i*4+0] = 255 // R
				rgba[i*4+1] = 255 // G
				rgba[i*4+2] = 255 // B
				rgba[i*4+3] = 255 // A (fully opaque)
			}

			err := AlphaBlendRGBA10(yuv, rgba, width, h)
			if err != nil {
				t.Fatalf("AlphaBlendRGBA10: %v", err)
			}

			// White overlay: Y8 = 235, Y10 = 940
			// With A=255: result = (940*255 + 512*0 + 128) >> 8 = 239828 >> 8 = 936
			expectedY := (940*255 + 128) >> 8

			for py := 0; py < h; py++ {
				for px := 0; px < width; px++ {
					gotY := int(readY10At(yuv, px, py, width))
					diff := gotY - expectedY
					if diff < -4 || diff > 4 {
						t.Errorf("width=%d, pixel(%d,%d): got Y=%d, expected ~%d (diff=%d)",
							width, px, py, gotY, expectedY, diff)
						failedWidths = append(failedWidths, width)
						return
					}
				}
			}
		})
	}
	if len(failedWidths) > 0 {
		t.Logf("Failed widths: %v", failedWidths)
	}
}

// ---------------------------------------------------------------------------
// TestAlphaBlendRGBA10_AlphaZeroPassthrough
//
// A=0 overlay leaves 10-bit frame unchanged (byte-exact).
// ---------------------------------------------------------------------------

func TestAlphaBlendRGBA10_AlphaZeroPassthrough(t *testing.T) {
	widths := []int{2, 4, 8, 16, 32, 64}

	for _, width := range widths {
		t.Run(fmt.Sprintf("width_%d", width), func(t *testing.T) {
			h := 4
			// Fill frame with a distinctive pattern
			yuv := make([]byte, width*h*4)
			ySize := width * h
			chromaW := width / 2
			chromaSize := chromaW * h
			for i := 0; i < ySize; i++ {
				binary.LittleEndian.PutUint16(yuv[i*2:], uint16((i*37+100)%1024))
			}
			cbOff := ySize * 2
			for i := 0; i < chromaSize; i++ {
				binary.LittleEndian.PutUint16(yuv[cbOff+i*2:], uint16((i*23+300)%1024))
			}
			crOff := cbOff + chromaSize*2
			for i := 0; i < chromaSize; i++ {
				binary.LittleEndian.PutUint16(yuv[crOff+i*2:], uint16((i*41+600)%1024))
			}

			// Save original
			origYUV := make([]byte, len(yuv))
			copy(origYUV, yuv)

			// Transparent RGBA overlay (A=0, non-zero RGB)
			rgbaSize := width * h * 4
			rgba := make([]byte, rgbaSize)
			for i := 0; i < width*h; i++ {
				rgba[i*4+0] = 200 // R (non-zero!)
				rgba[i*4+1] = 150 // G
				rgba[i*4+2] = 100 // B
				rgba[i*4+3] = 0   // A = 0 (transparent)
			}

			err := AlphaBlendRGBA10(yuv, rgba, width, h)
			if err != nil {
				t.Fatalf("AlphaBlendRGBA10: %v", err)
			}

			// Verify byte-exact unchanged
			var mismatches int
			for i := 0; i < len(yuv); i++ {
				if yuv[i] != origYUV[i] {
					if mismatches < 10 {
						t.Logf("byte[%d]: was 0x%02X, now 0x%02X (should be unchanged with A=0)",
							i, origYUV[i], yuv[i])
					}
					mismatches++
				}
			}
			if mismatches > 0 {
				t.Errorf("A=0 passthrough: %d/%d bytes changed (should be byte-exact unchanged)",
					mismatches, len(yuv))
			}
		})
	}
}

// ---------------------------------------------------------------------------
// TestAlphaBlendRGBA10_FullyOpaque
//
// A=255 overlay replaces frame completely (within /256 approximation error).
// ---------------------------------------------------------------------------

func TestAlphaBlendRGBA10_FullyOpaque(t *testing.T) {
	w, h := 16, 4
	yuv := make10bitTestFrame(w, h, 64, 512, 512) // start with black

	// Opaque green overlay
	rgba := make([]byte, w*h*4)
	for i := 0; i < w*h; i++ {
		rgba[i*4+0] = 0   // R
		rgba[i*4+1] = 255 // G
		rgba[i*4+2] = 0   // B
		rgba[i*4+3] = 255 // A
	}

	err := AlphaBlendRGBA10(yuv, rgba, w, h)
	if err != nil {
		t.Fatalf("AlphaBlendRGBA10: %v", err)
	}

	// Green: Y8 = (66*0+129*255+25*0+128)>>8+16 = (32895+128)>>8+16 = 128+16 = 144 (wait...)
	// 129*255 = 32895, +128 = 33023, >>8 = 128, +16 = 144. But that doesn't seem right...
	// Let me check: RGBToYUV_BT709Limited green Y = 173.
	// The formula in AlphaBlendRGBA10 is different (uses different coefficients):
	// y8 = (66*0 + 129*255 + 25*0 + 128) >> 8 + 16 = (32895 + 128) >> 8 + 16 = 129 + 16 = 145
	// Hmm: 33023 >> 8 = 129 (33023 / 256 = 129.0). So y8 = 129 + 16 = 145. Y10 = 580.
	// After alpha blend with A=255: (580*255 + 64*0 + 128) >> 8 = (147900 + 128) >> 8 = 578
	expectedY := (int(145)*4*255 + 128) >> 8

	gotY := int(readY10At(yuv, 0, 0, w))

	// Allow tolerance for /256 approximation
	diff := gotY - expectedY
	if diff < -4 || diff > 4 {
		t.Errorf("opaque green Y: got %d, expected ~%d (diff=%d)", gotY, expectedY, diff)
	}
}

// ---------------------------------------------------------------------------
// TestAlphaBlendRGBA10_HalfAlpha
//
// A=128 produces intermediate values.
// ---------------------------------------------------------------------------

func TestAlphaBlendRGBA10_HalfAlphaBlend(t *testing.T) {
	w, h := 16, 4
	backgroundY := uint16(64) // black

	yuv := make10bitTestFrame(w, h, backgroundY, 512, 512)

	// Half-transparent white overlay
	rgba := make([]byte, w*h*4)
	for i := 0; i < w*h; i++ {
		rgba[i*4+0] = 255 // R
		rgba[i*4+1] = 255 // G
		rgba[i*4+2] = 255 // B
		rgba[i*4+3] = 128 // A (half)
	}

	err := AlphaBlendRGBA10(yuv, rgba, w, h)
	if err != nil {
		t.Fatalf("AlphaBlendRGBA10: %v", err)
	}

	// White: Y8=235, Y10=940
	// A=128, inv=127
	// result = (940*128 + 64*127 + 128) >> 8 = (120320 + 8128 + 128) >> 8 = 128576 >> 8 = 502
	overlayY10 := 940
	expectedY := (overlayY10*128 + int(backgroundY)*127 + 128) >> 8

	gotY := int(readY10At(yuv, 0, 0, w))
	diff := gotY - expectedY
	t.Logf("half-alpha: got Y=%d, expected ~%d", gotY, expectedY)
	if diff < -4 || diff > 4 {
		t.Errorf("half-alpha Y: got %d, expected ~%d (diff=%d)", gotY, expectedY, diff)
	}

	// Verify it's between background and overlay
	if gotY <= int(backgroundY) || gotY >= overlayY10 {
		t.Errorf("half-alpha Y=%d should be between %d and %d", gotY, backgroundY, overlayY10)
	}
}

// ---------------------------------------------------------------------------
// TestAlphaBlendRGBA10_ChromaFullHeight
//
// Verify alpha blend operates on full-height chroma planes (4:2:2).
// Check the very last chroma row is affected by the overlay.
// ---------------------------------------------------------------------------

func TestAlphaBlendRGBA10_ChromaFullHeight(t *testing.T) {
	w, h := 16, 8
	yuv := make10bitTestFrame(w, h, 512, 512, 512) // neutral

	// Opaque red overlay on entire frame
	rgba := make([]byte, w*h*4)
	for i := 0; i < w*h; i++ {
		rgba[i*4+0] = 255 // R
		rgba[i*4+1] = 0   // G
		rgba[i*4+2] = 0   // B
		rgba[i*4+3] = 255 // A
	}

	err := AlphaBlendRGBA10(yuv, rgba, w, h)
	if err != nil {
		t.Fatalf("AlphaBlendRGBA10: %v", err)
	}

	// Check the very last row of chroma
	// Red Cb8 = (-38*255 + 128) >> 8 + 128 = (-9690+128)>>8+128 = -37+128 = 91
	// Cb10 = 91*4 = 364
	// After alpha blend: (364*255 + 512*0 + 128) >> 8 = (92820+128)>>8 = 363

	lastCb := readCb10At(yuv, 0, h-1, w, h)
	lastCr := readCr10At(yuv, 0, h-1, w, h)

	// The important thing is that the last row was affected (not still neutral 512)
	if lastCb == 512 {
		t.Errorf("last chroma row Cb=512 unchanged - chroma not full height?")
	}
	if lastCr == 512 {
		t.Errorf("last chroma row Cr=512 unchanged - chroma not full height?")
	}
	t.Logf("last row Cb=%d, Cr=%d (should differ from 512 for red overlay)", lastCb, lastCr)
}

// ---------------------------------------------------------------------------
// TestAlphaBlendRGBA10_OddWidthRejected
//
// Verify odd widths are rejected (YUV422 requires even width).
// ---------------------------------------------------------------------------

func TestAlphaBlendRGBA10_OddWidthRejected(t *testing.T) {
	oddWidths := []int{1, 3, 5, 7, 9, 15, 17}
	for _, w := range oddWidths {
		t.Run(fmt.Sprintf("w=%d", w), func(t *testing.T) {
			err := AlphaBlendRGBA10(nil, nil, w, 2)
			if err == nil {
				t.Errorf("AlphaBlendRGBA10(w=%d) should reject odd width", w)
			}
		})
	}
}
