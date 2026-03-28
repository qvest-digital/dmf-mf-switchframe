//go:build amd64

package transition

// blendRows10 blends two rows of uint16 LE samples with 16.16 fixed-point weight:
//
//	dst[i] = clamp10((top[i] * (65536-fy) + bot[i] * fy + 32768) >> 16)
//
// AMD64 AVX2 implementation processes 8 samples per iteration using widened
// 32-bit multiply (VPMOVZXWD + VPMULLD), then pack back to uint16.
//
//go:noescape
func blendRows10(dst, topRow, botRow *byte, fy, count int)
