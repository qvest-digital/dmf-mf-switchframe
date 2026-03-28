//go:build !amd64 && !arm64

package frcasm

import (
	"encoding/binary"
	"unsafe"
)

// Sad10Block computes the Sum of Absolute Differences between two blocks in a
// 10-bit (uint16 LE) Y plane. a and b point to the start of each block.
// stride is the row stride in bytes, bw and bh are block dimensions in samples.
func Sad10Block(a, b *byte, stride, bw, bh int) int {
	sum := 0
	aPtr := unsafe.Pointer(a)
	bPtr := unsafe.Pointer(b)
	for y := 0; y < bh; y++ {
		aRow := unsafe.Slice((*byte)(aPtr), bw*2)
		bRow := unsafe.Slice((*byte)(bPtr), bw*2)
		for x := 0; x < bw; x++ {
			aVal := int(binary.LittleEndian.Uint16(aRow[x*2:]))
			bVal := int(binary.LittleEndian.Uint16(bRow[x*2:]))
			diff := aVal - bVal
			if diff < 0 {
				diff = -diff
			}
			sum += diff
		}
		aPtr = unsafe.Add(aPtr, uintptr(stride))
		bPtr = unsafe.Add(bPtr, uintptr(stride))
	}
	return sum
}
