package replay

import "math"

// fftState holds precomputed data for radix-2 Cooley-Tukey FFT.
type fftState struct {
	n          int       // FFT size (must be power of 2)
	log2N      int       // log2(n)
	twiddle    []float32 // interleaved [re0, im0, re1, im1, ...], length 2*n
	r2cTwiddle []float32 // precomputed R2C unscramble twiddles, length 2*(n+1)
	bitrev     []int     // bit-reversal permutation, length n
	packed     []float32 // reusable scratch buffer for r2c/c2r, length 2*n
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

	// Precompute R2C unscramble twiddle factors: W(k, 2*N) for k in [0, N]
	// These are at double resolution relative to the C2C twiddles.
	bigN := 2 * n
	r2cTwiddle := make([]float32, 2*(n+1))
	for k := 0; k <= n; k++ {
		angle := -2.0 * math.Pi * float64(k) / float64(bigN)
		r2cTwiddle[2*k] = float32(math.Cos(angle))
		r2cTwiddle[2*k+1] = float32(math.Sin(angle))
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
		n:          n,
		log2N:      log2N,
		twiddle:    twiddle,
		r2cTwiddle: r2cTwiddle,
		bitrev:     bitrev,
		packed:     make([]float32, 2*n),
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
		m := 1 << s      // number of elements in each group
		halfM := m / 2    // number of butterflies per group
		twStride := n / m // twiddle stride for this stage

		for groupStart := 0; groupStart < n; groupStart += m {
			butterflyRadix2(data[groupStart*2:], f.twiddle, halfM, twStride)
		}
	}
}

// r2c performs a real-to-complex FFT. input contains N real samples where N = 2*f.n.
// output has length 2*(f.n+1) containing f.n+1 complex bins as [re0, im0, re1, im1, ...].
// The DC and Nyquist bins have zero imaginary parts.
func (f *fftState) r2c(input []float32, output []float32) {
	halfN := f.n // C2C size

	// Pack real samples as complex: z[k] = input[2k] + i*input[2k+1]
	packed := f.packed
	copy(packed, input[:2*halfN])

	// C2C FFT of size halfN
	f.forward(packed)

	// Unscramble: extract N/2+1 bins from the halfN-point C2C result.
	// X[k] = 0.5 * (Z[k] + Z*[N/2-k]) - 0.5i * W(k,N) * (Z[k] - Z*[N/2-k])
	for k := 0; k <= halfN; k++ {
		var zRe, zIm float32

		if k < halfN {
			zRe = packed[2*k]
			zIm = packed[2*k+1]
		} else {
			// Z[N/2] = Z[0] for periodicity
			zRe = packed[0]
			zIm = packed[1]
		}

		// Conjugate mirror: Z*[N/2-k]
		mirrorK := halfN - k
		if mirrorK == halfN {
			mirrorK = 0
		}
		zcRe := packed[2*mirrorK]
		zcIm := -packed[2*mirrorK+1] // conjugate

		// Even part: Xe = 0.5 * (Z[k] + Z*[N/2-k])
		xeRe := 0.5 * (zRe + zcRe)
		xeIm := 0.5 * (zIm + zcIm)

		// Odd part: Xo = 0.5 * (Z[k] - Z*[N/2-k])
		xoRe := 0.5 * (zRe - zcRe)
		xoIm := 0.5 * (zIm - zcIm)

		// Precomputed twiddle: W(k,N)
		wRe := f.r2cTwiddle[2*k]
		wIm := f.r2cTwiddle[2*k+1]

		// -i * W * Xo
		niWXoRe := wIm*xoRe + wRe*xoIm
		niWXoIm := wIm*xoIm - wRe*xoRe

		output[2*k] = xeRe + niWXoRe
		output[2*k+1] = xeIm + niWXoIm
	}
}

// c2r performs a complex-to-real inverse FFT. input has length 2*(f.n+1) containing
// f.n+1 complex bins. output has length N = 2*f.n real samples.
func (f *fftState) c2r(input []float32, output []float32) {
	halfN := f.n

	// Re-pack N/2+1 bins back into N/2 complex values for C2C inverse.
	packed := f.packed

	for k := 0; k < halfN; k++ {
		xRe := input[2*k]
		xIm := input[2*k+1]

		// Mirror: conjugate symmetry
		mirrorK := halfN - k
		if mirrorK > halfN {
			mirrorK = halfN
		}
		xcRe := input[2*mirrorK]
		xcIm := -input[2*mirrorK+1] // conjugate

		// Xe = 0.5*(X[k] + X*[N-k])
		xeRe := 0.5 * (xRe + xcRe)
		xeIm := 0.5 * (xIm + xcIm)

		// Xo' = 0.5*(X[k] - X*[N-k])
		xoRe := 0.5 * (xRe - xcRe)
		xoIm := 0.5 * (xIm - xcIm)

		// Precomputed inverse twiddle: conj(W(k,N))
		wRe := f.r2cTwiddle[2*k]
		wIm := -f.r2cTwiddle[2*k+1] // conjugate for inverse

		// i * W^-1 * Xo'
		iWRe := -wIm
		iWIm := wRe
		iWXoRe := iWRe*xoRe - iWIm*xoIm
		iWXoIm := iWRe*xoIm + iWIm*xoRe

		// Z[k] = Xe + i*W^-1*Xo'
		packed[2*k] = xeRe + iWXoRe
		packed[2*k+1] = xeIm + iWXoIm
	}

	// Inverse C2C FFT
	f.inverse(packed)

	// Unpack: output[2k] = Re(z[k]), output[2k+1] = Im(z[k])
	copy(output[:2*halfN], packed[:2*halfN])
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
