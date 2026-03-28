//go:build arm64

package transition

// blendRows10 blends two rows of uint16 LE samples with 16.16 fixed-point weight:
//
//	dst[i] = clamp10((top[i] * (65536-fy) + bot[i] * fy + 32768) >> 16)
//
// ARM64 NEON implementation processes 8 samples per iteration using widening
// multiply (UMULL/UMULL2) to 32-bit, then saturating narrow back to 16-bit.
//
//go:noescape
func blendRows10(dst, topRow, botRow *byte, fy, count int)
