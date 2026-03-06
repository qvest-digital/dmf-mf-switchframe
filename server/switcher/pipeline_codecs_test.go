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
