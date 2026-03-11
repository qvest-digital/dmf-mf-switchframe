//go:build cgo && !noffmpeg

package codec

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestProbeEncoders_ReturnsResult(t *testing.T) {
	enc, dec := ProbeEncoders()
	assert.NotEmpty(t, enc, "encoder name should not be empty")
	assert.NotEmpty(t, dec, "decoder name should not be empty")
	// Decoder should always be available when FFmpeg is linked.
	assert.NotEqual(t, "none", dec, "decoder should not be 'none' when FFmpeg is available")
	// Encoder may be "none" if FFmpeg was built without libx264 (e.g. CI).
	if enc == "none" {
		t.Log("no H.264 encoder available — skipping encoder assertions")
	}
}

func TestProbeEncoders_Idempotent(t *testing.T) {
	enc1, dec1 := ProbeEncoders()
	enc2, dec2 := ProbeEncoders()
	assert.Equal(t, enc1, enc2, "encoder should be the same across calls")
	assert.Equal(t, dec1, dec2, "decoder should be the same across calls")
}

func TestProbeEncoders_SelectsKnownEncoder(t *testing.T) {
	enc, _ := ProbeEncoders()
	// Encoder may be "none" if FFmpeg was built without libx264 (e.g. CI).
	validEncoders := []string{"libx264", "h264_videotoolbox", "h264_nvenc", "h264_vaapi", "openh264", "none"}
	assert.Contains(t, validEncoders, enc,
		"encoder should be one of the known candidates, got %q", enc)
}

func TestHWDeviceCtx_MatchesEncoder(t *testing.T) {
	enc, _ := ProbeEncoders()
	ctx := HWDeviceCtx()

	hwEncoders := map[string]bool{
		"h264_videotoolbox": true,
		"h264_nvenc":        true,
		"h264_vaapi":        true,
	}

	if hwEncoders[enc] {
		require.NotNil(t, ctx, "HWDeviceCtx should be non-nil when hardware encoder %q is selected", enc)
	} else {
		require.Nil(t, ctx, "HWDeviceCtx should be nil for software encoder %q", enc)
	}
}

func TestNewVideoEncoder_Works(t *testing.T) {
	probedEnc, _ := ProbeEncoders()
	if probedEnc == "none" {
		t.Skip("no H.264 encoder available")
	}

	enc, err := NewVideoEncoder(160, 120, 200000, 30, 1, false)
	require.NoError(t, err)
	require.NotNil(t, enc)
	defer enc.Close()

	// Encode frames to verify it works end-to-end. Hardware encoders
	// (e.g. VideoToolbox) may buffer the first few frames (EAGAIN).
	w, h := 160, 120
	ySize := w * h
	uvSize := (w / 2) * (h / 2)
	yuv := make([]byte, ySize+2*uvSize)
	for i := range yuv {
		yuv[i] = 128
	}

	var gotOutput bool
	for i := range 30 {
		data, isKey, err := enc.Encode(yuv, int64(i*3000), i == 0)
		require.NoError(t, err)
		if len(data) > 0 {
			if !gotOutput {
				require.True(t, isKey, "first output frame should be a keyframe")
			}
			gotOutput = true
			break
		}
	}
	require.True(t, gotOutput, "encoder should produce output within 30 frames")
}

func TestNewVideoDecoder_Works(t *testing.T) {
	dec, err := NewVideoDecoder()
	require.NoError(t, err)
	require.NotNil(t, dec)
	dec.Close()
}

func TestNewVideoEncoder_FullRoundTrip(t *testing.T) {
	probedEnc, _ := ProbeEncoders()
	if probedEnc == "none" {
		t.Skip("no H.264 encoder available")
	}

	w, h := 160, 120

	enc, err := NewVideoEncoder(w, h, 500000, 30, 1, false)
	require.NoError(t, err)
	defer enc.Close()

	dec, err := NewVideoDecoder()
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

	// Encode frames until we get output. Without zerolatency and with
	// multi-threaded encoding, the pipeline may buffer ~15 frames.
	var encoded []byte
	for i := range 30 {
		data, _, err := enc.Encode(yuv, int64(i*3000), i == 0)
		require.NoError(t, err)
		if len(data) > 0 {
			encoded = data
			break
		}
	}
	require.NotEmpty(t, encoded, "encoder should produce output within 30 frames")

	// Decode it back. With multi-threaded decode, the decoder may also
	// need a few packets before producing output.
	var decoded []byte
	var dw, dh int
	decoded, dw, dh, err = dec.Decode(encoded)
	if err != nil {
		// Decoder is buffering — feed more frames to flush it.
		for i := 0; i < 30; i++ {
			var moreEncoded []byte
			moreEncoded, _, err = enc.Encode(yuv, int64((30+i)*3000), false)
			require.NoError(t, err)
			if moreEncoded == nil {
				continue
			}
			decoded, dw, dh, err = dec.Decode(moreEncoded)
			if err == nil {
				break
			}
		}
	}
	require.NoError(t, err, "decoder should produce output")
	require.Equal(t, w, dw)
	require.Equal(t, h, dh)
	require.Equal(t, ySize+2*uvSize, len(decoded))
}

