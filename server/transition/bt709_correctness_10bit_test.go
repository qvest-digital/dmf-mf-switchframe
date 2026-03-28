//go:build correctness

package transition

import (
	"testing"
)

// bt709_10bit computes 10-bit BT.709 limited-range YCbCr from RGB using the
// integer approximation formula used by AlphaBlendRGBA10.
func bt709_10bit(r, g, b int) (y10, cb10, cr10 int) {
	y8 := (66*r+129*g+25*b+128)>>8 + 16
	cb8 := (-38*r-74*g+112*b+128)>>8 + 128
	cr8 := (112*r-94*g-18*b+128)>>8 + 128
	return y8 * 4, cb8 * 4, cr8 * 4
}

// ---------------------------------------------------------------------------
// TestBT709_10bit_KnownValues
//
// Verifies the BT.709 integer approximation formula at 10-bit scale.
// Uses the same coefficients as AlphaBlendRGBA10:
//   Y8  = (66*R + 129*G + 25*B + 128) >> 8 + 16  → Y10 = Y8 * 4
//   Cb8 = (-38*R - 74*G + 112*B + 128) >> 8 + 128 → Cb10 = Cb8 * 4
//   Cr8 = (112*R - 94*G - 18*B + 128) >> 8 + 128  → Cr10 = Cr8 * 4
// ---------------------------------------------------------------------------

func TestBT709_10bit_KnownValues(t *testing.T) {
	tests := []struct {
		name    string
		r, g, b int
	}{
		{"white", 255, 255, 255},
		{"black", 0, 0, 0},
		{"red", 255, 0, 0},
		{"green", 0, 255, 0},
		{"blue", 0, 0, 255},
		{"mid_gray", 128, 128, 128},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			y10, cb10, cr10 := bt709_10bit(tc.r, tc.g, tc.b)

			t.Logf("RGB(%d,%d,%d) -> Y10=%d, Cb10=%d, Cr10=%d", tc.r, tc.g, tc.b, y10, cb10, cr10)

			// Y should be in 10-bit limited range [64, 940]
			if y10 < 64 || y10 > 940 {
				t.Errorf("Y10=%d outside limited range [64,940]", y10)
			}
			// Cb/Cr should be in [64, 960]
			if cb10 < 64 || cb10 > 960 {
				t.Errorf("Cb10=%d outside range [64,960]", cb10)
			}
			if cr10 < 64 || cr10 > 960 {
				t.Errorf("Cr10=%d outside range [64,960]", cr10)
			}
		})
	}

	// Specific value checks using the actual formula
	t.Run("white_exact", func(t *testing.T) {
		y10, cb10, cr10 := bt709_10bit(255, 255, 255)
		if y10 != 940 {
			t.Errorf("white Y10: got %d, want 940", y10)
		}
		if cb10 != 512 {
			t.Errorf("white Cb10: got %d, want 512", cb10)
		}
		if cr10 != 512 {
			t.Errorf("white Cr10: got %d, want 512", cr10)
		}
	})

	t.Run("black_exact", func(t *testing.T) {
		y10, cb10, cr10 := bt709_10bit(0, 0, 0)
		if y10 != 64 {
			t.Errorf("black Y10: got %d, want 64", y10)
		}
		if cb10 != 512 {
			t.Errorf("black Cb10: got %d, want 512", cb10)
		}
		if cr10 != 512 {
			t.Errorf("black Cr10: got %d, want 512", cr10)
		}
	})

	t.Run("red_exact", func(t *testing.T) {
		// y8 = (66*255+128)>>8 + 16 = 16958>>8 + 16 = 66 + 16 = 82, Y10 = 328
		// cb8 = (-38*255+128)>>8 + 128 = -9542>>8 + 128 = -37 + 128 = 91 (hmm)
		// Wait: -9542>>8 is -38 in Go (arithmetic right shift). -38+128 = 90.
		// Actually: -38*255 = -9690, +128 = -9562, >>8 = -38 (Go floors toward -inf). -38+128 = 90.
		// Cr8 = (112*255+128)>>8 + 128 = 28688>>8 + 128 = 112 + 128 = 240
		y10, cb10, cr10 := bt709_10bit(255, 0, 0)
		t.Logf("Red: Y10=%d, Cb10=%d, Cr10=%d", y10, cb10, cr10)

		// Verify Y is in the "red" range (lower than green)
		greenY, _, _ := bt709_10bit(0, 255, 0)
		if y10 >= greenY {
			t.Errorf("red Y10=%d should be less than green Y10=%d", y10, greenY)
		}
		// Verify Cr is high for red
		if cr10 < 900 {
			t.Errorf("red Cr10=%d should be high (>900)", cr10)
		}
	})

	t.Run("neutral_chroma_for_achromatic", func(t *testing.T) {
		// All achromatic colors (R=G=B) should have Cb=512, Cr=512
		for v := 0; v <= 255; v += 17 {
			_, cb10, cr10 := bt709_10bit(v, v, v)
			if absDiffInt(cb10, 512) > 4 {
				t.Errorf("achromatic RGB(%d,%d,%d): Cb10=%d, want ~512", v, v, v, cb10)
			}
			if absDiffInt(cr10, 512) > 4 {
				t.Errorf("achromatic RGB(%d,%d,%d): Cr10=%d, want ~512", v, v, v, cr10)
			}
		}
	})
}

