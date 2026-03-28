package switcher

import "encoding/binary"

// mcfiWarp10 warps a YUV422P10LE frame along motion vectors.
// For each block, the source is offset by the motion vector scaled by t,
// then bilinear-sampled from the source. This is the Go fallback path;
// SIMD acceleration can be added later.
//
// dst and src are YUV422P10LE frames (width*height*4 bytes).
// mvX and mvY are per-block motion vectors (cols*rows elements where
// cols=width/frcMEBlockSize, rows=height/frcMEBlockSize).
// t is the interpolation factor (0.0 = src position, 1.0 = full MV offset).
func mcfiWarp10(dst, src []byte, width, height int, mvX, mvY []float32, t float64) {
	bs := frcMEBlockSize
	cols := width / bs
	rows := height / bs

	ySize := width * height
	chromaW := width / 2
	chromaH := height // 4:2:2: full height chroma
	chromaSize := chromaW * chromaH

	// Warp Y plane
	warp10Plane(dst[:ySize*2], src[:ySize*2], width, height,
		mvX, mvY, cols, rows, bs, t, 1)

	// Warp Cb plane (half width, full height for 4:2:2)
	cbOff := ySize * 2
	warp10Plane(dst[cbOff:cbOff+chromaSize*2], src[cbOff:cbOff+chromaSize*2],
		chromaW, chromaH, mvX, mvY, cols, rows, bs, t, 2)

	// Warp Cr plane
	crOff := cbOff + chromaSize*2
	warp10Plane(dst[crOff:crOff+chromaSize*2], src[crOff:crOff+chromaSize*2],
		chromaW, chromaH, mvX, mvY, cols, rows, bs, t, 2)
}

// warp10Plane warps a single plane of 10-bit samples using motion vectors.
// chromaScale is 1 for luma, 2 for chroma (MVs are in luma coordinates).
func warp10Plane(dst, src []byte, planeW, planeH int,
	mvX, mvY []float32, mvCols, mvRows, blockSize int,
	t float64, chromaScale int,
) {
	mvScale := 1.0 / float64(chromaScale)

	for py := 0; py < planeH; py++ {
		// Map to luma coordinates for MV lookup
		lumaY := float64(py * chromaScale)
		// Find block row
		by := int(lumaY) / blockSize
		if by >= mvRows {
			by = mvRows - 1
		}

		for px := 0; px < planeW; px++ {
			lumaX := float64(px * chromaScale)
			bx := int(lumaX) / blockSize
			if bx >= mvCols {
				bx = mvCols - 1
			}

			idx := by*mvCols + bx
			// Displace source position by MV * t
			srcX := float64(px) + float64(mvX[idx])*t*mvScale
			srcY := float64(py) + float64(mvY[idx])*t*mvScale

			val := bilinearSample10(src, planeW, planeH, srcX, srcY)
			off := (py*planeW + px) * 2
			binary.LittleEndian.PutUint16(dst[off:], val)
		}
	}
}

// bilinearSample10 samples a 10-bit plane at sub-pixel position (x, y)
// using bilinear interpolation. Coordinates are clamped to plane boundaries.
func bilinearSample10(src []byte, width, height int, x, y float64) uint16 {
	// Clamp to valid range
	if x < 0 {
		x = 0
	}
	maxX := float64(width - 1)
	if x > maxX {
		x = maxX
	}
	if y < 0 {
		y = 0
	}
	maxY := float64(height - 1)
	if y > maxY {
		y = maxY
	}

	x0 := int(x)
	y0 := int(y)
	x1 := x0 + 1
	y1 := y0 + 1
	if x1 >= width {
		x1 = width - 1
	}
	if y1 >= height {
		y1 = height - 1
	}

	fx := x - float64(x0)
	fy := y - float64(y0)

	// Read four corner samples (10-bit, little-endian uint16)
	off00 := (y0*width + x0) * 2
	off10 := (y0*width + x1) * 2
	off01 := (y1*width + x0) * 2
	off11 := (y1*width + x1) * 2

	v00 := float64(binary.LittleEndian.Uint16(src[off00:]))
	v10 := float64(binary.LittleEndian.Uint16(src[off10:]))
	v01 := float64(binary.LittleEndian.Uint16(src[off01:]))
	v11 := float64(binary.LittleEndian.Uint16(src[off11:]))

	v := v00*(1-fx)*(1-fy) + v10*fx*(1-fy) + v01*(1-fx)*fy + v11*fx*fy

	result := uint16(v + 0.5)
	if result > 1023 {
		result = 1023
	}
	return result
}
