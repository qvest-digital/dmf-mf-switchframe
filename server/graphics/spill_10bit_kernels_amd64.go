//go:build amd64

package graphics

// spillSuppress10Row applies spill suppression on 10-bit Cb/Cr chroma planes.
//
// On amd64, the Go compiler's SSE2 autovectorization outperforms our scalar
// float assembly for this workload (0.70x regression measured). The assembly
// uses scalar MOVSS/MULSS/SUBSS which processes one sample at a time, while
// the Go compiler generates better pipelined SSE code for the float32 loop.
//
// Use the Go reference directly.
func spillSuppress10Row(cbPlane, crPlane *byte, count int,
	keyCb, keyCr, spillSuppress, invSpillDistSq, replaceCb, replaceCr float32) {
	spillSuppress10RowGo(cbPlane, crPlane, count, keyCb, keyCr, spillSuppress, invSpillDistSq, replaceCb, replaceCr)
}
