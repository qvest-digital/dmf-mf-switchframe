//go:build !arm64 && !amd64

package transition

import "unsafe"

// downsampleAlpha2x2 computes 2x2 box average downsampling for one row pair.
// For each output pixel i: dst[i] = (row0[2*i] + row0[2*i+1] + row1[2*i] + row1[2*i+1] + 2) / 4.
// pairs is the number of output pixels (half of input width per row).
func downsampleAlpha2x2(dst, row0, row1 *byte, pairs int) {
	if pairs <= 0 {
		return
	}
	dstS := unsafe.Slice(dst, pairs)
	r0 := unsafe.Slice(row0, pairs*2)
	r1 := unsafe.Slice(row1, pairs*2)
	for i := 0; i < pairs; i++ {
		a00 := int(r0[2*i])
		a10 := int(r0[2*i+1])
		a01 := int(r1[2*i])
		a11 := int(r1[2*i+1])
		dstS[i] = byte((a00 + a10 + a01 + a11 + 2) / 4)
	}
}
