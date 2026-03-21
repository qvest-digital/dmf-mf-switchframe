//go:build !arm64 && !amd64

package stmap

import "unsafe"

// warpBilinearRow is the generic Go fallback for platforms without assembly
// kernels. Performs bilinear interpolation using precomputed 16.16 fixed-point
// LUT coordinates. Functionally identical to the assembly versions.
func warpBilinearRow(dst, src *byte, srcW, srcH, n int, lutX, lutY *int64) {
	if n <= 0 {
		return
	}
	dstS := unsafe.Slice(dst, n)
	srcS := unsafe.Slice(src, srcW*srcH)
	lx := unsafe.Slice(lutX, n)
	ly := unsafe.Slice(lutY, n)

	maxX := int64(srcW-1) << 16
	maxY := int64(srcH-1) << 16
	lastCol := srcW - 1
	lastRow := srcH - 1

	for i := 0; i < n; i++ {
		sx := lx[i]
		sy := ly[i]

		// Clamp to valid source range.
		if sx < 0 {
			sx = 0
		} else if sx > maxX {
			sx = maxX
		}
		if sy < 0 {
			sy = 0
		} else if sy > maxY {
			sy = maxY
		}

		ix := int(sx >> 16)
		iy := int(sy >> 16)
		fx := int(sx & 0xFFFF)
		fy := int(sy & 0xFFFF)

		ix1 := ix + 1
		if ix1 > lastCol {
			ix1 = lastCol
		}
		iy1 := iy + 1
		if iy1 > lastRow {
			iy1 = lastRow
		}

		p00 := int(srcS[iy*srcW+ix])
		p10 := int(srcS[iy*srcW+ix1])
		p01 := int(srcS[iy1*srcW+ix])
		p11 := int(srcS[iy1*srcW+ix1])

		invFx := 65536 - fx
		invFy := 65536 - fy
		top := (p00*invFx + p10*fx + 32768) >> 16
		bot := (p01*invFx + p11*fx + 32768) >> 16
		val := (top*invFy + bot*fy + 32768) >> 16

		if val > 255 {
			val = 255
		}
		dstS[i] = byte(val)
	}
}
