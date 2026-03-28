package testutil

import (
	"encoding/binary"
	"testing"

	"github.com/stretchr/testify/require"
)

// ==========================================================================
// 10-bit YUV pixel reading/writing helpers and plane offset tests.
//
// YUV422P10LE layout:
//   Y  plane: w * h     samples, each uint16 LE -> w*h*2 bytes
//   Cb plane: (w/2) * h samples, each uint16 LE -> (w/2)*h*2 bytes
//   Cr plane: (w/2) * h samples, each uint16 LE -> (w/2)*h*2 bytes
//   Total: w*h*4 bytes
//
// Key difference from YUV420P: chroma height = h (not h/2).
// ==========================================================================

// YUV422P10LEPixelAt reads a pixel from a planar YUV422P10LE frame.
// Returns Y, Cb, Cr as uint16 values.
func YUV422P10LEPixelAt(buf []byte, w, h, x, y int) (yVal, cb, cr uint16) {
	// Y plane: w*h samples at offset 0, each 2 bytes
	yOff := (y*w + x) * 2
	yVal = binary.LittleEndian.Uint16(buf[yOff:])

	// Chroma planes: (w/2)*h each, full height (not half height)
	chromaW := w / 2
	chromaX := x / 2
	chromaY := y // full height, not y/2

	ySize := w * h
	chromaSize := chromaW * h

	cbOff := ySize*2 + (chromaY*chromaW+chromaX)*2
	crOff := ySize*2 + chromaSize*2 + (chromaY*chromaW+chromaX)*2

	cb = binary.LittleEndian.Uint16(buf[cbOff:])
	cr = binary.LittleEndian.Uint16(buf[crOff:])
	return
}

// YUV422P10LEInLegalRange checks that all Y samples are in [64, 940]
// and all Cb/Cr samples are in [64, 960] (BT.709 10-bit limited range).
func YUV422P10LEInLegalRange(buf []byte, w, h int) bool {
	ySize := w * h
	chromaW := w / 2
	chromaSize := chromaW * h

	// Check Y plane
	for i := 0; i < ySize; i++ {
		v := binary.LittleEndian.Uint16(buf[i*2:])
		if v < 64 || v > 940 {
			return false
		}
	}

	// Check Cb plane
	cbOff := ySize * 2
	for i := 0; i < chromaSize; i++ {
		v := binary.LittleEndian.Uint16(buf[cbOff+i*2:])
		if v < 64 || v > 960 {
			return false
		}
	}

	// Check Cr plane
	crOff := cbOff + chromaSize*2
	for i := 0; i < chromaSize; i++ {
		v := binary.LittleEndian.Uint16(buf[crOff+i*2:])
		if v < 64 || v > 960 {
			return false
		}
	}

	return true
}

// --- Tests ---

func TestYUV422P10LEPixelAt(t *testing.T) {
	w, h := 8, 8
	frame := GenerateSolidYUV422P10LE(w, h, 500, 300, 700)

	yVal, cb, cr := YUV422P10LEPixelAt(frame, w, h, 0, 0)
	require.Equal(t, uint16(500), yVal, "Y at (0,0)")
	require.Equal(t, uint16(300), cb, "Cb at (0,0)")
	require.Equal(t, uint16(700), cr, "Cr at (0,0)")

	// Check last pixel
	yVal, cb, cr = YUV422P10LEPixelAt(frame, w, h, w-1, h-1)
	require.Equal(t, uint16(500), yVal, "Y at last pixel")
	require.Equal(t, uint16(300), cb, "Cb at last pixel")
	require.Equal(t, uint16(700), cr, "Cr at last pixel")
}

