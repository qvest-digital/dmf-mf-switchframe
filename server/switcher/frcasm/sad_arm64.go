//go:build arm64

package frcasm

// SadBlock16x16 computes the Sum of Absolute Differences between two 16x16 blocks.
// a and b point to the top-left pixel. aStride and bStride are the row pitch in bytes.
// Returns SAD value (0 = identical, max = 16*16*255 = 65280).
// NEON implementation uses UABD + widening accumulate.
//
//go:noescape
func SadBlock16x16(a, b *byte, aStride, bStride int) uint32

// SadRow computes SAD across n bytes: sum(|a[i] - b[i]|).
// Used for scene change detection on Y-plane rows.
// NEON implementation processes 16 bytes per iteration.
//
//go:noescape
func SadRow(a, b *byte, n int) uint64
