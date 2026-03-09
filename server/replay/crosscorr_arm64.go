//go:build arm64

package replay

// crossCorrFloat32 computes three dot products over contiguous float32 arrays:
//   corr  = sum(a[i] * b[i])
//   normA = sum(a[i] * a[i])
//   normB = sum(b[i] * b[i])
//
// ARM64 NEON implementation processes 4 float32s per iteration.
//
//go:noescape
func crossCorrFloat32(a, b *float32, n int) (corr, normA, normB float32)