func TestYUV422P10LEPixelAt_ChromaSubsampling(t *testing.T) {
	// In YUV422, chroma is half-width but FULL-height.
	// Pixels (0,0) and (1,0) share the same chroma sample.
	// Pixels (0,0) and (0,1) have DIFFERENT chroma rows (full height).
	w, h := 4, 4
	ySize := w * h
	chromaW := w / 2
	chromaSize := chromaW * h
	totalBytes := (ySize + 2*chromaSize) * 2
	frame := make([]byte, totalBytes)

	// Fill Y with 500
	for i := 0; i < ySize; i++ {
		binary.LittleEndian.PutUint16(frame[i*2:], 500)
	}

	// Set distinct Cb/Cr for each chroma sample to verify addressing
	cbOff := ySize * 2
	crOff := cbOff + chromaSize*2

	// Cb at chroma position (0,0) = 200
	binary.LittleEndian.PutUint16(frame[cbOff:], 200)
	// Cb at chroma position (1,0) = 300
	binary.LittleEndian.PutUint16(frame[cbOff+2:], 300)
	// Cb at chroma position (0,1) = 400 (next row -- full height!)
	binary.LittleEndian.PutUint16(frame[cbOff+chromaW*2:], 400)

	// Cr at chroma position (0,0) = 600
	binary.LittleEndian.PutUint16(frame[crOff:], 600)
	// Cr at chroma position (1,0) = 700
	binary.LittleEndian.PutUint16(frame[crOff+2:], 700)
	// Cr at chroma position (0,1) = 800
	binary.LittleEndian.PutUint16(frame[crOff+chromaW*2:], 800)

	// Luma pixels (0,0) and (1,0) share chroma sample (0,0)
	_, cb00, cr00 := YUV422P10LEPixelAt(frame, w, h, 0, 0)
	require.Equal(t, uint16(200), cb00, "Cb at pixel (0,0) = chroma (0,0)")
	require.Equal(t, uint16(600), cr00, "Cr at pixel (0,0) = chroma (0,0)")

	_, cb10, cr10 := YUV422P10LEPixelAt(frame, w, h, 1, 0)
	require.Equal(t, uint16(200), cb10, "pixel (1,0) shares chroma with (0,0)")
	require.Equal(t, uint16(600), cr10)

	// Luma pixels (2,0) and (3,0) share chroma sample (1,0)
	_, cb20, cr20 := YUV422P10LEPixelAt(frame, w, h, 2, 0)
	require.Equal(t, uint16(300), cb20, "pixel (2,0) = chroma (1,0)")
	require.Equal(t, uint16(700), cr20)

	// Luma pixel (0,1) uses chroma sample (0,1) -- different row!
	// This is the key difference from YUV420: each row has its own chroma.
	_, cb01, cr01 := YUV422P10LEPixelAt(frame, w, h, 0, 1)
	require.Equal(t, uint16(400), cb01,
		"pixel (0,1) should use chroma row 1, not row 0 (YUV422 = full height chroma)")
	require.Equal(t, uint16(800), cr01)
}

