//go:build correctness

package graphics

import (
	"fmt"
	"testing"
)

// TestChromaKeyBoundarySizes runs chroma key mask generation at chroma-resolution
// sizes n=1 through n=33. Tests two extremes:
//   - Pure green (Cb=54, Cr=34 in BT.709): every mask byte must be 0 (transparent)
//   - Pure red (Cb=103, Cr=240 in BT.709): every mask byte must be 255 (opaque)
//
// This exercises the SIMD kernel (chromaKeyMaskChroma) at sizes that may not
// align to SIMD register widths (16 bytes for NEON, various for SSE/AVX).
func TestChromaKeyBoundarySizes(t *testing.T) {
	// BT.709 green key color: Y~145, Cb~54, Cr~34
	keyColor := YCbCr{Y: 145, Cb: 54, Cr: 34}
	similarity := float32(0.3)
	smoothness := float32(0.1)

	// Precompute thresholds (same as ChromaKeyWithSpillColorInto)
	simDist := similarity * 181.0
	smoothDist := smoothness * 181.0
	simDistSq := int(simDist * simDist)
	totalDist := simDist + smoothDist
	totalDistSq := int(totalDist * totalDist)
	rangeSq := totalDistSq - simDistSq
	invRange := 0
	if rangeSq > 0 {
		invRange = 255 * 65536 / rangeSq
	}

	t.Run("pure_green_transparent", func(t *testing.T) {
		var failedSizes []int
		for n := 1; n <= 33; n++ {
			mask := make([]byte, n)
			cbPlane := make([]byte, n)
			crPlane := make([]byte, n)

			// Fill with key color chroma values (should be keyed out = transparent = 0)
			for i := range cbPlane {
				cbPlane[i] = keyColor.Cb
				crPlane[i] = keyColor.Cr
			}

			chromaKeyMaskChroma(&mask[0], &cbPlane[0], &crPlane[0],
				int(keyColor.Cb), int(keyColor.Cr), simDistSq, totalDistSq, invRange, n)

			for i := 0; i < n; i++ {
				if mask[i] != 0 {
					t.Logf("pure green n=%d: mask[%d] = %d, expected 0 (transparent)", n, i, mask[i])
					failedSizes = append(failedSizes, n)
					break
				}
			}
		}
		if len(failedSizes) > 0 {
			t.Errorf("pure green mask non-zero at %d sizes: %v", len(failedSizes), failedSizes)
		}
	})

	t.Run("pure_red_opaque", func(t *testing.T) {
		var failedSizes []int
		for n := 1; n <= 33; n++ {
			mask := make([]byte, n)
			cbPlane := make([]byte, n)
			crPlane := make([]byte, n)

			// BT.709 red: Cb~103, Cr~240
			// Distance from green key: dCb=103-54=49, dCr=240-34=206
			// distSq = 49*49 + 206*206 = 2401 + 42436 = 44837
			// This should be well above totalDistSq for typical settings
			for i := range cbPlane {
				cbPlane[i] = 103
				crPlane[i] = 240
			}

			chromaKeyMaskChroma(&mask[0], &cbPlane[0], &crPlane[0],
				int(keyColor.Cb), int(keyColor.Cr), simDistSq, totalDistSq, invRange, n)

			for i := 0; i < n; i++ {
				if mask[i] != 255 {
					t.Logf("pure red n=%d: mask[%d] = %d, expected 255 (opaque)", n, i, mask[i])
					failedSizes = append(failedSizes, n)
					break
				}
			}
		}
		if len(failedSizes) > 0 {
			t.Errorf("pure red mask not 255 at %d sizes: %v", len(failedSizes), failedSizes)
		}
	})
}

// TestLumaKeyBoundarySizes runs lumaKeyMaskLUT at sizes n=1 through n=17.
// Creates a LUT where Y<128 → mask=255 (opaque), Y>=128 → mask=0 (transparent).
// Feeds known Y values and verifies every output matches the LUT.
func TestLumaKeyBoundarySizes(t *testing.T) {
	// Build LUT: Y<128 → 255, Y>=128 → 0
	var lut [256]byte
	for y := 0; y < 128; y++ {
		lut[y] = 255
	}
	for y := 128; y < 256; y++ {
		lut[y] = 0
	}

	var failedSizes []int
	for n := 1; n <= 17; n++ {
		t.Run(fmt.Sprintf("n_%d", n), func(t *testing.T) {
			mask := make([]byte, n)
			yPlane := make([]byte, n)

			// Alternate Y values: even indices get Y=50 (→255), odd get Y=200 (→0)
			for i := range yPlane {
				if i%2 == 0 {
					yPlane[i] = 50
				} else {
					yPlane[i] = 200
				}
			}

			lumaKeyMaskLUT(&mask[0], &yPlane[0], &lut[0], n)

			for i := 0; i < n; i++ {
				expected := lut[yPlane[i]]
				if mask[i] != expected {
					t.Logf("n=%d, byte[%d]: Y=%d, got mask=%d, expected %d",
						n, i, yPlane[i], mask[i], expected)
					failedSizes = append(failedSizes, n)
					t.Errorf("n=%d: mask mismatch at byte %d", n, i)
					return
				}
			}
		})
	}
	if len(failedSizes) > 0 {
		t.Errorf("lumaKeyMaskLUT failed at sizes: %v", failedSizes)
	}
}

