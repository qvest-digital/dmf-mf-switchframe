//go:build correctness

package htmlgfx

import (
	"bytes"
	"fmt"
	"testing"
)

// TestBGRAToRGBAFullyOpaque verifies B=100,G=150,R=200,A=255 yields R=200,G=150,B=100,A=255.
// Fully opaque pixels need only a B↔R swap, no un-premultiply math.
func TestBGRAToRGBAFullyOpaque(t *testing.T) {
	src := []byte{100, 150, 200, 255} // BGRA: B=100, G=150, R=200, A=255
	dst := make([]byte, 4)
	bgraToRGBA(dst, src)

	t.Logf("Input  BGRA: B=%d G=%d R=%d A=%d", src[0], src[1], src[2], src[3])
	t.Logf("Output RGBA: R=%d G=%d B=%d A=%d", dst[0], dst[1], dst[2], dst[3])

	want := []byte{200, 150, 100, 255}
	for i, b := range dst {
		if b != want[i] {
			t.Errorf("byte[%d] = %d, want %d", i, b, want[i])
		}
	}
}

// TestBGRAToRGBAFullyTransparent verifies division-by-zero handling when A=0.
func TestBGRAToRGBAFullyTransparent(t *testing.T) {
	src := []byte{0, 0, 0, 0}
	dst := make([]byte, 4)
	bgraToRGBA(dst, src)

	t.Logf("Input  BGRA: B=%d G=%d R=%d A=%d", src[0], src[1], src[2], src[3])
	t.Logf("Output RGBA: R=%d G=%d B=%d A=%d", dst[0], dst[1], dst[2], dst[3])

	want := []byte{0, 0, 0, 0}
	for i, b := range dst {
		if b != want[i] {
			t.Errorf("byte[%d] = %d, want %d", i, b, want[i])
		}
	}
}

// TestBGRAToRGBASemiTransparent verifies un-premultiply math for semi-transparent pixels.
// Premultiplied B=50, G=75, R=100, A=128.
// Straight: R = (100*255 + 64) / 128 = 199, G = (75*255 + 64) / 128 = 149, B = (50*255 + 64) / 128 = 100.
func TestBGRAToRGBASemiTransparent(t *testing.T) {
	src := []byte{50, 75, 100, 128} // BGRA premultiplied
	dst := make([]byte, 4)
	bgraToRGBA(dst, src)

	t.Logf("Input  BGRA (premul): B=%d G=%d R=%d A=%d", src[0], src[1], src[2], src[3])
	t.Logf("Output RGBA (straight): R=%d G=%d B=%d A=%d", dst[0], dst[1], dst[2], dst[3])

	// Expected exact values from integer math:
	// R = min((100*255 + 64)/128, 255) = min(25564/128, 255) = min(199, 255) = 199
	// G = min((75*255 + 64)/128, 255) = min(19189/128, 255) = min(149, 255) = 149
	// B = min((50*255 + 64)/128, 255) = min(12814/128, 255) = min(100, 255) = 100
	wantR, wantG, wantB, wantA := byte(199), byte(149), byte(100), byte(128)
	t.Logf("Expected RGBA: R=%d G=%d B=%d A=%d", wantR, wantG, wantB, wantA)

	// Allow ±1 tolerance for rounding
	checkWithTolerance := func(name string, got, want byte) {
		diff := int(got) - int(want)
		if diff < -1 || diff > 1 {
			t.Errorf("%s = %d, want %d (±1), diff = %d", name, got, want, diff)
		} else if diff != 0 {
			t.Logf("%s = %d, want %d (within ±1 tolerance, diff = %d)", name, got, want, diff)
		}
	}

	checkWithTolerance("R", dst[0], wantR)
	checkWithTolerance("G", dst[1], wantG)
	checkWithTolerance("B", dst[2], wantB)
	if dst[3] != wantA {
		t.Errorf("A = %d, want %d (alpha must be exact)", dst[3], wantA)
	}
}

