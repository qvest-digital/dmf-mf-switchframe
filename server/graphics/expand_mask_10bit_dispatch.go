package graphics

import "unsafe"

// expandChromaMask10RowGo is the pure Go reference implementation for expanding
// a chroma-resolution mask to luma resolution for 4:2:2 (horizontal duplication).
// Each input byte is duplicated to produce two output bytes.
// count = number of INPUT samples (chroma width); output is count*2 bytes.
func expandChromaMask10RowGo(dst, src *byte, count int) {
	if count <= 0 {
		return
	}
	srcS := unsafe.Slice(src, count)
	dstS := unsafe.Slice(dst, count*2)

	for i := 0; i < count; i++ {
		v := srcS[i]
		dstS[i*2] = v
		dstS[i*2+1] = v
	}
}
