//go:build arm64

package graphics

// chromaKey10Row computes chroma key mask for count chroma samples.
// ARM64 NEON implementation using integer squared-distance.
//
//go:noescape
func chromaKey10Row(mask *byte, cb *byte, cr *byte, count int,
	targetCb, targetCr int, simDistSq, totalDistSq, invRange int)
