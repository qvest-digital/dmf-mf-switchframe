//go:build !amd64 && !arm64

package frcasm

import (
	"encoding/binary"
	"unsafe"
)

// DownsampleY10_2x downsamples a 10-bit (uint16 LE) Y plane by 2x in both dimensions.
// Uses box filter averaging of 2x2 blocks: dst[i] = (p00 + p10 + p01 + p11 + 2) >> 2.
// srcStride is the source row stride in bytes (= srcW * 2 for packed uint16).
// dstW and dstH are the output dimensions (= srcW/2, srcH/2).
// Output is uint8 (downsampled for motion estimation, 10→8 bit right shift by 2).
func DownsampleY10_2x(dst, src *byte, srcStride, dstW, dstH int) {
	if dstW <= 0 || dstH <= 0 {
		return
	}

	srcSlice := unsafe.Slice(src, srcStride*dstH*2)
	dstSlice := unsafe.Slice(dst, dstW*dstH)

	for row := 0; row < dstH; row++ {
		srcRow0 := row * 2 * srcStride
		srcRow1 := srcRow0 + srcStride
		dstOff := row * dstW
		for col := 0; col < dstW; col++ {
			sc := col * 2 * 2 // 2 pixels × 2 bytes each
			p00 := int(binary.LittleEndian.Uint16(srcSlice[srcRow0+sc:]))
			p10 := int(binary.LittleEndian.Uint16(srcSlice[srcRow0+sc+2:]))
			p01 := int(binary.LittleEndian.Uint16(srcSlice[srcRow1+sc:]))
			p11 := int(binary.LittleEndian.Uint16(srcSlice[srcRow1+sc+2:]))

			// Average 2x2 block, then convert 10-bit to 8-bit by right-shifting 2
			avg := (p00 + p10 + p01 + p11 + 2) >> 2 // 10-bit average
			dstSlice[dstOff+col] = byte(avg >> 2)    // 10→8 bit
		}
	}
}
