package switcher

import (
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/zsiec/switchframe/server/transition"
)

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

	// Encode at 8x8 -- encoder should be recreated
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

func TestPipelineCodecs_Close(t *testing.T) {
	pc := &pipelineCodecs{
		encoderFactory: func(w, h, bitrate int, fps float32) (transition.VideoEncoder, error) {
			return transition.NewMockEncoder(), nil
		},
	}

	// Init encoder via an encode call
	pf := &ProcessingFrame{
		YUV: make([]byte, 4*4*3/2), Width: 4, Height: 4,
		PTS: 1000, IsKeyframe: true, Codec: "h264",
	}
	_, err := pc.encode(pf, true)
	require.NoError(t, err)
	require.NotNil(t, pc.encoder)

	pc.close()
	require.Nil(t, pc.encoder)
}

func BenchmarkPipelineEncode(b *testing.B) {
	pc := &pipelineCodecs{
		encoderFactory: func(w, h, bitrate int, fps float32) (transition.VideoEncoder, error) {
			return transition.NewMockEncoder(), nil
		},
	}

	pf := &ProcessingFrame{
		YUV:        make([]byte, 320*240*3/2),
		Width:      320,
		Height:     240,
		PTS:        1000,
		IsKeyframe: true,
		Codec:      "h264",
		GroupID:    1,
	}

	// Prime the encoder
	_, err := pc.encode(pf, true)
	require.NoError(b, err)

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		pf.PTS = int64(i * 3000)
		pf.IsKeyframe = i%30 == 0
		_, _ = pc.encode(pf, i%30 == 0)
	}
}
