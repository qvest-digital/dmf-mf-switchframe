//go:build !arm64 && !amd64

package graphics

import "unsafe"

// blendMaskY blends fill onto bg using mask as per-pixel alpha, in-place.
// Integer fixed-point with rounding: matches every other blend path in the codebase.
//
//	w = mask[i] + (mask[i] >> 7)   // 0-255 → 0-256
//	inv = 256 - w
//	bg[i] = (bg[i]*inv + fill[i]*w + 128) >> 8
//
// The +128 bias rounds instead of truncating, eliminating systematic -1 bias.
func blendMaskY(bg *byte, fill *byte, mask *byte, n int) {
	if n <= 0 {
		return
	}
	bgS := unsafe.Slice(bg, n)
	fillS := unsafe.Slice(fill, n)
	maskS := unsafe.Slice(mask, n)
	_ = bgS[n-1]
	_ = fillS[n-1]
	_ = maskS[n-1]
	for i := 0; i < n; i++ {
		w := int(maskS[i])
		w += w >> 7 // map 0-255 to 0-256
		if w == 0 {
			continue
		}
		inv := 256 - w
		bgS[i] = byte((int(bgS[i])*inv + int(fillS[i])*w + 128) >> 8)
	}
}
