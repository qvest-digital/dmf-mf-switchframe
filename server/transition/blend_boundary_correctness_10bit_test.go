//go:build correctness

package transition

import (
	"encoding/binary"
	"fmt"
	"testing"
)

// ---------------------------------------------------------------------------
// TestBlendMix10_BoundarySizes
//
// Mirror of TestBlendBoundarySizes for 10-bit YUV422P10LE.
// Exercises BlendMix10 at even sizes from 2 to 64. Source A=400, B=800,
// pos=128 → expected Y = (400*128 + 800*128 + 128) >> 8 = 600.
// ---------------------------------------------------------------------------

func TestBlendMix10_BoundarySizes(t *testing.T) {
	var failedSizes []int
	for _, size := range []int{2, 4, 6, 8, 10, 14, 16, 18, 32, 64} {
		t.Run(fmt.Sprintf("size_%d", size), func(t *testing.T) {
			w, h := size, 2 // minimum height=2, width varies (even only)
			a := make10bitFrame(w, h, 400, 512, 512)
			b := make10bitFrame(w, h, 800, 512, 512)
			dst := make([]byte, w*h*4)

			if err := BlendMix10(dst, a, b, w, h, 128); err != nil {
				t.Fatalf("BlendMix10: %v", err)
			}

			// (400*128 + 800*128 + 128) >> 8 = (51200 + 102400 + 128) >> 8 = 600
			for py := 0; py < h; py++ {
				for px := 0; px < w; px++ {
					got := readY10(dst, px, py, w)
					if got < 599 || got > 601 {
						t.Errorf("size=%d, pixel(%d,%d): got Y=%d, expected 600 +/-1", size, px, py, got)
						failedSizes = append(failedSizes, size)
						return
					}
				}
			}
		})
	}
	if len(failedSizes) > 0 {
		t.Errorf("failed at %d sizes: %v", len(failedSizes), failedSizes)
	}
}

// ---------------------------------------------------------------------------
// TestBlendMix10_BoundarySizes_Rounding
//
// Uses values that expose rounding differences between +128 rounding and
// truncation. A=401, B=800, pos=128.
// With +128 rounding: (401*128 + 800*128 + 128) >> 8 = 601
// Without rounding:   (401*128 + 800*128) >> 8 = 600
// ---------------------------------------------------------------------------

func TestBlendMix10_BoundarySizes_Rounding(t *testing.T) {
	var roundingFailSizes []int
	for _, size := range []int{2, 4, 6, 8, 10, 14, 16, 18, 32, 64} {
		w, h := size, 2
		a := make10bitFrame(w, h, 401, 512, 512)
		b := make10bitFrame(w, h, 800, 512, 512)
		dst := make([]byte, w*h*4)

		if err := BlendMix10(dst, a, b, w, h, 128); err != nil {
			t.Fatalf("BlendMix10: %v", err)
		}

		// With +128 rounding: (401*128 + 800*128 + 128) >> 8 = (51328 + 102400 + 128) >> 8 = 601
		expected := uint16((401*128 + 800*128 + 128) >> 8)
		got := readY10(dst, 0, 0, w)
		if got != expected {
			t.Logf("size=%d: got %d, expected %d (rounding difference)", size, got, expected)
			roundingFailSizes = append(roundingFailSizes, size)
		}
	}
	if len(roundingFailSizes) > 0 {
		t.Errorf("rounding mismatch at %d sizes: first failures at %v", len(roundingFailSizes), roundingFailSizes)
	}
}

// ---------------------------------------------------------------------------
// TestBlendMix10_ExtremePositions
//
// pos=0 → byte-exact source A, pos=256 → byte-exact source B.
// Tests sizes that stress SIMD boundaries.
// ---------------------------------------------------------------------------

func TestBlendMix10_ExtremePositions(t *testing.T) {
	sizes := []int{2, 4, 8, 14, 16, 18, 32, 64}

	for _, size := range sizes {
		t.Run(fmt.Sprintf("size_%d", size), func(t *testing.T) {
			w, h := size, 2
			a := make10bitFrame(w, h, 300, 400, 700)
			b := make10bitFrame(w, h, 800, 600, 300)
			dst := make([]byte, w*h*4)

			// pos=0 → 100% A
			if err := BlendMix10(dst, a, b, w, h, 0); err != nil {
				t.Fatalf("BlendMix10 pos=0: %v", err)
			}
			for py := 0; py < h; py++ {
				for px := 0; px < w; px++ {
					got := readY10(dst, px, py, w)
					if got != 300 {
						t.Errorf("pos=0, size=%d, pixel(%d,%d): got Y=%d, expected 300 (exact source A)", size, px, py, got)
					}
				}
			}

			// pos=256 → 100% B
			if err := BlendMix10(dst, a, b, w, h, 256); err != nil {
				t.Fatalf("BlendMix10 pos=256: %v", err)
			}
			for py := 0; py < h; py++ {
				for px := 0; px < w; px++ {
					got := readY10(dst, px, py, w)
					if got != 800 {
						t.Errorf("pos=256, size=%d, pixel(%d,%d): got Y=%d, expected 800 (exact source B)", size, px, py, got)
					}
				}
			}
		})
	}
}

