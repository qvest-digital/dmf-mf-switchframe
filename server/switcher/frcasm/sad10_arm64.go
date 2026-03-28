//go:build arm64

package frcasm

// Sad10Block computes the Sum of Absolute Differences between two blocks in a
// 10-bit (uint16 LE) Y plane. a and b point to the start of each block.
// stride is the row stride in bytes, bw and bh are block dimensions in samples.
// NEON implementation: processes 8 uint16 per iteration using UABD(h) + UADDLP.
//
//go:noescape
func Sad10Block(a, b *byte, stride, bw, bh int) int
