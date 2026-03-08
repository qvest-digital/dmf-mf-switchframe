package switcher

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestProcessingFrameYUVSize(t *testing.T) {
	pf := &ProcessingFrame{
		Width:  8,
		Height: 8,
		YUV:    make([]byte, 8*8*3/2),
	}
	require.Equal(t, 8*8*3/2, len(pf.YUV))
	require.Equal(t, 8, pf.Width)
	require.Equal(t, 8, pf.Height)
}

func TestProcessingFrameDeepCopy(t *testing.T) {
	original := &ProcessingFrame{
		Width:      4,
		Height:     4,
		YUV:        []byte{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16, 17, 18, 19, 20, 21, 22, 23, 24},
		PTS:        1000,
		DTS:        900,
		IsKeyframe: true,
		GroupID:    5,
		Codec:      "h264",
	}

	copied := original.DeepCopy()

	require.Equal(t, original.Width, copied.Width)
	require.Equal(t, original.Height, copied.Height)
	require.Equal(t, original.PTS, copied.PTS)
	require.Equal(t, original.DTS, copied.DTS)
	require.Equal(t, original.IsKeyframe, copied.IsKeyframe)
	require.Equal(t, original.GroupID, copied.GroupID)
	require.Equal(t, original.Codec, copied.Codec)
	require.Equal(t, original.YUV, copied.YUV)

	// Verify it's a true deep copy
	copied.YUV[0] = 255
	require.NotEqual(t, original.YUV[0], copied.YUV[0])
}
