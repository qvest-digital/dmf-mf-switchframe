//go:build cgo && !noffmpeg

package codec

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/zsiec/switchframe/server/transition"
)

// --- HEVC Encoder Probing Tests ---

func TestProbeHEVCEncoders_ReturnsResult(t *testing.T) {
	enc, dec := ProbeHEVCEncoders()
	assert.NotEmpty(t, enc, "HEVC encoder name should not be empty")
	assert.NotEmpty(t, dec, "HEVC decoder name should not be empty")
	// Decoder should always be "hevc" when FFmpeg is linked.
	assert.Equal(t, "hevc", dec, "HEVC decoder should be 'hevc' when FFmpeg is available")
}

func TestProbeHEVCEncoders_Idempotent(t *testing.T) {
	enc1, dec1 := ProbeHEVCEncoders()
	enc2, dec2 := ProbeHEVCEncoders()
	assert.Equal(t, enc1, enc2, "HEVC encoder should be the same across calls")
	assert.Equal(t, dec1, dec2, "HEVC decoder should be the same across calls")
}

func TestProbeHEVCEncoders_SelectsKnownEncoder(t *testing.T) {
	enc, _ := ProbeHEVCEncoders()
	validEncoders := []string{"libx265", "hevc_videotoolbox", "hevc_nvenc", "hevc_vaapi", "none"}
	assert.Contains(t, validEncoders, enc,
		"HEVC encoder should be one of the known candidates, got %q", enc)
}

func TestListAvailableHEVCEncoders(t *testing.T) {
	encoders := ListAvailableHEVCEncoders()

	// HEVC encoders may or may not be available depending on the build environment.
	// If none are available, that's OK — just verify the shape.
	if len(encoders) == 0 {
		enc, _ := ProbeHEVCEncoders()
		assert.Equal(t, "none", enc, "if no HEVC encoders listed, probe should return 'none'")
		return
	}

	defaultCount := 0
	for _, e := range encoders {
		if e.IsDefault {
			defaultCount++
		}
		assert.NotEmpty(t, e.Name, "encoder name must not be empty")
		assert.NotEmpty(t, e.DisplayName, "encoder display name must not be empty")
	}
	assert.Equal(t, 1, defaultCount, "expected exactly 1 default HEVC encoder, got %d", defaultCount)

	defaultEnc, _ := ProbeHEVCEncoders()
	found := false
	for _, e := range encoders {
		if e.IsDefault && e.Name == defaultEnc {
			found = true
		}
	}
	assert.True(t, found, "default HEVC encoder %q should be in available list", defaultEnc)
}

func TestHEVCCandidateOrder_NoMacOSDemotion(t *testing.T) {
	// Verify hevc_videotoolbox is NOT demoted on macOS (unlike h264_videotoolbox).
	vtPos := -1
	libx265Pos := -1
	for i, c := range hevcCandidates {
		switch c.name {
		case "hevc_videotoolbox":
			vtPos = i
		case "libx265":
			libx265Pos = i
		}
	}

	require.NotEqual(t, -1, vtPos, "hevc_videotoolbox should be in hevcCandidates")
	require.NotEqual(t, -1, libx265Pos, "libx265 should be in hevcCandidates")

	// On all platforms, hevc_videotoolbox should come before libx265
	// (no macOS demotion like we have for AVC).
	require.Less(t, vtPos, libx265Pos,
		"hevc_videotoolbox should be probed before libx265 on all platforms")
}

// --- HEVC Encoder Tests ---

func TestFFmpegHEVCEncoderCreate(t *testing.T) {
	enc, _ := ProbeHEVCEncoders()
	if enc == "none" {
		t.Skip("no HEVC encoder available")
	}

	e, err := NewFFmpegEncoder(enc, 640, 480, 1000000, 30, 1, 2, nil)
	require.NoError(t, err)
	require.NotNil(t, e)
	e.Close()
}

