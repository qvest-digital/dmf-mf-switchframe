package graphics

import (
	"encoding/binary"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zsiec/switchframe/server/internal/video"
)

// ============================================================================
// 10-bit Alpha Blend Formula Tests
//
// These tests verify the RGBA-to-YUV422P10LE alpha blend formula against
// BT.709 10-bit color science values derived from the ITU-R BT.709 spec.
// ============================================================================

// generate10bitFrame creates a solid-color YUV422P10LE frame for testing.
func generate10bitFrame(w, h int, y, cb, cr uint16) []byte {
	buf := make([]byte, video.YUV422_10bitFrameSize(w, h))
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

// generateSolidRGBA creates a uniform RGBA overlay.
func generateSolidRGBA(w, h int, r, g, b, a byte) []byte {
	buf := make([]byte, w*h*4)
	for i := 0; i < w*h; i++ {
		buf[i*4+0] = r
		buf[i*4+1] = g
		buf[i*4+2] = b
		buf[i*4+3] = a
	}
	return buf
}

// readYSample reads a uint16 Y sample at the given luma index.
func readYSample(buf []byte, idx int) uint16 {
	return binary.LittleEndian.Uint16(buf[idx*2:])
}

// readCbSample10 reads a Cb sample at the given chroma index.
func readCbSample10(buf []byte, w, h, idx int) uint16 {
	ySize := w * h
	return binary.LittleEndian.Uint16(buf[ySize*2+idx*2:])
}

// readCrSample10 reads a Cr sample at the given chroma index.
func readCrSample10(buf []byte, w, h, idx int) uint16 {
	ySize := w * h
	chromaW := w / 2
	chromaSize := chromaW * h
	return binary.LittleEndian.Uint16(buf[ySize*2+chromaSize*2+idx*2:])
}

// ============================================================================
// Alpha Blend Formula Verification
// ============================================================================

// TestAlphaBlendRGBA10_Formula verifies the blend formula at several alpha levels.
//
// For a red (255,0,0) overlay on a Y=512 background:
// BT.709 8-bit Y = (66*255 + 129*0 + 25*0 + 128) >> 8 + 16 = 16830>>8 + 16 = 65+16 = 81
// 10-bit Y = 81 * 4 = 324
//
// At alpha=128: result = (324*128 + 512*127 + 128) >> 8 = (41472 + 65024 + 128) >> 8 = 416
func TestAlphaBlendRGBA10_Formula(t *testing.T) {
	w, h := 64, 32

	alphaTests := []struct {
		alpha byte
		desc  string
	}{
		{0, "transparent"},
		{64, "quarter"},
		{128, "half"},
		{192, "three-quarter"},
		{255, "opaque"},
	}

	for _, tc := range alphaTests {
		t.Run(tc.desc, func(t *testing.T) {
			yuv := generate10bitFrame(w, h, 512, 512, 512)
			rgba := generateSolidRGBA(w, h, 255, 0, 0, tc.alpha) // red overlay

			err := AlphaBlendRGBA10(yuv, rgba, w, h)
			require.NoError(t, err)

			gotY := readYSample(yuv, 0)

			if tc.alpha == 0 {
				// Transparent: frame must be unchanged
				assert.Equal(t, uint16(512), gotY,
					"alpha=0: Y must be unchanged (512), got %d", gotY)
			} else {
				// BT.709 red Y in 10-bit: (66*255 + 128)>>8 + 16 = 81 => 81*4 = 324
				overlayY := int32(81 * 4)
				a := int32(tc.alpha)
				inv := int32(255) - a
				expected := (overlayY*a + 512*inv + 128) >> 8

				assert.InDelta(t, float64(expected), float64(gotY), 4,
					"alpha=%d: Y expected ~%d, got %d", tc.alpha, expected, gotY)
			}
		})
	}
}

// ============================================================================
// Fully Opaque White Overlay
// ============================================================================

// TestAlphaBlendRGBA10_FullyOpaque verifies that a fully opaque white
// (255,255,255,255) overlay produces BT.709 10-bit white.
// BT.709 8-bit Y = (66*255 + 129*255 + 25*255 + 128)>>8 + 16
//                = (56100 + 128) >> 8 + 16 = 219 + 16 = 235
// 10-bit Y = 235 * 4 = 940
func TestAlphaBlendRGBA10_FullyOpaque(t *testing.T) {
	w, h := 32, 16

	yuv := generate10bitFrame(w, h, 64, 512, 512) // start with black
	rgba := generateSolidRGBA(w, h, 255, 255, 255, 255)

	err := AlphaBlendRGBA10(yuv, rgba, w, h)
	require.NoError(t, err)

	gotY := readYSample(yuv, 0)
	// BT.709 white in 10-bit limited: Y=940
	assert.InDelta(t, 940, int(gotY), 4,
		"opaque white overlay Y should be near 940 (10-bit white), got %d", gotY)
}

// ============================================================================
// Scale Zero = Unchanged
// ============================================================================

// TestAlphaBlendRGBA10_ScaleZero verifies that when alpha=0, the frame is
// completely unchanged (byte-exact). When every RGBA pixel has A=0, the RGB
// values are irrelevant — the frame MUST be byte-exact unchanged.
func TestAlphaBlendRGBA10_ScaleZero(t *testing.T) {
	w, h := 64, 32

	yuv := generate10bitFrame(w, h, 500, 300, 700)
	original := make([]byte, len(yuv))
	copy(original, yuv)

	// RGBA with A=0 (transparent) — RGB values are garbage, should be ignored
	rgba := generateSolidRGBA(w, h, 255, 128, 64, 0)

	err := AlphaBlendRGBA10(yuv, rgba, w, h)
	require.NoError(t, err)

	// Must be byte-exact unchanged
	assert.Equal(t, original, yuv,
		"alpha=0 overlay must leave frame byte-exact unchanged")
}

// ============================================================================
// Chroma Formula Verification
// ============================================================================

// TestAlphaBlendRGBA10_ChromaFormula verifies the BT.709 chroma conversion
// for a fully opaque red overlay.
//
// BT.709 8-bit Cb = (-38*255 - 74*0 + 112*0 + 128) >> 8 + 128
//                  = (-9690 + 128) >> 8 + 128 = -37 + 128 = 91
// 10-bit Cb = 91 * 4 = 364
//
// BT.709 8-bit Cr = (112*255 - 94*0 - 18*0 + 128) >> 8 + 128
//                  = (28560 + 128) >> 8 + 128 = 112 + 128 = 240
// 10-bit Cr = 240 * 4 = 960
func TestAlphaBlendRGBA10_ChromaFormula(t *testing.T) {
	w, h := 64, 32

	// Start with neutral chroma
	yuv := generate10bitFrame(w, h, 500, 512, 512)
	// Fully opaque red overlay
	rgba := generateSolidRGBA(w, h, 255, 0, 0, 255)

	err := AlphaBlendRGBA10(yuv, rgba, w, h)
	require.NoError(t, err)

	// BT.709 red in 10-bit:
	// Cb = 91*4 = 364; at alpha=255, blend = (364*255 + 512*0 + 128) >> 8 ~ 364
	gotCb := readCbSample10(yuv, w, h, 0)
	assert.InDelta(t, 364, int(gotCb), 8,
		"opaque red Cb should be ~364 (10-bit BT.709), got %d", gotCb)

	// Cr = 240*4 = 960; at alpha=255, blend = (960*255 + 512*0 + 128) >> 8 ~ 960
	gotCr := readCrSample10(yuv, w, h, 0)
	assert.InDelta(t, 960, int(gotCr), 8,
		"opaque red Cr should be ~960 (10-bit BT.709), got %d", gotCr)
}

// ============================================================================
// Transparent Overlay = Unchanged
// ============================================================================

// TestAlphaBlendRGBA10_TransparentOverlayUnchanged verifies that an RGBA overlay
// where every pixel has A=0 leaves the YUV frame byte-exact unchanged, regardless
// of what RGB values are present.
func TestAlphaBlendRGBA10_TransparentOverlayUnchanged(t *testing.T) {
	w, h := 32, 16

	yuv := generate10bitFrame(w, h, 750, 400, 600)
	original := make([]byte, len(yuv))
	copy(original, yuv)

	// Transparent overlay with non-zero RGB — RGB must be ignored
	rgba := make([]byte, w*h*4)
	for i := 0; i < w*h; i++ {
		rgba[i*4+0] = 200 // R - should be ignored
		rgba[i*4+1] = 100 // G - should be ignored
		rgba[i*4+2] = 50  // B - should be ignored
		rgba[i*4+3] = 0   // A = transparent
	}

	err := AlphaBlendRGBA10(yuv, rgba, w, h)
	require.NoError(t, err)

	// Every byte must be identical
	for i := range yuv {
		if yuv[i] != original[i] {
			t.Fatalf("byte %d changed: was 0x%02X, now 0x%02X (A=0 overlay must not modify frame)", i, original[i], yuv[i])
		}
	}
}
