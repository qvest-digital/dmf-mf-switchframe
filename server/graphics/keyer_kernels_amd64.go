//go:build amd64

package graphics

// chromaKeyMaskChroma computes a chroma-resolution alpha mask from Cb/Cr planes.
// AMD64 scalar assembly with efficient register use and no bounds checks.
//
// See keyer_kernels_generic.go for algorithm documentation.
//
//go:noescape
func chromaKeyMaskChroma(mask *byte, cbPlane, crPlane *byte, keyCb, keyCr int, simThreshSq, totalThreshSq int, invRange int, n int)