func TestYUV422P10LEInLegalRange(t *testing.T) {
	w, h := 8, 8

	// Legal: Y=500 (in [64,940]), Cb=Cr=512 (in [64,960])
	legal := GenerateSolidYUV422P10LE(w, h, 500, 512, 512)
	require.True(t, YUV422P10LEInLegalRange(legal, w, h),
		"Y=500, Cb=Cr=512 should be in legal range")

	// Boundary legal: Y=64, Cb=Cr=64
	edgeLow := GenerateSolidYUV422P10LE(w, h, 64, 64, 64)
	require.True(t, YUV422P10LEInLegalRange(edgeLow, w, h),
		"Y=64, Cb=Cr=64 are minimum legal values")

	// Boundary legal: Y=940, Cb=Cr=960
	edgeHigh := GenerateSolidYUV422P10LE(w, h, 940, 960, 960)
	require.True(t, YUV422P10LEInLegalRange(edgeHigh, w, h),
		"Y=940, Cb=Cr=960 are maximum legal values")

	// Illegal: Y=0 (below 64)
	illegalY0 := GenerateSolidYUV422P10LE(w, h, 0, 512, 512)
	require.False(t, YUV422P10LEInLegalRange(illegalY0, w, h),
		"Y=0 is below 10-bit limited range minimum of 64")

	// Illegal: Y=63 (one below minimum)
	illegalY63 := GenerateSolidYUV422P10LE(w, h, 63, 512, 512)
	require.False(t, YUV422P10LEInLegalRange(illegalY63, w, h),
		"Y=63 is below 10-bit limited range minimum of 64")

	// Illegal: Y=941 (one above maximum)
	illegalY941 := GenerateSolidYUV422P10LE(w, h, 941, 512, 512)
	require.False(t, YUV422P10LEInLegalRange(illegalY941, w, h),
		"Y=941 is above 10-bit limited range maximum of 940")

	// Illegal: Y=1023 (max possible)
	illegalY1023 := GenerateSolidYUV422P10LE(w, h, 1023, 512, 512)
	require.False(t, YUV422P10LEInLegalRange(illegalY1023, w, h),
		"Y=1023 is above 10-bit limited range maximum of 940")

	// Illegal: Cb=0
	illegalCb := GenerateSolidYUV422P10LE(w, h, 500, 0, 512)
	require.False(t, YUV422P10LEInLegalRange(illegalCb, w, h),
		"Cb=0 is below 10-bit chroma minimum of 64")

	// Illegal: Cr=961 (one above maximum)
	illegalCr := GenerateSolidYUV422P10LE(w, h, 500, 512, 961)
	require.False(t, YUV422P10LEInLegalRange(illegalCr, w, h),
		"Cr=961 is above 10-bit chroma maximum of 960")
}

func TestYUV422P10LE_PlaneOffsets(t *testing.T) {
	w, h := 16, 8
	ySize := w * h
	chromaW := w / 2
	chromaSize := chromaW * h

	// Verify plane offset calculations match the spec.
	yPlaneBytes := ySize * 2
	cbPlaneBytes := chromaSize * 2
	crPlaneBytes := chromaSize * 2
	totalBytes := yPlaneBytes + cbPlaneBytes + crPlaneBytes

	// w*h*4 identity
	require.Equal(t, w*h*4, totalBytes,
		"total frame size should equal w*h*4 for YUV422P10LE")

	// Y plane starts at 0
	yPlaneOffset := 0
	require.Equal(t, 0, yPlaneOffset)

	// Cb plane starts at ySize*2
	cbPlaneOffset := ySize * 2
	require.Equal(t, yPlaneBytes, cbPlaneOffset)

	// Cr plane starts at ySize*2 + chromaSize*2
	crPlaneOffset := ySize*2 + chromaSize*2
	require.Equal(t, yPlaneBytes+cbPlaneBytes, crPlaneOffset)

	// Create frame and verify we can read the planes back
	frame := GenerateSolidYUV422P10LE(w, h, 111, 222, 333)
	require.Len(t, frame, totalBytes)

	// Read from calculated offsets
	yAtStart := binary.LittleEndian.Uint16(frame[yPlaneOffset:])
	require.Equal(t, uint16(111), yAtStart)

	cbAtStart := binary.LittleEndian.Uint16(frame[cbPlaneOffset:])
	require.Equal(t, uint16(222), cbAtStart)

	crAtStart := binary.LittleEndian.Uint16(frame[crPlaneOffset:])
	require.Equal(t, uint16(333), crAtStart)
}

func TestYUV422P10LE_FrameEqual(t *testing.T) {
	w, h := 8, 8

	a := GenerateSolidYUV422P10LE(w, h, 500, 512, 512)
	b := GenerateSolidYUV422P10LE(w, h, 500, 512, 512)
	c := GenerateSolidYUV422P10LE(w, h, 501, 512, 512)

	require.True(t, YUVFrameEqual(a, b), "identical 10-bit frames should be equal")
	require.False(t, YUVFrameEqual(a, c), "frames with different Y should not be equal")

	// Different lengths (8-bit vs 10-bit frame)
	d := GenerateSolidYUV420(w, h, 128, 128, 128)
	require.False(t, YUVFrameEqual(a, d),
		"10-bit and 8-bit frames have different sizes and should not be equal")
}
