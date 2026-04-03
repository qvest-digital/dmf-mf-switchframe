//go:build !arm64 && !amd64

package mxl

import (
	"encoding/binary"
	"unsafe"
)

// v210UnpackRow10 extracts Y, Cb, Cr from V210 packed data for one row (lossless 10-bit).
// Each group of 16 bytes (4 uint32 words) produces 6 Y + 3 Cb + 3 Cr uint16 samples.
// 10-bit values are preserved as-is in uint16 containers.
func v210UnpackRow10(yOut, cbOut, crOut []uint16, v210In []byte, groups int) {
	if groups <= 0 {
		return
	}
	v210UnpackRow10Ptr(
		unsafe.SliceData(yOut),
		unsafe.SliceData(cbOut),
		unsafe.SliceData(crOut),
		unsafe.SliceData(v210In),
		groups,
	)
}

// v210PackRow10 packs Y, Cb, Cr uint16 samples into V210 format for one row (lossless 10-bit).
// Each group of 6 Y + 3 Cb + 3 Cr produces 16 bytes (4 uint32 words).
func v210PackRow10(v210Out []byte, yIn, cbIn, crIn []uint16, groups int) {
	if groups <= 0 {
		return
	}
	v210PackRow10Ptr(
		unsafe.SliceData(v210Out),
		unsafe.SliceData(yIn),
		unsafe.SliceData(cbIn),
		unsafe.SliceData(crIn),
		groups,
	)
}

func v210UnpackRow10Ptr(yOut, cbOut, crOut *uint16, v210In *byte, groups int) {
	v210S := unsafe.Slice(v210In, groups*16)
	yS := unsafe.Slice(yOut, groups*6)
	cbS := unsafe.Slice(cbOut, groups*3)
	crS := unsafe.Slice(crOut, groups*3)

	for g := 0; g < groups; g++ {
		wordOff := g * 16

		w0 := binary.LittleEndian.Uint32(v210S[wordOff:])
		w1 := binary.LittleEndian.Uint32(v210S[wordOff+4:])
		w2 := binary.LittleEndian.Uint32(v210S[wordOff+8:])
		w3 := binary.LittleEndian.Uint32(v210S[wordOff+12:])

		yBase := g * 6
		yS[yBase+0] = uint16((w0 >> 10) & 0x3FF) // Y0
		yS[yBase+1] = uint16(w1 & 0x3FF)         // Y1
		yS[yBase+2] = uint16((w1 >> 20) & 0x3FF) // Y2
		yS[yBase+3] = uint16((w2 >> 10) & 0x3FF) // Y3
		yS[yBase+4] = uint16(w3 & 0x3FF)         // Y4
		yS[yBase+5] = uint16((w3 >> 20) & 0x3FF) // Y5

		cBase := g * 3
		cbS[cBase+0] = uint16(w0 & 0x3FF)         // Cb0
		cbS[cBase+1] = uint16((w1 >> 10) & 0x3FF) // Cb2
		cbS[cBase+2] = uint16((w2 >> 20) & 0x3FF) // Cb4

		crS[cBase+0] = uint16((w0 >> 20) & 0x3FF) // Cr0
		crS[cBase+1] = uint16(w2 & 0x3FF)         // Cr2
		crS[cBase+2] = uint16((w3 >> 10) & 0x3FF) // Cr4
	}
}

func v210PackRow10Ptr(v210Out *byte, yIn, cbIn, crIn *uint16, groups int) {
	v210S := unsafe.Slice(v210Out, groups*16)
	yS := unsafe.Slice(yIn, groups*6)
	cbS := unsafe.Slice(cbIn, groups*3)
	crS := unsafe.Slice(crIn, groups*3)

	for g := 0; g < groups; g++ {
		yBase := g * 6
		cBase := g * 3

		y0 := uint32(yS[yBase+0])
		y1 := uint32(yS[yBase+1])
		y2 := uint32(yS[yBase+2])
		y3 := uint32(yS[yBase+3])
		y4 := uint32(yS[yBase+4])
		y5 := uint32(yS[yBase+5])

		cb0 := uint32(cbS[cBase+0])
		cb2 := uint32(cbS[cBase+1])
		cb4 := uint32(cbS[cBase+2])

		cr0 := uint32(crS[cBase+0])
		cr2 := uint32(crS[cBase+1])
		cr4 := uint32(crS[cBase+2])

		wordOff := g * 16
		binary.LittleEndian.PutUint32(v210S[wordOff:],
			(cb0&0x3FF)|((y0&0x3FF)<<10)|((cr0&0x3FF)<<20))
		binary.LittleEndian.PutUint32(v210S[wordOff+4:],
			(y1&0x3FF)|((cb2&0x3FF)<<10)|((y2&0x3FF)<<20))
		binary.LittleEndian.PutUint32(v210S[wordOff+8:],
			(cr2&0x3FF)|((y3&0x3FF)<<10)|((cb4&0x3FF)<<20))
		binary.LittleEndian.PutUint32(v210S[wordOff+12:],
			(y4&0x3FF)|((cr4&0x3FF)<<10)|((y5&0x3FF)<<20))
	}
}
