//go:build amd64

package frcasm

// DownsampleY10_2x downsamples a 10-bit (uint16 LE) Y plane by 2x in both dimensions.
// Uses box filter: avg(2x2 block of uint16) → uint8 output (10→8 bit conversion).
// srcStride is source row stride in bytes, dstW and dstH are output dimensions.
//
// AMD64 AVX2/SSE2 implementation uses VPADDW for horizontal pair add,
// VPADDD for vertical add, and VPACKUSWB for narrowing.
//
//go:noescape
func DownsampleY10_2x(dst, src *byte, srcStride, dstW, dstH int)
