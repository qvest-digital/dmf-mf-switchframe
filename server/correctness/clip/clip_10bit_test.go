//go:build correctness

package clip

import (
	"encoding/binary"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/zsiec/switchframe/server/switcher"
)

// ==========================================================================
// 10-bit YUV422P10LE clip correctness tests.
//
// These verify that 8-bit clip content is correctly upconverted to 10-bit
// when played on a 10-bit pipeline, with the expected value mappings.
// ==========================================================================

// ==========================================================================
// 1. TestClip10bit_UpconvertOnIngest — 8-bit H.264 clip played on 10-bit
//    pipeline: verify upconversion Y*4, Cb*4, Cr*4.
// ==========================================================================

func TestClip10bit_UpconvertOnIngest(t *testing.T) {
	t.Parallel()

	w, h := 320, 240

	// Simulate an 8-bit YUV420 frame as a clip decoder would produce.
	src8 := make([]byte, switcher.ColorYUV420_8bit.FrameSize(w, h))
	ySize := w * h
	chromaW := w / 2
	chromaH := h / 2

	// Y = 128 everywhere.
	for i := 0; i < ySize; i++ {
		src8[i] = 128
	}
	// Cb = 100 everywhere.
	cbOff := ySize
	for i := 0; i < chromaW*chromaH; i++ {
		src8[cbOff+i] = 100
	}
	// Cr = 200 everywhere.
	crOff := cbOff + chromaW*chromaH
	for i := 0; i < chromaW*chromaH; i++ {
		src8[crOff+i] = 200
	}

	// Upconvert to 10-bit.
	dst10 := make([]byte, switcher.ColorYUV422_10bit.FrameSize(w, h))
	err := switcher.Upconvert420to422_10(src8, dst10, w, h)
	require.NoError(t, err)

	// Verify Y: 128 * 4 = 512.
	for i := 0; i < ySize; i++ {
		val := binary.LittleEndian.Uint16(dst10[i*2:])
		require.Equal(t, uint16(512), val,
			"Y[%d]: 8-bit 128 should upconvert to 10-bit 512, got %d", i, val)
	}

	// Verify Cb on even rows (direct copy): 100 * 4 = 400.
	cbOff10 := ySize * 2
	chromaW10 := w / 2
	// Even row (row 0) Cb.
	for x := 0; x < chromaW10; x++ {
		val := binary.LittleEndian.Uint16(dst10[cbOff10+x*2:])
		require.Equal(t, uint16(400), val,
			"Cb[0,%d]: 8-bit 100 should upconvert to 10-bit 400, got %d", x, val)
	}

	// Verify Cr on even rows: 200 * 4 = 800.
	crOff10 := cbOff10 + chromaW10*h*2
	for x := 0; x < chromaW10; x++ {
		val := binary.LittleEndian.Uint16(dst10[crOff10+x*2:])
		require.Equal(t, uint16(800), val,
			"Cr[0,%d]: 8-bit 200 should upconvert to 10-bit 800, got %d", x, val)
	}
}

// ==========================================================================
// 2. TestClip10bit_UpconvertBlackPreservesLegalRange — 8-bit broadcast black
//    upconverts to 10-bit broadcast black.
// ==========================================================================

func TestClip10bit_UpconvertBlackPreservesLegalRange(t *testing.T) {
	t.Parallel()

	w, h := 64, 48

	// 8-bit broadcast black: Y=16, Cb=128, Cr=128.
	src8 := make([]byte, switcher.ColorYUV420_8bit.FrameSize(w, h))
	ySize := w * h
	chromaW := w / 2
	chromaH := h / 2

	for i := 0; i < ySize; i++ {
		src8[i] = 16
	}
	cbOff := ySize
	for i := 0; i < chromaW*chromaH; i++ {
		src8[cbOff+i] = 128
	}
	crOff := cbOff + chromaW*chromaH
	for i := 0; i < chromaW*chromaH; i++ {
		src8[crOff+i] = 128
	}

	dst10 := make([]byte, switcher.ColorYUV422_10bit.FrameSize(w, h))
	err := switcher.Upconvert420to422_10(src8, dst10, w, h)
	require.NoError(t, err)

	// Y: 16 * 4 = 64 (10-bit broadcast black).
	for i := 0; i < ySize; i++ {
		val := binary.LittleEndian.Uint16(dst10[i*2:])
		require.Equal(t, uint16(64), val,
			"Y[%d]: 8-bit black (16) should upconvert to 10-bit black (64), got %d", i, val)
	}

	// Cb: 128 * 4 = 512 (10-bit neutral).
	cbOff10 := ySize * 2
	val := binary.LittleEndian.Uint16(dst10[cbOff10:])
	require.Equal(t, uint16(512), val,
		"Cb: 8-bit neutral (128) should upconvert to 10-bit neutral (512), got %d", val)
}

