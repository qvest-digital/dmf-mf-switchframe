package graphics

import (
	"fmt"
	"image"
	"testing"
)

// ============================================================================
// BT.709 consistency tests for the 10-bit blend path.
//
// The 8-bit blend path converts RGB to YCbCr with true BT.709 integer
// coefficients (alphablend_kernels_generic.go / alphablend_chroma_kernels_generic.go):
//
//	Y  = 16 + ((47*R + 157*G + 16*B + 128) >> 8)
//	Cb = ((-26*R - 86*G + 112*B + 128) >> 8) + 128
//	Cr = ((112*R - 102*G - 10*B + 128) >> 8) + 128
//
// and maps alpha exactly to 0-256 via A' = A + (A>>7) so fully opaque
// pixels replace the background exactly.
//
// The 10-bit path must produce 4x those 8-bit values so the same graphic
// composites with identical colors on both pipelines (10-bit HD video is
// BT.709). These tests pin that equivalence.
// ============================================================================

func bt709Y8ref(r, g, b int) int  { return 16 + ((47*r + 157*g + 16*b + 128) >> 8) }
func bt709Cb8ref(r, g, b int) int { return ((-26*r - 86*g + 112*b + 128) >> 8) + 128 }
func bt709Cr8ref(r, g, b int) int { return ((112*r - 102*g - 10*b + 128) >> 8) + 128 }

// blend10EntryPoints exercises every public/internal entry into the 10-bit
// blend path with a full-frame overlay.
var blend10EntryPoints = []struct {
	name string
	fn   func(yuv, rgba []byte, w, h int) error
}{
	{"AlphaBlendRGBA10", func(yuv, rgba []byte, w, h int) error {
		return AlphaBlendRGBA10(yuv, rgba, w, h)
	}},
	{"AlphaBlendRGBA10Scaled_1.0", func(yuv, rgba []byte, w, h int) error {
		return AlphaBlendRGBA10Scaled(yuv, rgba, w, h, 1.0)
	}},
	{"alphaBlendRGBA10Dispatch", func(yuv, rgba []byte, w, h int) error {
		return alphaBlendRGBA10Dispatch(yuv, rgba, w, h)
	}},
	{"AlphaBlendRGBARect10_fullframe", func(yuv, rgba []byte, w, h int) error {
		return AlphaBlendRGBARect10(yuv, rgba, w, h, w, h, image.Rect(0, 0, w, h), 1.0)
	}},
}

// TestAlphaBlendRGBA10_BT709MatchesEightBitPath verifies that an opaque
// overlay converts RGB with the same BT.709 coefficients as the 8-bit blend
// path (scaled by 4) and replaces the background exactly (no 255/256
// darkening of opaque pixels).
func TestAlphaBlendRGBA10_BT709MatchesEightBitPath(t *testing.T) {
	colors := []struct {
		name    string
		r, g, b byte
	}{
		{"red", 255, 0, 0},
		{"green", 0, 255, 0},
		{"blue", 0, 0, 255},
		{"white", 255, 255, 255},
		{"saturated_orange", 255, 128, 0},
	}

	w, h := 32, 8
	for _, ep := range blend10EntryPoints {
		for _, c := range colors {
			t.Run(fmt.Sprintf("%s/%s", ep.name, c.name), func(t *testing.T) {
				yuv := make10bitGraphicsFrame(w, h, 512, 512, 512)
				rgba := make([]byte, w*h*4)
				for i := 0; i < w*h; i++ {
					rgba[i*4+0] = c.r
					rgba[i*4+1] = c.g
					rgba[i*4+2] = c.b
					rgba[i*4+3] = 255
				}

				if err := ep.fn(yuv, rgba, w, h); err != nil {
					t.Fatal(err)
				}

				wantY := bt709Y8ref(int(c.r), int(c.g), int(c.b)) * 4
				wantCb := bt709Cb8ref(int(c.r), int(c.g), int(c.b)) * 4
				wantCr := bt709Cr8ref(int(c.r), int(c.g), int(c.b)) * 4

				if gotY := int(readY10g(yuv, 2, 1, w)); gotY != wantY {
					t.Errorf("Y = %d, want %d (4x 8-bit BT.709 value %d)", gotY, wantY, wantY/4)
				}
				if gotCb := int(readCb10g(yuv, 1, 1, w, h)); gotCb != wantCb {
					t.Errorf("Cb = %d, want %d (4x 8-bit BT.709 value %d)", gotCb, wantCb, wantCb/4)
				}
				if gotCr := int(readCr10g(yuv, 1, 1, w, h)); gotCr != wantCr {
					t.Errorf("Cr = %d, want %d (4x 8-bit BT.709 value %d)", gotCr, wantCr, wantCr/4)
				}
			})
		}
	}
}

// TestAlphaBlendRGBA10_OpaqueWhiteIsExactWhite pins that a fully opaque
// white overlay produces exactly 10-bit white (Y=940), not 940*255/256=936.
// The 8-bit path maps A=255 to weight 256 (A + A>>7), so opaque overlay
// pixels replace the background exactly; the 10-bit path must match.
func TestAlphaBlendRGBA10_OpaqueWhiteIsExactWhite(t *testing.T) {
	w, h := 16, 4
	for _, ep := range blend10EntryPoints {
		t.Run(ep.name, func(t *testing.T) {
			yuv := make10bitGraphicsFrame(w, h, 64, 512, 512) // black background
			rgba := make([]byte, w*h*4)
			for i := 0; i < w*h; i++ {
				rgba[i*4+0] = 255
				rgba[i*4+1] = 255
				rgba[i*4+2] = 255
				rgba[i*4+3] = 255
			}
			if err := ep.fn(yuv, rgba, w, h); err != nil {
				t.Fatal(err)
			}
			if gotY := int(readY10g(yuv, 0, 0, w)); gotY != 940 {
				t.Errorf("opaque white Y = %d, want exactly 940", gotY)
			}
		})
	}
}

// TestAlphaBlendRGBA10_HalfAlphaMatchesEightBitMapping verifies the partial
// alpha mapping matches the 8-bit path: A=128 maps to weight 129 (128 + 128>>7)
// out of 256, so result = (overlay*129 + existing*127 + 128) >> 8.
func TestAlphaBlendRGBA10_HalfAlphaMatchesEightBitMapping(t *testing.T) {
	w, h := 16, 4
	for _, ep := range blend10EntryPoints {
		t.Run(ep.name, func(t *testing.T) {
			yuv := make10bitGraphicsFrame(w, h, 64, 512, 512)
			rgba := make([]byte, w*h*4)
			for i := 0; i < w*h; i++ {
				rgba[i*4+0] = 255
				rgba[i*4+1] = 255
				rgba[i*4+2] = 255
				rgba[i*4+3] = 128
			}
			if err := ep.fn(yuv, rgba, w, h); err != nil {
				t.Fatal(err)
			}
			// White overlay Y10 = 235*4 = 940; a256 = 128 + (128>>7) = 129.
			want := (940*129 + 64*(256-129) + 128) >> 8
			if gotY := int(readY10g(yuv, 0, 0, w)); gotY != want {
				t.Errorf("half-alpha Y = %d, want %d", gotY, want)
			}
		})
	}
}
