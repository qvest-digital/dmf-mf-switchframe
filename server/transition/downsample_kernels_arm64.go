//go:build arm64

package transition

// downsampleAlpha2x2 computes 2x2 box average downsampling for one row pair.
// For each output pixel i: dst[i] = (row0[2*i] + row0[2*i+1] + row1[2*i] + row1[2*i+1] + 2) / 4.
// pairs is the number of output pixels (half of input width per row).
//
// NEON implementation uses URHADD (unsigned rounding halving add) to compute
// rounding averages. Processes 8 output pixels (16 input bytes) per iteration.
// Scalar tail handles remaining pairs.
//
//go:noescape
func downsampleAlpha2x2(dst, row0, row1 *byte, pairs int)
