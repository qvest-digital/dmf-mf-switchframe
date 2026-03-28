//go:build amd64

package graphics

// keyMaskBlend10Row blends fill onto bg using mask as per-pixel alpha for 10-bit samples.
//
//	w = mask[i] + (mask[i] >> 7)   // 0-255 → 0-256
//	bg[i] = clamp10((bg[i] * (256-w) + fill[i] * w + 128) >> 8)
//
// AMD64 AVX2 implementation processes 8 uint16 samples per iteration
// using VPMULLD for 32-bit multiply and VPACKUSDW for narrowing.
//
//go:noescape
func keyMaskBlend10Row(bg, fill, mask *byte, count int)
