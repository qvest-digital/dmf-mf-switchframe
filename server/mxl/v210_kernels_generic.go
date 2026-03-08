//go:build !arm64 && !amd64

package mxl

import (
	"encoding/binary"
	"unsafe"
)

// chromaVAvg computes dst[i] = (top[i] + bot[i] + 1) >> 1 for n bytes.
// Used for vertical 4:2:2 → 4:2:0 chroma downsampling.
func chromaVAvg(dst, top, bot *byte, n int) {
	if n <= 0 {
		return
	}
	dstS := unsafe.Slice(dst, n)
	topS := unsafe.Slice(top, n)
	botS := unsafe.Slice(bot, n)
	for i := 0; i < n; i++ {
		dstS[i] = byte((uint16(topS[i]) + uint16(botS[i]) + 1) >> 1)
	}
}

// v210UnpackRow extracts Y, Cb, Cr from V210 packed data for one row.
// Each group of 16 bytes (4 uint32 words) produces 6 Y + 3 Cb + 3 Cr bytes.
// 10-bit values are right-shifted by 2 to produce 8-bit output.
func v210UnpackRow(yOut, cbOut, crOut, v210In *byte, groups int) {
	if groups <= 0 {
		return
	}
	v210S := unsafe.Slice(v210In, groups*16)
	yS := unsafe.Slice(yOut, groups*6)
	cbS := unsafe.Slice(cbOut, groups*3)
	crS := unsafe.Slice(crOut, groups*3)

	for g := 0; g < groups; g++ {
		offset := g * 16

		w0 := binary.LittleEndian.Uint32(v210S[offset:])
		w1 := binary.LittleEndian.Uint32(v210S[offset+4:])
		w2 := binary.LittleEndian.Uint32(v210S[offset+8:])
		w3 := binary.LittleEndian.Uint32(v210S[offset+12:])

		// Extract 10-bit values and convert to 8-bit
		yBase := g * 6
		yS[yBase+0] = byte((w0 >> 10 & 0x3FF) >> 2) // Y0
		yS[yBase+1] = byte((w1 & 0x3FF) >> 2)       // Y1
		yS[yBase+2] = byte((w1 >> 20 & 0x3FF) >> 2) // Y2
		yS[yBase+3] = byte((w2 >> 10 & 0x3FF) >> 2) // Y3
		yS[yBase+4] = byte((w3 & 0x3FF) >> 2)       // Y4
		yS[yBase+5] = byte((w3 >> 20 & 0x3FF) >> 2) // Y5

		cBase := g * 3
		cbS[cBase+0] = byte((w0 & 0x3FF) >> 2)       // Cb0
		cbS[cBase+1] = byte((w1 >> 10 & 0x3FF) >> 2) // Cb2
		cbS[cBase+2] = byte((w2 >> 20 & 0x3FF) >> 2) // Cb4

		crS[cBase+0] = byte((w0 >> 20 & 0x3FF) >> 2) // Cr0
		crS[cBase+1] = byte((w2 & 0x3FF) >> 2)       // Cr2
		crS[cBase+2] = byte((w3 >> 10 & 0x3FF) >> 2) // Cr4
	}
}

// v210PackRow packs Y, Cb, Cr bytes into V210 format for one row.
// Each group of 6 Y + 3 Cb + 3 Cr bytes produces 16 bytes (4 uint32 words).
// 8-bit values are left-shifted by 2 to produce 10-bit output.
func v210PackRow(v210Out, yIn, cbIn, crIn *byte, groups int) {
	if groups <= 0 {
		return
	}
	v210S := unsafe.Slice(v210Out, groups*16)
	yS := unsafe.Slice(yIn, groups*6)
	cbS := unsafe.Slice(cbIn, groups*3)
	crS := unsafe.Slice(crIn, groups*3)

	for g := 0; g < groups; g++ {
		yBase := g * 6
		cBase := g * 3

		y0 := uint32(yS[yBase+0]) << 2
		y1 := uint32(yS[yBase+1]) << 2
		y2 := uint32(yS[yBase+2]) << 2
		y3 := uint32(yS[yBase+3]) << 2
		y4 := uint32(yS[yBase+4]) << 2
		y5 := uint32(yS[yBase+5]) << 2

		cb0 := uint32(cbS[cBase+0]) << 2
		cb2 := uint32(cbS[cBase+1]) << 2
		cb4 := uint32(cbS[cBase+2]) << 2

		cr0 := uint32(crS[cBase+0]) << 2
		cr2 := uint32(crS[cBase+1]) << 2
		cr4 := uint32(crS[cBase+2]) << 2

		offset := g * 16
		binary.LittleEndian.PutUint32(v210S[offset:], (cb0&0x3FF)|((y0&0x3FF)<<10)|((cr0&0x3FF)<<20))
		binary.LittleEndian.PutUint32(v210S[offset+4:], (y1&0x3FF)|((cb2&0x3FF)<<10)|((y2&0x3FF)<<20))
		binary.LittleEndian.PutUint32(v210S[offset+8:], (cr2&0x3FF)|((y3&0x3FF)<<10)|((cb4&0x3FF)<<20))
		binary.LittleEndian.PutUint32(v210S[offset+12:], (y4&0x3FF)|((cr4&0x3FF)<<10)|((y5&0x3FF)<<20))
	}
}
