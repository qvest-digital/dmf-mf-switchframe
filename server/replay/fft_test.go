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

func TestButterflyRadix2_SinglePair(t *testing.T) {
	// data = [(1+2i), (3+4i)] as interleaved [1,2,3,4]
	// twiddle W(0) = 1+0i
	data := []float32{1, 2, 3, 4}
	tw := []float32{1, 0}
	butterflyRadix2(data, tw, 1, 1, 0)
	// out[0] = (1+2i) + 1*(3+4i) = 4+6i
	// out[1] = (1+2i) - 1*(3+4i) = -2-2i
	assert.InDelta(t, 4.0, data[0], 1e-6)
	assert.InDelta(t, 6.0, data[1], 1e-6)
	assert.InDelta(t, -2.0, data[2], 1e-6)
	assert.InDelta(t, -2.0, data[3], 1e-6)
}

func TestFFT_Forward_DC(t *testing.T) {
	fft := newFFT(8)
	// All ones: DC component should be 8+0i, all others 0
	data := make([]float32, 16)
	for i := 0; i < 8; i++ {
		data[2*i] = 1.0
	}
	fft.forward(data)
	assert.InDelta(t, 8.0, data[0], 1e-4)
	assert.InDelta(t, 0.0, data[1], 1e-4)
	for i := 1; i < 8; i++ {
		assert.InDelta(t, 0.0, data[2*i], 1e-4, "bin %d re", i)
		assert.InDelta(t, 0.0, data[2*i+1], 1e-4, "bin %d im", i)
	}
}

func TestFFT_Forward_KnownSine(t *testing.T) {
	N := 16
	fft := newFFT(N)
	data := make([]float32, N*2)
	// Pure sine at bin 1: x[n] = sin(2*pi*1*n/N)
	for n := 0; n < N; n++ {
		data[n*2] = float32(math.Sin(2 * math.Pi * float64(n) / float64(N)))
	}
	fft.forward(data)
	// Bin 1 should have magnitude N/2 in imaginary (negative)
	assert.InDelta(t, 0.0, data[2], 1e-4, "bin1 real")
	assert.InDelta(t, -float64(N)/2, float64(data[3]), 1e-3, "bin1 imag")
}

func TestFFT_Roundtrip(t *testing.T) {
	N := 1024
	fft := newFFT(N)
	original := make([]float32, N*2)
	for i := range original {
		original[i] = float32(math.Sin(float64(i) * 0.1))
	}
	data := make([]float32, N*2)
	copy(data, original)

	fft.forward(data)
	fft.inverse(data)

	for i := range data {
		assert.InDelta(t, original[i], data[i], 1e-4, "index %d", i)
	}
}

func TestFFT_Roundtrip_Large(t *testing.T) {
	N := 4096
	fft := newFFT(N)
	original := make([]float32, N*2)
	for i := 0; i < N; i++ {
		original[2*i] = float32(math.Sin(2*math.Pi*440*float64(i)/48000) +
			0.5*math.Sin(2*math.Pi*880*float64(i)/48000))
	}
	data := make([]float32, N*2)
	copy(data, original)

	fft.forward(data)
	fft.inverse(data)

	for i := range data {
		assert.InDelta(t, original[i], data[i], 1e-3, "index %d", i)
	}
}