// ---------------------------------------------------------------------------
// TestBlendMix10_PositionLinearity
//
// Sweep pos 0→256. Verify Y increases monotonically from A to B.
// This catches non-linear SIMD bugs.
// ---------------------------------------------------------------------------

func TestBlendMix10_PositionLinearity(t *testing.T) {
	w, h := 16, 2
	a := make10bitFrame(w, h, 0, 512, 512)
	b := make10bitFrame(w, h, 1023, 512, 512)
	dst := make([]byte, w*h*4)

	positions := []int{0, 32, 64, 96, 128, 160, 192, 224, 256}
	prevY := int(-1)

	for _, pos := range positions {
		if err := BlendMix10(dst, a, b, w, h, pos); err != nil {
			t.Fatalf("BlendMix10 pos=%d: %v", pos, err)
		}

		gotY := int(readY10(dst, 0, 0, w))

		// With rounding: (0*inv + 1023*pos + 128) >> 8
		expectedRound := (1023*pos + 128) >> 8
		expectedNoRound := (1023 * pos) >> 8

		t.Logf("pos=%d: actual=%d, expected_no_round=%d, expected_round=%d", pos, gotY, expectedNoRound, expectedRound)

		// Allow either rounding mode
		if gotY != expectedNoRound && gotY != expectedRound {
			t.Errorf("pos=%d: got %d, expected %d (no round) or %d (round)", pos, gotY, expectedNoRound, expectedRound)
		}

		// Monotonic increase
		if gotY < prevY {
			t.Errorf("non-monotonic at pos=%d: got %d < previous %d", pos, gotY, prevY)
		}
		prevY = gotY
	}
}

// ---------------------------------------------------------------------------
// TestBlendFTB10_IsBlack
//
// pos=256 → Y=64, Cb=512, Cr=512 (10-bit BT.709 limited-range black).
// Tests multiple frame sizes.
// ---------------------------------------------------------------------------

func TestBlendFTB10_IsBlack(t *testing.T) {
	sizes := []int{2, 4, 8, 16, 32, 64}

	for _, size := range sizes {
		t.Run(fmt.Sprintf("size_%d", size), func(t *testing.T) {
			w, h := size, 2
			src := make10bitFrame(w, h, 940, 600, 400)
			dst := make([]byte, w*h*4)

			if err := BlendFTB10(dst, src, w, h, 256, true); err != nil {
				t.Fatalf("BlendFTB10: %v", err)
			}

			// Y plane: all samples should be 64 (limited-range black)
			ySize := w * h
			for i := 0; i < ySize; i++ {
				got := binary.LittleEndian.Uint16(dst[i*2:])
				if got != 64 {
					t.Errorf("Y pixel %d: got %d, want 64 (10-bit black)", i, got)
					break
				}
			}

			// Cb plane: all samples should be 512 (neutral chroma)
			chromaW := w / 2
			chromaSize := chromaW * h
			cbOff := ySize * 2
			for i := 0; i < chromaSize; i++ {
				got := binary.LittleEndian.Uint16(dst[cbOff+i*2:])
				if got != 512 {
					t.Errorf("Cb sample %d: got %d, want 512 (neutral chroma)", i, got)
					break
				}
			}

			// Cr plane: all samples should be 512
			crOff := cbOff + chromaSize*2
			for i := 0; i < chromaSize; i++ {
				got := binary.LittleEndian.Uint16(dst[crOff+i*2:])
				if got != 512 {
					t.Errorf("Cr sample %d: got %d, want 512 (neutral chroma)", i, got)
					break
				}
			}
		})
	}
}

// ---------------------------------------------------------------------------
// TestBlendDip10_MidpointIsBlack
//
// pos=128 → Y=64, Cb=512, Cr=512 (midpoint of dip is full black).
// Tests multiple frame sizes.
// ---------------------------------------------------------------------------

func TestBlendDip10_MidpointIsBlack(t *testing.T) {
	sizes := []int{2, 4, 8, 16, 32, 64}

	for _, size := range sizes {
		t.Run(fmt.Sprintf("size_%d", size), func(t *testing.T) {
			w, h := size, 2
			a := make10bitFrame(w, h, 940, 600, 400)
			b := make10bitFrame(w, h, 800, 300, 700)
			dst := make([]byte, w*h*4)

			if err := BlendDip10(dst, a, b, w, h, 128, true); err != nil {
				t.Fatalf("BlendDip10: %v", err)
			}

			// At midpoint: gain = 256 - 128*2 = 0, so result = blackY * 256/256 = blackY
			ySize := w * h
			for i := 0; i < ySize; i++ {
				got := binary.LittleEndian.Uint16(dst[i*2:])
				if got != 64 {
					t.Errorf("Y pixel %d: got %d, want 64 (dip midpoint = black)", i, got)
					break
				}
			}

			// Chroma should be neutral (512)
			chromaW := w / 2
			chromaSize := chromaW * h
			cbOff := ySize * 2
			for i := 0; i < chromaSize; i++ {
				got := binary.LittleEndian.Uint16(dst[cbOff+i*2:])
				if got != 512 {
					t.Errorf("Cb sample %d: got %d, want 512 (dip midpoint = neutral)", i, got)
					break
				}
			}
		})
	}
}

