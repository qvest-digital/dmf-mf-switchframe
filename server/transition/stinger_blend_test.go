package transition

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestBlendStinger_FullAlpha(t *testing.T) {
	// 4x4 frame
	fb := NewFrameBlender(4, 4)
	ySize := 16
	uvSize := 4
	totalSize := ySize + 2*uvSize

	// base = all 100
	base := make([]byte, totalSize)
	for i := range base {
		base[i] = 100
	}

	// stinger = all 200
	stinger := make([]byte, totalSize)
	for i := range stinger {
		stinger[i] = 200
	}

	// full alpha = should show all stinger
	alpha := make([]byte, ySize)
	for i := range alpha {
		alpha[i] = 255
	}

	result := fb.BlendStinger(base, stinger, alpha)
	require.Len(t, result, totalSize)

	// Y should be all 200
	for i := 0; i < ySize; i++ {
		require.Equal(t, byte(200), result[i], "Y pixel %d", i)
	}
}

func TestBlendStinger_ZeroAlpha(t *testing.T) {
	fb := NewFrameBlender(4, 4)
	ySize := 16
	uvSize := 4
	totalSize := ySize + 2*uvSize

	base := make([]byte, totalSize)
	for i := range base {
		base[i] = 100
	}

	stinger := make([]byte, totalSize)
	for i := range stinger {
		stinger[i] = 200
	}

	// zero alpha = should show all base
	alpha := make([]byte, ySize)

	result := fb.BlendStinger(base, stinger, alpha)
	for i := 0; i < ySize; i++ {
		require.Equal(t, byte(100), result[i], "Y pixel %d", i)
	}
}

func TestBlendStinger_HalfAlpha(t *testing.T) {
	fb := NewFrameBlender(4, 4)
	ySize := 16
	uvSize := 4
	totalSize := ySize + 2*uvSize

	base := make([]byte, totalSize)
	for i := range base {
		base[i] = 0
	}

	stinger := make([]byte, totalSize)
	for i := range stinger {
		stinger[i] = 200
	}

	alpha := make([]byte, ySize)
	for i := range alpha {
		alpha[i] = 128
	}

	result := fb.BlendStinger(base, stinger, alpha)
	// Y should be approximately 100 (128/255 * 200 ≈ 100)
	for i := 0; i < ySize; i++ {
		require.InDelta(t, 100, int(result[i]), 2, "Y pixel %d", i)
	}
}
