//go:build !amd64 && !arm64

package frcasm

import "unsafe"

// SadBlock16x16 computes the Sum of Absolute Differences between two 16x16 blocks.
// a and b point to the top-left pixel. aStride and bStride are the row pitch in bytes.
// Returns SAD value (0 = identical, max = 16*16*255 = 65280).
func SadBlock16x16(a, b *byte, aStride, bStride int) uint32 {
	var sad uint32
	aPtr := unsafe.Pointer(a)
	bPtr := unsafe.Pointer(b)
	for row := 0; row < 16; row++ {
		aRow := unsafe.Slice((*byte)(aPtr), 16)
		bRow := unsafe.Slice((*byte)(bPtr), 16)
		for col := 0; col < 16; col++ {
			d := int(aRow[col]) - int(bRow[col])
			if d < 0 {
				d = -d
			}
			sad += uint32(d)
		}
		aPtr = unsafe.Add(aPtr, uintptr(aStride))
		bPtr = unsafe.Add(bPtr, uintptr(bStride))
	}
	return sad
}

// SadRow computes SAD across n bytes: sum(|a[i] - b[i]|).
// Used for scene change detection on Y-plane rows.
func SadRow(a, b *byte, n int) uint64 {
	if n <= 0 {
		return 0
	}
	aS := unsafe.Slice(a, n)
	bS := unsafe.Slice(b, n)
	var sad uint64
	for i := 0; i < n; i++ {
		d := int(aS[i]) - int(bS[i])
		if d < 0 {
			d = -d
		}
		sad += uint64(d)
	}
	return sad
}
