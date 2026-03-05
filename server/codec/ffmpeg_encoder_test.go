//go:build cgo && !noffmpeg

package codec

import (
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/zsiec/switchframe/server/transition"
)

func TestFFmpegEncoderCreate(t *testing.T) {
	enc, err := NewFFmpegEncoder("libx264", 640, 480, 1000000, 30.0, nil)
	require.NoError(t, err)
	require.NotNil(t, enc)
	enc.Close()
}

func TestFFmpegEncoderDoubleClose(t *testing.T) {
	enc, err := NewFFmpegEncoder("libx264", 640, 480, 1000000, 30.0, nil)
	require.NoError(t, err)
	enc.Close()
	enc.Close() // should not panic
}

func TestFFmpegEncoderInvalidParams(t *testing.T) {
	// 0 width
	_, err := NewFFmpegEncoder("libx264", 0, 480, 1000000, 30.0, nil)
	require.Error(t, err)

	// 0 height
	_, err = NewFFmpegEncoder("libx264", 640, 0, 1000000, 30.0, nil)
	require.Error(t, err)

	// 0 bitrate
	_, err = NewFFmpegEncoder("libx264", 640, 480, 0, 30.0, nil)
	require.Error(t, err)

	// 0 fps
	_, err = NewFFmpegEncoder("libx264", 640, 480, 1000000, 0, nil)
	require.Error(t, err)

	// Negative dimensions
	_, err = NewFFmpegEncoder("libx264", -1, 480, 1000000, 30.0, nil)
	require.Error(t, err)
}

func TestFFmpegEncoderInvalidCodec(t *testing.T) {
	_, err := NewFFmpegEncoder("nonexistent_codec", 640, 480, 1000000, 30.0, nil)
	require.Error(t, err)
	require.Contains(t, err.Error(), "nonexistent_codec")
}

func TestFFmpegEncoderEncodeFrame(t *testing.T) {
	w, h := 320, 240
	enc, err := NewFFmpegEncoder("libx264", w, h, 500000, 30.0, nil)
	require.NoError(t, err)
	defer enc.Close()

	ySize := w * h
	uvSize := (w / 2) * (h / 2)
	yuv := make([]byte, ySize+2*uvSize)
	// Fill Y plane with a pattern.
	for i := 0; i < ySize; i++ {
		yuv[i] = byte((i * 7) % 256)
	}
	// Fill U and V with neutral gray.
	for i := ySize; i < ySize+2*uvSize; i++ {
		yuv[i] = 128
	}

	// First frame with forceIDR=true should produce a keyframe.
	encoded, isKeyframe, err := enc.Encode(yuv, true)
	require.NoError(t, err)
	require.True(t, isKeyframe)
	require.NotEmpty(t, encoded)

	// Verify Annex B start code prefix.
	require.True(t, len(encoded) >= 4)
	require.Equal(t, byte(0x00), encoded[0])
	require.Equal(t, byte(0x00), encoded[1])
	require.Equal(t, byte(0x00), encoded[2])
	require.Equal(t, byte(0x01), encoded[3])
}

func TestFFmpegEncoderMultipleFrames(t *testing.T) {
	w, h := 160, 120
	enc, err := NewFFmpegEncoder("libx264", w, h, 200000, 30.0, nil)
	require.NoError(t, err)
	defer enc.Close()

	ySize := w * h
	uvSize := (w / 2) * (h / 2)
	yuv := make([]byte, ySize+2*uvSize)

	for i := 0; i < 10; i++ {
		// Vary the Y pattern each frame.
		for j := 0; j < ySize; j++ {
			yuv[j] = byte((j*7 + i*13) % 256)
		}
		for j := ySize; j < len(yuv); j++ {
			yuv[j] = 128
		}

		forceIDR := i == 0
		data, isKey, err := enc.Encode(yuv, forceIDR)
		require.NoError(t, err, "frame %d", i)
		require.NotEmpty(t, data, "frame %d", i)
		if i == 0 {
			require.True(t, isKey, "first frame should be keyframe")
		}
	}
}

func TestFFmpegEncoderForceIDR(t *testing.T) {
	w, h := 160, 120
	enc, err := NewFFmpegEncoder("libx264", w, h, 500000, 30.0, nil)
	require.NoError(t, err)
	defer enc.Close()

	ySize := w * h
	uvSize := (w / 2) * (h / 2)
	yuv := make([]byte, ySize+2*uvSize)
	for i := range yuv {
		yuv[i] = 128
	}

	// Encode 5 frames without forcing IDR (except first).
	for i := 0; i < 5; i++ {
		forceIDR := i == 0
		_, _, err := enc.Encode(yuv, forceIDR)
		require.NoError(t, err, "frame %d", i)
	}

	// Force IDR on 6th frame.
	data, isKeyframe, err := enc.Encode(yuv, true)
	require.NoError(t, err)
	require.True(t, isKeyframe, "forced IDR frame should be a keyframe")
	require.NotEmpty(t, data)
}

func TestFFmpegEncoderWrongYUVSize(t *testing.T) {
	enc, err := NewFFmpegEncoder("libx264", 320, 240, 500000, 30.0, nil)
	require.NoError(t, err)
	defer enc.Close()

	// Wrong size YUV buffer.
	_, _, err = enc.Encode([]byte{1, 2, 3}, false)
	require.Error(t, err)
	require.Contains(t, err.Error(), "YUV buffer")
}

func TestFFmpegEncoderClosedEncode(t *testing.T) {
	enc, err := NewFFmpegEncoder("libx264", 320, 240, 500000, 30.0, nil)
	require.NoError(t, err)
	enc.Close()

	yuv := make([]byte, 320*240*3/2)
	_, _, err = enc.Encode(yuv, false)
	require.Error(t, err)
	require.Contains(t, err.Error(), "closed")
}

func TestFFmpegEncoderInterface(t *testing.T) {
	// Verify FFmpegEncoder implements transition.VideoEncoder.
	var enc transition.VideoEncoder
	e, err := NewFFmpegEncoder("libx264", 320, 240, 500000, 30.0, nil)
	require.NoError(t, err)
	enc = e
	require.NotNil(t, enc)
	enc.Close()
}
