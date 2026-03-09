package replay

import "golang.org/x/sys/cpu"

// avx2Available is set at init time if the CPU supports AVX2.
//
// Referenced from assembly via ·avx2Available(SB).
var avx2Available = cpu.X86.HasAVX2 //nolint:unused // used in fft_butterfly_amd64.s

// butterflyRadix2 performs radix-2 butterfly operations for one FFT stage.
// data contains interleaved complex values [re0, im0, re1, im1, ...].
// halfN is the number of butterfly pairs in this stage.
// twiddleStride is N/(2*halfN) — step through twiddle table.
//
// AMD64 implementation with AVX2 (4 butterflies/iter), SSE (2 butterflies/iter),
// and scalar fallback. AVX2 path selected at runtime via avx2Available.
//
//go:noescape
func butterflyRadix2(data, twiddle []float32, halfN, twiddleStride int)
