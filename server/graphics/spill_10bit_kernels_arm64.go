//go:build arm64

package graphics

// spillSuppress10Row applies spill suppression on 10-bit Cb/Cr chroma planes.
// ARM64 NEON implementation using float32 distance computation.
//
//go:noescape
func spillSuppress10Row(cbPlane, crPlane *byte, count int,
	keyCb, keyCr, spillSuppress, invSpillDistSq, replaceCb, replaceCr float32)
