//go:build arm64

package frcasm

// DownsampleY10_2x downsamples a 10-bit (uint16 LE) Y plane by 2x in both dimensions.
// Uses box filter: avg(2x2 block of uint16) → uint8 output (10→8 bit conversion).
// srcStride is source row stride in bytes, dstW and dstH are output dimensions.
//
// ARM64 NEON implementation processes 8 output pixels per iteration using
// UADDLP for pairwise add and SHRN for narrowing.
//
//go:noescape
func DownsampleY10_2x(dst, src *byte, srcStride, dstW, dstH int)
