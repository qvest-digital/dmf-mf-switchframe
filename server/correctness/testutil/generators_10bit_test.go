package testutil

import (
	"encoding/binary"
	"testing"

	"github.com/stretchr/testify/require"
)

// ==========================================================================
// 10-bit frame generator tests.
//
// These verify that the 10-bit YUV422P10LE generator helpers produce frames
// matching the BT.709 10-bit spec:
//   - Y limited range: [64, 940]
//   - Cb/Cr limited range: [64, 960], neutral = 512
//   - All sample values in [0, 1023]
//   - Buffer size = w * h * 4
// ==========================================================================

// GenerateSolidYUV422P10LE creates a solid-color YUV422P10LE frame.
// Layout: Y plane (w*h*2 bytes, LE uint16) + Cb plane (w/2*h*2 bytes) + Cr plane (w/2*h*2 bytes).
// Total = w*h*2 + 2*(w/2)*h*2 = w*h*4 bytes.
func GenerateSolidYUV422P10LE(w, h int, y, cb, cr uint16) []byte {
	ySize := w * h
	chromaW := w / 2
	chromaSize := chromaW * h
	totalBytes := (ySize + 2*chromaSize) * 2
	buf := make([]byte, totalBytes)

	// Y plane
	for i := 0; i < ySize; i++ {
		binary.LittleEndian.PutUint16(buf[i*2:], y)
	}

	// Cb plane
	cbOff := ySize * 2
	for i := 0; i < chromaSize; i++ {
		binary.LittleEndian.PutUint16(buf[cbOff+i*2:], cb)
	}

	// Cr plane
	crOff := cbOff + chromaSize*2
	for i := 0; i < chromaSize; i++ {
		binary.LittleEndian.PutUint16(buf[crOff+i*2:], cr)
	}

	return buf
}

// GenerateGradientYUV422P10LE creates a horizontal Y gradient from 64 to 940
// with neutral chroma (Cb=512, Cr=512) in YUV422P10LE format.
func GenerateGradientYUV422P10LE(w, h int) []byte {
	ySize := w * h
	chromaW := w / 2
	chromaSize := chromaW * h
	totalBytes := (ySize + 2*chromaSize) * 2
	buf := make([]byte, totalBytes)

	// Y plane: horizontal gradient 64 -> 940
	for row := 0; row < h; row++ {
		for col := 0; col < w; col++ {
			yVal := uint16(64 + int(float64(col)/float64(w-1)*876.0+0.5))
			binary.LittleEndian.PutUint16(buf[(row*w+col)*2:], yVal)
		}
	}

	// Cb plane: neutral 512
	cbOff := ySize * 2
	for i := 0; i < chromaSize; i++ {
		binary.LittleEndian.PutUint16(buf[cbOff+i*2:], 512)
	}

	// Cr plane: neutral 512
	crOff := cbOff + chromaSize*2
	for i := 0; i < chromaSize; i++ {
		binary.LittleEndian.PutUint16(buf[crOff+i*2:], 512)
	}

	return buf
}

func TestGenerateSolidYUV422P10LE(t *testing.T) {
	w, h := 64, 48
	y, cb, cr := uint16(500), uint16(300), uint16(700)
	frame := GenerateSolidYUV422P10LE(w, h, y, cb, cr)

	// YUV422P10LE: total = w*h*4 bytes
	expectedSize := w * h * 4
	require.Len(t, frame, expectedSize,
		"YUV422P10LE frame size should be w*h*4 = %d, got %d", expectedSize, len(frame))

	ySize := w * h
	chromaW := w / 2
	chromaSize := chromaW * h

	// Check Y plane: all samples should be y
	for i := 0; i < ySize; i++ {
		got := binary.LittleEndian.Uint16(frame[i*2:])
		require.Equal(t, y, got, "Y plane mismatch at sample %d: want %d, got %d", i, y, got)
		if i > 10 {
			break // spot-check first several, then move on
		}
	}
	// Check last Y sample too
	got := binary.LittleEndian.Uint16(frame[(ySize-1)*2:])
	require.Equal(t, y, got, "last Y sample should be %d, got %d", y, got)

	// Check Cb plane
	cbOff := ySize * 2
	for i := 0; i < chromaSize; i++ {
		got := binary.LittleEndian.Uint16(frame[cbOff+i*2:])
		require.Equal(t, cb, got, "Cb plane mismatch at sample %d", i)
		if i > 5 {
			break
		}
	}
	// Last Cb
	got = binary.LittleEndian.Uint16(frame[cbOff+(chromaSize-1)*2:])
	require.Equal(t, cb, got, "last Cb sample should be %d", cb)

	// Check Cr plane
	crOff := cbOff + chromaSize*2
	for i := 0; i < chromaSize; i++ {
		got := binary.LittleEndian.Uint16(frame[crOff+i*2:])
		require.Equal(t, cr, got, "Cr plane mismatch at sample %d", i)
		if i > 5 {
			break
		}
	}
	got = binary.LittleEndian.Uint16(frame[crOff+(chromaSize-1)*2:])
	require.Equal(t, cr, got, "last Cr sample should be %d", cr)
}

