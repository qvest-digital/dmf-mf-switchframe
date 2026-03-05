package transition

// NOTE: These functions are currently unused in production — blending operates directly in YUV420
// space (see blend.go). Retained for potential future use (e.g., WebGPU integration, debug tooling).

// YUV420ToRGB converts YUV420 planar (full-range) to interleaved RGB using BT.709 coefficients.
// yuv layout: Y[w*h] + U[w/2 * h/2] + V[w/2 * h/2]
// rgb layout: R,G,B,R,G,B,... (w*h*3 bytes)
func YUV420ToRGB(yuv []byte, width, height int, rgb []byte) {
	ySize := width * height
	uOffset := ySize
	vOffset := ySize + (width/2)*(height/2)
	halfW := width / 2

	for row := 0; row < height; row++ {
		for col := 0; col < width; col++ {
			yIdx := row*width + col
			uvIdx := (row/2)*halfW + (col / 2)

			y := float64(yuv[yIdx])
			cb := float64(yuv[uOffset+uvIdx]) - 128.0
			cr := float64(yuv[vOffset+uvIdx]) - 128.0

			r := y + 1.5748*cr
			g := y - 0.1873*cb - 0.4681*cr
			b := y + 1.8556*cb

			rgbIdx := yIdx * 3
			rgb[rgbIdx] = clampByte(r)
			rgb[rgbIdx+1] = clampByte(g)
			rgb[rgbIdx+2] = clampByte(b)
		}
	}
}

// RGBToYUV420 converts interleaved RGB to YUV420 planar (full-range) using BT.709 coefficients.
func RGBToYUV420(rgb []byte, width, height int, yuv []byte) {
	ySize := width * height
	uOffset := ySize
	vOffset := ySize + (width/2)*(height/2)
	halfW := width / 2

	// Compute Y for every pixel
	for row := 0; row < height; row++ {
		for col := 0; col < width; col++ {
			idx := (row*width + col) * 3
			r := float64(rgb[idx])
			g := float64(rgb[idx+1])
			b := float64(rgb[idx+2])
			yuv[row*width+col] = clampByte(0.2126*r + 0.7152*g + 0.0722*b)
		}
	}

	// Compute U, V by averaging 2x2 pixel blocks
	for row := 0; row < height; row += 2 {
		for col := 0; col < width; col += 2 {
			var sumR, sumG, sumB float64
			for dy := 0; dy < 2; dy++ {
				for dx := 0; dx < 2; dx++ {
					idx := ((row+dy)*width + (col + dx)) * 3
					sumR += float64(rgb[idx])
					sumG += float64(rgb[idx+1])
					sumB += float64(rgb[idx+2])
				}
			}
			avgR := sumR / 4
			avgG := sumG / 4
			avgB := sumB / 4

			y := 0.2126*avgR + 0.7152*avgG + 0.0722*avgB
			uvIdx := (row/2)*halfW + col/2
			yuv[uOffset+uvIdx] = clampByte((avgB-y)/1.8556 + 128)
			yuv[vOffset+uvIdx] = clampByte((avgR-y)/1.5748 + 128)
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
