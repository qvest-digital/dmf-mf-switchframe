//go:build !arm64 && !amd64

package v210asm

import (
	"encoding/binary"
	"unsafe"
)

// conv10to8 converts a 10-bit value to 8-bit with rounding and clamping.
// (val+2)>>2 rounds to nearest, but 10-bit values 1022-1023 produce 256
// which overflows byte. Clamping to 255 prevents wrap-to-zero.
func conv10to8(v uint32) byte {
	r := (v + 2) >> 2
	if r > 255 {
		return 255
	}
	return byte(r)
}

// ChromaVAvg computes dst[i] = (top[i] + bot[i] + 1) >> 1 for n bytes.
// Used for vertical 4:2:2 → 4:2:0 chroma downsampling.
func ChromaVAvg(dst, top, bot *byte, n int) {
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

// V210UnpackRow extracts Y, Cb, Cr from V210 packed data for one row.
// Each group of 16 bytes (4 uint32 words) produces 6 Y + 3 Cb + 3 Cr bytes.
// 10-bit values are converted to 8-bit with rounding: (val + 2) >> 2.
//
// NOTE: SIMD kernels (amd64/arm64) also need this rounding fix applied.
func V210UnpackRow(yOut, cbOut, crOut, v210In *byte, groups int) {
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

		// Extract 10-bit values and convert to 8-bit with rounding and clamping.
		yBase := g * 6
		yS[yBase+0] = conv10to8(w0 >> 10 & 0x3FF) // Y0
		yS[yBase+1] = conv10to8(w1 & 0x3FF)       // Y1
		yS[yBase+2] = conv10to8(w1 >> 20 & 0x3FF) // Y2
		yS[yBase+3] = conv10to8(w2 >> 10 & 0x3FF) // Y3
		yS[yBase+4] = conv10to8(w3 & 0x3FF)       // Y4
		yS[yBase+5] = conv10to8(w3 >> 20 & 0x3FF) // Y5

		cBase := g * 3
		cbS[cBase+0] = conv10to8(w0 & 0x3FF)       // Cb0
		cbS[cBase+1] = conv10to8(w1 >> 10 & 0x3FF) // Cb2
		cbS[cBase+2] = conv10to8(w2 >> 20 & 0x3FF) // Cb4

		crS[cBase+0] = conv10to8(w0 >> 20 & 0x3FF) // Cr0
		crS[cBase+1] = conv10to8(w2 & 0x3FF)       // Cr2
		crS[cBase+2] = conv10to8(w3 >> 10 & 0x3FF) // Cr4
	}
}

// V210PackRow packs Y, Cb, Cr bytes into V210 format for one row.
// Each group of 6 Y + 3 Cb + 3 Cr bytes produces 16 bytes (4 uint32 words).
// 8-bit values are expanded to 10-bit using bit-replication: (val<<2)|(val>>6).
// This maps the full 8-bit range [0,255] to the full 10-bit range [0,1023].
//
// NOTE: SIMD kernels (amd64/arm64) also need this bit-replication fix applied.
func V210PackRow(v210Out, yIn, cbIn, crIn *byte, groups int) {
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

		y0 := uint32(yS[yBase+0])<<2 | uint32(yS[yBase+0])>>6
		y1 := uint32(yS[yBase+1])<<2 | uint32(yS[yBase+1])>>6
		y2 := uint32(yS[yBase+2])<<2 | uint32(yS[yBase+2])>>6
		y3 := uint32(yS[yBase+3])<<2 | uint32(yS[yBase+3])>>6
		y4 := uint32(yS[yBase+4])<<2 | uint32(yS[yBase+4])>>6
		y5 := uint32(yS[yBase+5])<<2 | uint32(yS[yBase+5])>>6

		cb0 := uint32(cbS[cBase+0])<<2 | uint32(cbS[cBase+0])>>6
		cb2 := uint32(cbS[cBase+1])<<2 | uint32(cbS[cBase+1])>>6
		cb4 := uint32(cbS[cBase+2])<<2 | uint32(cbS[cBase+2])>>6

		cr0 := uint32(crS[cBase+0])<<2 | uint32(crS[cBase+0])>>6
		cr2 := uint32(crS[cBase+1])<<2 | uint32(crS[cBase+1])>>6
		cr4 := uint32(crS[cBase+2])<<2 | uint32(crS[cBase+2])>>6

		offset := g * 16
		binary.LittleEndian.PutUint32(v210S[offset:], (cb0&0x3FF)|((y0&0x3FF)<<10)|((cr0&0x3FF)<<20))
		binary.LittleEndian.PutUint32(v210S[offset+4:], (y1&0x3FF)|((cb2&0x3FF)<<10)|((y2&0x3FF)<<20))
		binary.LittleEndian.PutUint32(v210S[offset+8:], (cr2&0x3FF)|((y3&0x3FF)<<10)|((cb4&0x3FF)<<20))
		binary.LittleEndian.PutUint32(v210S[offset+12:], (y4&0x3FF)|((cr4&0x3FF)<<10)|((y5&0x3FF)<<20))
	}
}