func TestFFmpegHEVCEncoderEncodeFrame(t *testing.T) {
	enc, _ := ProbeHEVCEncoders()
	if enc == "none" {
		t.Skip("no HEVC encoder available")
	}

	w, h := 320, 240
	e, err := NewFFmpegEncoder(enc, w, h, 500000, 30, 1, 2, nil)
	require.NoError(t, err)
	defer e.Close()

	yuv := make([]byte, yuvFrameSize(w, h))
	for i := 0; i < w*h; i++ {
		yuv[i] = byte((i * 7) % 256)
	}
	for i := w * h; i < len(yuv); i++ {
		yuv[i] = 128
	}

	// Encode frames until we get output. Software HEVC encoders (libx265)
	// may buffer several frames before producing output.
	var encoded []byte
	var isKeyframe bool
	for i := 0; i < 60; i++ {
		encoded, isKeyframe, err = e.Encode(yuv, int64(i*3000), i == 0)
		require.NoError(t, err, "frame %d", i)
		if len(encoded) > 0 {
			break
		}
	}
	require.NotEmpty(t, encoded, "HEVC encoder should produce output within 60 frames")
	require.True(t, isKeyframe, "first output should be a keyframe")

	// Verify Annex B start code prefix.
	require.True(t, len(encoded) >= 4)
	require.Equal(t, byte(0x00), encoded[0])
	require.Equal(t, byte(0x00), encoded[1])
	require.Equal(t, byte(0x00), encoded[2])
	require.Equal(t, byte(0x01), encoded[3])
}

func TestFFmpegHEVCEncoderMultipleFrames(t *testing.T) {
	enc, _ := ProbeHEVCEncoders()
	if enc == "none" {
		t.Skip("no HEVC encoder available")
	}

	w, h := 256, 256
	e, err := NewFFmpegEncoder(enc, w, h, 500000, 30, 1, 2, nil)
	require.NoError(t, err)
	defer e.Close()

	yuv := make([]byte, yuvFrameSize(w, h))
	outputCount := 0
	for i := 0; i < 60; i++ {
		for j := 0; j < w*h; j++ {
			yuv[j] = byte((j*7 + i*13) % 256)
		}
		for j := w * h; j < len(yuv); j++ {
			yuv[j] = 128
		}

		data, _, err := e.Encode(yuv, int64(i*3000), i == 0)
		require.NoError(t, err, "frame %d", i)
		if len(data) > 0 {
			outputCount++
		}
	}
	require.Greater(t, outputCount, 0, "HEVC encoder should produce at least one frame of output")
}

func TestNewHEVCVideoEncoder(t *testing.T) {
	probedEnc, _ := ProbeHEVCEncoders()
	if probedEnc == "none" {
		t.Skip("no HEVC encoder available")
	}

	enc, err := NewHEVCVideoEncoder(256, 256, 500000, 30, 1)
	require.NoError(t, err)
	require.NotNil(t, enc)
	defer enc.Close()

	// Verify it can encode frames.
	w, h := 256, 256
	yuv := make([]byte, yuvFrameSize(w, h))
	for i := range yuv {
		yuv[i] = 128
	}

	var gotOutput bool
	for i := 0; i < 60; i++ {
		data, _, err := enc.Encode(yuv, int64(i*3000), i == 0)
		require.NoError(t, err)
		if len(data) > 0 {
			gotOutput = true
			break
		}
	}
	require.True(t, gotOutput, "HEVC encoder should produce output within 60 frames")
}

// --- HEVC Decoder Tests ---

func TestFFmpegHEVCDecoderCreate(t *testing.T) {
	dec, err := NewFFmpegHEVCDecoder(nil)
	require.NoError(t, err)
	require.NotNil(t, dec)
	dec.Close()
}

func TestFFmpegHEVCDecoderDoubleClose(t *testing.T) {
	dec, err := NewFFmpegHEVCDecoder(nil)
	require.NoError(t, err)
	dec.Close()
	dec.Close() // should not panic
}

func TestFFmpegHEVCDecoderEmptyInput(t *testing.T) {
	dec, err := NewFFmpegHEVCDecoder(nil)
	require.NoError(t, err)
	defer dec.Close()

	_, _, _, err = dec.Decode(nil)
	require.Error(t, err)
	require.Contains(t, err.Error(), "empty")

	_, _, _, err = dec.Decode([]byte{})
	require.Error(t, err)
	require.Contains(t, err.Error(), "empty")
}

func TestFFmpegHEVCDecoderCorruptedInput(t *testing.T) {
	dec, err := NewFFmpegHEVCDecoder(nil)
	require.NoError(t, err)
	defer dec.Close()

	garbage := make([]byte, 256)
	for i := range garbage {
		garbage[i] = byte(i * 37 % 256)
	}
	_, _, _, err = dec.Decode(garbage)
	require.Error(t, err)
}

func TestFFmpegHEVCDecoderClosedDecode(t *testing.T) {
	dec, err := NewFFmpegHEVCDecoder(nil)
	require.NoError(t, err)
	dec.Close()

	_, _, _, err = dec.Decode([]byte{0x00, 0x00, 0x00, 0x01, 0x26, 0x01})
	require.Error(t, err)
	require.Contains(t, err.Error(), "closed")
}

