package transition

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestMockDecoderDecode(t *testing.T) {
	dec := &mockDecoder{
		width:  1920,
		height: 1080,
		yuvOut: make([]byte, 1920*1080*3/2),
	}
	yuv, w, h, err := dec.Decode([]byte{0x00, 0x00, 0x00, 0x01})
	require.NoError(t, err)
	require.Equal(t, 1920, w)
	require.Equal(t, 1080, h)
	require.Equal(t, 1920*1080*3/2, len(yuv))
}

func TestMockDecoderAllocatesWhenNilYUV(t *testing.T) {
	dec := &mockDecoder{width: 1280, height: 720}
	yuv, w, h, err := dec.Decode([]byte{0x00, 0x00, 0x00, 0x01})
	require.NoError(t, err)
	require.Equal(t, 1280, w)
	require.Equal(t, 720, h)
	require.Equal(t, 1280*720*3/2, len(yuv))
}

func TestMockDecoderClose(t *testing.T) {
	dec := &mockDecoder{width: 1920, height: 1080}
	dec.Close() // should not panic
}

func TestMockEncoderEncode(t *testing.T) {
	enc := &mockEncoder{
		avcOut:     []byte{0x00, 0x00, 0x00, 0x01, 0x65},
		isKeyframe: true,
	}
	yuv := make([]byte, 1920*1080*3/2)
	data, isIDR, err := enc.Encode(yuv, true)
	require.NoError(t, err)
	require.True(t, isIDR)
	require.NotEmpty(t, data)
}

func TestMockEncoderDefaultOutput(t *testing.T) {
	enc := &mockEncoder{}
	yuv := make([]byte, 1920*1080*3/2)
	data, isIDR, err := enc.Encode(yuv, false)
	require.NoError(t, err)
	require.False(t, isIDR)
	require.NotEmpty(t, data)
}

func TestMockEncoderForceIDR(t *testing.T) {
	enc := &mockEncoder{}
	yuv := make([]byte, 1920*1080*3/2)
	data, isIDR, err := enc.Encode(yuv, true)
	require.NoError(t, err)
	require.True(t, isIDR)
	require.NotEmpty(t, data)
}

func TestMockEncoderClose(t *testing.T) {
	enc := &mockEncoder{}
	enc.Close() // should not panic
}

func TestDecoderFactoryFunc(t *testing.T) {
	factory := DecoderFactory(func() (VideoDecoder, error) {
		return &mockDecoder{width: 1280, height: 720}, nil
	})
	dec, err := factory()
	require.NoError(t, err)
	require.NotNil(t, dec)
	dec.Close()
}

func TestEncoderFactoryFunc(t *testing.T) {
	factory := EncoderFactory(func(w, h, bitrate int, fps float32) (VideoEncoder, error) {
		return &mockEncoder{}, nil
	})
	enc, err := factory(1920, 1080, 4000000, 30.0)
	require.NoError(t, err)
	require.NotNil(t, enc)
	enc.Close()
}

// TestVideoDecoderInterface verifies the interface is satisfied.
func TestVideoDecoderInterface(t *testing.T) {
	var dec VideoDecoder = &mockDecoder{width: 640, height: 480}
	require.NotNil(t, dec)
	dec.Close()
}

// TestVideoEncoderInterface verifies the interface is satisfied.
func TestVideoEncoderInterface(t *testing.T) {
	var enc VideoEncoder = &mockEncoder{}
	require.NotNil(t, enc)
	enc.Close()
}
