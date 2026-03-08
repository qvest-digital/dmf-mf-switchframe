//go:build amd64

package frcasm

import "golang.org/x/sys/cpu"

// avx2Available is set at init time if the CPU supports AVX2.
// Assembly routines branch to AVX2 or SSE2 path based on this flag.
// Referenced from assembly via ·avx2Available(SB).
var avx2Available = cpu.X86.HasAVX2 //nolint:unused // used in sad_amd64.s

// SadBlock16x16 computes the Sum of Absolute Differences between two 16x16 blocks.
// a and b point to the top-left pixel. aStride and bStride are the row pitch in bytes.
// Returns SAD value (0 = identical, max = 16*16*255 = 65280).
//
//go:noescape
func SadBlock16x16(a, b *byte, aStride, bStride int) uint32

// SadRow computes SAD across n bytes: sum(|a[i] - b[i]|).
// Used for scene change detection on Y-plane rows.
//
//go:noescape
func SadRow(a, b *byte, n int) uint64
