//go:build amd64

package frcasm

// DownsampleY2x downsamples a Y plane by 2× using a box filter.
// Uses cascaded PAVGB/PAVGW rounding: avg(avg(row0_even, row0_odd), avg(row1_even, row1_odd)).
// dst must be at least (srcW/2)*(srcH/2) bytes. src must be at least srcW*srcH bytes.
// SSE2 implementation processes 8 output pixels per iteration.
// AVX2 path processes 16 output pixels per iteration when available.
//
//go:noescape
func DownsampleY2x(dst, src *byte, srcW, srcH int)
