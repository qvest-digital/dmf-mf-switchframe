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

// forward performs an in-place forward FFT on interleaved complex data.
// data has length 2*n (n complex values as [re0, im0, re1, im1, ...]).
func (f *fftState) forward(data []float32) {
	n := f.n

	// Bit-reversal permutation
	for i := 0; i < n; i++ {
		j := f.bitrev[i]
		if i < j {
			// Swap complex values
			data[2*i], data[2*j] = data[2*j], data[2*i]
			data[2*i+1], data[2*j+1] = data[2*j+1], data[2*i+1]
		}
	}

	// Iterative Cooley-Tukey: log2N stages
	for s := 1; s <= f.log2N; s++ {
		m := 1 << s       // number of elements in each group
		halfM := m / 2     // number of butterflies per group
		twStride := n / m  // twiddle stride for this stage

		for groupStart := 0; groupStart < n; groupStart += m {
			butterflyRadix2(data[groupStart*2:], f.twiddle, halfM, 1, twStride)
		}
	}
}

// inverse performs an in-place inverse FFT on interleaved complex data.
// data has length 2*n. Result is scaled by 1/n.
func (f *fftState) inverse(data []float32) {
	n := f.n

	// Conjugate twiddle factors (negate imaginary parts in data)
	for i := 0; i < n; i++ {
		data[2*i+1] = -data[2*i+1]
	}

	// Forward FFT
	f.forward(data)

	// Conjugate again and scale by 1/N
	scale := float32(1.0) / float32(n)
	for i := 0; i < n; i++ {
		data[2*i] *= scale
		data[2*i+1] *= -scale
	}
}
