package graphics

import (
	"encoding/binary"
	"unsafe"
)

// chromaKey10Dispatch performs 10-bit chroma keying using SIMD-accelerated row processing.
// Bit-identical to ChromaKey10 for all inputs.
func chromaKey10Dispatch(yuv []byte, w, h int, targetCb, targetCr uint16, tolerance, softness int) []byte {
	pixelCount := w * h
	if pixelCount == 0 {
		return nil
	}
	if w%2 != 0 {
		return nil
	}

	ySize := w * h
	chromaW := w / 2
	chromaSize := chromaW * h

	needBytes := ySize*2 + chromaSize*2*2
	if len(yuv) < needBytes {
		return nil
	}

	mask := make([]byte, pixelCount)

	cbByteOff := ySize * 2
	crByteOff := cbByteOff + chromaSize*2

	tCb := int(targetCb)
	tCr := int(targetCr)

	simDistSq := tolerance * tolerance
	totalDist := tolerance + softness
	totalDistSq := totalDist * totalDist

	rangeSq := totalDistSq - simDistSq
	invRange := 0
	if rangeSq > 0 {
		invRange = 255 * 65536 / rangeSq
	}

	// Process at chroma resolution using SIMD row kernel, then expand to luma.
	chromaMask := make([]byte, chromaSize)

	for py := 0; py < h; py++ {
		cbOff := cbByteOff + py*chromaW*2
		crOff := crByteOff + py*chromaW*2
		maskOff := py * chromaW
		chromaKey10Row(&chromaMask[maskOff], &yuv[cbOff], &yuv[crOff], chromaW,
			tCb, tCr, simDistSq, totalDistSq, invRange)
	}

	// Expand chroma mask to luma resolution (2 horizontal pixels per chroma sample)
	for py := 0; py < h; py++ {
		for cx := 0; cx < chromaW; cx++ {
			keyVal := chromaMask[py*chromaW+cx]
			lx := cx * 2
			lumaIdx := py*w + lx
			mask[lumaIdx] = keyVal
			if lx+1 < w {
				mask[lumaIdx+1] = keyVal
			}
		}
	}

	return mask
}

// chromaKey10RowGo is the pure Go reference for 10-bit chroma key row processing.
// For each chroma sample, computes squared distance from target and generates a mask byte.
// Uses integer squared-distance space for the soft edge (no sqrt).
func chromaKey10RowGo(mask *byte, cb *byte, cr *byte, count int, targetCb, targetCr int, simDistSq, totalDistSq, invRange int) {
	if count <= 0 {
		return
	}

	maskS := unsafe.Slice(mask, count)
	cbS := unsafe.Slice(cb, count*2)
	crS := unsafe.Slice(cr, count*2)

	rangeSq := totalDistSq - simDistSq

	for i := 0; i < count; i++ {
		cbVal := int(binary.LittleEndian.Uint16(cbS[i*2:]))
		crVal := int(binary.LittleEndian.Uint16(crS[i*2:]))

		dCb := cbVal - targetCb
		dCr := crVal - targetCr
		distSq := dCb*dCb + dCr*dCr

		if distSq <= simDistSq {
			maskS[i] = 255
		} else if distSq < totalDistSq && rangeSq > 0 {
			v := 255 - (distSq-simDistSq)*invRange>>16
			if v < 0 {
				v = 0
			}
			maskS[i] = byte(v)
		} else {
			maskS[i] = 0
		}
	}
}
