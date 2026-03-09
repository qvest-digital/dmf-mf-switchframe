package replay

import (
	"math"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewFFT_PowerOfTwo(t *testing.T) {
	fft := newFFT(4096)
	require.NotNil(t, fft)
	assert.Equal(t, 4096, fft.n)
	assert.Equal(t, 12, fft.log2N)
	assert.Len(t, fft.twiddle, 4096*2)
	assert.Len(t, fft.bitrev, 4096)
}

func TestNewFFT_PanicsNonPowerOfTwo(t *testing.T) {
	assert.Panics(t, func() { newFFT(100) })
	assert.Panics(t, func() { newFFT(0) })
	assert.Panics(t, func() { newFFT(1) })
}

func TestTwiddleFactors_Size8(t *testing.T) {
	fft := newFFT(8)
	require.Len(t, fft.twiddle, 16)

	// W(0,8) = 1 + 0i
	assert.InDelta(t, 1.0, fft.twiddle[0], 1e-7)
	assert.InDelta(t, 0.0, fft.twiddle[1], 1e-7)

	// W(1,8) = cos(-π/4) + i·sin(-π/4) = √2/2 - √2/2·i
	assert.InDelta(t, math.Sqrt2/2, fft.twiddle[2], 1e-6)
	assert.InDelta(t, -math.Sqrt2/2, fft.twiddle[3], 1e-6)

	// W(2,8) = cos(-π/2) + i·sin(-π/2) = 0 - 1i
	assert.InDelta(t, 0.0, fft.twiddle[4], 1e-6)
	assert.InDelta(t, -1.0, fft.twiddle[5], 1e-6)

	// W(4,8) = cos(-π) + i·sin(-π) = -1 + 0i
	assert.InDelta(t, -1.0, fft.twiddle[8], 1e-6)
	assert.InDelta(t, 0.0, fft.twiddle[9], 1e-6)
}

func TestBitReversal_Size4(t *testing.T) {
	fft := newFFT(4)
	assert.Equal(t, []int{0, 2, 1, 3}, fft.bitrev)
}

func TestBitReversal_Size8(t *testing.T) {
	fft := newFFT(8)
	assert.Equal(t, []int{0, 4, 2, 6, 1, 5, 3, 7}, fft.bitrev)
}

func TestBitReversal_Size16(t *testing.T) {
	fft := newFFT(16)
	// Every index should map to a unique index (permutation)
	seen := make(map[int]bool)
	for _, r := range fft.bitrev {
		require.False(t, seen[r], "duplicate bit-reversal index %d", r)
		seen[r] = true
		require.True(t, r >= 0 && r < 16)
	}
	// Double reversal should give identity
	for i := 0; i < 16; i++ {
		assert.Equal(t, i, fft.bitrev[fft.bitrev[i]], "double reversal of %d", i)
	}
}
