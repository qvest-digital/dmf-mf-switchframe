//go:build correctness

package layout

import (
	"encoding/binary"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/zsiec/switchframe/server/layout"
)

// ==========================================================================
// 10-bit YUV422P10LE layout correctness tests.
//
// These verify PIP compositing, black fill, and border drawing operations
// on 10-bit frames with proper Y=64 black and 512 neutral chroma.
// ==========================================================================

const (
	testWidth10  = 320
	testHeight10 = 240
)

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
// 1. TestLayout10bit_FillBlack — FillRectBlack10 fills Y=64, Cb=512, Cr=512.
// ==========================================================================

func TestLayout10bit_FillBlack(t *testing.T) {
	t.Parallel()

	w, h := testWidth10, testHeight10

	// Start with a non-black frame.
	dst := generateSolid10(w, h, 500, 300, 700)

	// Fill a rectangle with black.
	layout.FillRectBlack10(dst, w, h, 0, 0, 100, 100)

	// Verify the filled region has correct 10-bit black.
	for py := 0; py < 100; py += 5 {
		for px := 0; px < 100; px += 5 {
			y := readY10(dst, w, px, py)
			require.Equal(t, uint16(64), y,
				"FillRectBlack10: Y at (%d,%d) should be 64, got %d", px, py, y)
		}
	}

	// Verify chroma in the filled region.
	for cy := 0; cy < 100; cy += 5 {
		for cx := 0; cx < 50; cx += 5 { // chroma is half-width
			cb := readCb10(dst, w, h, cx, cy)
			cr := readCr10(dst, w, h, cx, cy)
			require.Equal(t, uint16(512), cb,
				"FillRectBlack10: Cb at (%d,%d) should be 512, got %d", cx, cy, cb)
			require.Equal(t, uint16(512), cr,
				"FillRectBlack10: Cr at (%d,%d) should be 512, got %d", cx, cy, cr)
		}
	}

	// Verify pixels OUTSIDE the rectangle are NOT affected.
	y := readY10(dst, w, 150, 150)
	require.Equal(t, uint16(500), y,
		"pixel outside fill rect should be unchanged: Y=%d, expected 500", y)
}

// ==========================================================================
// 2. TestLayout10bit_FillBlackEntireFrame — Fill the entire frame with black.
// ==========================================================================

func TestLayout10bit_FillBlackEntireFrame(t *testing.T) {
	t.Parallel()

	w, h := 64, 48
	dst := generateSolid10(w, h, 800, 300, 700)

	layout.FillRectBlack10(dst, w, h, 0, 0, w, h)

	// Every Y sample should be 64.
	for i := 0; i < w*h; i++ {
		val := binary.LittleEndian.Uint16(dst[i*2:])
		require.Equal(t, uint16(64), val,
			"Y[%d] should be 64 after full-frame black fill, got %d", i, val)
	}

	// Every Cb/Cr should be 512.
	yBytes := w * h * 2
	chromaW := w / 2
	chromaSize := chromaW * h
	for i := 0; i < chromaSize; i++ {
		cb := binary.LittleEndian.Uint16(dst[yBytes+i*2:])
		require.Equal(t, uint16(512), cb,
			"Cb[%d] should be 512, got %d", i, cb)
	}
	crOff := yBytes + chromaSize*2
	for i := 0; i < chromaSize; i++ {
		cr := binary.LittleEndian.Uint16(dst[crOff+i*2:])
		require.Equal(t, uint16(512), cr,
			"Cr[%d] should be 512, got %d", i, cr)
	}
}

// ==========================================================================
// 3. TestLayout10bit_PIPComposite — Composite 10-bit PIP source onto
//    10-bit background, verify correct placement and value preservation.
// ==========================================================================

