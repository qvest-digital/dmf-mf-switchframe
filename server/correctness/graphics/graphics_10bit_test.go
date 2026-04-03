//go:build correctness

package graphics

import (
	"encoding/binary"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/zsiec/switchframe/server/graphics"
)

// ==========================================================================
// 10-bit YUV422P10LE graphics correctness tests.
//
// These verify DSK overlay compositing (AlphaBlendRGBA10), chroma keying
// (ChromaKey10), and luma keying (LumaKey10) on 10-bit frames.
// ==========================================================================

const (
	testWidth10  = 320
	testHeight10 = 240
)

// generateSolid422P10LE creates a solid-color YUV422P10LE frame.
func generateSolid422P10LE(w, h int, y, cb, cr uint16) []byte {
	frameSize := w * h * 4
	buf := make([]byte, frameSize)

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

// readY10 reads the Y sample at luma position (x, y).
func readY10(buf []byte, w, x, y int) uint16 {
	return binary.LittleEndian.Uint16(buf[(y*w+x)*2:])
}

// readCb10 reads the Cb sample at chroma position (cx, cy).
func readCb10(buf []byte, w, h, cx, cy int) uint16 {
	ySize := w * h
	chromaW := w / 2
	off := ySize*2 + (cy*chromaW+cx)*2
	return binary.LittleEndian.Uint16(buf[off:])
}

// readCr10 reads the Cr sample at chroma position (cx, cy).
func readCr10(buf []byte, w, h, cx, cy int) uint16 {
	ySize := w * h
	chromaW := w / 2
	chromaSize := chromaW * h
	off := ySize*2 + chromaSize*2 + (cy*chromaW+cx)*2
	return binary.LittleEndian.Uint16(buf[off:])
}

// ==========================================================================
// 1. TestGraphics10bit_AlphaBlendRGBA_OpaqueWhite — Blend opaque white RGBA
//    overlay onto 10-bit black frame, verify Y is approximately 940 (white).
// ==========================================================================

func TestGraphics10bit_AlphaBlendRGBA_OpaqueWhite(t *testing.T) {
	t.Parallel()

	w, h := testWidth10, testHeight10

	// 10-bit black frame: Y=64, Cb=512, Cr=512.
	yuv := generateSolid422P10LE(w, h, 64, 512, 512)

	// Opaque white RGBA overlay.
	rgba := makeRGBA(w, h, 255, 255, 255, 255)

	err := graphics.AlphaBlendRGBA10(yuv, rgba, w, h)
	require.NoError(t, err)

	// White in BT.709 10-bit limited range:
	// Y8 = (66*255 + 129*255 + 25*255 + 128)>>8 + 16 = (56100+128)>>8 + 16 = 219 + 16 = 235
	// Y10 = 235 * 4 = 940
	// With alpha=255: result = (940*255 + 64*0 + 128)>>8 = (239700+128)>>8 = 936
	y := readY10(yuv, w, 0, 0)
	require.InDelta(t, 940, int(y), 5,
		"opaque white overlay on black: Y should be ~940, got %d", y)

	// Cb/Cr should be neutral (white has no chroma).
	cb := readCb10(yuv, w, h, 0, 0)
	cr := readCr10(yuv, w, h, 0, 0)
	require.InDelta(t, 512, int(cb), 5,
		"opaque white overlay: Cb should be ~512, got %d", cb)
	require.InDelta(t, 512, int(cr), 5,
		"opaque white overlay: Cr should be ~512, got %d", cr)
}

// ==========================================================================
// 2. TestGraphics10bit_AlphaBlendRGBA_Transparent — Alpha=0 overlay must
//    not modify the 10-bit frame.
// ==========================================================================

func TestGraphics10bit_AlphaBlendRGBA_Transparent(t *testing.T) {
	t.Parallel()

	w, h := testWidth10, testHeight10
	yuv := generateSolid422P10LE(w, h, 500, 300, 700)
	original := make([]byte, len(yuv))
	copy(original, yuv)

	// Fully transparent overlay (alpha=0).
	rgba := makeRGBA(w, h, 255, 0, 0, 0)

	err := graphics.AlphaBlendRGBA10(yuv, rgba, w, h)
	require.NoError(t, err)

	require.Equal(t, original, yuv,
		"alpha=0 overlay must produce byte-exact passthrough")
}

// ==========================================================================
// 3. TestGraphics10bit_AlphaBlendRGBA_HalfAlpha — Alpha=128 produces a blend.
// ==========================================================================

func TestGraphics10bit_AlphaBlendRGBA_HalfAlpha(t *testing.T) {
	t.Parallel()

	w, h := 32, 32
	yuv := generateSolid422P10LE(w, h, 400, 512, 512)

	// Green overlay with alpha=128.
	rgba := makeRGBA(w, h, 0, 255, 0, 128)

	err := graphics.AlphaBlendRGBA10(yuv, rgba, w, h)
	require.NoError(t, err)

	y := readY10(yuv, w, 0, 0)

	// Green in BT.709 10-bit:
	// Y8 = (66*0 + 129*255 + 25*0 + 128)>>8 + 16 = (32895+128)>>8 + 16 = 129 + 16 = 145
	// Y10 = 145 * 4 = 580
	// Blend: (580*128 + 400*127 + 128) >> 8 = (74240 + 50800 + 128)>>8 = 125168>>8 = 488
	require.InDelta(t, 490, int(y), 10,
		"half-alpha green on Y=400: Y should be ~490, got %d", y)

	// Result should differ from original.
	require.NotEqual(t, uint16(400), y,
		"alpha=128 blend must modify the frame")
}

// ==========================================================================
// 4. TestGraphics10bit_ChromaKey — Green pixels at 10-bit values are keyed
//    out (mask=255), non-green pixels remain opaque (mask=0).
// ==========================================================================

func TestGraphics10bit_ChromaKey(t *testing.T) {
	t.Parallel()

	w, h := 32, 32

	// BT.709 green in 10-bit: Cb~216 (54*4), Cr~104 (26*4)
	greenCb := uint16(216)
	greenCr := uint16(104)

	// Create a frame that is all green.
	greenFrame := generateSolid422P10LE(w, h, 580, greenCb, greenCr)

	mask := graphics.ChromaKey10(greenFrame, w, h, greenCb, greenCr, 100, 50)
	require.NotNil(t, mask)
	require.Len(t, mask, w*h)

	// All pixels should be keyed (mask=255 for green).
	for i, m := range mask {
		require.GreaterOrEqual(t, m, byte(200),
			"pixel %d: green pixel should be keyed (>=200), got %d", i, m)
	}

	// Now test with a non-green frame (red in 10-bit).
	// BT.709 red: Cb~360 (90*4), Cr~960 (240*4)
	redFrame := generateSolid422P10LE(w, h, 324, 360, 960)

	maskRed := graphics.ChromaKey10(redFrame, w, h, greenCb, greenCr, 100, 50)
	require.NotNil(t, maskRed)
	require.Len(t, maskRed, w*h)

	// Red pixels should NOT be keyed (mask=0).
	for i, m := range maskRed {
		require.LessOrEqual(t, m, byte(5),
			"pixel %d: red pixel should not be keyed, got %d", i, m)
	}
}

// ==========================================================================
// 5. TestGraphics10bit_LumaKey — Key at Y threshold 512 (10-bit midpoint).
//    Below threshold is keyed out, above is kept.
// ==========================================================================

func TestGraphics10bit_LumaKey(t *testing.T) {
	t.Parallel()

	w, h := 32, 32

	// Frame with Y=300 (below threshold).
	darkFrame := generateSolid422P10LE(w, h, 300, 512, 512)
	mask := graphics.LumaKey10(darkFrame, w, h, 512, 1023)
	require.NotNil(t, mask)
	require.Len(t, mask, w*h)

	// Y=300 is below lowThresh=512, so all pixels should be keyed out (mask=0).
	for i, m := range mask {
		require.Equal(t, byte(0), m,
			"pixel %d: Y=300 below threshold 512 should be keyed (0), got %d", i, m)
	}

	// Frame with Y=700 (above threshold).
	brightFrame := generateSolid422P10LE(w, h, 700, 512, 512)
	maskBright := graphics.LumaKey10(brightFrame, w, h, 512, 1023)
	require.NotNil(t, maskBright)
	require.Len(t, maskBright, w*h)

	// Y=700 is within [512, 1023], so all pixels should be opaque (mask=255).
	for i, m := range maskBright {
		require.Equal(t, byte(255), m,
			"pixel %d: Y=700 within [512,1023] should be opaque (255), got %d", i, m)
	}
}

// ==========================================================================
// 6. TestGraphics10bit_LumaKeyMidpoint — Pixels exactly at threshold boundary.
// ==========================================================================

func TestGraphics10bit_LumaKeyMidpoint(t *testing.T) {
	t.Parallel()

	w, h := 8, 8

	// Y=512 is exactly at lowThresh.
	atThresh := generateSolid422P10LE(w, h, 512, 512, 512)
	mask := graphics.LumaKey10(atThresh, w, h, 512, 1023)
	require.NotNil(t, mask)

	// Y=512 is >= lowThresh=512, should be opaque.
	for i, m := range mask {
		require.Equal(t, byte(255), m,
			"pixel %d: Y=512 at threshold should be opaque (255), got %d", i, m)
	}

	// Y=511 is below threshold.
	belowThresh := generateSolid422P10LE(w, h, 511, 512, 512)
	maskBelow := graphics.LumaKey10(belowThresh, w, h, 512, 1023)
	require.NotNil(t, maskBelow)

	for i, m := range maskBelow {
		require.Equal(t, byte(0), m,
			"pixel %d: Y=511 below threshold should be keyed (0), got %d", i, m)
	}
}

// ==========================================================================
// 7. TestGraphics10bit_AlphaBlendOutputRange — Blended output stays within
//    valid 10-bit range [0, 1023].
// ==========================================================================

func TestGraphics10bit_AlphaBlendOutputRange(t *testing.T) {
	t.Parallel()

	w, h := 32, 32

	testCases := []struct {
		name         string
		baseY        uint16
		overlayR     byte
		overlayG     byte
		overlayB     byte
		overlayAlpha byte
	}{
		{"max_on_max", 1023, 255, 255, 255, 255},
		{"max_on_min", 0, 255, 255, 255, 255},
		{"red_on_white", 940, 255, 0, 0, 200},
		{"blue_on_black", 64, 0, 0, 255, 128},
		{"green_on_mid", 500, 0, 255, 0, 192},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			yuv := generateSolid422P10LE(w, h, tc.baseY, 512, 512)
			rgba := makeRGBA(w, h, tc.overlayR, tc.overlayG, tc.overlayB, tc.overlayAlpha)

			err := graphics.AlphaBlendRGBA10(yuv, rgba, w, h)
			require.NoError(t, err)

			// Verify all samples are in [0, 1023].
			totalSamples := w*h + 2*(w/2)*h // Y + Cb + Cr (in uint16)
			for i := 0; i < totalSamples; i++ {
				val := binary.LittleEndian.Uint16(yuv[i*2:])
				require.LessOrEqual(t, val, uint16(1023),
					"sample %d = %d exceeds 10-bit max", i, val)
			}
		})
	}
}

// ==========================================================================
// 8. TestGraphics10bit_AlphaBlendDeterminism — Repeated blending is identical.
// ==========================================================================

func TestGraphics10bit_AlphaBlendDeterminism(t *testing.T) {
	t.Parallel()

	w, h := 64, 48
	rgba := makeRGBA(w, h, 180, 60, 220, 137)

	var reference []byte
	for i := 0; i < 10; i++ {
		yuv := generateSolid422P10LE(w, h, 400, 300, 700)
		err := graphics.AlphaBlendRGBA10(yuv, rgba, w, h)
		require.NoError(t, err)

		if i == 0 {
			reference = make([]byte, len(yuv))
			copy(reference, yuv)
		} else {
			require.Equal(t, reference, yuv,
				"run %d produced different alpha blend output", i)
		}
	}
}
