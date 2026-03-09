//go:build !arm64 && !amd64

package replay

// butterflyRadix2 performs radix-2 butterfly operations for one FFT stage.
// data contains interleaved complex values [re0, im0, re1, im1, ...].
// halfN is the number of butterfly pairs in this stage.
// twiddleStride is N/(2*halfN) — step through twiddle table.
func butterflyRadix2(data, twiddle []float32, halfN, twiddleStride int) {
	for k := 0; k < halfN; k++ {
		twIdx := k * twiddleStride * 2
		wRe := twiddle[twIdx]
		wIm := twiddle[twIdx+1]

		evenIdx := k * 2
		oddIdx := (k + halfN) * 2

		// Complex multiply: t = W * data[odd]
		oddRe := data[oddIdx]
		oddIm := data[oddIdx+1]
		tRe := wRe*oddRe - wIm*oddIm
		tIm := wRe*oddIm + wIm*oddRe

		// Butterfly: even += t, odd = even_old - t
		eRe := data[evenIdx]
		eIm := data[evenIdx+1]
		data[evenIdx] = eRe + tRe
		data[evenIdx+1] = eIm + tIm
		data[oddIdx] = eRe - tRe
		data[oddIdx+1] = eIm - tIm
	}
}
