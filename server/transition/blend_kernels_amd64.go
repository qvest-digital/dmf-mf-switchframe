//go:build amd64

package transition

import "golang.org/x/sys/cpu"

// avx2Available is set at init time if the CPU supports AVX2.
// Assembly routines branch to AVX2 or SSE2 path based on this flag.
// Referenced from assembly via ·avx2Available(SB).
var avx2Available = cpu.X86.HasAVX2 //nolint:unused // used in blend_kernels_amd64.s

// blendUniform computes dst[i] = (a[i]*inv + b[i]*pos) >> 8 for n bytes.
// AVX2 path processes 32 bytes/iteration, SSE2 fallback processes 16.
//
//go:noescape
func blendUniform(dst, a, b *byte, n, pos, inv int)

// blendFadeConst computes dst[i] = (src[i]*gain + constTerm) >> 8 for n bytes.
// Uses 32-bit arithmetic (constTerm can exceed uint16 range).
//
//go:noescape
func blendFadeConst(dst, src *byte, n, gain, constTerm int)

// blendAlpha computes dst[i] = (a[i]*(256-w) + b[i]*w) >> 8
// where w = alpha[i] + (alpha[i]>>7), mapping 0-255 to 0-256.
//
//go:noescape
func blendAlpha(dst, a, b, alpha *byte, n int)
