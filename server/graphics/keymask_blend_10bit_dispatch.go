package graphics

import (
	"encoding/binary"
	"unsafe"
)

// keyMaskBlend10RowGo is the pure Go reference implementation for 10-bit
// key mask blending. For each uint16 LE sample:
//
//	w = mask[i] + (mask[i] >> 7)   // 0-255 → 0-256
//	bg[i] = clamp10((bg[i] * (256-w) + fill[i] * w + 128) >> 8)
//
// bg is modified in-place. fill and mask are read-only.
// count is the number of uint16 samples to process.
func keyMaskBlend10RowGo(bg, fill, mask *byte, count int) {
	if count <= 0 {
		return
	}

	bgS := unsafe.Slice(bg, count*2)
	fillS := unsafe.Slice(fill, count*2)
	maskS := unsafe.Slice(mask, count)

	for i := 0; i < count; i++ {
		w := int(maskS[i])
		w += w >> 7 // map 0-255 to 0-256
		if w == 0 {
			continue
		}
		inv := 256 - w
		off := i * 2
		bgVal := int(binary.LittleEndian.Uint16(bgS[off:]))
		fillVal := int(binary.LittleEndian.Uint16(fillS[off:]))
		result := (bgVal*inv + fillVal*w + 128) >> 8
		if result > 1023 {
			result = 1023
		}
		binary.LittleEndian.PutUint16(bgS[off:], uint16(result))
	}
}