// ==========================================================================
// 3. TestClip10bit_UpconvertWhitePreservesLegalRange — 8-bit broadcast white
//    upconverts to 10-bit broadcast white.
// ==========================================================================

func TestClip10bit_UpconvertWhitePreservesLegalRange(t *testing.T) {
	t.Parallel()

	w, h := 64, 48

	// 8-bit broadcast white: Y=235, Cb=128, Cr=128.
	src8 := make([]byte, switcher.ColorYUV420_8bit.FrameSize(w, h))
	ySize := w * h
	chromaW := w / 2
	chromaH := h / 2

	for i := 0; i < ySize; i++ {
		src8[i] = 235
	}
	cbOff := ySize
	for i := 0; i < chromaW*chromaH; i++ {
		src8[cbOff+i] = 128
	}
	crOff := cbOff + chromaW*chromaH
	for i := 0; i < chromaW*chromaH; i++ {
		src8[crOff+i] = 128
	}

	dst10 := make([]byte, switcher.ColorYUV422_10bit.FrameSize(w, h))
	err := switcher.Upconvert420to422_10(src8, dst10, w, h)
	require.NoError(t, err)

	// Y: 235 * 4 = 940 (10-bit broadcast white).
	for i := 0; i < ySize; i++ {
		val := binary.LittleEndian.Uint16(dst10[i*2:])
		require.Equal(t, uint16(940), val,
			"Y[%d]: 8-bit white (235) should upconvert to 10-bit white (940), got %d", i, val)
	}
}

// ==========================================================================
// 4. TestClip10bit_UpconvertChromaInterpolation — Odd chroma rows use
//    interpolation between adjacent 420 rows when upsampling to 422.
// ==========================================================================

func TestClip10bit_UpconvertChromaInterpolation(t *testing.T) {
	t.Parallel()

	w, h := 8, 8

	src8 := make([]byte, switcher.ColorYUV420_8bit.FrameSize(w, h))
	ySize := w * h
	chromaW := w / 2
	chromaH := h / 2

	// Y plane: all 128.
	for i := 0; i < ySize; i++ {
		src8[i] = 128
	}

	// Cb: row 0 = 100, row 1 = 200, row 2 = 100, row 3 = 200.
	cbOff := ySize
	for row := 0; row < chromaH; row++ {
		for col := 0; col < chromaW; col++ {
			if row%2 == 0 {
				src8[cbOff+row*chromaW+col] = 100
			} else {
				src8[cbOff+row*chromaW+col] = 200
			}
		}
	}
	// Cr: uniform 128.
	crOff := cbOff + chromaW*chromaH
	for i := 0; i < chromaW*chromaH; i++ {
		src8[crOff+i] = 128
	}

	dst10 := make([]byte, switcher.ColorYUV422_10bit.FrameSize(w, h))
	err := switcher.Upconvert420to422_10(src8, dst10, w, h)
	require.NoError(t, err)

	// In 422 output, even rows are direct copies, odd rows are interpolated.
	// 420 row 0 -> 422 rows 0,1
	// 422 row 0 (even): Cb = 100*4 = 400
	// 422 row 1 (odd): Cb = avg(row0=100, row1=200)*4 = 150*4 = 600

	cbOff10 := ySize * 2

	// Row 0: direct copy -> 400.
	row0Cb := binary.LittleEndian.Uint16(dst10[cbOff10:])
	require.Equal(t, uint16(400), row0Cb,
		"422 row 0 Cb should be 400 (direct copy of 100*4), got %d", row0Cb)

	// Row 1: interpolated -> avg(100, 200) = 150 -> 150*4 = 600.
	row1Off := cbOff10 + chromaW*2 // skip one row of chroma
	row1Cb := binary.LittleEndian.Uint16(dst10[row1Off:])
	// avg = (100 + 200 + 1) >> 1 = 150, then 150 << 2 = 600
	require.InDelta(t, 600, int(row1Cb), 4,
		"422 row 1 Cb should be ~600 (interpolated), got %d", row1Cb)
}
