//go:build !amd64 && !arm64

package transition

// blendRows10 blends two rows of uint16 LE samples with 16.16 fixed-point weight:
//
//	dst[i] = clamp10((top[i] * (65536-fy) + bot[i] * fy + 32768) >> 16)
//
// Generic Go fallback for platforms without SIMD acceleration.
func blendRows10(dst, topRow, botRow *byte, fy, count int) {
	blendRows10Go(dst, topRow, botRow, fy, count)
}
