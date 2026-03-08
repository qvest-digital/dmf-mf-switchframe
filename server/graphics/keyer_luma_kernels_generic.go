//go:build !arm64 && !amd64

package graphics

import "unsafe"

// lumaKeyMaskLUT applies a 256-byte lookup table to the Y plane, writing
// mask[i] = lut[yPlane[i]] for n pixels. The LUT is precomputed by the
// caller from the luma key parameters (lowClip, highClip, softness).
//
// This generic implementation uses unsafe.Slice to eliminate bounds checks
// in the inner loop.
func lumaKeyMaskLUT(mask, yPlane *byte, lut *byte, n int) {
	if n <= 0 {
		return
	}
	maskS := unsafe.Slice(mask, n)
	yS := unsafe.Slice(yPlane, n)
	lutS := unsafe.Slice(lut, 256)

	for i := 0; i < n; i++ {
		maskS[i] = lutS[yS[i]]
	}
}
