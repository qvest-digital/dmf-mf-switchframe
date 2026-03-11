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

// SadBlock16x16x4 computes 4 SADs in one pass: loads each current-block row
// once and computes SAD against 4 reference blocks simultaneously.
// Amortizes source-block memory loads across 4 computations (~4x fewer
// source-block cache line fetches vs 4 individual SadBlock16x16 calls).
// NEON implementation uses UABD + UADALP with 4 halfword accumulators.
//
//go:noescape
func SadBlock16x16x4(cur *byte, refs [4]*byte, curStride, refStride int) [4]uint32

// SadBlock16x16HpelH computes SAD between a 16x16 current block and a
// horizontally half-pel interpolated reference block.
// ref points to the integer-pel base; the kernel averages ref[x] and ref[x+1].
// Uses URHADD (a+b+1)>>1 rounding for hardware-matching semantics.
//
//go:noescape
func SadBlock16x16HpelH(cur, ref *byte, curStride, refStride int) uint32

// SadBlock16x16HpelV computes SAD between a 16x16 current block and a
// vertically half-pel interpolated reference block.
// ref points to the integer-pel base; the kernel averages ref[y] and ref[y+stride].
// Uses URHADD (a+b+1)>>1 rounding for hardware-matching semantics.
//
//go:noescape
func SadBlock16x16HpelV(cur, ref *byte, curStride, refStride int) uint32

// SadBlock16x16HpelD computes SAD between a 16x16 current block and a
// diagonally half-pel interpolated reference block.
// ref points to the integer-pel base; the kernel averages 4 neighbors using
// cascaded URHADD: avg(avg(a,b), avg(c,d)). May differ by ±1 LSB from exact
// (a+b+c+d+2)>>2, matching x264/libvpx behavior.
//
//go:noescape
func SadBlock16x16HpelD(cur, ref *byte, curStride, refStride int) uint32
