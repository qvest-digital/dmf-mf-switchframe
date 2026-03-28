package switcher

import (
	"unsafe"

	"github.com/zsiec/switchframe/server/switcher/frcasm"
)

// sad10Dispatch computes the Sum of Absolute Differences between two blocks in a
// 10-bit (uint16 LE) Y plane using SIMD acceleration when available.
// Parameters match SAD10: a and b are byte slices, aOff and bOff are byte offsets,
// stride is in samples (not bytes), bw and bh are block dimensions in samples.
func sad10Dispatch(a, b []byte, aOff, bOff, stride, bw, bh int) int {
	strideBytes := stride * 2
	return frcasm.Sad10Block(
		(*byte)(unsafe.Pointer(&a[aOff])),
		(*byte)(unsafe.Pointer(&b[bOff])),
		strideBytes, bw, bh,
	)
}
