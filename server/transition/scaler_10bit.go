package transition

import (
	"encoding/binary"
	"fmt"
)

// ScaleYUV422P10 scales a YUV422P10LE frame using bilinear interpolation.
// src is srcW*srcH*4 bytes, dst is dstW*dstH*4 bytes.
//
// Buffer layout (YUV422P10LE planar):
//   - Y plane:  srcW * srcH samples × 2 bytes (offset 0)
//   - Cb plane: srcW/2 * srcH samples × 2 bytes
//   - Cr plane: srcW/2 * srcH samples × 2 bytes
//   - Total:    srcW * srcH * 4 bytes
//
// The scaler uses 16.16 fixed-point arithmetic for sub-pixel coordinate
// mapping (same approach as the 8-bit ScaleYUV420 scaler) but operates
// on uint16 samples with 32-bit intermediate math and clamps to [0, 1023].
//
// Chroma planes are full height (4:2:2), unlike 4:2:0 which halves vertically.
func ScaleYUV422P10(src []byte, srcW, srcH int, dst []byte, dstW, dstH int) error {
	// Validate dimensions
	if srcW <= 0 || srcH <= 0 || dstW <= 0 || dstH <= 0 {
		return fmt.Errorf("invalid dimensions: src=%dx%d dst=%dx%d", srcW, srcH, dstW, dstH)
	}
	if srcW%2 != 0 {
		return fmt.Errorf("src width must be even for YUV422: got %d", srcW)
	}
	if dstW%2 != 0 {
		return fmt.Errorf("dst width must be even for YUV422: got %d", dstW)
	}

	srcFrameSize := srcW * srcH * 4
	dstFrameSize := dstW * dstH * 4
	if len(src) < srcFrameSize {
		return fmt.Errorf("src buffer too small: need %d, got %d", srcFrameSize, len(src))
	}
	if len(dst) < dstFrameSize {
		return fmt.Errorf("dst buffer too small: need %d, got %d", dstFrameSize, len(dst))
	}

	// Y plane offsets and sizes (full resolution)
	srcYSize := srcW * srcH * 2
	dstYSize := dstW * dstH * 2

	// Chroma plane offsets and sizes (half-width, full height for 4:2:2)
	srcChromaW := srcW / 2
	srcChromaPlaneSize := srcChromaW * srcH * 2
	dstChromaW := dstW / 2
	dstChromaPlaneSize := dstChromaW * dstH * 2

	// Y plane: scale at full resolution
	scalePlane10(
		src[:srcYSize], srcW, srcH,
		dst[:dstYSize], dstW, dstH,
	)

	// Cb plane: half-width, full height (4:2:2)
	srcCbOff := srcYSize
	dstCbOff := dstYSize
	scalePlane10(
		src[srcCbOff:srcCbOff+srcChromaPlaneSize], srcChromaW, srcH,
		dst[dstCbOff:dstCbOff+dstChromaPlaneSize], dstChromaW, dstH,
	)

	// Cr plane: half-width, full height (4:2:2)
	srcCrOff := srcCbOff + srcChromaPlaneSize
	dstCrOff := dstCbOff + dstChromaPlaneSize
	scalePlane10(
		src[srcCrOff:srcCrOff+srcChromaPlaneSize], srcChromaW, srcH,
		dst[dstCrOff:dstCrOff+dstChromaPlaneSize], dstChromaW, dstH,
	)

	return nil
}

// scalePlane10 performs bilinear interpolation on a single 10-bit plane
// using 16.16 fixed-point arithmetic. Samples are stored as uint16 LE,
// with 32-bit intermediates for interpolation math. Output is clamped
// to [0, 1023].
func scalePlane10(src []byte, srcW, srcH int, dst []byte, dstW, dstH int) {
	// Fast path: same dimensions, just copy
	if srcW == dstW && srcH == dstH {
		copy(dst, src)
		return
	}

	// Edge case: source is 1x1, fill destination with the single sample
	if srcW == 1 && srcH == 1 {
		val := binary.LittleEndian.Uint16(src[:2])
		for i := 0; i < dstW*dstH; i++ {
			binary.LittleEndian.PutUint16(dst[i*2:], val)
		}
		return
	}

	// Precompute source X coordinates in 16.16 fixed-point for each
	// destination column to avoid per-pixel division.
	dstWm1 := int64(dstW - 1)
	dstHm1 := int64(dstH - 1)
	srcWm1 := int64(srcW - 1)
	srcHm1 := int64(srcH - 1)

	xCoords := make([]int64, dstW)
	if dstWm1 > 0 {
		for dx := 0; dx < dstW; dx++ {
			xCoords[dx] = (int64(dx) * srcWm1 << 16) / dstWm1
		}
	}

	for dy := 0; dy < dstH; dy++ {
		// Compute source Y in 16.16 fixed-point
		var srcY int64
		if dstHm1 > 0 {
			srcY = (int64(dy) * srcHm1 << 16) / dstHm1
		}

		iy := int(srcY >> 16)
		fy := int(srcY & 0xFFFF)
		invFy := 65536 - fy

		// Clamp iy+1 to stay in bounds
		iy1 := iy + 1
		if iy1 >= srcH {
			iy1 = srcH - 1
		}

		row0Off := iy * srcW * 2
		row1Off := iy1 * srcW * 2
		dstRowOff := dy * dstW * 2

		for dx := 0; dx < dstW; dx++ {
			srcX := xCoords[dx]
			ix := int(srcX >> 16)
			fx := int(srcX & 0xFFFF)
			invFx := 65536 - fx

			ix1 := ix + 1
			if ix1 >= srcW {
				ix1 = srcW - 1
			}

			// Read 4 neighboring uint16 samples
			p00 := int(binary.LittleEndian.Uint16(src[row0Off+ix*2:]))
			p10 := int(binary.LittleEndian.Uint16(src[row0Off+ix1*2:]))
			p01 := int(binary.LittleEndian.Uint16(src[row1Off+ix*2:]))
			p11 := int(binary.LittleEndian.Uint16(src[row1Off+ix1*2:]))

			// Bilinear interpolation with 16.16 fixed-point
			top := (p00*invFx + p10*fx + 32768) >> 16
			bot := (p01*invFx + p11*fx + 32768) >> 16
			val := (top*invFy + bot*fy + 32768) >> 16

			// Clamp to 10-bit range
			if val < 0 {
				val = 0
			} else if val > 1023 {
				val = 1023
			}

			binary.LittleEndian.PutUint16(dst[dstRowOff+dx*2:], uint16(val))
		}
	}
}
