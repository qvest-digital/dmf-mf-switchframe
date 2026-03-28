//go:build amd64

package graphics

// chromaKey10Row computes chroma key mask for count chroma samples.
// AMD64 scalar assembly implementation using integer squared-distance.
//
//go:noescape
func chromaKey10Row(mask *byte, cb *byte, cr *byte, count int,
	targetCb, targetCr int, simDistSq, totalDistSq, invRange int)