func TestFFmpegHEVCDecoderInterface(t *testing.T) {
	var dec transition.VideoDecoder
	d, err := NewFFmpegHEVCDecoder(nil)
	require.NoError(t, err)
	dec = d
	require.NotNil(t, dec)
	dec.Close()
}

func TestFFmpegHEVCDecoderWithThreads(t *testing.T) {
	dec, err := NewFFmpegHEVCDecoderWithThreads(nil, 1)
	require.NoError(t, err)
	require.NotNil(t, dec)
	dec.Close()
}

// --- HEVC Encode/Decode Round-Trip ---

func TestFFmpegHEVCEncodeDecodeRoundTrip(t *testing.T) {
	encName, _ := ProbeHEVCEncoders()
	if encName == "none" {
		t.Skip("no HEVC encoder available")
	}

	w, h := 256, 256

	enc, err := NewFFmpegEncoder(encName, w, h, 500000, 30, 1, 2, nil)
	require.NoError(t, err)
	defer enc.Close()

	dec, err := NewFFmpegHEVCDecoder(nil)
	require.NoError(t, err)
	defer dec.Close()

	ySize := w * h
	uvSize := (w / 2) * (h / 2)
	yuv := make([]byte, ySize+2*uvSize)
	for i := 0; i < ySize; i++ {
		yuv[i] = byte((i * 7) % 256)
	}
	for i := ySize; i < len(yuv); i++ {
		yuv[i] = 128
	}

	// Collect encoded frames. HEVC encoders may buffer significantly.
	var encodedFrames [][]byte
	for i := 0; i < 90; i++ {
		forceIDR := i == 0
		data, _, err := enc.Encode(yuv, int64(i*3000), forceIDR)
		require.NoError(t, err)
		if len(data) > 0 {
			encodedFrames = append(encodedFrames, append([]byte(nil), data...))
		}
	}
	require.NotEmpty(t, encodedFrames, "encoder should produce output within 90 frames")

	// Decode frames until we get output.
	var decoded []byte
	var dw, dh int
	for _, ef := range encodedFrames {
		decoded, dw, dh, err = dec.Decode(ef)
		if err == nil && len(decoded) > 0 {
			break
		}
	}
	require.NoError(t, err, "decoder should produce output")
	require.Equal(t, w, dw)
	require.Equal(t, h, dh)
	require.Equal(t, ySize+2*uvSize, len(decoded))
}

// --- Video Factory Tests ---

func TestNewVideoDecoderForCodec_H264(t *testing.T) {
	dec, err := NewVideoDecoderForCodec("h264")
	require.NoError(t, err)
	require.NotNil(t, dec)
	dec.Close()
}

func TestNewVideoDecoderForCodec_H265(t *testing.T) {
	dec, err := NewVideoDecoderForCodec("h265")
	require.NoError(t, err)
	require.NotNil(t, dec)
	dec.Close()
}

func TestNewVideoDecoderForCodec_HEVC(t *testing.T) {
	dec, err := NewVideoDecoderForCodec("hevc")
	require.NoError(t, err)
	require.NotNil(t, dec)
	dec.Close()
}

func TestNewVideoDecoderForCodec_Unknown(t *testing.T) {
	_, err := NewVideoDecoderForCodec("vp9")
	require.Error(t, err)
	require.Contains(t, err.Error(), "unsupported")
}

func TestNewVideoDecoderForCodecSingleThread_H264(t *testing.T) {
	dec, err := NewVideoDecoderForCodecSingleThread("h264")
	require.NoError(t, err)
	require.NotNil(t, dec)
	dec.Close()
}

func TestNewVideoDecoderForCodecSingleThread_HEVC(t *testing.T) {
	dec, err := NewVideoDecoderForCodecSingleThread("hevc")
	require.NoError(t, err)
	require.NotNil(t, dec)
	dec.Close()
}

func TestNewHEVCVideoDecoder_Works(t *testing.T) {
	dec, err := NewHEVCVideoDecoder()
	require.NoError(t, err)
	require.NotNil(t, dec)
	dec.Close()
}

func TestNewHEVCVideoDecoderSingleThread_Works(t *testing.T) {
	dec, err := NewHEVCVideoDecoderSingleThread()
	require.NoError(t, err)
	require.NotNil(t, dec)
	dec.Close()
}
