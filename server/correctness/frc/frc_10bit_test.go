//go:build correctness

package frc

import (
	"encoding/binary"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/zsiec/switchframe/server/switcher"
)

// ==========================================================================
// 10-bit YUV422P10LE FRC (frame rate conversion) correctness tests.
//
// These verify SAD10 computation and Interpolate10 (10-bit MCFI blend) with
// proper 10-bit sample arithmetic.
// ==========================================================================

// generateSolid10 creates a solid-color YUV422P10LE frame.
func generateSolid10(w, h int, y, cb, cr uint16) []byte {
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

// readY10 reads the Y sample at luma position (x, y).
func readY10(buf []byte, w, x, y int) uint16 {
	return binary.LittleEndian.Uint16(buf[(y*w+x)*2:])
}

// ==========================================================================
// 1. TestFRC10bit_SAD_Identical — SAD on identical blocks is 0.
// ==========================================================================

func TestFRC10bit_SAD_Identical(t *testing.T) {
	t.Parallel()

	w, h := 32, 32
	frame := generateSolid10(w, h, 500, 512, 512)

	// SAD of a block with itself should be 0.
	blockW := 8
	blockH := 8
	sad := switcher.SAD10(frame, frame, 0, 0, w, blockW, blockH)
	require.Equal(t, 0, sad, "SAD of identical blocks should be 0")
}

// ==========================================================================
// 2. TestFRC10bit_SAD_MaxDifference — SAD on max-difference blocks.
// ==========================================================================

func TestFRC10bit_SAD_MaxDifference(t *testing.T) {
	t.Parallel()

	w, h := 32, 32
	blockW := 8
	blockH := 8

	// Frame A: all Y=0.
	frameA := generateSolid10(w, h, 0, 512, 512)
	// Frame B: all Y=1023.
	frameB := generateSolid10(w, h, 1023, 512, 512)

	sad := switcher.SAD10(frameA, frameB, 0, 0, w, blockW, blockH)
	expected := blockW * blockH * 1023
	require.Equal(t, expected, sad,
		"SAD of max-difference blocks should be %d, got %d", expected, sad)
}

// ==========================================================================
// 3. TestFRC10bit_SAD_KnownDifference — SAD with known per-pixel delta.
// ==========================================================================

func TestFRC10bit_SAD_KnownDifference(t *testing.T) {
	t.Parallel()

	w, h := 32, 32
	blockW := 4
	blockH := 4

	// Frame A: Y=200 everywhere.
	frameA := generateSolid10(w, h, 200, 512, 512)
	// Frame B: Y=300 everywhere. Difference per pixel = 100.
	frameB := generateSolid10(w, h, 300, 512, 512)

	sad := switcher.SAD10(frameA, frameB, 0, 0, w, blockW, blockH)
	expected := blockW * blockH * 100
	require.Equal(t, expected, sad,
		"SAD with delta=100: expected %d, got %d", expected, sad)
}

// ==========================================================================
// 4. TestFRC10bit_Interpolate_AtZero — t=0 returns frameA.
// ==========================================================================

func TestFRC10bit_Interpolate_AtZero(t *testing.T) {
	t.Parallel()

	w, h := testWidth, testHeight
	frameA := generateSolid10(w, h, 200, 400, 600)
	frameB := generateSolid10(w, h, 800, 200, 800)

	mcfi := switcher.NewMCFIState()
	result := mcfi.Interpolate10(frameA, frameB, w, h, 0.0)
	require.NotNil(t, result)

	require.Equal(t, frameA[:len(result)], result,
		"Interpolate10 at t=0 should return frameA")
}

// ==========================================================================
// 5. TestFRC10bit_Interpolate_AtOne — t=1 returns frameB.
// ==========================================================================

func TestFRC10bit_Interpolate_AtOne(t *testing.T) {
	t.Parallel()

	w, h := testWidth, testHeight
	frameA := generateSolid10(w, h, 200, 400, 600)
	frameB := generateSolid10(w, h, 800, 200, 800)

	mcfi := switcher.NewMCFIState()
	result := mcfi.Interpolate10(frameA, frameB, w, h, 1.0)
	require.NotNil(t, result)

	require.Equal(t, frameB[:len(result)], result,
		"Interpolate10 at t=1 should return frameB")
}

// ==========================================================================
// 6. TestFRC10bit_Interpolate_Midpoint — t=0.5 produces linear interpolation.
// ==========================================================================

func TestFRC10bit_Interpolate_Midpoint(t *testing.T) {
	t.Parallel()

	w, h := testWidth, testHeight
	frameA := generateSolid10(w, h, 200, 512, 512)
	frameB := generateSolid10(w, h, 800, 512, 512)

	mcfi := switcher.NewMCFIState()
	result := mcfi.Interpolate10(frameA, frameB, w, h, 0.5)
	require.NotNil(t, result)

	y := readY10(result, w, 0, 0)
	// At 50% blend: ~500
	require.InDelta(t, 500, int(y), 2,
		"Interpolate10 at t=0.5: Y should be ~500, got %d", y)
}

// ==========================================================================
// 7. TestFRC10bit_Interpolate_QuarterPositions — Verify at 25% and 75%.
// ==========================================================================

func TestFRC10bit_Interpolate_QuarterPositions(t *testing.T) {
	t.Parallel()

	w, h := testWidth, testHeight
	frameA := generateSolid10(w, h, 200, 512, 512)
	frameB := generateSolid10(w, h, 800, 512, 512)

	mcfi := switcher.NewMCFIState()

	// t=0.25: result should be ~350
	result25 := mcfi.Interpolate10(frameA, frameB, w, h, 0.25)
	y25 := readY10(result25, w, 0, 0)
	require.InDelta(t, 350, int(y25), 5,
		"Interpolate10 at t=0.25: Y should be ~350, got %d", y25)

	// t=0.75: result should be ~650
	result75 := mcfi.Interpolate10(frameA, frameB, w, h, 0.75)
	y75 := readY10(result75, w, 0, 0)
	require.InDelta(t, 650, int(y75), 5,
		"Interpolate10 at t=0.75: Y should be ~650, got %d", y75)
}

// ==========================================================================
// 8. TestFRC10bit_Interpolate_ChromaPreserved — Chroma blending at midpoint.
// ==========================================================================

func TestFRC10bit_Interpolate_ChromaPreserved(t *testing.T) {
	t.Parallel()

	w, h := testWidth, testHeight
	frameA := generateSolid10(w, h, 400, 200, 300)
	frameB := generateSolid10(w, h, 600, 800, 700)

	mcfi := switcher.NewMCFIState()
	result := mcfi.Interpolate10(frameA, frameB, w, h, 0.5)
	require.NotNil(t, result)

	// Check chroma at midpoint.
	ySize := w * h
	chromaW := w / 2
	cbOff := ySize * 2
	cb := binary.LittleEndian.Uint16(result[cbOff:])
	require.InDelta(t, 500, int(cb), 5,
		"Interpolate10 at t=0.5: Cb should be ~500, got %d", cb)

	chromaSize := chromaW * h
	crOff := cbOff + chromaSize*2
	cr := binary.LittleEndian.Uint16(result[crOff:])
	require.InDelta(t, 500, int(cr), 5,
		"Interpolate10 at t=0.5: Cr should be ~500, got %d", cr)
}

// ==========================================================================
// 9. TestFRC10bit_Interpolate_OutputSize — Output size matches expected.
// ==========================================================================

func TestFRC10bit_Interpolate_OutputSize(t *testing.T) {
	t.Parallel()

	w, h := testWidth, testHeight
	frameA := generateSolid10(w, h, 200, 512, 512)
	frameB := generateSolid10(w, h, 800, 512, 512)

	mcfi := switcher.NewMCFIState()
	result := mcfi.Interpolate10(frameA, frameB, w, h, 0.5)

	expectedSize := switcher.ColorYUV422_10bit.FrameSize(w, h)
	require.Len(t, result, expectedSize,
		"Interpolate10 output should be %d bytes for %dx%d, got %d",
		expectedSize, w, h, len(result))
}
