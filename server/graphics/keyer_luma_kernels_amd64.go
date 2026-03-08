//go:build amd64

package graphics

// lumaKeyMaskLUT applies a 256-byte lookup table to the Y plane.
// AMD64 scalar assembly with 4x unrolled inner loop and no bounds checks.
//
// mask[i] = lut[yPlane[i]] for i in [0, n).
//
// See keyer_luma_kernels_generic.go for algorithm documentation.
//
//go:noescape
func lumaKeyMaskLUT(mask, yPlane *byte, lut *byte, n int)
