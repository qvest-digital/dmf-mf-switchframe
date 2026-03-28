//go:build arm64

package transition

// blendMix10Row blends count uint16 LE samples using NEON:
//
//	dst[i] = clamp10((a[i]*(256-pos) + b[i]*pos + 128) >> 8)
//
// NEON implementation processes 8 samples per iteration using widening
// multiply (UMULL/UMULL2) to 32-bit, then saturating narrow back to 16-bit.
//
//go:noescape
func blendMix10Row(dst, a, b *byte, count, pos256 int)
