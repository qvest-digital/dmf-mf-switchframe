//go:build correctness

package transition

import (
	"fmt"
	"testing"
)

// TestBlendBoundarySizes exercises BlendUniformBytes at every buffer size from 1
// to 65 at position 0.5 (pos=128). Source A is all 100, source B is all 200.
// Expected output: each byte = (100*128 + 200*128 + 128) >> 8 = 150.
// On SIMD paths that lack the +128 rounding term, the result will be 149.
func TestBlendBoundarySizes(t *testing.T) {
	var failedSizes []int
	for size := 1; size <= 65; size++ {
		a := make([]byte, size)
		b := make([]byte, size)
		dst := make([]byte, size)
		for i := range a {
			a[i] = 100
			b[i] = 200
		}

		BlendUniformBytes(dst, a, b, 128) // pos=128 means 0.5

		for i := 0; i < size; i++ {
			// (100*128 + 200*128) >> 8 = 150 exactly (no rounding needed here)
			// But with +128 rounding: (100*128 + 200*128 + 128) >> 8 = 150
			// Without rounding: also 150 (because 38400/256 = 150.0 exactly)
			// Use values that expose rounding: 101 and 200
			// (101*128 + 200*128) >> 8 = (12928 + 25600) >> 8 = 38528 >> 8 = 150.5 → 150 without rounding, 151 with rounding
			if dst[i] < 149 || dst[i] > 151 {
				t.Errorf("size=%d, byte[%d]: got %d, expected 150 +/-1", size, i, dst[i])
				failedSizes = append(failedSizes, size)
				break
			}
		}
	}

	// Now test with values that specifically expose rounding differences
	t.Run("rounding_sensitive", func(t *testing.T) {
		var roundingFailSizes []int
		for size := 1; size <= 65; size++ {
			a := make([]byte, size)
			b := make([]byte, size)
			dst := make([]byte, size)
			for i := range a {
				a[i] = 101
				b[i] = 200
			}

			BlendUniformBytes(dst, a, b, 128) // pos=128, inv=128

			// With +128 rounding: (101*128 + 200*128 + 128) >> 8 = (12928 + 25600 + 128) >> 8 = 38656 >> 8 = 151
			// Without +128 rounding: (101*128 + 200*128) >> 8 = 38528 >> 8 = 150
			// The generic code uses +128 rounding. SIMD might not.
			expected := byte((101*128 + 200*128 + 128) >> 8)
			for i := 0; i < size; i++ {
				actual := dst[i]
				if actual != expected {
					t.Logf("size=%d, byte[%d]: got %d, expected %d (rounding difference detected)", size, i, actual, expected)
					roundingFailSizes = append(roundingFailSizes, size)
					break
				}
			}
		}
		if len(roundingFailSizes) > 0 {
			t.Errorf("rounding mismatch at %d sizes: first failures at %v", len(roundingFailSizes), roundingFailSizes)
		}
	})

	if len(failedSizes) > 0 {
		t.Errorf("basic blend failed at %d sizes: %v", len(failedSizes), failedSizes)
	}
}

// TestBlendExtremePositions verifies that pos=0 yields exact source A and
// pos=256 yields exact source B, with no tolerance. Tests sizes that are
// likely SIMD boundary values (1, 7, 15, 16, 17, 31, 32, 33, 63, 64, 65).
func TestBlendExtremePositions(t *testing.T) {
	sizes := []int{1, 7, 15, 16, 17, 31, 32, 33, 63, 64, 65}

	for _, size := range sizes {
		t.Run(fmt.Sprintf("size_%d", size), func(t *testing.T) {
			a := make([]byte, size)
			b := make([]byte, size)
			dst := make([]byte, size)
			for i := range a {
				a[i] = byte(50 + i%150)
				b[i] = byte(200 - i%150)
			}

			// pos=0 → all source A
			BlendUniformBytes(dst, a, b, 0)
			for i := 0; i < size; i++ {
				if dst[i] != a[i] {
					t.Errorf("pos=0, size=%d, byte[%d]: got %d, expected %d (exact source A)",
						size, i, dst[i], a[i])
				}
			}

			// pos=256 → all source B
			BlendUniformBytes(dst, a, b, 256)
			for i := 0; i < size; i++ {
				if dst[i] != b[i] {
					t.Errorf("pos=256, size=%d, byte[%d]: got %d, expected %d (exact source B)",
						size, i, dst[i], b[i])
				}
			}
		})
	}
}