func TestNewVideoEncoder_InvalidParams(t *testing.T) {
	_, err := NewVideoEncoder(0, 120, 200000, 30, 1, false)
	require.Error(t, err)

	_, err = NewVideoEncoder(160, 0, 200000, 30, 1, false)
	require.Error(t, err)

	_, err = NewVideoEncoder(160, 120, 0, 30, 1, false)
	require.Error(t, err)

	_, err = NewVideoEncoder(160, 120, 200000, 0, 1, false)
	require.Error(t, err)
}

func TestHWDeviceCtx_Type(t *testing.T) {
	// Verify that HWDeviceCtx returns an unsafe.Pointer (type check at compile time).
	ptr := HWDeviceCtx()
	_ = ptr // compile-time type assertion
}

func TestHWDecode_ChromaPreserved(t *testing.T) {
	// Verifies that the NV12 de-interleave path (used by hardware decoders)
	// correctly separates Cb and Cr planes. A bug in de-interleave would
	// swap or corrupt chroma, producing wrong colors.
	probedEnc, _ := ProbeEncoders()
	if probedEnc == "none" {
		t.Skip("no H.264 encoder available")
	}
	if HWDeviceCtx() == nil {
		t.Skip("no hardware decode available — NV12 path not exercised")
	}

	w, h := 160, 120
	ySize := w * h
	uvSize := (w / 2) * (h / 2)

	// Build a frame with distinct Cb and Cr values.
	// Y=128 (mid gray), Cb=80 (blue-ish), Cr=200 (red-ish).
	// After encode→decode, lossy compression will shift values, but
	// Cb and Cr should remain clearly distinguishable — not swapped.
	yuv := make([]byte, ySize+2*uvSize)
	for i := 0; i < ySize; i++ {
		yuv[i] = 128
	}
	for i := 0; i < uvSize; i++ {
		yuv[ySize+i] = 80         // Cb
		yuv[ySize+uvSize+i] = 200 // Cr
	}

	enc, err := NewVideoEncoder(w, h, 500000, 30, 1, false)
	require.NoError(t, err)
	defer enc.Close()

	dec, err := NewVideoDecoder()
	require.NoError(t, err)
	defer dec.Close()

	// Encode enough frames for the pipeline to produce output.
	var encoded []byte
	for i := range 30 {
		data, _, encErr := enc.Encode(yuv, int64(i*3000), i == 0)
		require.NoError(t, encErr)
		if len(data) > 0 {
			encoded = data
			break
		}
	}
	require.NotEmpty(t, encoded, "encoder should produce output")

	// Decode — may need multiple frames for multi-threaded decoder.
	var decoded []byte
	decoded, _, _, err = dec.Decode(encoded)
	if err != nil {
		for i := 0; i < 30; i++ {
			more, _, encErr := enc.Encode(yuv, int64((30+i)*3000), false)
			require.NoError(t, encErr)
			if more == nil {
				continue
			}
			decoded, _, _, err = dec.Decode(more)
			if err == nil {
				break
			}
		}
	}
	require.NoError(t, err, "decoder should produce output")
	require.Equal(t, ySize+2*uvSize, len(decoded))

	// Sample the center of the Cb and Cr planes.
	// H.264 is lossy, so values won't be exact, but Cb should be
	// clearly below 128 and Cr clearly above 128.
	cbCenter := decoded[ySize+uvSize/2]
	crCenter := decoded[ySize+uvSize+uvSize/2]

	t.Logf("input Cb=80 Cr=200 → decoded Cb=%d Cr=%d", cbCenter, crCenter)

	// Cb (input 80) should decode to something below 128 (blue channel)
	require.Less(t, cbCenter, byte(128),
		"Cb should be below 128 (input was 80); if above, chroma planes may be swapped")

	// Cr (input 200) should decode to something above 128 (red channel)
	require.Greater(t, crCenter, byte(128),
		"Cr should be above 128 (input was 200); if below, chroma planes may be swapped")

	// Verify they're not equal (would indicate both planes got the same data)
	require.NotEqual(t, cbCenter, crCenter,
		"Cb and Cr should be distinct; equal values suggest de-interleave error")
}

func TestCreateHWDeviceCtx_InvalidType(t *testing.T) {
	// An invalid hardware type should return nil, not panic.
	ctx := CreateHWDeviceCtx("nonexistent_hw_type")
	require.Nil(t, ctx, "invalid hw type should return nil")
}

func TestCreateHWDeviceCtx_EmptyType(t *testing.T) {
	ctx := CreateHWDeviceCtx("")
	require.Nil(t, ctx, "empty hw type should return nil")
}

func TestNewVideoEncoder_CBRMode(t *testing.T) {
	probedEnc, _ := ProbeEncoders()
	if probedEnc == "none" {
		t.Skip("no H.264 encoder available")
	}

	enc, err := NewVideoEncoder(160, 120, 200000, 30, 1, true)
	require.NoError(t, err)
	require.NotNil(t, enc)
	defer enc.Close()

	// Encode a few frames to verify CBR mode works end-to-end.
	w, h := 160, 120
	yuv := make([]byte, w*h*3/2)
	for i := range yuv {
		yuv[i] = 128
	}

	var gotOutput bool
	for i := range 30 {
		data, _, err := enc.Encode(yuv, int64(i*3000), i == 0)
		require.NoError(t, err)
		if len(data) > 0 {
			gotOutput = true
			break
		}
	}
	require.True(t, gotOutput, "CBR encoder should produce output within 30 frames")
}
