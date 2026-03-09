//go:build !arm64 && !amd64

package replay

import "unsafe"

// crossCorrFloat32 computes three dot products over contiguous float32 arrays:
//   corr  = sum(a[i] * b[i])
//   normA = sum(a[i] * a[i])
//   normB = sum(b[i] * b[i])
//
// Used by findBestOverlap for normalized cross-correlation in WSOLA.
func crossCorrFloat32(a, b *float32, n int) (corr, normA, normB float32) {
	if n <= 0 {
		return
	}
	aS := unsafe.Slice(a, n)
	bS := unsafe.Slice(b, n)

	for i := 0; i < n; i++ {
		ai := aS[i]
		bi := bS[i]
		corr += ai * bi
		normA += ai * ai
		normB += bi * bi
	}
	return
}
