package replay

// butterflyRadix2 performs radix-2 butterfly operations for one FFT stage.
// data contains interleaved complex values [re0, im0, re1, im1, ...].
// halfN is the number of butterfly pairs in this stage.
// twiddleStride is N/(2*halfN) — step through twiddle table.
//
// AMD64 SSE implementation processes 2 butterflies per iteration when
// twiddleStride == 1 (the largest FFT stages which dominate computation).
// Falls back to scalar for other strides.
//
//go:noescape
func butterflyRadix2(data, twiddle []float32, halfN, twiddleStride int)