func TestGenerateSolidYUV422P10LE_BlackFrame(t *testing.T) {
	w, h := 32, 16
	// BT.709 10-bit limited black: Y=64, Cb=512, Cr=512
	frame := GenerateSolidYUV422P10LE(w, h, 64, 512, 512)

	expectedSize := w * h * 4
	require.Len(t, frame, expectedSize)

	// Verify all Y samples are exactly 64
	ySize := w * h
	for i := 0; i < ySize; i++ {
		got := binary.LittleEndian.Uint16(frame[i*2:])
		require.Equal(t, uint16(64), got,
			"10-bit black Y[%d] should be 64 (not 16), got %d", i, got)
	}

	// Verify Cb is 512 (not 128)
	cbOff := ySize * 2
	chromaSize := (w / 2) * h
	for i := 0; i < chromaSize; i++ {
		got := binary.LittleEndian.Uint16(frame[cbOff+i*2:])
		require.Equal(t, uint16(512), got,
			"10-bit neutral Cb[%d] should be 512 (not 128), got %d", i, got)
	}
}

func TestGenerateSolidYUV422P10LE_ValuesInRange(t *testing.T) {
	w, h := 16, 8
	frame := GenerateSolidYUV422P10LE(w, h, 940, 960, 960)

	totalSamples := w*h + 2*(w/2)*h
	for i := 0; i < totalSamples; i++ {
		val := binary.LittleEndian.Uint16(frame[i*2:])
		require.LessOrEqual(t, val, uint16(1023),
			"all 10-bit samples must be <= 1023, got %d at sample %d", val, i)
	}
}

func TestGenerateGradientYUV422P10LE(t *testing.T) {
	w, h := 64, 48
	frame := GenerateGradientYUV422P10LE(w, h)

	expectedSize := w * h * 4
	require.Len(t, frame, expectedSize)

	// First Y sample should be 64 (10-bit black)
	firstY := binary.LittleEndian.Uint16(frame[0:])
	require.Equal(t, uint16(64), firstY,
		"first Y pixel should be 64 (10-bit limited black), got %d", firstY)

	// Last Y in first row should be 940 (10-bit white)
	lastY := binary.LittleEndian.Uint16(frame[(w-1)*2:])
	require.Equal(t, uint16(940), lastY,
		"last Y pixel in row should be 940 (10-bit limited white), got %d", lastY)

	// All rows should have the same gradient
	row0 := make([]byte, w*2)
	copy(row0, frame[:w*2])
	for row := 1; row < h; row++ {
		off := row * w * 2
		require.Equal(t, row0, frame[off:off+w*2],
			"row %d should match row 0", row)
	}

	// Check chroma planes are neutral (512)
	ySize := w * h
	cbOff := ySize * 2
	chromaSize := (w / 2) * h
	for i := 0; i < chromaSize; i++ {
		got := binary.LittleEndian.Uint16(frame[cbOff+i*2:])
		require.Equal(t, uint16(512), got,
			"Cb[%d] should be 512 (neutral), got %d", i, got)
	}
	crOff := cbOff + chromaSize*2
	for i := 0; i < chromaSize; i++ {
		got := binary.LittleEndian.Uint16(frame[crOff+i*2:])
		require.Equal(t, uint16(512), got,
			"Cr[%d] should be 512 (neutral), got %d", i, got)
	}
}

func TestGenerateGradientYUV422P10LE_MonotonicIncrease(t *testing.T) {
	w, h := 128, 2
	frame := GenerateGradientYUV422P10LE(w, h)

	// Y values across a row should be monotonically non-decreasing
	for col := 1; col < w; col++ {
		prev := binary.LittleEndian.Uint16(frame[(col-1)*2:])
		curr := binary.LittleEndian.Uint16(frame[col*2:])
		require.GreaterOrEqual(t, curr, prev,
			"Y gradient should be monotonic: Y[%d]=%d < Y[%d]=%d", col, curr, col-1, prev)
	}
}

func TestGenerateSolidYUV422P10LE_ChromaIsFullHeight(t *testing.T) {
	// Key spec property: YUV422 has full-height chroma (unlike YUV420 which is half-height).
	w, h := 32, 16

	// In YUV422P10LE: Cb/Cr have (w/2) width but full h height.
	// Total bytes = (w*h + 2*(w/2)*h) * 2 = w*h*(1+1)*2 = w*h*4
	frame := GenerateSolidYUV422P10LE(w, h, 500, 300, 700)

	yBytes := w * h * 2
	chromaW := w / 2
	cbBytes := chromaW * h * 2 // full height!
	crBytes := chromaW * h * 2

	require.Equal(t, yBytes+cbBytes+crBytes, len(frame),
		"frame size should account for full-height chroma planes")

	// Specifically, this differs from YUV420 where chroma would be half-height:
	yuv420ChromaBytes := (w / 2) * (h / 2) * 2 // hypothetical YUV420P10LE
	require.Greater(t, cbBytes, yuv420ChromaBytes,
		"YUV422 chroma plane should be larger than YUV420 chroma plane (full vs half height)")
}
