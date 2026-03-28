//go:build amd64

package transition

// blendMix10Row blends count uint16 LE samples using AVX2/SSE2:
//
//	dst[i] = clamp10((a[i]*(256-pos) + b[i]*pos + 128) >> 8)
//
// AVX2 implementation processes 8 samples per iteration by widening uint16
// to int32 (VPMOVZXWD), 32-bit multiply (VPMULLD), add, shift, then
// saturating pack back to uint16 (VPACKUSDW).
// SSE2 fallback uses scalar loop.
//
//go:noescape
func blendMix10Row(dst, a, b *byte, count, pos256 int)
