//go:build arm64

package frcasm

// DownsampleY2x downsamples a Y plane by 2× using a box filter.
// Uses cascaded URHADD rounding: avg(avg(row0_even, row0_odd), avg(row1_even, row1_odd)).
// dst must be at least (srcW/2)*(srcH/2) bytes. src must be at least srcW*srcH bytes.
// NEON implementation processes 16 output pixels per iteration using UZP1/UZP2 deinterleave
// and URHADD for rounding-halving-add.
//
//go:noescape
func DownsampleY2x(dst, src *byte, srcW, srcH int)
