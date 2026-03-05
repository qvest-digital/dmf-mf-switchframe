//go:build cgo && !noffmpeg

package codec

import (
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/zsiec/switchframe/server/transition"
)

func TestFFmpegDecoderCreate(t *testing.T) {
	dec, err := NewFFmpegDecoder(nil)
	require.NoError(t, err)
	require.NotNil(t, dec)
	dec.Close()
}

func TestFFmpegDecoderDoubleClose(t *testing.T) {
	dec, err := NewFFmpegDecoder(nil)
	require.NoError(t, err)
	dec.Close()
	dec.Close() // should not panic
}

func TestFFmpegDecoderEmptyInput(t *testing.T) {
	dec, err := NewFFmpegDecoder(nil)
	require.NoError(t, err)
	defer dec.Close()

	// nil input
	_, _, _, err = dec.Decode(nil)
	require.Error(t, err)
	require.Contains(t, err.Error(), "empty")

	// empty slice
	_, _, _, err = dec.Decode([]byte{})
	require.Error(t, err)
	require.Contains(t, err.Error(), "empty")
}

func TestFFmpegDecoderCorruptedInput(t *testing.T) {
	dec, err := NewFFmpegDecoder(nil)
	require.NoError(t, err)
	defer dec.Close()

	// Random bytes should produce an error, not a crash.
	garbage := make([]byte, 256)
	for i := range garbage {
		garbage[i] = byte(i * 37 % 256)
	}
	_, _, _, err = dec.Decode(garbage)
	require.Error(t, err)
}

func TestFFmpegDecoderClosedDecode(t *testing.T) {
	dec, err := NewFFmpegDecoder(nil)
	require.NoError(t, err)
	dec.Close()

	_, _, _, err = dec.Decode([]byte{0x00, 0x00, 0x00, 0x01, 0x65})
	require.Error(t, err)
	require.Contains(t, err.Error(), "closed")
}

func TestFFmpegDecoderInterface(t *testing.T) {
	// Verify FFmpegDecoder implements transition.VideoDecoder.
	var dec transition.VideoDecoder
	d, err := NewFFmpegDecoder(nil)
	require.NoError(t, err)
	dec = d
	require.NotNil(t, dec)
	dec.Close()
}

func TestFFmpegEncodeDecodeRoundTrip(t *testing.T) {
	w, h := 320, 240

	enc, err := NewFFmpegEncoder("libx264", w, h, 500000, 30.0, nil)
	require.NoError(t, err)
	defer enc.Close()

	dec, err := NewFFmpegDecoder(nil)
	require.NoError(t, err)
	defer dec.Close()

	// Build a YUV420 frame with a recognizable pattern.
	ySize := w * h
	uvSize := (w / 2) * (h / 2)
	yuv := make([]byte, ySize+2*uvSize)
	for i := 0; i < ySize; i++ {
		yuv[i] = byte((i * 7) % 256)
	}
	for i := ySize; i < len(yuv); i++ {
		yuv[i] = 128
	}

	// Encode a keyframe.
	encoded, isKey, err := enc.Encode(yuv, true)
	require.NoError(t, err)
	require.True(t, isKey)
	require.NotEmpty(t, encoded)

	// Decode it back.
	decoded, dw, dh, err := dec.Decode(encoded)
	require.NoError(t, err)
	require.Equal(t, w, dw)
	require.Equal(t, h, dh)
	require.Equal(t, ySize+2*uvSize, len(decoded))
}

func TestFFmpegMultiFrameDecodeSequence(t *testing.T) {
	w, h := 160, 120

	enc, err := NewFFmpegEncoder("libx264", w, h, 200000, 30.0, nil)
	require.NoError(t, err)
	defer enc.Close()

	dec, err := NewFFmpegDecoder(nil)
	require.NoError(t, err)
	defer dec.Close()

	ySize := w * h
	uvSize := (w / 2) * (h / 2)
	yuv := make([]byte, ySize+2*uvSize)

	successCount := 0
	for i := 0; i < 10; i++ {
		// Vary Y plane each frame.
		for j := 0; j < ySize; j++ {
			yuv[j] = byte((j*7 + i*13) % 256)
		}
		for j := ySize; j < len(yuv); j++ {
			yuv[j] = 128
		}

		forceIDR := i == 0
		encoded, _, err := enc.Encode(yuv, forceIDR)
		require.NoError(t, err, "encode frame %d", i)
		require.NotEmpty(t, encoded, "encode frame %d", i)

		decoded, dw, dh, err := dec.Decode(encoded)
		if err == nil {
			require.Equal(t, w, dw, "frame %d width", i)
			require.Equal(t, h, dh, "frame %d height", i)
			require.Equal(t, ySize+2*uvSize, len(decoded), "frame %d YUV size", i)
			successCount++
		}
	}

	// At least some frames should decode successfully.
	require.Greater(t, successCount, 0, "at least one frame should decode successfully")
}
