package transition

import "unsafe"

// downsampleAlpha10RowGo is the pure Go reference implementation for horizontal 2:1
// alpha downsample. Averages pairs of adjacent uint8 alpha values:
//
//	dst[i] = (src[2*i] + src[2*i+1] + 1) >> 1
//
// count is the number of OUTPUT samples (= srcWidth / 2).
// Used as the fallback on platforms without SIMD and as the reference for testing.
func downsampleAlpha10RowGo(dst, src *byte, count int) {
	if count <= 0 {
		return
	}

	dstSlice := unsafe.Slice(dst, count)
	srcSlice := unsafe.Slice(src, count*2)

	for i := 0; i < count; i++ {
		a := uint(srcSlice[2*i])
		b := uint(srcSlice[2*i+1])
		dstSlice[i] = byte((a + b + 1) >> 1)
	}
}