// TestLumaKeyBoundarySizesAllValues tests lumaKeyMaskLUT with all 256 Y values
// at various sizes to ensure the LUT lookup works correctly for every entry.
func TestLumaKeyBoundarySizesAllValues(t *testing.T) {
	// LUT: identity mapping (mask[y] = y)
	var lut [256]byte
	for y := 0; y < 256; y++ {
		lut[y] = byte(y)
	}

	sizes := []int{1, 2, 3, 4, 7, 8, 9, 15, 16, 17}
	for _, n := range sizes {
		t.Run(fmt.Sprintf("n_%d", n), func(t *testing.T) {
			mask := make([]byte, n)
			yPlane := make([]byte, n)

			// Fill with sequential Y values wrapping at 256
			for i := range yPlane {
				yPlane[i] = byte(i * 37 & 0xFF)
			}

			lumaKeyMaskLUT(&mask[0], &yPlane[0], &lut[0], n)

			for i := 0; i < n; i++ {
				expected := yPlane[i] // identity LUT
				if mask[i] != expected {
					t.Errorf("n=%d, byte[%d]: Y=%d, got mask=%d, expected %d",
						n, i, yPlane[i], mask[i], expected)
				}
			}
		})
	}
}

// TestExpandChromaMaskBoundary tests expandChromaMaskRow at chromaWidth=1 through 8.
// Each input byte should appear twice in output. Verifies output length = 2*chromaWidth
// and values are correct.
func TestExpandChromaMaskBoundary(t *testing.T) {
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

			expandChromaMaskRow(&dst[0], &src[0], chromaWidth)

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

// TestExpandChromaMaskBoundaryLarge tests expandChromaMaskRow at sizes near NEON
// register boundaries (16, 32) to catch SIMD tail handling bugs.
func TestExpandChromaMaskBoundaryLarge(t *testing.T) {
	sizes := []int{15, 16, 17, 31, 32, 33, 47, 48, 49, 63, 64, 65}

	for _, chromaWidth := range sizes {
		t.Run(fmt.Sprintf("chromaWidth_%d", chromaWidth), func(t *testing.T) {
			src := make([]byte, chromaWidth)
			dst := make([]byte, chromaWidth*2)

			for i := range src {
				src[i] = byte((i*7 + 13) & 0xFF)
			}

			expandChromaMaskRow(&dst[0], &src[0], chromaWidth)

			for i := 0; i < chromaWidth; i++ {
				if dst[i*2] != src[i] || dst[i*2+1] != src[i] {
					t.Errorf("chromaWidth=%d, pair[%d]: got (%d, %d), expected (%d, %d)",
						chromaWidth, i, dst[i*2], dst[i*2+1], src[i], src[i])
				}
			}
		})
	}
}

// TestChromaKeyFullFrameBoundary runs the full ChromaKey pipeline at small even
// dimensions to exercise all kernel paths including mask expansion.
func TestChromaKeyFullFrameBoundary(t *testing.T) {
	keyColor := YCbCr{Y: 145, Cb: 54, Cr: 34}

	dims := []struct{ w, h int }{
		{2, 2},
		{4, 4},
		{6, 6},
		{16, 2},
		{2, 16},
		{18, 18},
		{32, 32},
	}

	for _, d := range dims {
		t.Run(fmt.Sprintf("%dx%d_green", d.w, d.h), func(t *testing.T) {
			ySize := d.w * d.h
			uvSize := (d.w / 2) * (d.h / 2)
			frame := make([]byte, ySize+2*uvSize)

			// Fill with green key color
			for i := 0; i < ySize; i++ {
				frame[i] = keyColor.Y
			}
			for i := 0; i < uvSize; i++ {
				frame[ySize+i] = keyColor.Cb
				frame[ySize+uvSize+i] = keyColor.Cr
			}

			mask := ChromaKey(frame, d.w, d.h, keyColor, 0.3, 0.1, 0.0)

			if len(mask) != ySize {
				t.Fatalf("mask length = %d, expected %d", len(mask), ySize)
			}

			// All pixels should be transparent (mask=0) for green on green key
			var nonZero int
			for i := 0; i < ySize; i++ {
				if mask[i] != 0 {
					if nonZero < 5 {
						t.Logf("mask[%d] = %d, expected 0", i, mask[i])
					}
					nonZero++
				}
			}
			if nonZero > 0 {
				t.Errorf("%dx%d: %d/%d pixels non-zero (expected all transparent for green key)",
					d.w, d.h, nonZero, ySize)
			}
		})
	}
}
