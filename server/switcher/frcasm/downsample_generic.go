//go:build !amd64 && !arm64

package frcasm

import "unsafe"

// DownsampleY2x downsamples a Y plane by 2× using a box filter.
// Uses cascaded rounding matching PAVGB/URHADD hardware semantics:
// avg(avg(row0[2c], row0[2c+1]), avg(row1[2c], row1[2c+1]))
// where avg(a,b) = (a + b + 1) >> 1.
// This may differ by ±1 LSB from exact (a+b+c+d+2)/4, matching standard
// video encoder pyramid downsamplers (x264, libvpx).
func DownsampleY2x(dst, src *byte, srcW, srcH int) {
	dstW := srcW / 2
	dstH := srcH / 2
	srcSlice := unsafe.Slice(src, srcW*srcH)
	dstSlice := unsafe.Slice(dst, dstW*dstH)

	for row := 0; row < dstH; row++ {
		srcRow0 := row * 2 * srcW
		srcRow1 := srcRow0 + srcW
		dstOff := row * dstW
		for col := 0; col < dstW; col++ {
			sc := col * 2
			// Cascaded PAVG rounding (matches SIMD platforms)
			avgH0 := (uint(srcSlice[srcRow0+sc]) + uint(srcSlice[srcRow0+sc+1]) + 1) >> 1
			avgH1 := (uint(srcSlice[srcRow1+sc]) + uint(srcSlice[srcRow1+sc+1]) + 1) >> 1
			dstSlice[dstOff+col] = byte((avgH0 + avgH1 + 1) >> 1)
		}
	}
}
