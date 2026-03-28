package graphics

import (
	"encoding/binary"
	"unsafe"
)

// spillSuppress10RowGo is the pure Go reference for 10-bit spill suppression.
// Operates on uint16 LE Cb/Cr samples. For each sample, computes squared distance
// from keyCb/keyCr in float32. If within spill zone, lerps toward replacement color.
func spillSuppress10RowGo(cbPlane, crPlane *byte, count int,
	keyCb, keyCr, spillSuppress, invSpillDistSq, replaceCb, replaceCr float32) {
	if count <= 0 {
		return
	}

	cbS := unsafe.Slice(cbPlane, count*2)
	crS := unsafe.Slice(crPlane, count*2)

	for i := 0; i < count; i++ {
		cb := float32(binary.LittleEndian.Uint16(cbS[i*2:]))
		cr := float32(binary.LittleEndian.Uint16(crS[i*2:]))

		dCb := cb - keyCb
		dCr := cr - keyCr
		distSq := dCb*dCb + dCr*dCr
		ratio := distSq * invSpillDistSq

		if ratio < 1.0 {
			spillAmount := spillSuppress * (1.0 - ratio)
			if spillAmount > 0 {
				newCb := cb + (replaceCb-cb)*spillAmount
				newCr := cr + (replaceCr-cr)*spillAmount
				if newCb < 0 {
					newCb = 0
				} else if newCb > 1023 {
					newCb = 1023
				}
				if newCr < 0 {
					newCr = 0
				} else if newCr > 1023 {
					newCr = 1023
				}
				binary.LittleEndian.PutUint16(cbS[i*2:], uint16(newCb))
				binary.LittleEndian.PutUint16(crS[i*2:], uint16(newCr))
			}
		}
	}
}
