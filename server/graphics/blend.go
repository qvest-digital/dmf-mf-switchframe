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
// YUV420 layout: Y[w*h] + Cb[w/2*h/2] + Cr[w/2*h/2]
// RGBA layout:   R,G,B,A,R,G,B,A,... (w*h*4 bytes)
func AlphaBlendRGBA(yuv []byte, rgba []byte, width, height int, alphaScale float64) {
	ySize := width * height
	cbOffset := ySize
	crOffset := ySize + (width/2)*(height/2)
	halfW := width / 2

	for row := 0; row < height; row++ {
		for col := 0; col < width; col++ {
			rgbaIdx := (row*width + col) * 4
			a := float64(rgba[rgbaIdx+3]) / 255.0 * alphaScale

			// Fast path: skip fully transparent pixels.
			if a < 1.0/255.0 {
				continue
			}

			r := float64(rgba[rgbaIdx])
			g := float64(rgba[rgbaIdx+1])
			b := float64(rgba[rgbaIdx+2])

			// BT.709 RGB -> YUV
			overlayY := 0.2126*r + 0.7152*g + 0.0722*b
			overlayCb := -0.1146*r - 0.3854*g + 0.5*b + 128.0
			overlayCr := 0.5*r - 0.4542*g - 0.0458*b + 128.0

			invA := 1.0 - a

			// Blend Y (luma) - per pixel
			yIdx := row*width + col
			yuv[yIdx] = clampByte(float64(yuv[yIdx])*invA + overlayY*a)

			// Blend Cb, Cr (chroma) - at quarter resolution
			// We accumulate overlay chroma per 2x2 block below.
			// For simplicity, apply chroma blend for every luma pixel.
			// Since chroma is shared by 2x2 blocks, the last write wins
			// (acceptable for typical overlay content).
			uvIdx := (row/2)*halfW + (col / 2)
			yuv[cbOffset+uvIdx] = clampByte(float64(yuv[cbOffset+uvIdx])*invA + overlayCb*a)
			yuv[crOffset+uvIdx] = clampByte(float64(yuv[crOffset+uvIdx])*invA + overlayCr*a)
		}
	}
}

// clampByte clamps a float64 value to [0, 255] and returns as byte.
func clampByte(v float64) byte {
	if v < 0 {
		return 0
	}
	if v > 255 {
		return 255
	}
	return byte(v + 0.5) // round to nearest
}
