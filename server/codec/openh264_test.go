//go:build cgo && openh264

package codec

import (
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/zsiec/switchframe/server/transition"
)

func TestOpenH264DecoderCreate(t *testing.T) {
	dec, err := NewOpenH264Decoder()
	require.NoError(t, err)
	require.NotNil(t, dec)
	dec.Close()
}

func TestOpenH264DecoderDoubleClose(t *testing.T) {
	dec, err := NewOpenH264Decoder()
	require.NoError(t, err)
	dec.Close()
	dec.Close() // should not panic
}

func TestOpenH264EncoderCreate(t *testing.T) {
	enc, err := NewOpenH264Encoder(640, 480, 1000000, 30.0)
	require.NoError(t, err)
	require.NotNil(t, enc)
	enc.Close()
}

func TestOpenH264EncoderDoubleClose(t *testing.T) {
	enc, err := NewOpenH264Encoder(640, 480, 1000000, 30.0)
	require.NoError(t, err)
	enc.Close()
	enc.Close() // should not panic
}

func TestOpenH264EncoderInvalidParams(t *testing.T) {
	_, err := NewOpenH264Encoder(0, 0, 1000000, 30.0)
	require.Error(t, err)

	_, err = NewOpenH264Encoder(640, 480, 0, 30.0)
	require.Error(t, err)

	_, err = NewOpenH264Encoder(640, 480, 1000000, 0)
	require.Error(t, err)
}

func TestOpenH264EncodeDecodeRoundTrip(t *testing.T) {
	w, h := 320, 240
	enc, err := NewOpenH264Encoder(w, h, 500000, 30.0)
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

	// First frame should be IDR.
	encoded, isIDR, err := enc.Encode(yuv, true)
	require.NoError(t, err)
	require.True(t, isIDR)
	require.NotEmpty(t, encoded)

	dec, err := NewOpenH264Decoder()
	require.NoError(t, err)
	defer dec.Close()

	decoded, decW, decH, err := dec.Decode(encoded)
	require.NoError(t, err)
	require.Equal(t, w, decW)
	require.Equal(t, h, decH)
	require.Equal(t, ySize+2*uvSize, len(decoded))
}

func TestOpenH264EncoderMultipleFrames(t *testing.T) {
	w, h := 160, 120
	enc, err := NewOpenH264Encoder(w, h, 200000, 30.0)
	require.NoError(t, err)
	defer enc.Close()

	yuv := make([]byte, w*h*3/2)
	for i := range yuv {
		yuv[i] = 128
	}

	for i := 0; i < 5; i++ {
		forceIDR := i == 0
		data, isKey, err := enc.Encode(yuv, forceIDR)
		require.NoError(t, err)
		require.NotEmpty(t, data)
		if i == 0 {
			require.True(t, isKey)
		}
	}
}

func TestOpenH264EncoderWrongYUVSize(t *testing.T) {
	enc, err := NewOpenH264Encoder(320, 240, 500000, 30.0)
	require.NoError(t, err)
	defer enc.Close()

	// Wrong size YUV buffer.
	_, _, err = enc.Encode([]byte{1, 2, 3}, false)
	require.Error(t, err)
}

func TestOpenH264DecoderEmptyInput(t *testing.T) {
	dec, err := NewOpenH264Decoder()
	require.NoError(t, err)
	defer dec.Close()

	_, _, _, err = dec.Decode(nil)
	require.Error(t, err)

	_, _, _, err = dec.Decode([]byte{})
	require.Error(t, err)
}

func TestOpenH264DecoderInterface(t *testing.T) {
	// Verify OpenH264Decoder implements transition.VideoDecoder.
	var dec transition.VideoDecoder
	d, err := NewOpenH264Decoder()
	require.NoError(t, err)
	dec = d
	require.NotNil(t, dec)
	dec.Close()
}

func TestOpenH264EncoderInterface(t *testing.T) {
	// Verify OpenH264Encoder implements transition.VideoEncoder.
	var enc transition.VideoEncoder
	e, err := NewOpenH264Encoder(320, 240, 500000, 30.0)
	require.NoError(t, err)
	enc = e
	require.NotNil(t, enc)
	enc.Close()
}

func TestOpenH264MultiFrameDecodeSequence(t *testing.T) {
	w, h := 320, 240
	// Use a higher bitrate to reduce frame skipping by rate control.
	enc, err := NewOpenH264Encoder(w, h, 2000000, 30.0)
	require.NoError(t, err)
	defer enc.Close()

	dec, err := NewOpenH264Decoder()
	require.NoError(t, err)
	defer dec.Close()

	ySize := w * h
	uvSize := (w / 2) * (h / 2)
	yuv := make([]byte, ySize+2*uvSize)

	encodedFrames := 0
	decodedFrames := 0
	for i := 0; i < 10; i++ {
		// Vary the Y pattern each frame to give the encoder real content.
		for j := 0; j < ySize; j++ {
			yuv[j] = byte((j*7 + i*13) % 256)
		}
		for j := ySize; j < len(yuv); j++ {
			yuv[j] = 128
		}

		forceIDR := i == 0
		encoded, _, err := enc.Encode(yuv, forceIDR)
		if err != nil {
			// Frame may be skipped by rate control; that's acceptable.
			continue
		}
		if len(encoded) == 0 {
			continue
		}
		encodedFrames++

		decoded, decW, decH, err := dec.Decode(encoded)
		if err == nil {
			decodedFrames++
			require.Equal(t, w, decW)
			require.Equal(t, h, decH)
			require.Equal(t, ySize+2*uvSize, len(decoded))
		}
	}

	// At least some frames should have been encoded and decoded.
	require.Greater(t, encodedFrames, 0, "expected at least one encoded frame")
	require.Greater(t, decodedFrames, 0, "expected at least one decoded frame")
}
