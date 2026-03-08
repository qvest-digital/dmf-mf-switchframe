//go:build arm64

// Package vec provides SIMD-accelerated float32 vector operations for the
// audio mixing hot path. Separated from audio/ because that package uses
// cgo (FDK-AAC) which cannot coexist with Go assembly in the same package.
package vec

// AddFloat32 computes dst[i] += src[i] for n float32 elements.
// NEON implementation processes 4 float32s per iteration.
//
//go:noescape
func AddFloat32(dst, src *float32, n int)

// ScaleFloat32 computes dst[i] *= scale for n float32 elements.
// NEON implementation processes 4 float32s per iteration.
//
//go:noescape
func ScaleFloat32(dst *float32, scale float32, n int)

// MulAddFloat32 computes dst[i] = a[i]*x[i] + b[i]*y[i] for n float32 elements.
// NEON implementation processes 4 float32s per iteration.
//
//go:noescape
func MulAddFloat32(dst, a, x, b, y *float32, n int)
