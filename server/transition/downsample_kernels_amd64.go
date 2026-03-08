//go:build amd64

package transition

// downsampleAlpha2x2 computes 2x2 box average downsampling for one row pair.
// For each output pixel i: dst[i] = (row0[2*i] + row0[2*i+1] + row1[2*i] + row1[2*i+1] + 2) / 4.
// pairs is the number of output pixels (half of input width per row).
//
// AVX2 path processes 16 output pixels (32 input bytes) per iteration using
// VPAVGB for rounding halving addition. SSE2 fallback processes 8 output
// pixels (16 input bytes). Scalar tail handles remaining pairs.
//
// Note: VPAVGB computes (a+b+1)/2 with rounding, so two rounds give
// ((a+b+1)/2 + (c+d+1)/2 + 1)/2 which may differ from (a+b+c+d+2)/4
// by at most +/-1 for alpha downsampling, which is acceptable.
//
//go:noescape
func downsampleAlpha2x2(dst, row0, row1 *byte, pairs int)
