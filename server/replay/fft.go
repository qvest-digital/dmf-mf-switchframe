package replay

import "math"

// fftState holds precomputed data for radix-2 Cooley-Tukey FFT.
type fftState struct {
	n       int       // FFT size (must be power of 2)
	log2N   int       // log2(n)
	twiddle []float32 // interleaved [re0, im0, re1, im1, ...], length 2*n
	bitrev  []int     // bit-reversal permutation, length n
}

// newFFT creates a new FFT state for the given size (must be power of 2).
func newFFT(n int) *fftState {
	if n < 2 || n&(n-1) != 0 {
		panic("fft: size must be a power of 2")
	}

	log2N := 0
	for v := n; v > 1; v >>= 1 {
		log2N++
	}

	// Precompute twiddle factors: W(k,N) = exp(-2πi·k/N)
	twiddle := make([]float32, 2*n)
	for k := 0; k < n; k++ {
		angle := -2.0 * math.Pi * float64(k) / float64(n)
		twiddle[2*k] = float32(math.Cos(angle))
		twiddle[2*k+1] = float32(math.Sin(angle))
	}

	// Precompute bit-reversal permutation
	bitrev := make([]int, n)
	for i := 0; i < n; i++ {
		rev := 0
		v := i
		for b := 0; b < log2N; b++ {
			rev = (rev << 1) | (v & 1)
			v >>= 1
		}
		bitrev[i] = rev
	}

	return &fftState{
		n:       n,
		log2N:   log2N,
		twiddle: twiddle,
		bitrev:  bitrev,
	}
}
