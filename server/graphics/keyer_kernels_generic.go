//go:build !arm64 && !amd64

package graphics

import "unsafe"

// chromaKeyMaskChroma computes a chroma-resolution alpha mask from Cb/Cr planes.
//
// For each chroma pixel i (0..n-1):
//   - dCb = int(cbPlane[i]) - keyCb, dCr = int(crPlane[i]) - keyCr
//   - distSq = dCb*dCb + dCr*dCr
//   - if distSq < simThreshSq:   mask[i] = 0   (transparent)
//   - elif distSq < totalThreshSq: mask[i] = (distSq - simThreshSq) * invRange >> 16
//   - else:                       mask[i] = 255 (opaque)
//
// invRange = 255 * 65536 / (totalThreshSq - simThreshSq) precomputed by caller.
func chromaKeyMaskChroma(mask *byte, cbPlane, crPlane *byte, keyCb, keyCr int, simThreshSq, totalThreshSq int, invRange int, n int) {
	if n <= 0 {
		return
	}
	maskS := unsafe.Slice(mask, n)
	cbS := unsafe.Slice(cbPlane, n)
	crS := unsafe.Slice(crPlane, n)

	for i := 0; i < n; i++ {
		dCb := int(cbS[i]) - keyCb
		dCr := int(crS[i]) - keyCr
		distSq := dCb*dCb + dCr*dCr

		if distSq < simThreshSq {
			maskS[i] = 0
		} else if distSq < totalThreshSq {
			v := (distSq - simThreshSq) * invRange >> 16
			if v > 255 {
				v = 255
			}
			maskS[i] = byte(v)
		} else {
			maskS[i] = 255
		}
	}
}
