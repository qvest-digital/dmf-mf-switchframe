//go:build !amd64 && !arm64

package graphics

// spillSuppress10Row applies spill suppression on 10-bit Cb/Cr chroma planes.
// Generic Go fallback.
func spillSuppress10Row(cbPlane, crPlane *byte, count int,
	keyCb, keyCr, spillSuppress, invSpillDistSq, replaceCb, replaceCr float32) {
	spillSuppress10RowGo(cbPlane, crPlane, count, keyCb, keyCr, spillSuppress, invSpillDistSq, replaceCb, replaceCr)
}
