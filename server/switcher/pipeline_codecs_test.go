package switcher

import (
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/zsiec/prism/media"
	"github.com/zsiec/switchframe/server/transition"
)

func TestPipelineCodecs_DecodeToProcessingFrame(t *testing.T) {
	pc := &pipelineCodecs{
		decoderFactory: func() (transition.VideoDecoder, error) {
			return transition.NewMockDecoder(4, 4), nil
		},
	}

	frame := &media.VideoFrame{
		PTS:        1000,
		DTS:        900,
		IsKeyframe: true,
		WireData:   []byte{0x00, 0x00, 0x00, 0x04, 0x65, 0x88, 0x80, 0x40},
		Codec:      "h264",
		GroupID:    5,
		SPS:        []byte{0x67, 0x42, 0x00, 0x0a},
		PPS:        []byte{0x68, 0x42, 0x00},
	}

	pf, err := pc.decode(frame)
	require.NoError(t, err)
	require.NotNil(t, pf)
	require.Equal(t, 4, pf.Width)
	require.Equal(t, 4, pf.Height)
	require.Equal(t, int64(1000), pf.PTS)
	require.Equal(t, int64(900), pf.DTS)
	require.True(t, pf.IsKeyframe)
	require.Equal(t, "h264", pf.Codec)
	require.Equal(t, uint32(5), pf.GroupID)
	require.Equal(t, 4*4*3/2, len(pf.YUV))
}

func TestPipelineCodecs_DecodeNeedsKeyframe(t *testing.T) {
	pc := &pipelineCodecs{
		decoderFactory: func() (transition.VideoDecoder, error) {
			return transition.NewMockDecoder(4, 4), nil
		},
	}

	frame := &media.VideoFrame{
		PTS:        1000,
		IsKeyframe: false,
		WireData:   []byte{0x00, 0x00, 0x00, 0x04, 0x41, 0x9a, 0x80, 0x40},
	}

	pf, err := pc.decode(frame)
	require.Error(t, err, "should fail without keyframe to init decoder")
	require.Nil(t, pf)
}

func TestPipelineCodecs_EncodeProcessingFrame(t *testing.T) {
	pc := &pipelineCodecs{
		encoderFactory: func(w, h, bitrate int, fps float32) (transition.VideoEncoder, error) {
			return transition.NewMockEncoder(), nil
		},
	}
	// Must init encoder first (needs dimensions)
	pc.encWidth = 4
	pc.encHeight = 4
	enc, err := pc.encoderFactory(4, 4, 4_000_000, 30)
	require.NoError(t, err)
	pc.encoder = enc

	pf := &ProcessingFrame{
		YUV:        make([]byte, 4*4*3/2),
		Width:      4,
		Height:     4,
		PTS:        1000,
		IsKeyframe: true,
		Codec:      "h264",
		GroupID:    5,
	}

	frame, err := pc.encode(pf, true)
	require.NoError(t, err)
	require.NotNil(t, frame)
	require.Equal(t, int64(1000), frame.PTS)
	require.True(t, frame.IsKeyframe)
	require.NotEmpty(t, frame.WireData)
}

func TestPipelineCodecs_DecodeShortBuffer(t *testing.T) {
	// Mock decoder that returns a buffer shorter than w*h*3/2.
	shortDecoder := &shortBufferDecoder{width: 4, height: 4}
	pc := &pipelineCodecs{
		decoderFactory: func() (transition.VideoDecoder, error) {
			return shortDecoder, nil
		},
	}

	frame := &media.VideoFrame{
		PTS:        1000,
		IsKeyframe: true,
		WireData:   []byte{0x00, 0x00, 0x00, 0x04, 0x65, 0x88, 0x80, 0x40},
		SPS:        []byte{0x67, 0x42, 0x00, 0x0a},
		PPS:        []byte{0x68, 0x42, 0x00},
	}

	pf, err := pc.decode(frame)
	require.Error(t, err, "should return error for short buffer, not panic")
	require.Nil(t, pf)
	require.Contains(t, err.Error(), "decoder buffer too small")
}

func TestPipelineCodecs_ResolutionChange(t *testing.T) {
	encoderCreateCount := 0
	pc := &pipelineCodecs{
		encoderFactory: func(w, h, bitrate int, fps float32) (transition.VideoEncoder, error) {
			encoderCreateCount++
			return transition.NewMockEncoder(), nil
		},
	}

	// First encode at 4x4
	pf := &ProcessingFrame{
		YUV: make([]byte, 4*4*3/2), Width: 4, Height: 4,
		PTS: 1000, IsKeyframe: true, Codec: "h264",
	}
	_, err := pc.encode(pf, true)
	require.NoError(t, err)
	require.Equal(t, 1, encoderCreateCount)
	require.Equal(t, 4, pc.encWidth)
	require.Equal(t, 4, pc.encHeight)

	// Encode at 8x8 — encoder should be recreated
	pf2 := &ProcessingFrame{
		YUV: make([]byte, 8*8*3/2), Width: 8, Height: 8,
		PTS: 2000, IsKeyframe: true, Codec: "h264",
	}
	_, err = pc.encode(pf2, true)
	require.NoError(t, err)
	require.Equal(t, 2, encoderCreateCount, "encoder should be recreated on resolution change")
	require.Equal(t, 8, pc.encWidth)
	require.Equal(t, 8, pc.encHeight)
}

// shortBufferDecoder returns a YUV buffer that is too small for the stated dimensions.
type shortBufferDecoder struct {
	width, height int
}

func (d *shortBufferDecoder) Decode(data []byte) ([]byte, int, int, error) {
	// Return a buffer that is half the expected size
	expected := d.width * d.height * 3 / 2
	return make([]byte, expected/2), d.width, d.height, nil
}

func (d *shortBufferDecoder) Close() {}

func TestPipelineCodecs_GroupID(t *testing.T) {
	pc := &pipelineCodecs{
		encoderFactory: func(w, h, bitrate int, fps float32) (transition.VideoEncoder, error) {
			return transition.NewMockEncoder(), nil
		},
	}

	require.Equal(t, uint32(0), pc.GroupID(), "initial GroupID should be 0")

	// Encode a keyframe — GroupID should increment
	pf := &ProcessingFrame{
		YUV: make([]byte, 4*4*3/2), Width: 4, Height: 4,
		PTS: 1000, IsKeyframe: true, Codec: "h264", GroupID: 5,
	}
	_, err := pc.encode(pf, true)
	require.NoError(t, err)
	require.Equal(t, uint32(6), pc.GroupID(), "GroupID should be max(5,0)+1=6 after keyframe")
}

func TestPipelineCodecs_Close(t *testing.T) {
	pc := &pipelineCodecs{
		decoderFactory: func() (transition.VideoDecoder, error) {
			return transition.NewMockDecoder(4, 4), nil
		},
		encoderFactory: func(w, h, bitrate int, fps float32) (transition.VideoEncoder, error) {
			return transition.NewMockEncoder(), nil
		},
	}

	// Init decoder via a decode call
	frame := &media.VideoFrame{
		PTS: 1000, IsKeyframe: true,
		WireData: []byte{0x00, 0x00, 0x00, 0x04, 0x65, 0x88, 0x80, 0x40},
		SPS:      []byte{0x67, 0x42, 0x00, 0x0a},
		PPS:      []byte{0x68, 0x42, 0x00},
	}
	_, err := pc.decode(frame)
	require.NoError(t, err)
	require.NotNil(t, pc.decoder)

	pc.close()
	require.Nil(t, pc.decoder)
	require.Nil(t, pc.encoder)
}
