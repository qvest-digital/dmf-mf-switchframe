package layout

import "image"

// ComposePIPOpaque copies a scaled PIP source YUV420 buffer into a sub-region
// of the destination frame. All three planes are copied at their native resolutions.
// rect.Min must be even-aligned for YUV420 correctness.
func ComposePIPOpaque(dst []byte, dstW, dstH int, src []byte, srcW, srcH int, rect image.Rectangle) {
	// Y plane: row-by-row copy
	for y := 0; y < srcH; y++ {
		dstOff := (rect.Min.Y+y)*dstW + rect.Min.X
		srcOff := y * srcW
		copy(dst[dstOff:dstOff+srcW], src[srcOff:srcOff+srcW])
	}

	// Chroma planes
	dstYSize := dstW * dstH
	srcYSize := srcW * srcH
	chromaDstW := dstW / 2
	chromaSrcW := srcW / 2
	chromaSrcH := srcH / 2
	chromaX := rect.Min.X / 2
	chromaY := rect.Min.Y / 2

	for plane := 0; plane < 2; plane++ {
		dstBase := dstYSize + plane*(chromaDstW*(dstH/2))
		srcBase := srcYSize + plane*(chromaSrcW*chromaSrcH)
		for y := 0; y < chromaSrcH; y++ {
			dstOff := dstBase + (chromaY+y)*chromaDstW + chromaX
			srcOff := srcBase + y*chromaSrcW
			copy(dst[dstOff:dstOff+chromaSrcW], src[srcOff:srcOff+chromaSrcW])
		}
	}
}

// DrawBorderYUV draws a solid border around a rectangle in the YUV420 frame.
// borderColor is {Y, Cb, Cr}. thickness is in luma pixels.
// The border is drawn OUTSIDE the rectangle (expanding outward).
func DrawBorderYUV(dst []byte, dstW, dstH int, rect image.Rectangle, borderColor [3]byte, thickness int) {
	if thickness <= 0 {
		return
	}
	ySize := dstW * dstH
	chromaW := dstW / 2
	chromaH := dstH / 2

	// Expand rect outward by thickness
	outer := image.Rect(
		max(rect.Min.X-thickness, 0),
		max(rect.Min.Y-thickness, 0),
		min(rect.Max.X+thickness, dstW),
		min(rect.Max.Y+thickness, dstH),
	)

	// Fill Y plane border pixels (outer minus inner)
	for y := outer.Min.Y; y < outer.Max.Y; y++ {
		for x := outer.Min.X; x < outer.Max.X; x++ {
			if y >= rect.Min.Y && y < rect.Max.Y && x >= rect.Min.X && x < rect.Max.X {
				continue // skip interior
			}
			dst[y*dstW+x] = borderColor[0]
		}
	}

	// Fill chroma planes at half resolution
	chromaOuter := image.Rect(outer.Min.X/2, outer.Min.Y/2, outer.Max.X/2, outer.Max.Y/2)
	chromaInner := image.Rect(rect.Min.X/2, rect.Min.Y/2, rect.Max.X/2, rect.Max.Y/2)

	for y := chromaOuter.Min.Y; y < chromaOuter.Max.Y; y++ {
		for x := chromaOuter.Min.X; x < chromaOuter.Max.X; x++ {
			if y >= chromaInner.Min.Y && y < chromaInner.Max.Y && x >= chromaInner.Min.X && x < chromaInner.Max.X {
				continue
			}
			off := y*chromaW + x
			if off < chromaW*chromaH {
				dst[ySize+off] = borderColor[1]                 // Cb
				dst[ySize+chromaW*chromaH+off] = borderColor[2] // Cr
			}
		}
	}
}

// BlendRegion alpha-blends src onto dst for a rectangular region.
// alpha is 0.0 (fully transparent) to 1.0 (fully opaque).
// Used for dissolve transitions on PIP slots.
func BlendRegion(dst []byte, dstW, dstH int, src []byte, srcW, srcH int, rect image.Rectangle, alpha float64) {
	if alpha <= 0 {
		return
	}
	if alpha >= 1.0 {
		ComposePIPOpaque(dst, dstW, dstH, src, srcW, srcH, rect)
		return
	}

	a := uint16(alpha * 256)
	inv := 256 - a

	// Y plane
	for y := 0; y < srcH; y++ {
		for x := 0; x < srcW; x++ {
			di := (rect.Min.Y+y)*dstW + rect.Min.X + x
			si := y*srcW + x
			dst[di] = byte((uint16(dst[di])*inv + uint16(src[si])*a) >> 8)
		}
	}

	// Chroma planes
	dstYSize := dstW * dstH
	srcYSize := srcW * srcH
	chromaDstW := dstW / 2
	chromaSrcW := srcW / 2
	chromaSrcH := srcH / 2
	chromaX := rect.Min.X / 2
	chromaY := rect.Min.Y / 2

	for plane := 0; plane < 2; plane++ {
		dstBase := dstYSize + plane*(chromaDstW*(dstH/2))
		srcBase := srcYSize + plane*(chromaSrcW*chromaSrcH)
		for y := 0; y < chromaSrcH; y++ {
			for x := 0; x < chromaSrcW; x++ {
				di := dstBase + (chromaY+y)*chromaDstW + chromaX + x
				si := srcBase + y*chromaSrcW + x
				dst[di] = byte((uint16(dst[di])*inv + uint16(src[si])*a) >> 8)
			}
		}
	}
}