// TestBGRAToRGBABoundarySizes tests conversion at various pixel counts to exercise
// any SIMD boundary conditions: 1, 3, 4, 7, 8, 15, 16, 17, 100 pixels.
// All pixels are opaque red (BGRA: B=0,G=0,R=255,A=255 -> RGBA: R=255,G=0,B=0,A=255).
func TestBGRAToRGBABoundarySizes(t *testing.T) {
	sizes := []int{1, 3, 4, 7, 8, 15, 16, 17, 100}

	for _, nPixels := range sizes {
		t.Run(fmt.Sprintf("%dpx", nPixels), func(t *testing.T) {
			nBytes := nPixels * 4
			src := make([]byte, nBytes)
			dst := make([]byte, nBytes)

			// Fill with opaque red in BGRA
			for i := 0; i < nBytes; i += 4 {
				src[i] = 0     // B
				src[i+1] = 0   // G
				src[i+2] = 255 // R
				src[i+3] = 255 // A
			}

			bgraToRGBA(dst, src)

			for px := 0; px < nPixels; px++ {
				off := px * 4
				r, g, b, a := dst[off], dst[off+1], dst[off+2], dst[off+3]
				if r != 255 || g != 0 || b != 0 || a != 255 {
					t.Errorf("pixel %d: got RGBA(%d,%d,%d,%d), want (255,0,0,255)",
						px, r, g, b, a)
				}
			}
			t.Logf("All %d pixels correct", nPixels)
		})
	}
}

// TestBGRAToRGBADeterminism runs the same conversion 20 times and checks byte-exact output.
func TestBGRAToRGBADeterminism(t *testing.T) {
	const nPixels = 64
	nBytes := nPixels * 4
	src := make([]byte, nBytes)

	// Fill with a mix of alpha values for coverage
	for i := 0; i < nBytes; i += 4 {
		a := byte((i/4)%254 + 1) // alpha 1..254 (avoid 0 and 255 special cases only)
		// Premultiplied: channel values must be <= alpha
		src[i] = a / 3   // B
		src[i+1] = a / 2 // G
		src[i+2] = a     // R (max valid premultiplied value)
		src[i+3] = a     // A
	}

	// First run: get reference output
	reference := make([]byte, nBytes)
	bgraToRGBA(reference, src)
	t.Logf("Reference output first 5 pixels:")
	for px := 0; px < 5 && px < nPixels; px++ {
		off := px * 4
		t.Logf("  pixel %d: RGBA(%d,%d,%d,%d)", px, reference[off], reference[off+1], reference[off+2], reference[off+3])
	}

	// 19 more runs
	for run := 1; run < 20; run++ {
		dst := make([]byte, nBytes)
		bgraToRGBA(dst, src)
		if !bytes.Equal(dst, reference) {
			for i := range dst {
				if dst[i] != reference[i] {
					t.Errorf("run %d: byte[%d] = %d, reference = %d (pixel %d, component %d)",
						run, i, dst[i], reference[i], i/4, i%4)
				}
			}
			t.Fatalf("run %d: output differs from reference", run)
		}
	}
	t.Logf("All 20 runs produced byte-exact identical output")
}

