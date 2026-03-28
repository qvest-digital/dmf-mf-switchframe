//go:build !arm64 && !amd64

package transition

// lanczosHoriz10Row applies precomputed horizontal Lanczos weights to one 10-bit source row.
// Generic Go fallback.
func lanczosHoriz10Row(dst []float32, src []byte, srcW int, offsets []int32, weights []float32, maxTaps int) {
	lanczosHoriz10RowGo(dst, src, srcW, offsets, weights, maxTaps)
}

// lanczosVert10Row applies precomputed vertical Lanczos weights to float32 intermediate rows,
// producing uint16 LE output clamped to [0, 1023]. Generic Go fallback.
func lanczosVert10Row(dst []byte, temp []float32, tempStride int, startRow int32, weights []float32, maxTaps int) {
	lanczosVert10RowGo(dst, temp, tempStride, startRow, weights, maxTaps)
}
