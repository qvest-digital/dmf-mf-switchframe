//go:build amd64

package graphics

import "unsafe"

// spillSuppressChroma applies spill suppression on Cb/Cr chroma planes.
// AMD64 scalar Go fallback (no SIMD for this kernel on amd64).
//
// See spill_kernels_generic.go for algorithm documentation.
func spillSuppressChroma(cbPlane, crPlane *byte, keyCb, keyCr, spillSuppress, invSpillDistSq, replaceCb, replaceCr float32, n int) {
	if n <= 0 {
		return
	}
	cbS := unsafe.Slice(cbPlane, n)
	crS := unsafe.Slice(crPlane, n)

	for i := 0; i < n; i++ {
		cb := float32(cbS[i])
		cr := float32(crS[i])
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
				} else if newCb > 255 {
					newCb = 255
				}
				if newCr < 0 {
					newCr = 0
				} else if newCr > 255 {
					newCr = 255
				}
				cbS[i] = byte(newCb)
				crS[i] = byte(newCr)
			}
		}
	}
}
