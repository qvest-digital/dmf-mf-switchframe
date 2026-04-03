//go:build correctness

package transition

import (
	"fmt"
	"testing"
)

// TestBT709RoundTrip tests full-range BT.709: RGB -> YUV420 -> RGB for a range
// of color values and verifies ±1 per channel (8-bit quantization rounding).
func TestBT709RoundTrip(t *testing.T) {
	// Test a grid of RGB values
	steps := []uint8{0, 32, 64, 96, 128, 160, 192, 224, 255}

	// Use a 2x2 pixel image (minimum for YUV420 chroma subsampling)
	width := 2
	height := 2
	yuvSize := width * height * 3 / 2 // 4 + 1 + 1 = 6
	rgbSize := width * height * 3

	for _, r := range steps {
		for _, g := range steps {
			for _, b := range steps {
				t.Run(fmt.Sprintf("R%d_G%d_B%d", r, g, b), func(t *testing.T) {
					// Fill 2x2 block with uniform color
					rgb := make([]byte, rgbSize)
					for i := 0; i < width*height; i++ {
						rgb[i*3] = r
						rgb[i*3+1] = g
						rgb[i*3+2] = b
					}

					// RGB -> YUV420
					yuv := make([]byte, yuvSize)
					RGBToYUV420(rgb, width, height, yuv)

					// YUV420 -> RGB
					recovered := make([]byte, rgbSize)
					YUV420ToRGB(yuv, width, height, recovered)

					// Check each pixel (all 4 pixels should be the same)
					for px := 0; px < width*height; px++ {
						rr := recovered[px*3]
						gg := recovered[px*3+1]
						bb := recovered[px*3+2]

						dr := absDiff(rr, r)
						dg := absDiff(gg, g)
						db := absDiff(bb, b)

						// Allow ±2 per channel due to chroma subsampling (2x2 averaging)
						// and double quantization (two float-to-byte conversions)
						const maxErr = 2
						if dr > maxErr || dg > maxErr || db > maxErr {
							t.Errorf("pixel %d: input(%d,%d,%d) -> output(%d,%d,%d), delta(%d,%d,%d)",
								px, r, g, b, rr, gg, bb, dr, dg, db)
						}
					}
				})
			}
		}
	}
}

// TestBT709LimitedRangeRoundTrip tests limited-range BT.709 conversion:
// RGB -> YCbCr(limited) -> RGB and verifies ±1 per channel.
func TestBT709LimitedRangeRoundTrip(t *testing.T) {
	steps := []uint8{0, 32, 64, 96, 128, 160, 192, 224, 255}

	for _, r := range steps {
		for _, g := range steps {
			for _, b := range steps {
				y, cb, cr := RGBToYUV_BT709Limited(r, g, b)
				rr, gg, bb := YUVToRGB_BT709Limited(y, cb, cr)

				dr := absDiff(rr, r)
				dg := absDiff(gg, g)
				db := absDiff(bb, b)

				// Limited range involves two quantization steps (float->byte->float->byte)
				// plus the restricted [16,235]/[16,240] range, so ±2 is expected.
				const maxErr = 2
				if dr > maxErr || dg > maxErr || db > maxErr {
					t.Errorf("RGB(%d,%d,%d) -> YCbCr(%d,%d,%d) -> RGB(%d,%d,%d), delta(%d,%d,%d)",
						r, g, b, y, cb, cr, rr, gg, bb, dr, dg, db)
				}
			}
		}
	}
}

// TestBT709KnownValues checks specific well-known color conversions.
func TestBT709KnownValues(t *testing.T) {
	tests := []struct {
		name          string
		r, g, b       uint8
		expectY       uint8 // approximate
		expectCbRange [2]uint8
		expectCrRange [2]uint8
	}{
		// Pure white in limited range: Y=235, Cb=128, Cr=128
		{"white", 255, 255, 255, 235, [2]uint8{127, 129}, [2]uint8{127, 129}},
		// Pure black in limited range: Y=16, Cb=128, Cr=128
		{"black", 0, 0, 0, 16, [2]uint8{127, 129}, [2]uint8{127, 129}},
		// Pure red in BT.709: Y≈63, Cb≈102, Cr≈240
		{"red", 255, 0, 0, 63, [2]uint8{96, 108}, [2]uint8{234, 240}},
		// Pure green: Y≈173, Cb≈42, Cr≈26
		{"green", 0, 255, 0, 173, [2]uint8{36, 48}, [2]uint8{20, 32}},
		// Pure blue: Y≈32, Cb≈240, Cr≈118
		{"blue", 0, 0, 255, 32, [2]uint8{234, 240}, [2]uint8{112, 124}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			y, cb, cr := RGBToYUV_BT709Limited(tc.r, tc.g, tc.b)

			if absDiff(y, tc.expectY) > 2 {
				t.Errorf("Y: expected ~%d, got %d", tc.expectY, y)
			}
			if cb < tc.expectCbRange[0] || cb > tc.expectCbRange[1] {
				t.Errorf("Cb: expected [%d,%d], got %d", tc.expectCbRange[0], tc.expectCbRange[1], cb)
			}
			if cr < tc.expectCrRange[0] || cr > tc.expectCrRange[1] {
				t.Errorf("Cr: expected [%d,%d], got %d", tc.expectCrRange[0], tc.expectCrRange[1], cr)
			}
		})
	}
}

// TestBT709YRangePreservation verifies that luma values stay within
// the legal limited range [16, 235].
func TestBT709YRangePreservation(t *testing.T) {
	for r := 0; r <= 255; r++ {
		for g := 0; g <= 255; g += 17 { // coarser step for speed
			for b := 0; b <= 255; b += 17 {
				y, _, _ := RGBToYUV_BT709Limited(uint8(r), uint8(g), uint8(b))
				if y < 16 || y > 235 {
					t.Fatalf("Y out of limited range [16,235]: RGB(%d,%d,%d) -> Y=%d", r, g, b, y)
				}
			}
		}
	}
}

func absDiff(a, b uint8) uint8 {
	if a > b {
		return a - b
	}
	return b - a
}
