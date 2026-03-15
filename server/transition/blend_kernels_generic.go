//go:build !arm64 && !amd64

package transition

import "unsafe"

// blendUniform computes dst[i] = (a[i]*inv + b[i]*pos + 128) >> 8 for n bytes.
// Used by BlendMix for the entire YUV420 frame.
// Range: inv + pos = 256, max result = 255*256 + 128 = 65408, fits uint16.
//
// NOTE: SIMD kernels (amd64/arm64) also need this +128 rounding fix applied.
func blendUniform(dst, a, b *byte, n, pos, inv int) {
	if n <= 0 {
		return
	}
	dstS := unsafe.Slice(dst, n)
	aS := unsafe.Slice(a, n)
	bS := unsafe.Slice(b, n)
	for i := 0; i < n; i++ {
		dstS[i] = byte((int(aS[i])*inv + int(bS[i])*pos + 128) >> 8)
	}
}

// blendFadeConst computes dst[i] = (src[i]*gain + constTerm) >> 8 for n bytes.
// Used by BlendFTB and BlendDip for each YUV plane.
// Range: max = 255*256 + 128*256 = 98048, requires 32-bit arithmetic.
func blendFadeConst(dst, src *byte, n, gain, constTerm int) {
	if n <= 0 {
		return
	}
	dstS := unsafe.Slice(dst, n)
	srcS := unsafe.Slice(src, n)
	for i := 0; i < n; i++ {
		dstS[i] = byte((int(srcS[i])*gain + constTerm) >> 8)
	}
}

// blendAlpha computes dst[i] = (a[i]*(256-w) + b[i]*w + 128) >> 8
// where w = alpha[i] + (alpha[i]>>7), mapping 0-255 to 0-256.
// Used by BlendWipe and BlendStinger for the Y-plane.
// Range: w + inv = 256, max result = 255*256 + 128 = 65408, fits uint16.
//
// NOTE: SIMD kernels (amd64/arm64) also need this +128 rounding fix applied.
func blendAlpha(dst, a, b, alpha *byte, n int) {
	if n <= 0 {
		return
	}
	dstS := unsafe.Slice(dst, n)
	aS := unsafe.Slice(a, n)
	bS := unsafe.Slice(b, n)
	alphaS := unsafe.Slice(alpha, n)
	for i := 0; i < n; i++ {
		ai := int(alphaS[i])
		w := ai + (ai >> 7)
		inv := 256 - w
		dstS[i] = byte((int(aS[i])*inv + int(bS[i])*w + 128) >> 8)
	}
}
