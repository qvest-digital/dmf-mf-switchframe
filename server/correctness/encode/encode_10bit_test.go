//go:build correctness

package encode

import (
	"encoding/binary"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/zsiec/switchframe/server/switcher"
)

// ==========================================================================
// 10-bit encode/format conversion correctness tests.
//
// These verify the Downconvert422_10to420 path used by the encode node
// when the pipeline runs in 10-bit mode but the encoder requires 8-bit 420.
// ==========================================================================

// generateSolid422P10LE creates a solid-color YUV422P10LE frame.
func generateSolid422P10LE(w, h int, y, cb, cr uint16) []byte {
	format := switcher.ColorYUV422_10bit
	buf := make([]byte, format.FrameSize(w, h))

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

// ==========================================================================
// 1. TestEncode10bit_DownconvertBlack — 10-bit black (Y=64) maps to 8-bit
//    black (Y=16) after downconversion.
// ==========================================================================

func TestEncode10bit_DownconvertBlack(t *testing.T) {
	t.Parallel()

	w, h := 320, 240
	src := generateSolid422P10LE(w, h, 64, 512, 512)

	dstSize := switcher.ColorYUV420_8bit.FrameSize(w, h)
	dst := make([]byte, dstSize)

	err := switcher.Downconvert422_10to420(src, dst, w, h)
	require.NoError(t, err)

	// 10-bit Y=64 -> 8-bit: (64+2)>>2 = 16
	for i := 0; i < w*h; i++ {
		require.InDelta(t, 16, int(dst[i]), 1,
			"Y[%d]: 10-bit black (64) should map to 8-bit black (~16), got %d", i, dst[i])
	}

	// 10-bit Cb=512 -> 8-bit: (512+2)>>2 = 128
	cbOff := w * h
	chromaW := w / 2
	chromaH := h / 2
	for i := 0; i < chromaW*chromaH; i++ {
		require.InDelta(t, 128, int(dst[cbOff+i]), 1,
			"Cb[%d]: 10-bit neutral (512) should map to 8-bit (~128), got %d", i, dst[cbOff+i])
	}

	// 10-bit Cr=512 -> 8-bit: (512+2)>>2 = 128
	crOff := cbOff + chromaW*chromaH
	for i := 0; i < chromaW*chromaH; i++ {
		require.InDelta(t, 128, int(dst[crOff+i]), 1,
			"Cr[%d]: 10-bit neutral (512) should map to 8-bit (~128), got %d", i, dst[crOff+i])
	}
}

// ==========================================================================
// 2. TestEncode10bit_DownconvertWhite — 10-bit white (Y=940) maps to 8-bit
//    white (Y=235) after downconversion.
// ==========================================================================

func TestEncode10bit_DownconvertWhite(t *testing.T) {
	t.Parallel()

	w, h := 320, 240
	src := generateSolid422P10LE(w, h, 940, 512, 512)

	dstSize := switcher.ColorYUV420_8bit.FrameSize(w, h)
	dst := make([]byte, dstSize)

	err := switcher.Downconvert422_10to420(src, dst, w, h)
	require.NoError(t, err)

	// 10-bit Y=940 -> 8-bit: (940+2)>>2 = 235
	for i := 0; i < w*h; i++ {
		require.InDelta(t, 235, int(dst[i]), 1,
			"Y[%d]: 10-bit white (940) should map to 8-bit white (~235), got %d", i, dst[i])
	}
}

// ==========================================================================
// 3. TestEncode10bit_DownconvertRoundTrip — Upconvert then downconvert
//    should yield values close to the original 8-bit frame.
// ==========================================================================

func TestEncode10bit_DownconvertRoundTrip(t *testing.T) {
	t.Parallel()

	w, h := 320, 240

	// Create an 8-bit frame with known values.
	src8 := make([]byte, switcher.ColorYUV420_8bit.FrameSize(w, h))
	ySize := w * h
	chromaW := w / 2
	chromaH := h / 2

	// Y gradient.
	for i := 0; i < ySize; i++ {
		src8[i] = byte(16 + (i*219)/(ySize-1))
	}
	// Neutral chroma.
	cbOff := ySize
	for i := 0; i < chromaW*chromaH; i++ {
		src8[cbOff+i] = 128
	}
	crOff := cbOff + chromaW*chromaH
	for i := 0; i < chromaW*chromaH; i++ {
		src8[crOff+i] = 128
	}

	// Upconvert to 10-bit.
	dst10 := make([]byte, switcher.ColorYUV422_10bit.FrameSize(w, h))
	err := switcher.Upconvert420to422_10(src8, dst10, w, h)
	require.NoError(t, err)

	// Downconvert back to 8-bit.
	result8 := make([]byte, switcher.ColorYUV420_8bit.FrameSize(w, h))
	err = switcher.Downconvert422_10to420(dst10, result8, w, h)
	require.NoError(t, err)

	// Y plane should match closely (within +-1 due to rounding).
	for i := 0; i < ySize; i++ {
		diff := int(result8[i]) - int(src8[i])
		if diff < 0 {
			diff = -diff
		}
		require.LessOrEqual(t, diff, 1,
			"Y[%d]: round-trip should be within +-1, got %d vs %d (diff=%d)",
			i, result8[i], src8[i], diff)
	}

	// Chroma round-trip: 420->422 upsample->downsample introduces some
	// interpolation error, but for uniform chroma it should be exact.
	for i := 0; i < chromaW*chromaH; i++ {
		require.InDelta(t, 128, int(result8[cbOff+i]), 1,
			"Cb[%d] round-trip should be ~128, got %d", i, result8[cbOff+i])
		require.InDelta(t, 128, int(result8[crOff+i]), 1,
			"Cr[%d] round-trip should be ~128, got %d", i, result8[crOff+i])
	}
}

// ==========================================================================
// 4. TestEncode10bit_UpconvertMapping — Verify the multiplication-by-4 mapping.
// ==========================================================================

func TestEncode10bit_UpconvertMapping(t *testing.T) {
	t.Parallel()

	w, h := 8, 4

	// Create 8-bit frame with specific values.
	src8 := make([]byte, switcher.ColorYUV420_8bit.FrameSize(w, h))
	ySize := w * h
	chromaW := w / 2
	chromaH := h / 2

	// Set all Y to 100.
	for i := 0; i < ySize; i++ {
		src8[i] = 100
	}
	// Set all Cb to 80, Cr to 200.
	cbOff := ySize
	for i := 0; i < chromaW*chromaH; i++ {
		src8[cbOff+i] = 80
	}
	crOff := cbOff + chromaW*chromaH
	for i := 0; i < chromaW*chromaH; i++ {
		src8[crOff+i] = 200
	}

	dst10 := make([]byte, switcher.ColorYUV422_10bit.FrameSize(w, h))
	err := switcher.Upconvert420to422_10(src8, dst10, w, h)
	require.NoError(t, err)

	// Y should be 100*4 = 400.
	for i := 0; i < ySize; i++ {
		val := binary.LittleEndian.Uint16(dst10[i*2:])
		require.Equal(t, uint16(400), val,
			"Y[%d]: 8-bit 100 should upconvert to 10-bit 400, got %d", i, val)
	}

	// Cb should be 80*4 = 320 (on even rows, which are direct copies).
	cbOff10 := ySize * 2
	val := binary.LittleEndian.Uint16(dst10[cbOff10:])
	require.Equal(t, uint16(320), val,
		"Cb[0]: 8-bit 80 should upconvert to 10-bit 320, got %d", val)

	// Cr should be 200*4 = 800.
	crOff10 := cbOff10 + chromaW*h*2
	val = binary.LittleEndian.Uint16(dst10[crOff10:])
	require.Equal(t, uint16(800), val,
		"Cr[0]: 8-bit 200 should upconvert to 10-bit 800, got %d", val)
}

// ==========================================================================
// 5. TestEncode10bit_DownconvertBufferValidation — Invalid buffer sizes
//    return errors.
// ==========================================================================

func TestEncode10bit_DownconvertBufferValidation(t *testing.T) {
	t.Parallel()

	w, h := 320, 240

	// Source too small.
	tinyBuf := make([]byte, 100)
	dst := make([]byte, switcher.ColorYUV420_8bit.FrameSize(w, h))
	err := switcher.Downconvert422_10to420(tinyBuf, dst, w, h)
	require.Error(t, err, "should reject undersized source buffer")

	// Destination too small.
	src := make([]byte, switcher.ColorYUV422_10bit.FrameSize(w, h))
	tinyDst := make([]byte, 100)
	err = switcher.Downconvert422_10to420(src, tinyDst, w, h)
	require.Error(t, err, "should reject undersized destination buffer")

	// Odd width.
	err = switcher.Downconvert422_10to420(src, dst, 321, h)
	require.Error(t, err, "should reject odd width")

	// Odd height.
	err = switcher.Downconvert422_10to420(src, dst, w, 241)
	require.Error(t, err, "should reject odd height")
}

// ==========================================================================
// 6. TestEncode10bit_UpconvertBufferValidation — Invalid buffer sizes
//    return errors.
// ==========================================================================

func TestEncode10bit_UpconvertBufferValidation(t *testing.T) {
	t.Parallel()

	w, h := 320, 240

	// Source too small.
	tinyBuf := make([]byte, 100)
	dst := make([]byte, switcher.ColorYUV422_10bit.FrameSize(w, h))
	err := switcher.Upconvert420to422_10(tinyBuf, dst, w, h)
	require.Error(t, err, "should reject undersized source buffer")

	// Destination too small.
	src := make([]byte, switcher.ColorYUV420_8bit.FrameSize(w, h))
	tinyDst := make([]byte, 100)
	err = switcher.Upconvert420to422_10(src, tinyDst, w, h)
	require.Error(t, err, "should reject undersized destination buffer")

	// Zero dimensions.
	err = switcher.Upconvert420to422_10(src, dst, 0, h)
	require.Error(t, err, "should reject zero width")
}
