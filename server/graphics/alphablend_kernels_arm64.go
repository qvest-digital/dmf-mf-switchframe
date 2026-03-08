//go:build arm64

package graphics

// alphaBlendRGBARowY processes one row of the Y plane using integer fixed-point
// BT.709 coefficients. Scalar assembly: eliminates bounds checks and float64
// conversions. See alphablend_kernels_arm64.s for implementation.
//
//go:noescape
func alphaBlendRGBARowY(yRow *byte, rgba *byte, width int, alphaScale256 int)
