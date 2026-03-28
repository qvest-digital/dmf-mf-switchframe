//go:build correctness

package keying

import (
	"encoding/binary"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/zsiec/switchframe/server/graphics"
)

// ==========================================================================
// 10-bit YUV422P10LE keying correctness tests.
//
// These verify ChromaKey10 and LumaKey10 on 10-bit frames, including
// upstream key compositing with mask-based blending.
// ==========================================================================

// generateSolid10 creates a solid-color YUV422P10LE frame.
func generateSolid10(w, h int, y, cb, cr uint16) []byte {
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

// ==========================================================================
// 1. TestKeying10bit_ChromaKeyGreen — Pure green frame at 10-bit values
//    produces a fully transparent mask.
// ==========================================================================

func TestKeying10bit_ChromaKeyGreen(t *testing.T) {
	t.Parallel()

	w, h := 32, 32

	// BT.709 green in 10-bit: Y~580 (145*4), Cb~216 (54*4), Cr~104 (26*4)
	greenCb10 := uint16(216)
	greenCr10 := uint16(104)

	greenFrame := generateSolid10(w, h, 580, greenCb10, greenCr10)

	mask := graphics.ChromaKey10(greenFrame, w, h, greenCb10, greenCr10, 100, 50)
	require.NotNil(t, mask)
	require.Len(t, mask, w*h)

	// All pixels should be keyed (high mask values).
	for i, m := range mask {
		require.GreaterOrEqual(t, m, byte(200),
			"pixel %d: green should be keyed (>=200), got %d", i, m)
	}
}

// ==========================================================================
// 2. TestKeying10bit_ChromaKeyNonGreen — Red frame at 10-bit values
//    produces a fully opaque mask when keying green.
// ==========================================================================

func TestKeying10bit_ChromaKeyNonGreen(t *testing.T) {
	t.Parallel()

	w, h := 32, 32

	// Green key target.
	greenCb10 := uint16(216)
	greenCr10 := uint16(104)

	// Red in 10-bit: Cb~360 (90*4), Cr~960 (240*4)
	redFrame := generateSolid10(w, h, 324, 360, 960)

	mask := graphics.ChromaKey10(redFrame, w, h, greenCb10, greenCr10, 100, 50)
	require.NotNil(t, mask)
	require.Len(t, mask, w*h)

	// Red pixels should NOT be keyed (mask=0 for non-matching chroma).
	for i, m := range mask {
		require.LessOrEqual(t, m, byte(5),
			"pixel %d: red should not be keyed when keying green, got %d", i, m)
	}
}

// ==========================================================================
// 3. TestKeying10bit_LumaKeyThreshold — Luma key at 10-bit threshold.
// ==========================================================================

func TestKeying10bit_LumaKeyThreshold(t *testing.T) {
	t.Parallel()

	w, h := 32, 32

	// Test: keep pixels with Y in [400, 800]
	lowThresh := uint16(400)
	highThresh := uint16(800)

	// Frame with Y=500 (within range).
	inRangeFrame := generateSolid10(w, h, 500, 512, 512)
	mask := graphics.LumaKey10(inRangeFrame, w, h, lowThresh, highThresh)
	require.NotNil(t, mask)
	for i, m := range mask {
		require.Equal(t, byte(255), m,
			"pixel %d: Y=500 within [400,800] should be opaque (255), got %d", i, m)
	}

	// Frame with Y=200 (below range).
	belowFrame := generateSolid10(w, h, 200, 512, 512)
	maskBelow := graphics.LumaKey10(belowFrame, w, h, lowThresh, highThresh)
	require.NotNil(t, maskBelow)
	for i, m := range maskBelow {
		require.Equal(t, byte(0), m,
			"pixel %d: Y=200 below 400 should be keyed (0), got %d", i, m)
	}

	// Frame with Y=900 (above range).
	aboveFrame := generateSolid10(w, h, 900, 512, 512)
	maskAbove := graphics.LumaKey10(aboveFrame, w, h, lowThresh, highThresh)
	require.NotNil(t, maskAbove)
	for i, m := range maskAbove {
		require.Equal(t, byte(0), m,
			"pixel %d: Y=900 above 800 should be keyed (0), got %d", i, m)
	}
}

// ==========================================================================
// 4. TestKeying10bit_ChromaKeyMaskDeterminism — Repeated keying produces
//    identical masks.
// ==========================================================================

func TestKeying10bit_ChromaKeyMaskDeterminism(t *testing.T) {
	t.Parallel()

	w, h := 64, 48
	greenCb10 := uint16(216)
	greenCr10 := uint16(104)

	frame := generateSolid10(w, h, 580, greenCb10, greenCr10)

	reference := graphics.ChromaKey10(frame, w, h, greenCb10, greenCr10, 100, 50)
	require.NotNil(t, reference)

	for i := 0; i < 20; i++ {
		mask := graphics.ChromaKey10(frame, w, h, greenCb10, greenCr10, 100, 50)
		require.Equal(t, reference, mask,
			"run %d: chroma key mask should be deterministic", i)
	}
}

// ==========================================================================
// 5. TestKeying10bit_LumaKeyBoundary — Exact boundary values.
// ==========================================================================

func TestKeying10bit_LumaKeyBoundary(t *testing.T) {
	t.Parallel()

	w, h := 8, 8

	// At exact lowThresh.
	atLow := generateSolid10(w, h, 400, 512, 512)
	mask := graphics.LumaKey10(atLow, w, h, 400, 800)
	for _, m := range mask {
		require.Equal(t, byte(255), m, "Y=400 at lowThresh should be opaque")
	}

	// At exact highThresh.
	atHigh := generateSolid10(w, h, 800, 512, 512)
	mask = graphics.LumaKey10(atHigh, w, h, 400, 800)
	for _, m := range mask {
		require.Equal(t, byte(255), m, "Y=800 at highThresh should be opaque")
	}

	// Just below lowThresh.
	belowLow := generateSolid10(w, h, 399, 512, 512)
	mask = graphics.LumaKey10(belowLow, w, h, 400, 800)
	for _, m := range mask {
		require.Equal(t, byte(0), m, "Y=399 below lowThresh should be keyed")
	}

	// Just above highThresh.
	aboveHigh := generateSolid10(w, h, 801, 512, 512)
	mask = graphics.LumaKey10(aboveHigh, w, h, 400, 800)
	for _, m := range mask {
		require.Equal(t, byte(0), m, "Y=801 above highThresh should be keyed")
	}
}

// ==========================================================================
// 6. TestKeying10bit_ChromaKeyHalfGreenHalfRed — Mixed chroma frame.
// ==========================================================================

func TestKeying10bit_ChromaKeyHalfGreenHalfRed(t *testing.T) {
	t.Parallel()

	w, h := 16, 16
	greenCb10 := uint16(216)
	greenCr10 := uint16(104)

	// Build a frame: left half green chroma, right half red chroma.
	frameSize := w * h * 4
	frame := make([]byte, frameSize)

	ySize := w * h
	chromaW := w / 2

	// Fill Y plane with mid-gray.
	for i := 0; i < ySize; i++ {
		binary.LittleEndian.PutUint16(frame[i*2:], 500)
	}

	// Fill chroma planes.
	cbOff := ySize * 2
	chromaSize := chromaW * h
	crOff := cbOff + chromaSize*2

	for py := 0; py < h; py++ {
		for cx := 0; cx < chromaW; cx++ {
			ci := py*chromaW + cx
			if cx < chromaW/2 {
				// Left half: green chroma.
				binary.LittleEndian.PutUint16(frame[cbOff+ci*2:], greenCb10)
				binary.LittleEndian.PutUint16(frame[crOff+ci*2:], greenCr10)
			} else {
				// Right half: red chroma.
				binary.LittleEndian.PutUint16(frame[cbOff+ci*2:], 360)
				binary.LittleEndian.PutUint16(frame[crOff+ci*2:], 960)
			}
		}
	}

	mask := graphics.ChromaKey10(frame, w, h, greenCb10, greenCr10, 100, 50)
	require.NotNil(t, mask)
	require.Len(t, mask, w*h)

	// Left half pixels (green) should be keyed.
	for py := 0; py < h; py++ {
		for px := 0; px < w/2; px++ {
			m := mask[py*w+px]
			require.GreaterOrEqual(t, m, byte(200),
				"left half pixel (%d,%d) should be keyed (green), got %d", px, py, m)
		}
	}

	// Right half pixels (red) should NOT be keyed.
	for py := 0; py < h; py++ {
		for px := w / 2; px < w; px++ {
			m := mask[py*w+px]
			require.LessOrEqual(t, m, byte(5),
				"right half pixel (%d,%d) should not be keyed (red), got %d", px, py, m)
		}
	}
}