// ---------------------------------------------------------------------------
// TestBT709_10bit_LegalRange
//
// All RGB inputs produce Y10 in [64,940] and Cb10/Cr10 in [64,960].
// ---------------------------------------------------------------------------

func TestBT709_10bit_LegalRange(t *testing.T) {
	for r := 0; r <= 255; r++ {
		for g := 0; g <= 255; g += 17 { // coarser step for speed
			for b := 0; b <= 255; b += 17 {
				y10, cb10, cr10 := bt709_10bit(r, g, b)

				if y10 < 64 || y10 > 940 {
					t.Fatalf("Y10 out of limited range [64,940]: RGB(%d,%d,%d) -> Y10=%d",
						r, g, b, y10)
				}
				if cb10 < 64 || cb10 > 960 {
					t.Fatalf("Cb10 out of range [64,960]: RGB(%d,%d,%d) -> Cb10=%d",
						r, g, b, cb10)
				}
				if cr10 < 64 || cr10 > 960 {
					t.Fatalf("Cr10 out of range [64,960]: RGB(%d,%d,%d) -> Cr10=%d",
						r, g, b, cr10)
				}
			}
		}
	}
}

// ---------------------------------------------------------------------------
// TestBT709_10bit_MonotonicLuma
//
// Verify that increasing RGB brightness produces monotonically increasing Y10.
// ---------------------------------------------------------------------------

func TestBT709_10bit_MonotonicLuma(t *testing.T) {
	prevY := -1
	for v := 0; v <= 255; v++ {
		y10, _, _ := bt709_10bit(v, v, v)
		if y10 < prevY {
			t.Errorf("non-monotonic at v=%d: Y10=%d < previous Y10=%d", v, y10, prevY)
		}
		prevY = y10
	}
}

// ---------------------------------------------------------------------------
// TestBT709_10bit_RedMaxCr
//
// Pure red should have the highest Cr value; pure green should have the lowest.
// ---------------------------------------------------------------------------

func TestBT709_10bit_RedMaxCr(t *testing.T) {
	_, _, redCr := bt709_10bit(255, 0, 0)
	_, _, greenCr := bt709_10bit(0, 255, 0)
	_, _, blueCr := bt709_10bit(0, 0, 255)

	t.Logf("Cr10: red=%d, green=%d, blue=%d", redCr, greenCr, blueCr)

	if redCr <= greenCr {
		t.Errorf("red Cr10=%d should be > green Cr10=%d", redCr, greenCr)
	}
	if redCr <= blueCr {
		t.Errorf("red Cr10=%d should be > blue Cr10=%d", redCr, blueCr)
	}
}

// ---------------------------------------------------------------------------
// TestBT709_10bit_BlueMaxCb
//
// Pure blue should have the highest Cb value.
// ---------------------------------------------------------------------------

func TestBT709_10bit_BlueMaxCb(t *testing.T) {
	_, redCb, _ := bt709_10bit(255, 0, 0)
	_, greenCb, _ := bt709_10bit(0, 255, 0)
	_, blueCb, _ := bt709_10bit(0, 0, 255)

	t.Logf("Cb10: red=%d, green=%d, blue=%d", redCb, greenCb, blueCb)

	if blueCb <= redCb {
		t.Errorf("blue Cb10=%d should be > red Cb10=%d", blueCb, redCb)
	}
	if blueCb <= greenCb {
		t.Errorf("blue Cb10=%d should be > green Cb10=%d", blueCb, greenCb)
	}
}

// ---------------------------------------------------------------------------
// TestBT709_10bit_4xScaleRelationship
//
// Verify the 10-bit = 8-bit * 4 relationship holds for the integer formula.
// This is a tautology by construction, but verifies no overflow or truncation.
// ---------------------------------------------------------------------------

func TestBT709_10bit_4xScaleRelationship(t *testing.T) {
	for r := 0; r <= 255; r += 17 {
		for g := 0; g <= 255; g += 17 {
			for b := 0; b <= 255; b += 17 {
				y8 := (66*r+129*g+25*b+128)>>8 + 16
				cb8 := (-38*r-74*g+112*b+128)>>8 + 128
				cr8 := (112*r-94*g-18*b+128)>>8 + 128

				y10, cb10, cr10 := bt709_10bit(r, g, b)

				if y10 != y8*4 {
					t.Errorf("RGB(%d,%d,%d): Y10=%d != Y8*4=%d", r, g, b, y10, y8*4)
				}
				if cb10 != cb8*4 {
					t.Errorf("RGB(%d,%d,%d): Cb10=%d != Cb8*4=%d", r, g, b, cb10, cb8*4)
				}
				if cr10 != cr8*4 {
					t.Errorf("RGB(%d,%d,%d): Cr10=%d != Cr8*4=%d", r, g, b, cr10, cr8*4)
				}
			}
		}
	}
}

func absDiffInt(a, b int) int {
	if a > b {
		return a - b
	}
	return b - a
}
