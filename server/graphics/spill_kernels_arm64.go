//go:build arm64

package graphics

// spillSuppressChroma applies spill suppression on Cb/Cr chroma planes.
// ARM64 NEON implementation processes 4 pixels per iteration using
// float32x4 SIMD for distance computation, conditional blending, and clamping.
//
// See spill_kernels_generic.go for algorithm documentation.
//
//go:noescape
func spillSuppressChroma(cbPlane, crPlane *byte, keyCb, keyCr, spillSuppress, invSpillDistSq, replaceCb, replaceCr float32, n int)
