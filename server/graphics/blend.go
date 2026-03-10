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

	// Validate buffer lengths.
	if len(rgba) < overlayW*overlayH*4 {
		return
	}
	if len(yuv) < frameW*frameH*3/2 {
		return
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
		return
	}

	alphaScale256 := int(alphaScale*256 + 0.5)
	if alphaScale256 <= 0 {
		return
	}
	if alphaScale256 > 256 {
		alphaScale256 = 256
	}

	ySize := frameW * frameH
	cbOffset := ySize
	crOffset := ySize + (frameW/2)*(frameH/2)
	halfFrameW := frameW / 2

	// Blend Y plane: iterate over rows in the rect region.
	for dy := 0; dy < rectH; dy++ {
		frameRow := rect.Min.Y + dy
		// Map dy → overlay row via centered nearest-neighbor.
		srcRow := (dy*overlayH + rectH/2) / rectH
		if srcRow >= overlayH {
			srcRow = overlayH - 1
		}

		yDstStart := frameRow*frameW + rect.Min.X
		for dx := 0; dx < rectW; dx++ {
			srcCol := (dx*overlayW + rectW/2) / rectW
			if srcCol >= overlayW {
				srcCol = overlayW - 1
			}
			rgbaIdx := (srcRow*overlayW + srcCol) * 4

			a := int(rgba[rgbaIdx+3])
			if a == 0 {
				continue
			}
			a = (a * alphaScale256) >> 8
			if a == 0 {
				continue
			}

			r := int(rgba[rgbaIdx])
			g := int(rgba[rgbaIdx+1])
			b := int(rgba[rgbaIdx+2])

			// BT.709 Y = (54*R + 183*G + 18*B + 128) >> 8
			overlayY := (54*r + 183*g + 18*b + 128) >> 8

			yIdx := yDstStart + dx
			yuv[yIdx] = byte((int(yuv[yIdx])*(256-a) + overlayY*a + 128) >> 8)
		}
	}

	// Blend Cb/Cr planes: iterate over chroma rows in the rect region.
	halfRectW := rectW / 2
	halfRectH := rectH / 2
	for dy := 0; dy < halfRectH; dy++ {
		frameChromaRow := (rect.Min.Y/2) + dy
		// Map to overlay pixel (top-left of 2x2 block) via centered nearest-neighbor.
		srcRow := ((dy*2)*overlayH + rectH/2) / rectH
		if srcRow >= overlayH {
			srcRow = overlayH - 1
		}

		uvDstStart := frameChromaRow*halfFrameW + rect.Min.X/2
		for dx := 0; dx < halfRectW; dx++ {
			srcCol := ((dx*2)*overlayW + rectW/2) / rectW
			if srcCol >= overlayW {
				srcCol = overlayW - 1
			}
			rgbaIdx := (srcRow*overlayW + srcCol) * 4

			a := int(rgba[rgbaIdx+3])
			if a == 0 {
				continue
			}
			a = (a * alphaScale256) >> 8
			if a == 0 {
				continue
			}

			r := int(rgba[rgbaIdx])
			g := int(rgba[rgbaIdx+1])
			b := int(rgba[rgbaIdx+2])

			// BT.709 Cb = (-29*R - 99*G + 128*B + 128) >> 8 + 128
			overlayCb := ((-29*r - 99*g + 128*b + 128) >> 8) + 128
			// BT.709 Cr = (128*R - 116*G - 12*B + 128) >> 8 + 128
			overlayCr := ((128*r - 116*g - 12*b + 128) >> 8) + 128

			// Clamp to [0, 255] to prevent byte overflow.
			// Pure blue (0,0,255) produces overlayCb=256; pure red (255,0,0) produces overlayCr=256.
			if overlayCb > 255 {
				overlayCb = 255
			}
			if overlayCb < 0 {
				overlayCb = 0
			}
			if overlayCr > 255 {
				overlayCr = 255
			}
			if overlayCr < 0 {
				overlayCr = 0
			}

			cbIdx := cbOffset + uvDstStart + dx
			crIdx := crOffset + uvDstStart + dx
			yuv[cbIdx] = byte((int(yuv[cbIdx])*(256-a) + overlayCb*a + 128) >> 8)
			yuv[crIdx] = byte((int(yuv[crIdx])*(256-a) + overlayCr*a + 128) >> 8)
		}
	}
}
