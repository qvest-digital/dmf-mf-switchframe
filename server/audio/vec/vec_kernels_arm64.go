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

// PeakAbsFloat32 returns the maximum absolute value in n contiguous float32 elements.
// NEON implementation processes 4 float32s per iteration with FABS+FMAX.
//
//go:noescape
func PeakAbsFloat32(data *float32, n int) float32

// PeakAbsStereoFloat32 returns max |left| and max |right| from interleaved stereo
// float32 data. n is the total number of samples (must be even).
// NEON implementation uses UZP1/UZP2 to deinterleave in-register.
//
//go:noescape
func PeakAbsStereoFloat32(data *float32, n int) (peakL, peakR float32)
