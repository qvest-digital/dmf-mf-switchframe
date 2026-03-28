//go:build !amd64 && !arm64

package graphics

// chromaKey10Row computes chroma key mask for count chroma samples.
// Generic Go fallback.
func chromaKey10Row(mask *byte, cb *byte, cr *byte, count int,
	targetCb, targetCr int, simDistSq, totalDistSq, invRange int) {
	chromaKey10RowGo(mask, cb, cr, count, targetCb, targetCr, simDistSq, totalDistSq, invRange)
}
