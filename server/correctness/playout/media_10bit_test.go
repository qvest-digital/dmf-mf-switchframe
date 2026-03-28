//go:build correctness

package playout

import (
	"encoding/binary"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/zsiec/switchframe/server/switcher"
)

// ==========================================================================
// 10-bit playout media tests.
//
// These verify that playout media operations correctly handle 10-bit frames:
// - Idle/black frames use Y=64, Cb=512, Cr=512 (not 8-bit values)
// - Frame sizes are w*h*4
// - Color values are within valid 10-bit ranges
//
// Note: playout_media_10bit_test.go already tests the playout module's
// NewIdleFrame10 helper. These tests complement those by testing frame
// construction and validation from the correctness test perspective.
// ==========================================================================

// TestMediaFrame10bit_BlackLevel verifies that 10-bit black frames for
// playout use correct BT.709 10-bit limited-range values.
func TestMediaFrame10bit_BlackLevel(t *testing.T) {
	t.Parallel()

	format := switcher.ColorYUV422_10bit

	// BT.709 10-bit limited range constants.
	require.Equal(t, 64, format.BlackY(),
		"10-bit black Y should be 64 (not 16)")
	require.Equal(t, 512, format.NeutralChroma(),
		"10-bit neutral chroma should be 512 (not 128)")

	// Create a black frame.
	w, h := 320, 240
	frameSize := format.FrameSize(w, h)
	require.Equal(t, w*h*4, frameSize,
		"10-bit frame should be w*h*4 = %d bytes", w*h*4)

	black := make([]byte, frameSize)
	ySize := w * h
	chromaW := w / 2
	chromaSize := chromaW * h

	for i := 0; i < ySize; i++ {
		binary.LittleEndian.PutUint16(black[i*2:], uint16(format.BlackY()))
	}
	cbOff := ySize * 2
	for i := 0; i < chromaSize; i++ {
		binary.LittleEndian.PutUint16(black[cbOff+i*2:], uint16(format.NeutralChroma()))
	}
	crOff := cbOff + chromaSize*2
	for i := 0; i < chromaSize; i++ {
		binary.LittleEndian.PutUint16(black[crOff+i*2:], uint16(format.NeutralChroma()))
	}

	// Verify all Y samples are 64.
	for i := 0; i < ySize; i++ {
		got := binary.LittleEndian.Uint16(black[i*2:])
		if got != 64 {
			t.Fatalf("black Y[%d] = %d, want 64", i, got)
		}
	}

	// Verify all Cb samples are 512.
	for i := 0; i < chromaSize; i++ {
		got := binary.LittleEndian.Uint16(black[cbOff+i*2:])
		if got != 512 {
			t.Fatalf("black Cb[%d] = %d, want 512", i, got)
		}
	}

	// Verify all Cr samples are 512.
	for i := 0; i < chromaSize; i++ {
		got := binary.LittleEndian.Uint16(black[crOff+i*2:])
		if got != 512 {
			t.Fatalf("black Cr[%d] = %d, want 512", i, got)
		}
	}
}

// TestMediaFrame10bit_WhiteLevel verifies that 10-bit white frames use
// Y=940 (not 235).
func TestMediaFrame10bit_WhiteLevel(t *testing.T) {
	t.Parallel()

	format := switcher.ColorYUV422_10bit

	require.Equal(t, 940, format.WhiteY(),
		"10-bit white Y should be 940 (not 235)")

	w, h := 64, 48
	frameSize := format.FrameSize(w, h)
	white := make([]byte, frameSize)
	ySize := w * h
	chromaW := w / 2
	chromaSize := chromaW * h

	for i := 0; i < ySize; i++ {
		binary.LittleEndian.PutUint16(white[i*2:], uint16(format.WhiteY()))
	}
	cbOff := ySize * 2
	for i := 0; i < chromaSize; i++ {
		binary.LittleEndian.PutUint16(white[cbOff+i*2:], uint16(format.NeutralChroma()))
	}
	crOff := cbOff + chromaSize*2
	for i := 0; i < chromaSize; i++ {
		binary.LittleEndian.PutUint16(white[crOff+i*2:], uint16(format.NeutralChroma()))
	}

	// Verify Y samples.
	got := binary.LittleEndian.Uint16(white[0:])
	require.Equal(t, uint16(940), got,
		"white Y[0] should be 940, got %d", got)

	lastY := binary.LittleEndian.Uint16(white[(ySize-1)*2:])
	require.Equal(t, uint16(940), lastY,
		"white Y[last] should be 940, got %d", lastY)
}

// TestMediaFrame10bit_FrameSizeIsWH4 verifies the w*h*4 formula at
// multiple resolutions.
func TestMediaFrame10bit_FrameSizeIsWH4(t *testing.T) {
	t.Parallel()

	format := switcher.ColorYUV422_10bit

	resolutions := []struct{ w, h int }{
		{320, 240},
		{640, 480},
		{1280, 720},
		{1920, 1080},
		{3840, 2160},
	}

	for _, r := range resolutions {
		size := format.FrameSize(r.w, r.h)
		expected := r.w * r.h * 4
		require.Equal(t, expected, size,
			"%dx%d: FrameSize should be w*h*4 = %d, got %d", r.w, r.h, expected, size)
	}
}

// TestMediaFrame10bit_MaxSampleValue verifies that the maximum representable
// 10-bit sample value is 1023.
func TestMediaFrame10bit_MaxSampleValue(t *testing.T) {
	t.Parallel()

	format := switcher.ColorYUV422_10bit
	require.Equal(t, 1023, format.MaxSampleValue(),
		"10-bit max sample should be 1023 (2^10 - 1)")

	// Verify we can encode 1023 in a uint16 LE buffer.
	buf := make([]byte, 2)
	binary.LittleEndian.PutUint16(buf, 1023)
	got := binary.LittleEndian.Uint16(buf)
	require.Equal(t, uint16(1023), got)

	// Values above 1023 are invalid for 10-bit.
	// This is a spec constraint, not enforced by the buffer format.
}

// TestMediaFrame10bit_ChromaFullHeight verifies that YUV422 has full-height
// chroma planes (height = h, not h/2 like YUV420).
func TestMediaFrame10bit_ChromaFullHeight(t *testing.T) {
	t.Parallel()

	format := switcher.ColorYUV422_10bit
	w, h := 320, 240

	chromaH := format.ChromaHeight(h)
	require.Equal(t, h, chromaH,
		"YUV422 chroma height should equal luma height (%d), not %d", h, chromaH)

	chromaW := format.ChromaWidth(w)
	require.Equal(t, w/2, chromaW,
		"YUV422 chroma width should be w/2 = %d", w/2)

	// Compare with YUV420 for contrast.
	format8 := switcher.ColorYUV420_8bit
	chromaH8 := format8.ChromaHeight(h)
	require.Equal(t, h/2, chromaH8,
		"YUV420 chroma height should be h/2 = %d", h/2)

	require.Greater(t, chromaH, chromaH8,
		"YUV422 chroma height (%d) should be greater than YUV420 (%d)", chromaH, chromaH8)
}
