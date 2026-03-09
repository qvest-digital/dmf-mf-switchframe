package switcher

import (
	"fmt"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/zsiec/prism/media"
	"github.com/zsiec/switchframe/server/transition"
)

func TestEncodeNode_AlwaysActive(t *testing.T) {
	n := &encodeNode{}
	require.True(t, n.Active(), "encode node is always active")
	require.Equal(t, "h264-encode", n.Name())
	require.True(t, n.Latency() > 0)
	require.NoError(t, n.Close())
}

func TestEncodeNode_ProcessEncodes(t *testing.T) {
	mockEnc := transition.NewMockEncoder()
	codecs := &pipelineCodecs{
		encoderFactory: func(w, h, bitrate, fpsNum, fpsDen int) (transition.VideoEncoder, error) {
			return mockEnc, nil
		},
	}

	var encoded *media.VideoFrame
	var forceIDR atomic.Bool

	n := &encodeNode{
		codecs:   codecs,
		forceIDR: &forceIDR,
		onEncoded: func(frame *media.VideoFrame) {
			encoded = frame
		},
	}

	pf := &ProcessingFrame{
		YUV:        make([]byte, 4*4*3/2),
		Width:      4,
		Height:     4,
		PTS:        1000,
		IsKeyframe: true,
		Codec:      "h264",
	}

	out := n.Process(nil, pf)
	require.Same(t, pf, out, "encodeNode always returns src")
	require.NotNil(t, encoded, "onEncoded should have been called")
	require.Equal(t, int64(1000), encoded.PTS)
	require.Nil(t, n.Err())
}

func TestEncodeNode_ForceIDR(t *testing.T) {
	mockEnc := transition.NewMockEncoder()
	codecs := &pipelineCodecs{
		encoderFactory: func(w, h, bitrate, fpsNum, fpsDen int) (transition.VideoEncoder, error) {
			return mockEnc, nil
		},
	}

	var forceIDR atomic.Bool
	forceIDR.Store(true)

	var encoded *media.VideoFrame
	n := &encodeNode{
		codecs:   codecs,
		forceIDR: &forceIDR,
		onEncoded: func(frame *media.VideoFrame) {
			encoded = frame
		},
	}

	pf := &ProcessingFrame{
		YUV:        make([]byte, 4*4*3/2),
		Width:      4,
		Height:     4,
		PTS:        2000,
		IsKeyframe: false,
		Codec:      "h264",
	}

	n.Process(nil, pf)
	require.NotNil(t, encoded)
	// After CompareAndSwap, forceIDR should be false
	require.False(t, forceIDR.Load(), "forceIDR should be consumed")
}

func TestEncodeNode_EncodeError(t *testing.T) {
	codecs := &pipelineCodecs{
		encoderFactory: func(w, h, bitrate, fpsNum, fpsDen int) (transition.VideoEncoder, error) {
			return &failingMockEncoder{}, nil
		},
	}

	var forceIDR atomic.Bool
	callCount := 0
	n := &encodeNode{
		codecs:   codecs,
		forceIDR: &forceIDR,
		onEncoded: func(frame *media.VideoFrame) {
			callCount++
		},
	}

	pf := &ProcessingFrame{
		YUV:    make([]byte, 4*4*3/2),
		Width:  4,
		Height: 4,
		PTS:    3000,
	}

	out := n.Process(nil, pf)
	require.Same(t, pf, out, "should return src even on error")
	require.Error(t, n.Err(), "Err() should report the encode error")
	require.Equal(t, 0, callCount, "onEncoded should not be called on error")
}

// failingMockEncoder always returns an error from Encode.
type failingMockEncoder struct{}

func (e *failingMockEncoder) Encode(yuv []byte, pts int64, forceIDR bool) ([]byte, bool, error) {
	return nil, false, fmt.Errorf("mock encode error")
}
func (e *failingMockEncoder) Close() {}
