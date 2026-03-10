package graphics

import "image"

// AlphaBlendRGBA composites an RGBA overlay onto a YUV420 planar frame
// using BT.709 coefficients. The overlay is expected to be the same
// resolution as the video frame. alphaScale modulates the overlay alpha
// globally (0.0 = fully transparent, 1.0 = full overlay alpha), used
// for fade-in/out transitions.
//
// The fast path skips fully transparent pixels (alpha == 0), which is
// the common case for lower-third graphics that occupy <10% of the frame.
//
// The Y plane is processed per-row via alphaBlendRGBARowY (assembly on
// amd64/arm64, integer fallback elsewhere). Chroma planes are blended
// in Go at quarter resolution using integer fixed-point math.
//
// YUV420 layout: Y[w*h] + Cb[w/2*h/2] + Cr[w/2*h/2]
// RGBA layout:   R,G,B,A,R,G,B,A,... (w*h*4 bytes)
func AlphaBlendRGBA(yuv []byte, rgba []byte, width, height int, alphaScale float64) {
	ySize := width * height
	cbOffset := ySize
	crOffset := ySize + (width/2)*(height/2)
	halfW := width / 2

	// Convert float64 alphaScale to fixed-point 0-256 range.
	alphaScale256 := int(alphaScale*256 + 0.5)
	if alphaScale256 < 0 {
		alphaScale256 = 0
	}
	if alphaScale256 > 256 {
		alphaScale256 = 256
	}

	// Early exit: if alphaScale is zero, no blending needed.
	if alphaScale256 == 0 {
		return
	}

	// Process Y plane using per-row kernel (assembly on amd64/arm64).
	for row := 0; row < height; row++ {
		yStart := row * width
		rgbaStart := row * width * 4
		alphaBlendRGBARowY(&yuv[yStart], &rgba[rgbaStart], width, alphaScale256)
	}

	// Process Cb/Cr planes via per-row kernel (assembly on amd64/arm64).
	// Uses top-left pixel's RGBA for chroma blend (matching previous
	// "last write wins" behavior, now deterministic).
	//
	// BT.709 integer coefficients (scaled by 256):
	//   Cb = (-29*R - 99*G + 128*B + 128) >> 8 + 128
	//   Cr = (128*R - 116*G - 12*B + 128) >> 8 + 128
	for row := 0; row < height; row += 2 {
		rgbaStart := (row * width) * 4
		uvStart := (row / 2) * halfW
		alphaBlendRGBAChromaRow(&yuv[cbOffset+uvStart], &yuv[crOffset+uvStart], &rgba[rgbaStart], halfW, alphaScale256)
	}
}

// AlphaBlendRGBARect composites an RGBA overlay into a sub-region of a YUV420
// planar frame. The overlay is scaled to fit the given rect (nearest-neighbor).
// The rect is clipped to frame bounds and even-aligned for YUV420 compatibility.
//
// Parameters:
//   - yuv: destination YUV420 frame (Y[w*h] + Cb[w/2*h/2] + Cr[w/2*h/2])
//   - rgba: source RGBA overlay (overlayW * overlayH * 4 bytes)
//   - frameW, frameH: dimensions of the YUV frame
//   - overlayW, overlayH: dimensions of the RGBA overlay
//   - rect: target region within the YUV frame
//   - alphaScale: global alpha modulator (0.0-1.0)
func AlphaBlendRGBARect(yuv []byte, rgba []byte, frameW, frameH, overlayW, overlayH int,
	rect image.Rectangle, alphaScale float64) {
	_ = AlphaBlendRGBARectInto(yuv, rgba, frameW, frameH, overlayW, overlayH, rect, alphaScale, nil)
}