// TestBGRAToRGBAAllAlphaValues tests every alpha from 1 to 255 with a premultiplied
// channel value equal to alpha (meaning the straight value should be 255).
// This exercises the un-premultiply formula at every alpha level.
func TestBGRAToRGBAAllAlphaValues(t *testing.T) {
	var bugCount int

	for a := 1; a <= 255; a++ {
		alpha := byte(a)
		// Premultiplied R = alpha means straight R should be 255
		// (because premultiplied = straight * alpha / 255, so straight = premul * 255 / alpha)
		src := []byte{0, 0, alpha, alpha} // BGRA: B=0, G=0, R=alpha, A=alpha
		dst := make([]byte, 4)
		bgraToRGBA(dst, src)

		gotR := dst[0]
		gotA := dst[3]

		// Log sampled values (every 32nd alpha + boundaries)
		if a <= 3 || a >= 253 || a%32 == 0 {
			t.Logf("A=%3d: premulR=%3d -> straightR=%3d (expect 255±1), A_out=%d", a, alpha, gotR, gotA)
		}

		// Straight R should be 255 ±1
		diff := int(gotR) - 255
		if diff < -1 || diff > 1 {
			t.Errorf("A=%d: R = %d, want 255 (±1), diff = %d", a, gotR, diff)
			bugCount++
		}

		// Alpha must pass through exactly
		if a == 255 {
			if gotA != 255 {
				t.Errorf("A=%d: alpha output = %d, want 255", a, gotA)
				bugCount++
			}
		} else {
			if gotA != alpha {
				t.Errorf("A=%d: alpha output = %d, want %d", a, gotA, alpha)
				bugCount++
			}
		}

		// G and B should be 0
		if dst[1] != 0 {
			t.Errorf("A=%d: G = %d, want 0", a, dst[1])
			bugCount++
		}
		if dst[2] != 0 {
			t.Errorf("A=%d: B = %d, want 0", a, dst[2])
			bugCount++
		}
	}

	t.Logf("Total bugs found across all alpha values: %d", bugCount)
}

// TestBGRAToRGBAInvalidPremult documents behavior when premultiplied channel > alpha.
// In valid premultiplied alpha, no channel can exceed the alpha value.
// This test documents what happens with invalid input: clamp, wrap, or garbage.
func TestBGRAToRGBAInvalidPremult(t *testing.T) {
	testCases := []struct {
		name string
		b, g, r, a byte
	}{
		{"R>A slight", 0, 0, 200, 128},   // R=200 but A=128
		{"all>A", 200, 200, 200, 100},     // All channels > alpha
		{"max>min", 255, 255, 255, 1},     // Extreme: all 255 with alpha=1
		{"R=255 A=128", 0, 0, 255, 128},   // Half transparent, full premul R
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			src := []byte{tc.b, tc.g, tc.r, tc.a}
			dst := make([]byte, 4)
			bgraToRGBA(dst, src)

			t.Logf("Input  BGRA (INVALID premul): B=%d G=%d R=%d A=%d", tc.b, tc.g, tc.r, tc.a)
			t.Logf("Output RGBA: R=%d G=%d B=%d A=%d", dst[0], dst[1], dst[2], dst[3])

			// Document: does it clamp to 255?
			clamped := true
			for i := 0; i < 3; i++ {
				if dst[i] > 255 { // can't happen with byte, but check conceptually
					clamped = false
				}
			}

			// Check if the function clamps invalid values to 255
			// For R>A: straight = min((R*255 + A/2) / A, 255)
			// With R=200, A=128: (200*255+64)/128 = 51064/128 = 399 -> clamped to 255
			if dst[3] != tc.a {
				t.Errorf("alpha not preserved: got %d, want %d", dst[3], tc.a)
			}

			// Verify the formula produces values > 255 before clamping
			a16 := uint16(tc.a)
			rawR := (uint16(tc.r)*255 + a16/2) / a16
			rawG := (uint16(tc.g)*255 + a16/2) / a16
			rawB := (uint16(tc.b)*255 + a16/2) / a16
			t.Logf("Pre-clamp values: R=%d G=%d B=%d", rawR, rawG, rawB)

			wouldOverflow := rawR > 255 || rawG > 255 || rawB > 255
			t.Logf("Would overflow without clamp: %v", wouldOverflow)

			if wouldOverflow && clamped {
				t.Logf("BEHAVIOR: invalid premultiplied input is CLAMPED to 255 (not wrapped)")
			}

			// Verify output channels are <= 255 (they're bytes so always true, but verify clamping worked)
			for i := 0; i < 3; i++ {
				component := []string{"R", "G", "B"}[i]
				raws := []uint16{rawR, rawG, rawB}
				if raws[i] > 255 && dst[i] != 255 {
					t.Errorf("%s: pre-clamp value was %d, expected output 255 (clamped), got %d",
						component, raws[i], dst[i])
				}
			}
		})
	}
}