func TestLayout10bit_PIPComposite(t *testing.T) {
	t.Parallel()

	dstW, dstH := testWidth10, testHeight10
	srcW, srcH := 80, 60

	// Background: mid-gray.
	dst := generateSolid10(dstW, dstH, 500, 512, 512)

	// PIP source: bright with distinct chroma.
	src := generateSolid10(srcW, srcH, 800, 300, 700)

	// Place PIP at position (40, 20).
	dstX, dstY := 40, 20
	layout.ComposePIPOpaque10(dst, dstW, dstH, src, srcW, srcH, dstX, dstY)

	// Verify PIP region has the source values.
	for py := 0; py < srcH; py += 5 {
		for px := 0; px < srcW; px += 5 {
			y := readY10(dst, dstW, dstX+px, dstY+py)
			require.Equal(t, uint16(800), y,
				"PIP region Y at (%d,%d) should be 800, got %d", dstX+px, dstY+py, y)
		}
	}

	// Verify chroma in PIP region.
	for cy := 0; cy < srcH; cy += 5 {
		for cx := 0; cx < srcW/2; cx += 5 {
			cb := readCb10(dst, dstW, dstH, dstX/2+cx, dstY+cy)
			cr := readCr10(dst, dstW, dstH, dstX/2+cx, dstY+cy)
			require.Equal(t, uint16(300), cb,
				"PIP Cb at chroma (%d,%d) should be 300, got %d", dstX/2+cx, dstY+cy, cb)
			require.Equal(t, uint16(700), cr,
				"PIP Cr at chroma (%d,%d) should be 700, got %d", dstX/2+cx, dstY+cy, cr)
		}
	}

	// Verify background is preserved outside PIP.
	y := readY10(dst, dstW, 0, 0)
	require.Equal(t, uint16(500), y,
		"background pixel (0,0) should be 500, got %d", y)

	y = readY10(dst, dstW, dstW-1, dstH-1)
	require.Equal(t, uint16(500), y,
		"background pixel (%d,%d) should be 500, got %d", dstW-1, dstH-1, y)
}

// ==========================================================================
// 4. TestLayout10bit_PIPClipping — PIP that extends past frame bounds clips.
// ==========================================================================

func TestLayout10bit_PIPClipping(t *testing.T) {
	t.Parallel()

	dstW, dstH := 64, 48
	srcW, srcH := 40, 30

	dst := generateSolid10(dstW, dstH, 200, 512, 512)
	src := generateSolid10(srcW, srcH, 900, 400, 600)

	// Place PIP so it extends past the right and bottom edges.
	dstX, dstY := 40, 30
	layout.ComposePIPOpaque10(dst, dstW, dstH, src, srcW, srcH, dstX, dstY)

	// The visible PIP region should be clipped to (dstW-dstX) x (dstH-dstY) = 24 x 18.
	// Verify a pixel within the clipped region has source values.
	y := readY10(dst, dstW, 50, 35)
	require.Equal(t, uint16(900), y,
		"clipped PIP pixel should have source Y=900, got %d", y)

	// Verify a pixel at the top-left corner is still background.
	y = readY10(dst, dstW, 0, 0)
	require.Equal(t, uint16(200), y,
		"background pixel should be 200, got %d", y)
}

// ==========================================================================
// 5. TestLayout10bit_Border — DrawBorderYUV10 draws border with correct
//    10-bit color values around a rectangle.
// ==========================================================================

func TestLayout10bit_Border(t *testing.T) {
	t.Parallel()

	dstW, dstH := testWidth10, testHeight10
	dst := generateSolid10(dstW, dstH, 500, 512, 512)

	// Draw a border around a rectangle at (40, 20, 80x60) with width=4.
	// Border color: Y=940 (white), Cb=512, Cr=512.
	layout.DrawBorderYUV10(dst, dstW, dstH, 40, 20, 80, 60, 4, 940, 512, 512)

	// Verify top border strip (rows 16-19, cols 36-123).
	y := readY10(dst, dstW, 40, 16) // top border row
	require.Equal(t, uint16(940), y,
		"top border Y should be 940, got %d", y)

	// Verify bottom border strip.
	y = readY10(dst, dstW, 40, 80) // just below the rect (20+60=80)
	require.Equal(t, uint16(940), y,
		"bottom border Y should be 940, got %d", y)

	// Verify the interior of the rectangle is unchanged.
	y = readY10(dst, dstW, 60, 40)
	require.Equal(t, uint16(500), y,
		"interior pixel should be unchanged at 500, got %d", y)

	// Verify far outside is unchanged.
	y = readY10(dst, dstW, 200, 150)
	require.Equal(t, uint16(500), y,
		"far outside pixel should be unchanged at 500, got %d", y)
}

// ==========================================================================
// 6. TestLayout10bit_FillBlackClipping — FillRectBlack10 clips to bounds.
// ==========================================================================

func TestLayout10bit_FillBlackClipping(t *testing.T) {
	t.Parallel()

	w, h := 32, 32
	dst := generateSolid10(w, h, 700, 400, 600)

	// Fill rect that exceeds frame bounds.
	layout.FillRectBlack10(dst, w, h, 20, 20, 100, 100)

	// Should fill only the intersection: (20,20) to (31,31).
	y := readY10(dst, w, 25, 25)
	require.Equal(t, uint16(64), y,
		"inside clipped fill: Y should be 64, got %d", y)

	// Outside should be unchanged.
	y = readY10(dst, w, 10, 10)
	require.Equal(t, uint16(700), y,
		"outside fill: Y should be 700, got %d", y)
}
