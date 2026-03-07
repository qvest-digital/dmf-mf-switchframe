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

		scaleBilinearRow(&dst[dy*dstW], &src[iy*srcW], &src[iy1*srcW], srcW, dstW, &xCoords[0], fy)
	}
}
