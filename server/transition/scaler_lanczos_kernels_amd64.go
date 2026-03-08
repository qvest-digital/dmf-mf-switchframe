//go:build amd64

package transition

import "unsafe"

// AMD64 Lanczos-3 scaler kernels.
// Pure Go implementations with bounds-check elimination via unsafe.Slice.
// AVX2+FMA assembly can be added when tested on native x86-64 hardware
// (Rosetta 2 has subtle VEX-prefix behavioral differences that prevent
// reliable testing of AVX2 assembly under emulation).

// lanczosHorizRow applies precomputed horizontal Lanczos weights to one source row.
func lanczosHorizRow(dst []float32, src []byte, offsets []int32, weights []float32, maxTaps int) {
	dstW := len(dst)
	if dstW == 0 {
		return
	}
	srcLen := len(src)
	dstS := unsafe.Slice(&dst[0], dstW)
	srcS := unsafe.Slice(&src[0], srcLen)
	offS := unsafe.Slice(&offsets[0], dstW)

	for d := 0; d < dstW; d++ {
		off := int(offS[d])
		wBase := d * maxTaps
		var acc float32
		for t := 0; t < maxTaps; t++ {
			idx := off + t
			if idx >= 0 && idx < srcLen {
				acc += weights[wBase+t] * float32(srcS[idx])
			}
		}
		dstS[d] = acc
	}
}

// lanczosVertRow applies precomputed vertical Lanczos weights to intermediate rows.
func lanczosVertRow(dst []byte, temp []float32, tempStride int, startRow int32, weights []float32, maxTaps int) {
	dstW := len(dst)
	if dstW == 0 {
		return
	}
	dstS := unsafe.Slice(&dst[0], dstW)

	type rowInfo struct {
		base   int
		weight float32
	}
	var rowsBuf [16]rowInfo
	rows := rowsBuf[:0]
	for t := 0; t < maxTaps; t++ {
		w := weights[t]
		if w == 0 {
			continue
		}
		row := int(startRow) + t
		rows = append(rows, rowInfo{base: row * tempStride, weight: w})
	}

	nRows := len(rows)
	if nRows == 0 {
		for d := range dstS {
			dstS[d] = 0
		}
		return
	}

	for d := 0; d < dstW; d++ {
		var acc float32
		for i := 0; i < nRows; i++ {
			acc += rows[i].weight * temp[rows[i].base+d]
		}
		val := int32(acc + 0.5)
		if val < 0 {
			val = 0
		} else if val > 255 {
			val = 255
		}
		dstS[d] = byte(val)
	}
}
