//go:build correctness

package graphics

import (
	"encoding/binary"
	"fmt"
	"testing"
)

// makeChromaFrame10 creates a YUV422P10LE frame with uniform chroma and specified Y.
// Useful for keyer tests where the chroma values determine keying behavior.
func makeChromaFrame10(w, h int, y, cb, cr uint16) []byte {
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

// ---------------------------------------------------------------------------
// TestChromaKey10_BoundarySizes
//
// Mirror of TestChromaKeyBoundarySizes for 10-bit.
// Chroma key at various frame sizes:
//   - Green screen (10-bit chroma ~Cb=350, Cr=170) → keyed (mask=255)
//   - Red (10-bit chroma ~Cb=364, Cr=960) → not keyed (mask=0)
//
// Uses ChromaKey10 which operates on 10-bit chroma values.
// ---------------------------------------------------------------------------

func TestChromaKey10_BoundarySizes(t *testing.T) {
	// 10-bit green chroma values (approximately):
	// BT.709 green: Cb8≈42, Cr8≈26 → Cb10≈168, Cr10≈104
	// Use generous values that are clearly in the green zone
	greenCb10 := uint16(168)
	greenCr10 := uint16(104)

	// Key parameters: target chroma = the green we're looking for
	targetCb := greenCb10
	targetCr := greenCr10
	tolerance := 60 // squared distance threshold
	softness := 20

	dims := []struct{ w, h int }{
		{2, 2},
		{4, 4},
		{6, 6},
		{8, 4},
		{16, 2},
		{32, 2},
		{64, 4},
	}

	t.Run("green_transparent", func(t *testing.T) {
		for _, d := range dims {
			t.Run(fmt.Sprintf("%dx%d", d.w, d.h), func(t *testing.T) {
				// Frame with green chroma (should be keyed = mask value 255)
				yuv := makeChromaFrame10(d.w, d.h, 500, greenCb10, greenCr10)

				mask := ChromaKey10(yuv, d.w, d.h, targetCb, targetCr, tolerance, softness)
				if mask == nil {
					t.Fatal("ChromaKey10 returned nil")
				}

				pixelCount := d.w * d.h
				if len(mask) != pixelCount {
					t.Fatalf("mask length = %d, expected %d", len(mask), pixelCount)
				}

				// All pixels should be keyed (mask=255) since chroma matches target exactly
				var nonKeyed int
				for i := 0; i < pixelCount; i++ {
					if mask[i] != 255 {
						if nonKeyed < 5 {
							t.Logf("mask[%d] = %d, expected 255 (transparent)", i, mask[i])
						}
						nonKeyed++
					}
				}
				if nonKeyed > 0 {
					t.Errorf("%dx%d green: %d/%d pixels not keyed (expected all 255)",
						d.w, d.h, nonKeyed, pixelCount)
				}
			})
		}
	})

	t.Run("red_opaque", func(t *testing.T) {
		// 10-bit red chroma: Cb8≈91, Cr8≈240 → Cb10≈364, Cr10≈960
		// These are far from the green target so should not be keyed
		redCb10 := uint16(364)
		redCr10 := uint16(960)

		for _, d := range dims {
			t.Run(fmt.Sprintf("%dx%d", d.w, d.h), func(t *testing.T) {
				yuv := makeChromaFrame10(d.w, d.h, 300, redCb10, redCr10)

				mask := ChromaKey10(yuv, d.w, d.h, targetCb, targetCr, tolerance, softness)
				if mask == nil {
					t.Fatal("ChromaKey10 returned nil")
				}

				pixelCount := d.w * d.h
				var keyed int
				for i := 0; i < pixelCount; i++ {
					if mask[i] != 0 {
						if keyed < 5 {
							t.Logf("mask[%d] = %d, expected 0 (opaque)", i, mask[i])
						}
						keyed++
					}
				}
				if keyed > 0 {
					t.Errorf("%dx%d red: %d/%d pixels keyed (expected all 0/opaque)",
						d.w, d.h, keyed, pixelCount)
				}
			})
		}
	})
}

// ---------------------------------------------------------------------------
// TestLumaKey10_BoundarySizes
//
// Mirror of TestLumaKeyBoundarySizes for 10-bit.
// LumaKey10 returns mask=255 for Y in [lowThresh, highThresh], mask=0 outside.
// Tests at various frame sizes.
// ---------------------------------------------------------------------------

func TestLumaKey10_BoundarySizes(t *testing.T) {
	lowThresh := uint16(200)
	highThresh := uint16(800)

	dims := []struct{ w, h int }{
		{2, 2},
		{4, 4},
		{8, 4},
		{16, 2},
		{32, 2},
		{64, 4},
	}

	t.Run("below_threshold_keyed", func(t *testing.T) {
		for _, d := range dims {
			t.Run(fmt.Sprintf("%dx%d", d.w, d.h), func(t *testing.T) {
				// Y=100, below lowThresh=200 → keyed out (mask=0)
				yuv := makeChromaFrame10(d.w, d.h, 100, 512, 512)

				mask := LumaKey10(yuv, d.w, d.h, lowThresh, highThresh)
				if mask == nil {
					t.Fatal("LumaKey10 returned nil")
				}

				pixelCount := d.w * d.h
				for i := 0; i < pixelCount; i++ {
					if mask[i] != 0 {
						t.Errorf("pixel %d: mask=%d, expected 0 (Y=%d < lowThresh=%d)", i, mask[i], 100, lowThresh)
						break
					}
				}
			})
		}
	})

	t.Run("in_range_opaque", func(t *testing.T) {
		for _, d := range dims {
			t.Run(fmt.Sprintf("%dx%d", d.w, d.h), func(t *testing.T) {
				// Y=500, between 200 and 800 → opaque (mask=255)
				yuv := makeChromaFrame10(d.w, d.h, 500, 512, 512)

				mask := LumaKey10(yuv, d.w, d.h, lowThresh, highThresh)
				if mask == nil {
					t.Fatal("LumaKey10 returned nil")
				}

				pixelCount := d.w * d.h
				for i := 0; i < pixelCount; i++ {
					if mask[i] != 255 {
						t.Errorf("pixel %d: mask=%d, expected 255 (Y=%d in [%d,%d])", i, mask[i], 500, lowThresh, highThresh)
						break
					}
				}
			})
		}
	})

	t.Run("above_threshold_keyed", func(t *testing.T) {
		for _, d := range dims {
			t.Run(fmt.Sprintf("%dx%d", d.w, d.h), func(t *testing.T) {
				// Y=900, above highThresh=800 → keyed out (mask=0)
				yuv := makeChromaFrame10(d.w, d.h, 900, 512, 512)

				mask := LumaKey10(yuv, d.w, d.h, lowThresh, highThresh)
				if mask == nil {
					t.Fatal("LumaKey10 returned nil")
				}

				pixelCount := d.w * d.h
				for i := 0; i < pixelCount; i++ {
					if mask[i] != 0 {
						t.Errorf("pixel %d: mask=%d, expected 0 (Y=%d > highThresh=%d)", i, mask[i], 900, highThresh)
						break
					}
				}
			})
		}
	})
}

// ---------------------------------------------------------------------------
// TestLumaKey10_ThresholdBoundary
//
// Verify exact boundary behavior: Y == lowThresh and Y == highThresh
// should both be opaque (mask=255, in range inclusive).
// ---------------------------------------------------------------------------

func TestLumaKey10_ThresholdBoundary(t *testing.T) {
	lowThresh := uint16(200)
	highThresh := uint16(800)
	w, h := 8, 2

	// Y exactly at lowThresh → opaque
	yuv := makeChromaFrame10(w, h, lowThresh, 512, 512)
	mask := LumaKey10(yuv, w, h, lowThresh, highThresh)
	if mask[0] != 255 {
		t.Errorf("Y=%d == lowThresh: mask=%d, expected 255 (inclusive)", lowThresh, mask[0])
	}

	// Y exactly at highThresh → opaque
	yuv = makeChromaFrame10(w, h, highThresh, 512, 512)
	mask = LumaKey10(yuv, w, h, lowThresh, highThresh)
	if mask[0] != 255 {
		t.Errorf("Y=%d == highThresh: mask=%d, expected 255 (inclusive)", highThresh, mask[0])
	}

	// Y one below lowThresh → keyed
	yuv = makeChromaFrame10(w, h, lowThresh-1, 512, 512)
	mask = LumaKey10(yuv, w, h, lowThresh, highThresh)
	if mask[0] != 0 {
		t.Errorf("Y=%d < lowThresh: mask=%d, expected 0", lowThresh-1, mask[0])
	}

	// Y one above highThresh → keyed
	yuv = makeChromaFrame10(w, h, highThresh+1, 512, 512)
	mask = LumaKey10(yuv, w, h, lowThresh, highThresh)
	if mask[0] != 0 {
		t.Errorf("Y=%d > highThresh: mask=%d, expected 0", highThresh+1, mask[0])
	}
}

// ---------------------------------------------------------------------------
// TestExpandChromaMask10_Boundary
//
// Each input byte duplicated to 2 output bytes (4:2:2 expansion).
// Tests various chroma widths.
// ---------------------------------------------------------------------------

func TestExpandChromaMask10_Boundary(t *testing.T) {
	for chromaWidth := 1; chromaWidth <= 8; chromaWidth++ {
		t.Run(fmt.Sprintf("chromaWidth_%d", chromaWidth), func(t *testing.T) {
			src := make([]byte, chromaWidth)
			dst := make([]byte, chromaWidth*2)

			// Fill src with distinct values
			for i := range src {
				src[i] = byte(10 + i*30)
			}

			// Sentinel: fill dst with 0xFF to detect writes
			for i := range dst {
				dst[i] = 0xFF
			}

			expandChromaMask10Row(&dst[0], &src[0], chromaWidth)

			// Verify each src byte is duplicated
			for i := 0; i < chromaWidth; i++ {
				if dst[i*2] != src[i] {
					t.Errorf("chromaWidth=%d, dst[%d] = %d, expected %d (from src[%d])",
						chromaWidth, i*2, dst[i*2], src[i], i)
				}
				if dst[i*2+1] != src[i] {
					t.Errorf("chromaWidth=%d, dst[%d] = %d, expected %d (from src[%d])",
						chromaWidth, i*2+1, dst[i*2+1], src[i], i)
				}
			}
		})
	}
}

// ---------------------------------------------------------------------------
// TestExpandChromaMask10_BoundaryLarge
//
// Tests expandChromaMask10Row at sizes near SIMD register boundaries.
// ---------------------------------------------------------------------------

func TestExpandChromaMask10_BoundaryLarge(t *testing.T) {
	sizes := []int{15, 16, 17, 31, 32, 33, 63, 64, 65, 960}

	for _, chromaWidth := range sizes {
		t.Run(fmt.Sprintf("chromaWidth_%d", chromaWidth), func(t *testing.T) {
			src := make([]byte, chromaWidth)
			dst := make([]byte, chromaWidth*2)

			for i := range src {
				src[i] = byte((i*7 + 13) & 0xFF)
			}

			expandChromaMask10Row(&dst[0], &src[0], chromaWidth)

			for i := 0; i < chromaWidth; i++ {
				if dst[i*2] != src[i] || dst[i*2+1] != src[i] {
					t.Errorf("chromaWidth=%d, pair[%d]: got (%d, %d), expected (%d, %d)",
						chromaWidth, i, dst[i*2], dst[i*2+1], src[i], src[i])
				}
			}
		})
	}
}

// ---------------------------------------------------------------------------
// TestChromaKey10_OddWidthRejected
//
// Verify ChromaKey10 returns nil for odd widths.
// ---------------------------------------------------------------------------

func TestChromaKey10_OddWidthRejected(t *testing.T) {
	oddWidths := []int{1, 3, 5, 7, 15}
	for _, w := range oddWidths {
		mask := ChromaKey10(make([]byte, w*4*4), w, 4, 168, 104, 60, 20)
		if mask != nil {
			t.Errorf("ChromaKey10(w=%d) should return nil for odd width, got mask of len %d", w, len(mask))
		}
	}
}

// ---------------------------------------------------------------------------
// TestChromaKey10_MaskExpansion
//
// Verify that the chroma key mask is correctly expanded from chroma resolution
// to luma resolution (each chroma sample covers 2 horizontal luma pixels).
// ---------------------------------------------------------------------------

func TestChromaKey10_MaskExpansion(t *testing.T) {
	w, h := 8, 2
	// Create frame with alternating chroma: green in even chroma columns, red in odd
	yuv := make([]byte, w*h*4)
	ySize := w * h
	chromaW := w / 2

	// Fill Y plane
	for i := 0; i < ySize; i++ {
		binary.LittleEndian.PutUint16(yuv[i*2:], 500)
	}

	// Green chroma target
	targetCb := uint16(168)
	targetCr := uint16(104)

	// Red chroma (far from green)
	redCb := uint16(400)
	redCr := uint16(900)

	cbOff := ySize * 2
	crOff := cbOff + chromaW*h*2

	for py := 0; py < h; py++ {
		for cx := 0; cx < chromaW; cx++ {
			idx := py*chromaW + cx
			if cx%2 == 0 {
				// Green (will be keyed)
				binary.LittleEndian.PutUint16(yuv[cbOff+idx*2:], targetCb)
				binary.LittleEndian.PutUint16(yuv[crOff+idx*2:], targetCr)
			} else {
				// Red (won't be keyed)
				binary.LittleEndian.PutUint16(yuv[cbOff+idx*2:], redCb)
				binary.LittleEndian.PutUint16(yuv[crOff+idx*2:], redCr)
			}
		}
	}

	mask := ChromaKey10(yuv, w, h, targetCb, targetCr, 60, 20)
	if mask == nil {
		t.Fatal("ChromaKey10 returned nil")
	}

	// Check mask expansion: even chroma columns map to luma pairs (0,1), (4,5)
	// Odd chroma columns map to luma pairs (2,3), (6,7)
	for py := 0; py < h; py++ {
		for cx := 0; cx < chromaW; cx++ {
			lx0 := cx * 2
			lx1 := cx*2 + 1
			idx0 := py*w + lx0
			idx1 := py*w + lx1

			if cx%2 == 0 {
				// Green column: should be keyed (mask=255)
				if mask[idx0] != 255 {
					t.Errorf("row %d, green col %d, luma[%d]: mask=%d, expected 255", py, cx, lx0, mask[idx0])
				}
				if mask[idx1] != 255 {
					t.Errorf("row %d, green col %d, luma[%d]: mask=%d, expected 255", py, cx, lx1, mask[idx1])
				}
			} else {
				// Red column: should not be keyed (mask=0)
				if mask[idx0] != 0 {
					t.Errorf("row %d, red col %d, luma[%d]: mask=%d, expected 0", py, cx, lx0, mask[idx0])
				}
				if mask[idx1] != 0 {
					t.Errorf("row %d, red col %d, luma[%d]: mask=%d, expected 0", py, cx, lx1, mask[idx1])
				}
			}
		}
	}
}
