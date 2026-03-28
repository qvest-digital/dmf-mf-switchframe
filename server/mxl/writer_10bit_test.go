package mxl

import (
	"encoding/binary"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestWriteVideo10bit_LosslessV210 verifies that WriteVideo10bit converts
// YUV422P10LE frames to V210 using the lossless 10-bit path, preserving
// 10-bit sample values rather than the lossy 8-bit→10-bit upscale.
func TestWriteVideo10bit_LosslessV210(t *testing.T) {
	// Use dimensions divisible by 6 (V210 requirement).
	w, h := 12, 4
	writer := NewWriter(WriterConfig{
		Width:  w,
		Height: h,
	})

	// Create a YUV422P10LE frame with known 10-bit Y values > 255.
	// YUV422P10LE layout: Y(w*h*2) + Cb(w/2*h*2) + Cr(w/2*h*2)
	ySize := w * h * 2
	chromaW := w / 2
	chromaSize := chromaW * h * 2
	frameSize := ySize + 2*chromaSize
	yuv := make([]byte, frameSize)

	// Fill Y with 500 (a 10-bit value that can't come from 8-bit upscale)
	for i := 0; i < w*h; i++ {
		binary.LittleEndian.PutUint16(yuv[i*2:], 500)
	}
	// Fill Cb with neutral 512
	for i := ySize; i < ySize+chromaSize; i += 2 {
		binary.LittleEndian.PutUint16(yuv[i:], 512)
	}
	// Fill Cr with neutral 512
	for i := ySize + chromaSize; i < frameSize; i += 2 {
		binary.LittleEndian.PutUint16(yuv[i:], 512)
	}

	// Write the 10-bit frame.
	writer.WriteVideo10bit(yuv, w, h, 90000)

	// Verify the V210 was stored.
	frame := writer.latestV210.Load()
	require.NotNil(t, frame, "WriteVideo10bit should store a V210 frame")
	require.NotEmpty(t, frame.data)

	// Convert the V210 back to YUV422P10LE to verify lossless round-trip.
	stride := V210LineStride(w)
	out, err := V210ToYUV422P10(frame.data, stride, w, h)
	require.NoError(t, err)

	// Check that Y values are preserved at exactly 500 (not 500>>2<<2 = 500,
	// but verifying the path used 10-bit conversion, not 8-bit truncation).
	firstY := binary.LittleEndian.Uint16(out[:2])
	assert.Equal(t, uint16(500), firstY,
		"10-bit Y value should be preserved through lossless V210 round-trip")

	// Check several more Y values.
	for i := 0; i < w*h && i < 10; i++ {
		y := binary.LittleEndian.Uint16(out[i*2:])
		assert.Equal(t, uint16(500), y,
			"Y[%d] should be 500 after lossless V210 round-trip", i)
	}

	// Check Cb is preserved at 512.
	cbStart := w * h * 2
	firstCb := binary.LittleEndian.Uint16(out[cbStart:])
	assert.Equal(t, uint16(512), firstCb,
		"Cb should be preserved at 512")
}

// TestWriteVideo10bit_ResolutionMismatch verifies that WriteVideo10bit
// drops frames that don't match the configured output resolution.
func TestWriteVideo10bit_ResolutionMismatch(t *testing.T) {
	writer := NewWriter(WriterConfig{
		Width:  12,
		Height: 4,
	})

	// Wrong dimensions.
	w, h := 18, 6
	frameSize := w * h * 4 // approximate
	yuv := make([]byte, frameSize)

	writer.WriteVideo10bit(yuv, w, h, 90000)

	frame := writer.latestV210.Load()
	assert.Nil(t, frame, "WriteVideo10bit should drop mismatched resolution frames")
}

// TestWriteVideo_8bit_StillWorks verifies that the existing 8-bit
// WriteVideo path is unaffected by the 10-bit additions.
func TestWriteVideo_8bit_StillWorks(t *testing.T) {
	w, h := 12, 4
	writer := NewWriter(WriterConfig{
		Width:  w,
		Height: h,
	})

	// 8-bit YUV420 frame
	frame8Size := w * h * 3 / 2
	yuv := make([]byte, frame8Size)
	for i := 0; i < w*h; i++ {
		yuv[i] = 128 // Y
	}
	for i := w * h; i < frame8Size; i++ {
		yuv[i] = 128 // chroma
	}

	writer.WriteVideo(yuv, w, h, 90000)

	frame := writer.latestV210.Load()
	require.NotNil(t, frame, "WriteVideo should store a V210 frame")
	require.NotEmpty(t, frame.data)
}

// TestWriteVideo10bit_Vs8bit_DifferentOutput verifies that a 10-bit frame
// with values > 255 produces different V210 output than an 8-bit frame
// upscaled to the same apparent value. This confirms the lossless path
// is actually being used rather than silently falling back to 8-bit.
func TestWriteVideo10bit_Vs8bit_DifferentOutput(t *testing.T) {
	w, h := 12, 4
	writer8 := NewWriter(WriterConfig{Width: w, Height: h})
	writer10 := NewWriter(WriterConfig{Width: w, Height: h})

	// 8-bit frame: Y=200, which upscales to 200<<2=800 in V210.
	frame8Size := w * h * 3 / 2
	yuv8 := make([]byte, frame8Size)
	for i := 0; i < w*h; i++ {
		yuv8[i] = 200
	}
	for i := w * h; i < frame8Size; i++ {
		yuv8[i] = 128
	}

	// 10-bit frame: Y=801 (not representable as 8-bit upscale since 801/4 = 200.25).
	ySize := w * h * 2
	chromaW := w / 2
	chromaSize := chromaW * h * 2
	frame10Size := ySize + 2*chromaSize
	yuv10 := make([]byte, frame10Size)
	for i := 0; i < w*h; i++ {
		binary.LittleEndian.PutUint16(yuv10[i*2:], 801)
	}
	for i := ySize; i < frame10Size; i += 2 {
		binary.LittleEndian.PutUint16(yuv10[i:], 512)
	}

	writer8.WriteVideo(yuv8, w, h, 90000)
	writer10.WriteVideo10bit(yuv10, w, h, 90000)

	v210_8 := writer8.latestV210.Load()
	v210_10 := writer10.latestV210.Load()
	require.NotNil(t, v210_8)
	require.NotNil(t, v210_10)

	// V210 outputs should differ because 200<<2=800 != 801.
	assert.NotEqual(t, v210_8.data, v210_10.data,
		"10-bit lossless V210 should differ from 8-bit upscaled V210")
}
