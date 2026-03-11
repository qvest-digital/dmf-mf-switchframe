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

// SadBlock16x16HpelH computes SAD between a 16x16 current block and a
// horizontally half-pel interpolated reference block.
// ref points to the integer-pel base; the kernel averages ref[x] and ref[x+1].
// Uses (a+b+1)>>1 rounding (matching PAVGB/URHADD hardware semantics).
func SadBlock16x16HpelH(cur, ref *byte, curStride, refStride int) uint32 {
	var sad uint32
	curPtr := unsafe.Pointer(cur)
	refPtr := unsafe.Pointer(ref)
	for row := 0; row < 16; row++ {
		curRow := unsafe.Slice((*byte)(curPtr), 16)
		refRow := unsafe.Slice((*byte)(refPtr), 17) // need +1 for horizontal neighbor
		for col := 0; col < 16; col++ {
			interp := (int(refRow[col]) + int(refRow[col+1]) + 1) >> 1
			d := int(curRow[col]) - interp
			if d < 0 {
				d = -d
			}
			sad += uint32(d)
		}
		curPtr = unsafe.Add(curPtr, uintptr(curStride))
		refPtr = unsafe.Add(refPtr, uintptr(refStride))
	}
	return sad
}

// SadBlock16x16HpelV computes SAD between a 16x16 current block and a
// vertically half-pel interpolated reference block.
// ref points to the integer-pel base; the kernel averages ref[y] and ref[y+stride].
// Uses (a+b+1)>>1 rounding (matching PAVGB/URHADD hardware semantics).
func SadBlock16x16HpelV(cur, ref *byte, curStride, refStride int) uint32 {
	var sad uint32
	curPtr := unsafe.Pointer(cur)
	refPtr := unsafe.Pointer(ref)
	for row := 0; row < 16; row++ {
		curRow := unsafe.Slice((*byte)(curPtr), 16)
		refRow0 := unsafe.Slice((*byte)(refPtr), 16)
		refRow1 := unsafe.Slice((*byte)(unsafe.Add(refPtr, uintptr(refStride))), 16)
		for col := 0; col < 16; col++ {
			interp := (int(refRow0[col]) + int(refRow1[col]) + 1) >> 1
			d := int(curRow[col]) - interp
			if d < 0 {
				d = -d
			}
			sad += uint32(d)
		}
		curPtr = unsafe.Add(curPtr, uintptr(curStride))
		refPtr = unsafe.Add(refPtr, uintptr(refStride))
	}
	return sad
}

// SadBlock16x16HpelD computes SAD between a 16x16 current block and a
// diagonally half-pel interpolated reference block.
// ref points to the integer-pel base; the kernel averages 4 neighbors.
// Uses cascaded (a+b+1)>>1 rounding (matching PAVGB(PAVGB(a,b),PAVGB(c,d))
// hardware semantics). This may differ by ±1 LSB from the exact (a+b+c+d+2)>>2
// formula, which is the standard tradeoff used by x264, libvpx, and FFmpeg.
// SadBlock16x16x4 computes 4 SADs in one pass: loads the current block once
// and computes SAD against 4 reference blocks simultaneously. This amortizes
// the source-block memory loads across 4 computations.
func SadBlock16x16x4(cur *byte, refs [4]*byte, curStride, refStride int) [4]uint32 {
	var result [4]uint32
	for i := 0; i < 4; i++ {
		result[i] = SadBlock16x16(cur, refs[i], curStride, refStride)
	}
	return result
}

func SadBlock16x16HpelD(cur, ref *byte, curStride, refStride int) uint32 {
	var sad uint32
	curPtr := unsafe.Pointer(cur)
	refPtr := unsafe.Pointer(ref)
	for row := 0; row < 16; row++ {
		curRow := unsafe.Slice((*byte)(curPtr), 16)
		refRow0 := unsafe.Slice((*byte)(refPtr), 17)
		refRow1 := unsafe.Slice((*byte)(unsafe.Add(refPtr, uintptr(refStride))), 17)
		for col := 0; col < 16; col++ {
			// Cascaded PAVGB rounding: avg(avg(a,b), avg(c,d))
			avgTop := (int(refRow0[col]) + int(refRow0[col+1]) + 1) >> 1
			avgBot := (int(refRow1[col]) + int(refRow1[col+1]) + 1) >> 1
			interp := (avgTop + avgBot + 1) >> 1
			d := int(curRow[col]) - interp
			if d < 0 {
				d = -d
			}
			sad += uint32(d)
		}
		curPtr = unsafe.Add(curPtr, uintptr(curStride))
		refPtr = unsafe.Add(refPtr, uintptr(refStride))
	}
	return sad
}