// AlphaBlendRGBARectInto is like AlphaBlendRGBARect but accepts a reusable
// scratch buffer to avoid per-frame allocation. If scratch is nil or too small,
// a new buffer is allocated internally. Returns the scratch buffer (possibly
// grown) so callers can retain it for subsequent calls.
func AlphaBlendRGBARectInto(yuv []byte, rgba []byte, frameW, frameH, overlayW, overlayH int,
	rect image.Rectangle, alphaScale float64, scratch []byte) []byte {

	// Validate buffer lengths.
	if len(rgba) < overlayW*overlayH*4 {
		return scratch
	}
	if len(yuv) < frameW*frameH*3/2 {
		return scratch
	}

	// Even-align the rect for YUV420 compatibility.
	rect.Min.X = rect.Min.X &^ 1
	rect.Min.Y = rect.Min.Y &^ 1
	rect.Max.X = rect.Max.X &^ 1
	rect.Max.Y = rect.Max.Y &^ 1

	// Clip to frame bounds.
	if rect.Min.X < 0 {
		rect.Min.X = 0
	}
	if rect.Min.Y < 0 {
		rect.Min.Y = 0
	}
	if rect.Max.X > frameW {
		rect.Max.X = frameW
	}
	if rect.Max.Y > frameH {
		rect.Max.Y = frameH
	}

	rectW := rect.Dx()
	rectH := rect.Dy()
	if rectW <= 0 || rectH <= 0 {
		return scratch
	}

	alphaScale256 := int(alphaScale*256 + 0.5)
	if alphaScale256 <= 0 {
		return scratch
	}
	if alphaScale256 > 256 {
		alphaScale256 = 256
	}

	// Pre-scale overlay RGBA rows to rect width, then dispatch to SIMD
	// row kernels. This avoids per-pixel nearest-neighbor lookup interleaved
	// with blend math, enabling the same SIMD path used by full-frame
	// AlphaBlendRGBA (alphaBlendRGBARowY / alphaBlendRGBAChromaRow).
	rowBufSize := rectW * 4
	if cap(scratch) >= rowBufSize {
		scratch = scratch[:rowBufSize]
	} else {
		scratch = make([]byte, rowBufSize)
	}
	rowBuf := scratch[:rowBufSize]

	// Build column lookup table: for each dest column dx, the source RGBA
	// pixel offset. Avoids recomputing nearest-neighbor per row.
	colLUT := make([]int, rectW)
	for dx := 0; dx < rectW; dx++ {
		srcCol := (dx*overlayW + rectW/2) / rectW
		if srcCol >= overlayW {
			srcCol = overlayW - 1
		}
		colLUT[dx] = srcCol * 4
	}

	ySize := frameW * frameH
	cbOffset := ySize
	crOffset := ySize + (frameW/2)*(frameH/2)
	halfFrameW := frameW / 2

	// Blend Y plane: pre-scale each row then dispatch to SIMD kernel.
	for dy := 0; dy < rectH; dy++ {
		frameRow := rect.Min.Y + dy
		srcRow := (dy*overlayH + rectH/2) / rectH
		if srcRow >= overlayH {
			srcRow = overlayH - 1
		}
		srcRowOff := srcRow * overlayW * 4

		// Nearest-neighbor scale overlay row into rowBuf.
		for dx := 0; dx < rectW; dx++ {
			srcOff := srcRowOff + colLUT[dx]
			dstOff := dx * 4
			rowBuf[dstOff] = rgba[srcOff]
			rowBuf[dstOff+1] = rgba[srcOff+1]
			rowBuf[dstOff+2] = rgba[srcOff+2]
			rowBuf[dstOff+3] = rgba[srcOff+3]
		}

		// Dispatch to SIMD Y blend kernel (same as full-frame path).
		yStart := frameRow*frameW + rect.Min.X
		alphaBlendRGBARowY(&yuv[yStart], &rowBuf[0], rectW, alphaScale256)
	}

	// Blend Cb/Cr planes: pre-scale each chroma row then dispatch to SIMD kernel.
	halfRectW := rectW / 2
	halfRectH := rectH / 2
	for dy := 0; dy < halfRectH; dy++ {
		frameChromaRow := (rect.Min.Y / 2) + dy
		srcRow := ((dy * 2) * overlayH + rectH/2) / rectH
		if srcRow >= overlayH {
			srcRow = overlayH - 1
		}
		srcRowOff := srcRow * overlayW * 4

		// Scale at chroma resolution (every other pixel).
		for dx := 0; dx < halfRectW; dx++ {
			srcCol := ((dx * 2) * overlayW + rectW/2) / rectW
			if srcCol >= overlayW {
				srcCol = overlayW - 1
			}
			srcOff := srcRowOff + srcCol*4
			dstOff := dx * 4
			rowBuf[dstOff] = rgba[srcOff]
			rowBuf[dstOff+1] = rgba[srcOff+1]
			rowBuf[dstOff+2] = rgba[srcOff+2]
			rowBuf[dstOff+3] = rgba[srcOff+3]
		}

		// Dispatch to SIMD chroma blend kernel.
		uvStart := frameChromaRow*halfFrameW + rect.Min.X/2
		alphaBlendRGBAChromaRow(&yuv[cbOffset+uvStart], &yuv[crOffset+uvStart], &rowBuf[0], halfRectW, alphaScale256)
	}

	return scratch
}
