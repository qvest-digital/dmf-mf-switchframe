package graphics

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

	// Process Cb/Cr planes in Go using integer math (quarter resolution).
	// Uses top-left pixel's RGBA for chroma blend (matching previous
	// "last write wins" behavior, now deterministic).
	//
	// BT.709 integer coefficients (scaled by 256):
	//   Cb = (-29*R - 99*G + 128*B + 128) >> 8 + 128
	//   Cr = (128*R - 116*G - 12*B + 128) >> 8 + 128
	for row := 0; row < height; row += 2 {
		for col := 0; col < width; col += 2 {
			rgbaIdx := (row*width + col) * 4
			A := int(rgba[rgbaIdx+3])
			A += A >> 7 // map 0-255 to 0-256
			a256 := (A * alphaScale256) >> 8
			if a256 == 0 {
				continue
			}
			R := int(rgba[rgbaIdx])
			G := int(rgba[rgbaIdx+1])
			B := int(rgba[rgbaIdx+2])

			overlayCb := ((-29*R - 99*G + 128*B + 128) >> 8) + 128
			overlayCr := ((128*R - 116*G - 12*B + 128) >> 8) + 128

			inv := 256 - a256
			uvIdx := (row/2)*halfW + (col / 2)
			yuv[cbOffset+uvIdx] = byte(clampInt((int(yuv[cbOffset+uvIdx])*inv+overlayCb*a256+128)>>8, 0, 255))
			yuv[crOffset+uvIdx] = byte(clampInt((int(yuv[crOffset+uvIdx])*inv+overlayCr*a256+128)>>8, 0, 255))
		}
	}
}

// clampInt clamps an integer value to [min, max].
func clampInt(v, min, max int) int {
	if v < min {
		return min
	}
	if v > max {
		return max
	}
	return v
}
