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

func TestProcessingFrameDeepCopyWithPool(t *testing.T) {
	pool := NewFramePool(4, 4, 4)
	defer pool.Close()

	original := &ProcessingFrame{
		Width:      4,
		Height:     4,
		YUV:        pool.Acquire(),
		PTS:        1000,
		DTS:        900,
		IsKeyframe: true,
		GroupID:    5,
		Codec:      "h264",
		pool:       pool,
	}
	copy(original.YUV, []byte{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16, 17, 18, 19, 20, 21, 22, 23, 24})

	copied := original.DeepCopy()

	// Copy inherits pool reference
	require.NotNil(t, copied.pool)

	// Data is independent
	require.Equal(t, original.YUV, copied.YUV)
	copied.YUV[0] = 255
	require.NotEqual(t, original.YUV[0], copied.YUV[0])

	// Release both — should go back to pool
	original.ReleaseYUV()
	copied.ReleaseYUV()

	hits, misses := pool.Stats()
	require.Equal(t, uint64(2), hits)   // 2 acquires (original + copy)
	require.Equal(t, uint64(0), misses) // pool had 4 slots
}

func TestProcessingFrameReleaseYUVWithPool(t *testing.T) {
	pool := NewFramePool(4, 8, 8)
	defer pool.Close()

	pf := &ProcessingFrame{
		Width:  8,
		Height: 8,
		YUV:    pool.Acquire(),
		pool:   pool,
	}

	pf.ReleaseYUV()
	require.Nil(t, pf.YUV)

	// Double release is safe
	pf.ReleaseYUV()
}

func TestProcessingFrame_TimestampsDeepCopy(t *testing.T) {
	original := &ProcessingFrame{
		Width:           4,
		Height:          4,
		YUV:             make([]byte, 4*4*3/2),
		PTS:             1000,
		ArrivalNano:     100000,
		DecodeStartNano: 200000,
		DecodeEndNano:   300000,
		SyncReleaseNano: 400000,
	}

	copied := original.DeepCopy()

	require.Equal(t, original.ArrivalNano, copied.ArrivalNano, "ArrivalNano should be preserved")
	require.Equal(t, original.DecodeStartNano, copied.DecodeStartNano, "DecodeStartNano should be preserved")
	require.Equal(t, original.DecodeEndNano, copied.DecodeEndNano, "DecodeEndNano should be preserved")
	require.Equal(t, original.SyncReleaseNano, copied.SyncReleaseNano, "SyncReleaseNano should be preserved")

	// Verify modifying the copy doesn't affect the original's YUV
	copied.YUV[0] = 255
	require.NotEqual(t, original.YUV[0], copied.YUV[0])
}

func TestProcessingFrameNilPoolFallback(t *testing.T) {
	// No pool — DeepCopy falls back to make()
	original := &ProcessingFrame{
		Width:  4,
		Height: 4,
		YUV:    make([]byte, 4*4*3/2),
	}

	copied := original.DeepCopy()
	require.Len(t, copied.YUV, 4*4*3/2)

	// ReleaseYUV with nil pool doesn't panic
	copied.ReleaseYUV()
	require.Nil(t, copied.YUV)
}
