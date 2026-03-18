package transition

// BoxShrink2xYUV420 downscales a YUV420 frame by exactly 2x in both dimensions
// using box-average (2×2 block average for Y, 2×2 for chroma which is already
// at half-res). This is ~4x faster than bilinear for the same pixel count
// because each source pixel is read exactly once (no scatter-gather).
//
// srcW and srcH must be even. dstW = srcW/2, dstH = srcH/2.
func BoxShrink2xYUV420(src []byte, srcW, srcH int, dst []byte, dstW, dstH int) {
	srcYSize := srcW * srcH
	srcUVW := srcW / 2
	srcUVH := srcH / 2
	srcUVSize := srcUVW * srcUVH

	dstYSize := dstW * dstH
	dstUVW := dstW / 2
	dstUVH := dstH / 2
	dstUVSize := dstUVW * dstUVH

	// Y plane: average 2×2 blocks
	boxShrink2xPlane(src[:srcYSize], srcW, srcH, dst[:dstYSize], dstW, dstH)

	// Cb plane: average 2×2 blocks at half resolution
	srcCb := src[srcYSize : srcYSize+srcUVSize]
	dstCb := dst[dstYSize : dstYSize+dstUVSize]
	boxShrink2xPlane(srcCb, srcUVW, srcUVH, dstCb, dstUVW, dstUVH)

	// Cr plane
	srcCr := src[srcYSize+srcUVSize : srcYSize+2*srcUVSize]
	dstCr := dst[dstYSize+dstUVSize : dstYSize+2*dstUVSize]
	boxShrink2xPlane(srcCr, srcUVW, srcUVH, dstCr, dstUVW, dstUVH)
}

// boxShrink2xPlane averages 2×2 blocks to produce a half-size plane.
// Tight inner loop with no bounds checks in the hot path.
func boxShrink2xPlane(src []byte, srcW, srcH int, dst []byte, dstW, dstH int) {
	for dy := 0; dy < dstH; dy++ {
		sy := dy * 2
		srcRow0 := sy * srcW
		srcRow1 := srcRow0 + srcW
		dstRow := dy * dstW
		for dx := 0; dx < dstW; dx++ {
			sx := dx * 2
			// Average of 4 pixels: (a + b + c + d + 2) >> 2
			sum := int(src[srcRow0+sx]) + int(src[srcRow0+sx+1]) +
				int(src[srcRow1+sx]) + int(src[srcRow1+sx+1])
			dst[dstRow+dx] = byte((sum + 2) >> 2)
		}
	}
}
