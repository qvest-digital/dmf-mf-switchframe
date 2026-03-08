//go:build arm64

package transition

// lanczosHorizRow applies precomputed horizontal Lanczos weights to one source row.
// ARM64 assembly implementation with bounds-check elimination.
//
//go:noescape
func lanczosHorizRow(dst []float32, src []byte, offsets []int32, weights []float32, maxTaps int)

// lanczosVertRow applies precomputed vertical Lanczos weights to intermediate rows,
// producing one row of dstW uint8 output values.
// NEON implementation processes 4 float32 columns per iteration using FMLA.
//
//go:noescape
func lanczosVertRow(dst []byte, temp []float32, tempStride int, startRow int32, weights []float32, maxTaps int)