// ---------------------------------------------------------------------------
// TestBlendMix10_FrameOddDimensions
//
// Mirror of TestBlendFrameOddDimensions. Various frame sizes including
// those that stress SIMD alignment. Dimensions must be even for YUV422.
// ---------------------------------------------------------------------------

func TestBlendMix10_FrameOddDimensions(t *testing.T) {
	dims := []struct {
		w, h int
	}{
		{2, 2},
		{4, 4},
		{318, 238},
		{322, 242},
		{1920, 1080},
	}

	for _, d := range dims {
		t.Run(fmt.Sprintf("%dx%d", d.w, d.h), func(t *testing.T) {
			frameSize := d.w * d.h * 4
			a := make10bitFrame(d.w, d.h, 200, 400, 600)
			b := make10bitFrame(d.w, d.h, 800, 600, 400)
			dst := make([]byte, frameSize)

			if err := BlendMix10(dst, a, b, d.w, d.h, 128); err != nil {
				t.Fatalf("BlendMix10(%dx%d): %v", d.w, d.h, err)
			}

			// Output size check
			if len(dst) != frameSize {
				t.Errorf("output size = %d, expected %d", len(dst), frameSize)
			}

			// Y: (200*128 + 800*128 + 128) >> 8 = 500
			var mismatches int
			ySize := d.w * d.h
			for i := 0; i < ySize; i++ {
				got := binary.LittleEndian.Uint16(dst[i*2:])
				if got < 499 || got > 501 {
					if mismatches < 10 {
						t.Logf("Y pixel %d: got %d, expected 500 +/-1", i, got)
					}
					mismatches++
				}
			}
			if mismatches > 0 {
				t.Errorf("%dx%d: %d/%d Y pixels outside tolerance", d.w, d.h, mismatches, ySize)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// TestBlendMix10_OddWidthRejected
//
// Verify that odd widths are rejected (YUV422 requires even width).
// ---------------------------------------------------------------------------

func TestBlendMix10_OddWidthRejected(t *testing.T) {
	oddWidths := []int{1, 3, 5, 7, 9, 15, 17, 319}
	for _, w := range oddWidths {
		t.Run(fmt.Sprintf("w=%d", w), func(t *testing.T) {
			err := BlendMix10(nil, nil, nil, w, 2, 128)
			if err == nil {
				t.Errorf("BlendMix10(w=%d) should reject odd width", w)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// TestBlendFTB10_FullRange_IsZero
//
// pos=256 with limitedRange=false → Y=0, Cb=512, Cr=512 (full-range black).
// ---------------------------------------------------------------------------

func TestBlendFTB10_FullRange_IsZero(t *testing.T) {
	w, h := 16, 4
	src := make10bitFrame(w, h, 940, 600, 400)
	dst := make([]byte, w*h*4)

	if err := BlendFTB10(dst, src, w, h, 256, false); err != nil {
		t.Fatalf("BlendFTB10: %v", err)
	}

	gotY := readY10(dst, 0, 0, w)
	if gotY != 0 {
		t.Errorf("full-range black: got Y=%d, want 0", gotY)
	}
	gotCb := readCb10(dst, 0, 0, w, h)
	if gotCb != 512 {
		t.Errorf("full-range chroma: got Cb=%d, want 512", gotCb)
	}
}

// ---------------------------------------------------------------------------
// TestBlendMix10_ChromaFullHeight
//
// Verify chroma blending operates on full-height planes (4:2:2, not 4:2:0).
// Checks the very last chroma row.
// ---------------------------------------------------------------------------

func TestBlendMix10_ChromaFullHeight(t *testing.T) {
	sizes := []int{4, 8, 16, 32}

	for _, size := range sizes {
		t.Run(fmt.Sprintf("size_%d", size), func(t *testing.T) {
			w, h := size, 8
			a := make10bitFrame(w, h, 500, 300, 700)
			b := make10bitFrame(w, h, 500, 700, 300)
			dst := make([]byte, w*h*4)

			if err := BlendMix10(dst, a, b, w, h, 128); err != nil {
				t.Fatalf("BlendMix10: %v", err)
			}

			// Check chroma at last row (would be out of bounds if using h/2)
			ySize := w * h * 2
			chromaW := w / 2
			lastRowCbOff := ySize + (h-1)*chromaW*2
			lastRowCb := binary.LittleEndian.Uint16(dst[lastRowCbOff:])
			// (300*128 + 700*128 + 128) >> 8 = 500
			if lastRowCb != 500 {
				t.Errorf("size=%d: chroma last row Cb=%d, want 500", size, lastRowCb)
			}
		})
	}
}
