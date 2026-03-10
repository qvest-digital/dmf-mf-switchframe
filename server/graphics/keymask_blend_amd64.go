//go:build amd64

package graphics

// blendMaskY blends fill onto bg using mask as per-pixel alpha, in-place.
// SSE2 assembly: processes 16 bytes per iteration with uint16 arithmetic.
//
//	w = mask[i] + (mask[i] >> 7)   // 0-255 → 0-256
//	bg[i] = (bg[i]*(256-w) + fill[i]*w + 128) >> 8
//
//go:noescape
func blendMaskY(bg *byte, fill *byte, mask *byte, n int)
