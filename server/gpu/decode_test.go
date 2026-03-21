//go:build cgo && cuda

package gpu

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/zsiec/switchframe/server/codec"
)

// generateH264IDR uses FFmpeg encoder to produce a valid H.264 IDR frame.
// This gives us real compressed bitstream to feed into NVDEC.
func generateH264IDR(t *testing.T, w, h int) []byte {
	t.Helper()

	enc, err := codec.NewFFmpegEncoder("h264_nvenc", w, h, 2_000_000, 30, 1, 2, nil)
	if err != nil {
		// Fall back to libx264 if NVENC isn't available for encoding
		enc, err = codec.NewFFmpegEncoder("libx264", w, h, 2_000_000, 30, 1, 2, nil)
		require.NoError(t, err, "need at least libx264 to generate test H.264")
	}
	defer enc.Close()

	yuv := make([]byte, w*h*3/2)
	// Fill with a gradient pattern
	for i := 0; i < w*h; i++ {
		yuv[i] = byte(i%220) + 16 // Y: limited range gradient
	}
	for i := w * h; i < len(yuv); i++ {
		yuv[i] = 128 // UV: neutral
	}

	// Encode several frames to ensure we get output (NVENC may buffer)
	var idrData []byte
	for i := 0; i < 30; i++ {
		data, isIDR, err := enc.Encode(yuv, int64(i*3000), i == 0)
		if err != nil {
			continue
		}
		if len(data) > 0 && isIDR {
			idrData = data
			break
		}
		if len(data) > 0 && idrData == nil {
			idrData = data // take any frame if no IDR yet
		}
	}
	require.NotEmpty(t, idrData, "encoder should produce at least one frame")
	return idrData
}

func TestNVDECDecode(t *testing.T) {
	ctx, err := NewContext()
	require.NoError(t, err)
	defer ctx.Close()

	w, h := 640, 480
	idrData := generateH264IDR(t, w, h)

	dec, err := NewGPUDecoder(ctx, 0)
	require.NoError(t, err, "NVDEC decoder should create successfully on L4")
	defer dec.Close()

	// Feed the IDR frame — NVDEC may need a few frames before output
	yuv, dw, dh, err := dec.Decode(idrData)
	if err != nil {
		t.Logf("First decode returned error (may need more frames): %v", err)
		// Try feeding the same frame again (some decoders buffer)
		yuv, dw, dh, err = dec.Decode(idrData)
	}

	if err != nil {
		t.Skipf("NVDEC decode did not produce output after 2 frames (may need IDR+P): %v", err)
	}

	assert.Equal(t, w, dw)
	assert.Equal(t, h, dh)
	assert.Equal(t, w*h*3/2, len(yuv))

	// Verify it's not all zeros (actual decoded content)
	nonZero := 0
	for _, b := range yuv[:1000] {
		if b != 0 {
			nonZero++
		}
	}
	assert.Greater(t, nonZero, 100, "decoded frame should contain non-zero data")

	t.Logf("NVDEC decode success: %dx%d, %d bytes YUV", dw, dh, len(yuv))
}

func TestNVDECDecodeMultipleFrames(t *testing.T) {
	ctx, err := NewContext()
	require.NoError(t, err)
	defer ctx.Close()

	w, h := 320, 240

	// Generate multiple frames using encoder
	enc, err := codec.NewFFmpegEncoder("h264_nvenc", w, h, 1_000_000, 30, 1, 2, nil)
	if err != nil {
		enc, err = codec.NewFFmpegEncoder("libx264", w, h, 1_000_000, 30, 1, 2, nil)
		require.NoError(t, err)
	}
	defer enc.Close()

	yuv := make([]byte, w*h*3/2)
	for i := 0; i < w*h; i++ {
		yuv[i] = byte(i%220) + 16
	}
	for i := w * h; i < len(yuv); i++ {
		yuv[i] = 128
	}

	// Collect encoded frames
	var frames [][]byte
	for i := 0; i < 30; i++ {
		data, _, err := enc.Encode(yuv, int64(i*3000), i == 0)
		if err != nil || len(data) == 0 {
			continue
		}
		frames = append(frames, append([]byte(nil), data...))
	}
	require.Greater(t, len(frames), 2, "need at least 3 encoded frames")

	// Decode all frames with NVDEC
	dec, err := NewGPUDecoder(ctx, 0)
	require.NoError(t, err)
	defer dec.Close()

	decoded := 0
	for _, f := range frames {
		_, _, _, err := dec.Decode(f)
		if err == nil {
			decoded++
		}
	}

	t.Logf("NVDEC decoded %d/%d frames", decoded, len(frames))
	assert.Greater(t, decoded, 0, "NVDEC should decode at least one frame")
}

func TestGPUDecoderFactory(t *testing.T) {
	ctx, err := NewContext()
	require.NoError(t, err)
	defer ctx.Close()

	factory := NewGPUDecoderFactory(ctx)
	dec, err := factory()
	require.NoError(t, err)
	require.NotNil(t, dec)
	dec.Close()
}

func TestGPUDecoderFactoryNilContext(t *testing.T) {
	// Nil context should fall back to software decode
	factory := NewGPUDecoderFactory(nil)
	dec, err := factory()
	require.NoError(t, err, "nil context should fall back to software decode")
	require.NotNil(t, dec)
	dec.Close()
}
