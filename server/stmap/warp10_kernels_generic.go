//go:build !arm64 && !amd64

package stmap

import (
	"encoding/binary"
	"unsafe"
)

// warpBilinearRow10 is the generic Go fallback for 10-bit bilinear warp
// on platforms without assembly kernels. Implements the same algorithm as
// warpPlane10 but with the assembly-compatible pointer signature.
func warpBilinearRow10(dst, src *byte, srcW, srcH, n int, lutX, lutY *int32) {
	if n <= 0 {
		return
	}
	dstBytes := unsafe.Slice(dst, n*2)
	srcBytes := unsafe.Slice(src, srcW*srcH*2)
	lx := unsafe.Slice(lutX, n)
	ly := unsafe.Slice(lutY, n)

	maxX := int32((srcW - 1) << 16)
	maxY := int32((srcH - 1) << 16)
	lastCol := srcW - 1
	lastRow := srcH - 1

	for i := 0; i < n; i++ {
		sx := lx[i]
		sy := ly[i]

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
		fx := int32(sx & 0xFFFF)
		fy := int32(sy & 0xFFFF)

		ix1 := ix + 1
		if ix1 > lastCol {
			ix1 = lastCol
		}
		iy1 := iy + 1
		if iy1 > lastRow {
			iy1 = lastRow
		}

		// Read 4 neighboring uint16 LE samples.
		p00 := int32(binary.LittleEndian.Uint16(srcBytes[(iy*srcW+ix)*2:]))
		p10 := int32(binary.LittleEndian.Uint16(srcBytes[(iy*srcW+ix1)*2:]))
		p01 := int32(binary.LittleEndian.Uint16(srcBytes[(iy1*srcW+ix)*2:]))
		p11 := int32(binary.LittleEndian.Uint16(srcBytes[(iy1*srcW+ix1)*2:]))

		invFx := 65536 - fx
		top := p00*invFx + p10*fx
		bot := p01*invFx + p11*fx
		invFy := int64(65536) - int64(fy)
		val := (int64(top)*invFy + int64(bot)*int64(fy) + (1 << 31)) >> 32

		if val < 0 {
			val = 0
		} else if val > 1023 {
			val = 1023
		}

		binary.LittleEndian.PutUint16(dstBytes[i*2:], uint16(val))
	}
}
