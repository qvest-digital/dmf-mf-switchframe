package preview

import (
	"encoding/binary"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDownconvert422_10to420_BasicConversion(t *testing.T) {
	w, h := 4, 4

	// Create a 10-bit YUV422P10LE frame.
	srcSize := w * h * 4 // YUV422P10LE
	src := make([]byte, srcSize)

	// Y plane: all samples = 400 (10-bit).
	// Expected 8-bit: (400 + 2) >> 2 = 100.
	for i := 0; i < w*h; i++ {
		binary.LittleEndian.PutUint16(src[i*2:], 400)
	}
	// Cb plane: all samples = 512 (neutral).
	// Expected 8-bit: (512 + 2) >> 2 = 128 (exact).
	cbOff := w * h * 2
	chromaW := w / 2
	for i := 0; i < chromaW*h; i++ {
		binary.LittleEndian.PutUint16(src[cbOff+i*2:], 512)
	}
	// Cr plane: all samples = 512.
	crOff := cbOff + chromaW*h*2
	for i := 0; i < chromaW*h; i++ {
		binary.LittleEndian.PutUint16(src[crOff+i*2:], 512)
	}

	dstSize := yuvFrameSize(w, h)
	dst := make([]byte, dstSize)

	err := downconvert422_10to420(src, dst, w, h)
	require.NoError(t, err)

	// Verify Y values.
	for i := 0; i < w*h; i++ {
		assert.Equal(t, byte(100), dst[i], "Y[%d] should be 100 (from 10-bit 400)", i)
	}

	// Verify Cb values (420: h/2 rows).
	cbDstOff := w * h
	chromaH := h / 2
	for i := 0; i < chromaW*chromaH; i++ {
		assert.Equal(t, byte(128), dst[cbDstOff+i], "Cb[%d] should be 128 (from 10-bit 512)", i)
	}

	// Verify Cr values.
	crDstOff := cbDstOff + chromaW*chromaH
	for i := 0; i < chromaW*chromaH; i++ {
		assert.Equal(t, byte(128), dst[crDstOff+i], "Cr[%d] should be 128 (from 10-bit 512)", i)
	}
}

func TestDownconvert422_10to420_InvalidDimensions(t *testing.T) {
	err := downconvert422_10to420(nil, nil, 3, 4) // odd width
	assert.Error(t, err)

	err = downconvert422_10to420(nil, nil, 4, 3) // odd height
	assert.Error(t, err)

	err = downconvert422_10to420(nil, nil, 0, 0) // zero
	assert.Error(t, err)
}

func TestDownconvert422_10to420_BufferTooSmall(t *testing.T) {
	err := downconvert422_10to420(make([]byte, 10), make([]byte, 100), 4, 4)
	assert.Error(t, err)
}
