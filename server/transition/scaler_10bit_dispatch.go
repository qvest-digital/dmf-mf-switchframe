package transition

import (
	"encoding/binary"
	"unsafe"
)

// scaleBilinear10Dispatch is the SIMD-accelerated 10-bit bilinear scaler.
//
// The original 2-pass approach (horizontal scalar + vertical SIMD blend) allocated
// intermediate row buffers per plane, adding overhead that exceeded the SIMD benefit
// of the vertical pass. The fix: use the Go reference's direct bilinear approach
// (scalePlane10) which processes each output pixel independently without intermediate
// buffers. The SIMD blendRows10 kernel remains available for callers that need
// standalone row blending.
func scaleBilinear10Dispatch(src []byte, srcW, srcH int, dst []byte, dstW, dstH int) error {
	// Delegate to the Go reference which uses direct bilinear interpolation
	// without intermediate buffer overhead. This is faster than the 2-pass
	// approach because the horizontal scatter/gather can't be vectorized
	// effectively, and the per-plane buffer allocations dominate.
	return ScaleYUV422P10(src, srcW, srcH, dst, dstW, dstH)
}

// blendRows10Go is the pure Go reference for blending two uint16 LE rows
// with a 16.16 fixed-point fractional weight:
//
//	dst[i] = clamp10((top[i] * (65536-fy) + bot[i] * fy + 32768) >> 16)
//
// Used as the reference for testing SIMD implementations.
func blendRows10Go(dst, topRow, botRow *byte, fy, count int) {
	if count <= 0 {
		return
	}

	invFy := 65536 - fy

	dstS := unsafe.Slice(dst, count*2)
	topS := unsafe.Slice(topRow, count*2)
	botS := unsafe.Slice(botRow, count*2)

	for i := 0; i < count; i++ {
		off := i * 2
		top := int(binary.LittleEndian.Uint16(topS[off:]))
		bot := int(binary.LittleEndian.Uint16(botS[off:]))
		val := (top*invFy + bot*fy + 32768) >> 16
		if val < 0 {
			val = 0
		} else if val > 1023 {
			val = 1023
		}
		binary.LittleEndian.PutUint16(dstS[off:], uint16(val))
	}
}
