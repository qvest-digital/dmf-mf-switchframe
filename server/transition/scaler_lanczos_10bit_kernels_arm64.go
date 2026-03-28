//go:build arm64

package transition

// lanczosHoriz10Row applies precomputed horizontal Lanczos weights to one 10-bit source row.
// Reads uint16 LE samples from src, outputs float32 to dst.
// ARM64 NEON implementation with FMLA for the tap accumulation.
//
//go:noescape
func lanczosHoriz10Row(dst []float32, src []byte, srcW int, offsets []int32, weights []float32, maxTaps int)

// lanczosVert10Row applies precomputed vertical Lanczos weights to float32 intermediate rows,
// producing one row of dstW uint16 LE output values clamped to [0, 1023].
// NEON implementation processes 4 columns per iteration using FMLA.
//
//go:noescape
func lanczosVert10Row(dst []byte, temp []float32, tempStride int, startRow int32, weights []float32, maxTaps int)
