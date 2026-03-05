package transition

// ScaleYUV420 scales a YUV420 planar frame from (srcW x srcH) to (dstW x dstH)
// using bilinear interpolation. Both src and dst must be sized for their
// respective resolutions: w*h*3/2 bytes (Y[w*h] + Cb[w/2*h/2] + Cr[w/2*h/2]).
//
// When source and destination dimensions match, a plain copy is performed
// (zero interpolation overhead).
//
// The scaler uses 16.16 fixed-point arithmetic for sub-pixel coordinate
// mapping, which avoids floating-point per-pixel while maintaining accuracy
// for broadcast resolutions up to 4K.
func ScaleYUV420(src []byte, srcW, srcH int, dst []byte, dstW, dstH int) {
	srcYSize := srcW * srcH
	srcUVW := srcW / 2
	srcUVH := srcH / 2
	srcUVSize := srcUVW * srcUVH

	dstYSize := dstW * dstH
	dstUVW := dstW / 2
	dstUVH := dstH / 2
	dstUVSize := dstUVW * dstUVH

	// Y plane: full resolution
	scalePlane(
		src[:srcYSize], srcW, srcH,
		dst[:dstYSize], dstW, dstH,
	)

	// Cb plane: half resolution
	srcCbOff := srcYSize
	dstCbOff := dstYSize
	scalePlane(
		src[srcCbOff:srcCbOff+srcUVSize], srcUVW, srcUVH,
		dst[dstCbOff:dstCbOff+dstUVSize], dstUVW, dstUVH,
	)

	// Cr plane: half resolution
	srcCrOff := srcYSize + srcUVSize
	dstCrOff := dstYSize + dstUVSize
	scalePlane(
		src[srcCrOff:srcCrOff+srcUVSize], srcUVW, srcUVH,
		dst[dstCrOff:dstCrOff+dstUVSize], dstUVW, dstUVH,
	)
}

// scalePlane performs bilinear interpolation on a single plane using
// 16.16 fixed-point arithmetic. For each destination pixel, it maps back
// to the source coordinate, samples the four surrounding source pixels,
// and interpolates horizontally then vertically.
//
// Fixed-point format: upper 16 bits = integer part, lower 16 bits = fraction.
// This gives sub-pixel precision of 1/65536 which is more than sufficient
// for broadcast resolution scaling.
func scalePlane(src []byte, srcW, srcH int, dst []byte, dstW, dstH int) {
	// Fast path: same dimensions, just copy
	if srcW == dstW && srcH == dstH {
		copy(dst, src)
		return
	}

	// Edge case: source is 1x1, fill destination with the single pixel value
	if srcW == 1 && srcH == 1 {
		val := src[0]
		for i := range dst {
			dst[i] = val
		}
		return
	}

	// Precompute source X coordinates in 16.16 fixed-point for each
	// destination column. Computing per-pixel via:
	//   srcX = (dx * (srcW-1) << 16) / (dstW-1)
	// avoids step-accumulation rounding error and guarantees exact
	// corner mapping. The table is computed once and reused for every row.
	dstWm1 := int64(dstW - 1)
	dstHm1 := int64(dstH - 1)
	srcWm1 := int64(srcW - 1)
	srcHm1 := int64(srcH - 1)

	// xCoords[dx] holds the 16.16 fixed-point source X for destination col dx.
	xCoords := make([]int64, dstW)
	if dstWm1 > 0 {
		for dx := 0; dx < dstW; dx++ {
			xCoords[dx] = (int64(dx) * srcWm1 << 16) / dstWm1
		}
	}

	for dy := 0; dy < dstH; dy++ {
		// Compute source Y in 16.16 fixed-point directly per-row
		var srcY int64
		if dstHm1 > 0 {
			srcY = (int64(dy) * srcHm1 << 16) / dstHm1
		}

		iy := int(srcY >> 16)
		fy := int(srcY & 0xFFFF)

		// Clamp iy to ensure iy+1 is within bounds
		iy1 := iy + 1
		if iy1 >= srcH {
			iy1 = srcH - 1
		}

		row0 := iy * srcW
		row1 := iy1 * srcW
		dstRow := dy * dstW
		invFy := 65536 - fy

		for dx := 0; dx < dstW; dx++ {
			srcX := xCoords[dx]
			ix := int(srcX >> 16)
			fx := int(srcX & 0xFFFF)

			// Clamp ix to ensure ix+1 is within bounds
			ix1 := ix + 1
			if ix1 >= srcW {
				ix1 = srcW - 1
			}

			// Sample 4 neighbors
			p00 := int(src[row0+ix])
			p10 := int(src[row0+ix1])
			p01 := int(src[row1+ix])
			p11 := int(src[row1+ix1])

			// Bilinear interpolation using fixed-point:
			// Horizontal lerp for top row:    top = p00*(1-fx) + p10*fx
			// Horizontal lerp for bottom row: bot = p01*(1-fx) + p11*fx
			// Vertical lerp:                  out = top*(1-fy) + bot*fy
			//
			// In 16.16 fixed-point, (1-fx) = (65536-fx), and we shift
			// right by 16 after each multiply to stay in range.
			invFx := 65536 - fx
			top := (p00*invFx + p10*fx) >> 16
			bot := (p01*invFx + p11*fx) >> 16

			val := (top*invFy + bot*fy) >> 16

			// Clamp to 0-255 (guards against fixed-point rounding edge cases)
			if val < 0 {
				val = 0
			} else if val > 255 {
				val = 255
			}

			dst[dstRow+dx] = byte(val)
		}
	}
}
