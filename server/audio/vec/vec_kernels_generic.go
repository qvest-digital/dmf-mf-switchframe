//go:build !arm64 && !amd64

// Package vec provides SIMD-accelerated float32 vector operations for the
// audio mixing hot path. Separated from audio/ because that package uses
// cgo (FDK-AAC) which cannot coexist with Go assembly in the same package.
package vec

import "unsafe"

// AddFloat32 computes dst[i] += src[i] for n float32 elements.
// Generic scalar fallback for non-SIMD architectures.
func AddFloat32(dst, src *float32, n int) {
	if n <= 0 {
		return
	}
	dstS := unsafe.Slice(dst, n)
	srcS := unsafe.Slice(src, n)
	for i := 0; i < n; i++ {
		dstS[i] += srcS[i]
	}
}

// ScaleFloat32 computes dst[i] *= scale for n float32 elements.
// Generic scalar fallback for non-SIMD architectures.
func ScaleFloat32(dst *float32, scale float32, n int) {
	if n <= 0 {
		return
	}
	dstS := unsafe.Slice(dst, n)
	for i := 0; i < n; i++ {
		dstS[i] *= scale
	}
}

// MulAddFloat32 computes dst[i] = a[i]*x[i] + b[i]*y[i] for n float32 elements.
// Generic scalar fallback for non-SIMD architectures.
func MulAddFloat32(dst, a, x, b, y *float32, n int) {
	if n <= 0 {
		return
	}
	dstS := unsafe.Slice(dst, n)
	aS := unsafe.Slice(a, n)
	xS := unsafe.Slice(x, n)
	bS := unsafe.Slice(b, n)
	yS := unsafe.Slice(y, n)
	for i := 0; i < n; i++ {
		dstS[i] = aS[i]*xS[i] + bS[i]*yS[i]
	}
}

// PeakAbsFloat32 returns the maximum absolute value in n contiguous float32 elements.
// Generic scalar fallback for non-SIMD architectures.
func PeakAbsFloat32(data *float32, n int) float32 {
	if n <= 0 {
		return 0
	}
	s := unsafe.Slice(data, n)
	var peak float32
	for i := 0; i < n; i++ {
		v := s[i]
		if v < 0 {
			v = -v
		}
		if v > peak {
			peak = v
		}
	}
	return peak
}

// PeakAbsStereoFloat32 returns max |left| and max |right| from interleaved stereo
// float32 data. n is the total number of samples (must be even).
// Generic scalar fallback for non-SIMD architectures.
func PeakAbsStereoFloat32(data *float32, n int) (peakL, peakR float32) {
	if n < 2 {
		return 0, 0
	}
	s := unsafe.Slice(data, n)
	for i := 0; i < n-1; i += 2 {
		l := s[i]
		if l < 0 {
			l = -l
		}
		if l > peakL {
			peakL = l
		}
		r := s[i+1]
		if r < 0 {
			r = -r
		}
		if r > peakR {
			peakR = r
		}
	}
	return
}
