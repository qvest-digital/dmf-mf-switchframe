//go:build !arm64 && !amd64

package transition

import "unsafe"

// scaleBilinearRow performs bilinear interpolation for one destination row.
// row0 and row1 are source rows (iy and iy+1), srcW is their width.
// xCoords[dx] holds the 16.16 fixed-point source X for each destination column.
// fy is the 16.16 fractional Y component (0-65535).
func scaleBilinearRow(dst, row0, row1 *byte, srcW, dstW int, xCoords *int64, fy int) {
	if dstW <= 0 {
		return
	}
	dstS := unsafe.Slice(dst, dstW)
	r0 := unsafe.Slice(row0, srcW)
	r1 := unsafe.Slice(row1, srcW)
	xc := unsafe.Slice(xCoords, dstW)
	invFy := 65536 - fy

	for dx := 0; dx < dstW; dx++ {
		srcX := xc[dx]
		ix := int(srcX >> 16)
		fx := int(srcX & 0xFFFF)

		ix1 := ix + 1
		if ix1 >= srcW {
			ix1 = srcW - 1
		}

		p00 := int(r0[ix])
		p10 := int(r0[ix1])
		p01 := int(r1[ix])
		p11 := int(r1[ix1])

		invFx := 65536 - fx
		top := (p00*invFx + p10*fx) >> 16
		bot := (p01*invFx + p11*fx) >> 16
		val := (top*invFy + bot*fy) >> 16

		if val < 0 {
			val = 0
		} else if val > 255 {
			val = 255
		}

		dstS[dx] = byte(val)
	}
}