// TestBlendFrameOddDimensions verifies FrameBlender.BlendMix at various
// frame dimensions including odd-pixel sizes that stress SIMD alignment.
// Dimensions tested: 2x2, 318x238, 322x242, 1920x1080.
func TestBlendFrameOddDimensions(t *testing.T) {
	dims := []struct {
		w, h int
	}{
		{2, 2},
		{318, 238},
		{322, 242},
		{1920, 1080},
	}

	for _, d := range dims {
		t.Run(fmt.Sprintf("%dx%d", d.w, d.h), func(t *testing.T) {
			fb, err := NewFrameBlender(d.w, d.h)
			if err != nil {
				t.Fatalf("NewFrameBlender(%d,%d): %v", d.w, d.h, err)
			}

			yuvSize := d.w * d.h * 3 / 2
			yuvA := make([]byte, yuvSize)
			yuvB := make([]byte, yuvSize)
			for i := range yuvA {
				yuvA[i] = 80
				yuvB[i] = 200
			}

			result := fb.BlendMix(yuvA, yuvB, 0.5)

			// Verify output buffer size
			if len(result) != yuvSize {
				t.Errorf("output size = %d, expected %d", len(result), yuvSize)
			}

			// Verify blend result: at pos=0.5, pos_int = 128, inv = 128
			// (80*128 + 200*128) >> 8 = 35840 >> 8 = 140 (exact, no rounding issue)
			// With +128: (80*128 + 200*128 + 128) >> 8 = 35968 >> 8 = 140
			// Both should be 140.
			var mismatches int
			for i := 0; i < yuvSize; i++ {
				if result[i] < 139 || result[i] > 141 {
					if mismatches < 10 {
						t.Logf("byte[%d]: got %d, expected 140 +/-1", i, result[i])
					}
					mismatches++
				}
			}
			if mismatches > 0 {
				t.Errorf("%dx%d: %d/%d bytes outside tolerance (expected 140 +/-1)",
					d.w, d.h, mismatches, yuvSize)
			}
		})
	}
}

// TestBlendFrameOddDimensionsRejected verifies that odd dimensions are rejected.
func TestBlendFrameOddDimensionsRejected(t *testing.T) {
	oddDims := []struct {
		w, h int
	}{
		{1, 1},
		{3, 2},
		{2, 3},
		{319, 239},
	}

	for _, d := range oddDims {
		t.Run(fmt.Sprintf("%dx%d", d.w, d.h), func(t *testing.T) {
			_, err := NewFrameBlender(d.w, d.h)
			if err == nil {
				t.Errorf("NewFrameBlender(%d,%d) should have returned error for odd dimensions", d.w, d.h)
			}
		})
	}
}

// TestBlendPositionLinearity verifies that blending at equally spaced positions
// produces linearly interpolated output values. This catches non-linear SIMD bugs.
func TestBlendPositionLinearity(t *testing.T) {
	size := 64
	a := make([]byte, size)
	b := make([]byte, size)
	dst := make([]byte, size)
	for i := range a {
		a[i] = 0
		b[i] = 255
	}

	positions := []int{0, 32, 64, 96, 128, 160, 192, 224, 256}
	for _, pos := range positions {
		BlendUniformBytes(dst, a, b, pos)

		// Expected: (0*inv + 255*pos) >> 8 = (255*pos) >> 8
		// With rounding: (255*pos + 128) >> 8
		expectedNoRound := (255 * pos) >> 8
		expectedRound := (255*pos + 128) >> 8

		actual := int(dst[0])
		t.Logf("pos=%d: actual=%d, expected_no_round=%d, expected_round=%d",
			pos, actual, expectedNoRound, expectedRound)

		// Allow either rounding mode but flag which one is used
		if actual != expectedNoRound && actual != expectedRound {
			t.Errorf("pos=%d: got %d, expected %d (no round) or %d (round)",
				pos, actual, expectedNoRound, expectedRound)
		}
	}
}
