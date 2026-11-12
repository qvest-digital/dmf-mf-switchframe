//go:build amd64

package transition

// boxShrink2xRow averages the 2x2 blocks of one source row pair into n
// destination pixels: dst[i] = (row0[2i] + row0[2i+1] + row1[2i] +
// row1[2i+1] + 2) >> 2, reading 2n bytes from each row.
//
// AVX2 processes 16 output pixels per iteration, SSE2 8, a scalar tail the
// rest. The sums are taken in 16-bit lanes, so the result is exact rather
// than the +/-1 of two rounding averages (PAVGB).
//
//go:noescape
func boxShrink2xRow(dst, row0, row1 *byte, n int)
