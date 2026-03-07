//go:build arm64

package transition

// blendUniform computes dst[i] = (a[i]*inv + b[i]*pos) >> 8 for n bytes.
// NEON implementation processes 16 bytes per iteration using 16-bit arithmetic.
//
//go:noescape
func blendUniform(dst, a, b *byte, n, pos, inv int)

// blendFadeConst computes dst[i] = (src[i]*gain + constTerm) >> 8 for n bytes.
// NEON implementation uses 32-bit arithmetic (constTerm can exceed uint16 range).
//
//go:noescape
func blendFadeConst(dst, src *byte, n, gain, constTerm int)

// blendAlpha computes dst[i] = (a[i]*(256-w) + b[i]*w) >> 8
// where w = alpha[i] + (alpha[i]>>7), mapping 0-255 to 0-256.
// NEON implementation processes 16 bytes per iteration using 16-bit arithmetic.
//
//go:noescape
func blendAlpha(dst, a, b, alpha *byte, n int)
