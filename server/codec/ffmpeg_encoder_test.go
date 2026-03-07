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

	// Without zerolatency tune, the encoder may buffer initial frames
	// (frame-level threading fills the pipeline before producing output).
	// Feed frames until we get a keyframe output.
	var encoded []byte
	var isKeyframe bool
	for i := 0; i < 30; i++ {
		forceIDR := i == 0
		encoded, isKeyframe, err = enc.Encode(yuv, forceIDR)
		require.NoError(t, err)
		if encoded != nil {
			break
		}
	}
	require.NotEmpty(t, encoded, "encoder should produce output within 30 frames")
	require.True(t, isKeyframe, "first output should be a keyframe")

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

	outputCount := 0
	firstOutputIsKey := false
	for i := 0; i < 30; i++ {
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
		// Without zerolatency, initial frames may return nil (EAGAIN).
		if data != nil {
			outputCount++
			if outputCount == 1 {
				firstOutputIsKey = isKey
			}
		}
	}
	require.Greater(t, outputCount, 0, "should produce at least one output frame")
	require.True(t, firstOutputIsKey, "first output frame should be keyframe")
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

	// Encode frames to fill the pipeline and produce output.
	for i := 0; i < 30; i++ {
		forceIDR := i == 0
		_, _, err := enc.Encode(yuv, forceIDR)
		require.NoError(t, err, "frame %d", i)
	}

	// Force IDR. With multi-threaded encoding, output lags input by ~15 frames,
	// so we need to feed enough additional frames for the IDR to appear.
	foundIDR := false
	for i := 0; i < 30; i++ {
		forceOnFirst := i == 0
		data, isKeyframe, err := enc.Encode(yuv, forceOnFirst)
		require.NoError(t, err)
		if data != nil && isKeyframe {
			foundIDR = true
			break
		}
	}
	require.True(t, foundIDR, "forced IDR should produce a keyframe within pipeline delay")
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

func TestFFmpegEncoderVBVConstrainedOutput(t *testing.T) {
	w, h := 320, 240
	bitrate := 500000 // 500kbps
	enc, err := NewFFmpegEncoder("libx264", w, h, bitrate, 30.0, nil)
	require.NoError(t, err)
	defer enc.Close()

	yuv := make([]byte, w*h*3/2)
	var maxSize int

	for i := 0; i < 60; i++ {
		// Create varying content to stress rate control
		for j := 0; j < w*h; j++ {
			yuv[j] = byte((j*7 + i*37) % 256)
		}
		for j := w * h; j < len(yuv); j++ {
			yuv[j] = 128
		}

		forceIDR := i%30 == 0
		data, _, err := enc.Encode(yuv, forceIDR)
		require.NoError(t, err)
		if len(data) > maxSize {
			maxSize = len(data)
		}
	}

	// With VBV, max frame size should be bounded.
	// At 500kbps / 30fps, average frame ~2KB. VBV buffer = 250KB (500ms).
	// Max single frame should not exceed VBV buffer size.
	vbvBufferBytes := bitrate / 8 / 2 // 500ms buffer in bytes
	require.Less(t, maxSize, vbvBufferBytes,
		"max frame size %d should be less than VBV buffer %d bytes", maxSize, vbvBufferBytes)
}

func TestFFmpegEncoderProducesOutput_WithNewSettings(t *testing.T) {
	// Encode 30 frames at 360p to exercise the encoder under realistic load.
	w, h := 640, 360
	enc, err := NewFFmpegEncoder("libx264", w, h, 2_000_000, 30.0, nil)
	require.NoError(t, err)
	defer enc.Close()

	yuv := make([]byte, w*h*3/2)
	keyframeCount := 0
	for i := 0; i < 30; i++ {
		for j := 0; j < w*h; j++ {
			yuv[j] = byte((j + i*w) % 256)
		}
		for j := w * h; j < len(yuv); j++ {
			yuv[j] = 128
		}

		forceIDR := i == 0
		data, isKey, err := enc.Encode(yuv, forceIDR)
		require.NoError(t, err, "frame %d", i)
		// With threading/lookahead, initial frames may return nil (EAGAIN).
		// After pipeline fills, frames should produce output.
		if data != nil && isKey {
			keyframeCount++
		}
	}
	require.GreaterOrEqual(t, keyframeCount, 1, "should have at least 1 keyframe")
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
