//go:build arm64

package graphics

// keyMaskBlend10Row blends fill onto bg using mask as per-pixel alpha for 10-bit samples.
//
//	w = mask[i] + (mask[i] >> 7)   // 0-255 → 0-256
//	bg[i] = clamp10((bg[i] * (256-w) + fill[i] * w + 128) >> 8)
//
// ARM64 NEON implementation processes 4 uint16 samples per iteration
// using widening multiply to uint32 for overflow-safe intermediate math.
//
//go:noescape
func keyMaskBlend10Row(bg, fill, mask *byte, count int)
