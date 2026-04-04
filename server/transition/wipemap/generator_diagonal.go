package wipemap

import "math"

// GenDiagonal creates a gradient along a line at the given angle in degrees.
// 45° gives top-left→bottom-right, 135° gives top-right→bottom-left, etc.
func GenDiagonal(angleDeg float64) GeneratorFunc {
	return func(w, h int) []byte {
		buf := make([]byte, w*h)
		theta := angleDeg * math.Pi / 180.0
		cosT := math.Cos(theta)
		sinT := math.Sin(theta)

		// Find the projection range over all corners to normalize to 0-255.
		corners := [][2]float64{{0, 0}, {float64(w - 1), 0}, {0, float64(h - 1)}, {float64(w - 1), float64(h - 1)}}
		minP := math.Inf(1)
		maxP := math.Inf(-1)
		for _, c := range corners {
			p := c[0]*cosT + c[1]*sinT
			if p < minP {
				minP = p
			}
			if p > maxP {
				maxP = p
			}
		}
		span := maxP - minP
		if span < 1e-9 {
			span = 1
		}

		for y := 0; y < h; y++ {
			fy := float64(y)
			rowOff := y * w
			for x := 0; x < w; x++ {
				p := float64(x)*cosT + fy*sinT
				v := (p - minP) / span * 255.0
				buf[rowOff+x] = clampByte(v)
			}
		}
		return buf
	}
}

// GenBarnDoor creates two edges meeting at the center.
// axis "h" = horizontal split (left/right edges), "v" = vertical split (top/bottom edges).
// reverse inverts the gradient (close instead of open).
func GenBarnDoor(axis string, reverse bool) GeneratorFunc {
	return func(w, h int) []byte {
		buf := make([]byte, w*h)
		if axis == "v" {
			halfH := float64(h) / 2.0
			if halfH < 1 {
				halfH = 1
			}
			for y := 0; y < h; y++ {
				dist := math.Abs(float64(y) - halfH)
				v := 255.0 - dist/halfH*255.0
				if reverse {
					v = 255.0 - v
				}
				b := clampByte(v)
				row := buf[y*w : y*w+w]
				for i := range row {
					row[i] = b
				}
			}
		} else {
			halfW := float64(w) / 2.0
			if halfW < 1 {
				halfW = 1
			}
			for y := 0; y < h; y++ {
				rowOff := y * w
				for x := 0; x < w; x++ {
					dist := math.Abs(float64(x) - halfW)
					v := 255.0 - dist/halfW*255.0
					if reverse {
						v = 255.0 - v
					}
					buf[rowOff+x] = clampByte(v)
				}
			}
		}
		return buf
	}
}

// GenLetterbox creates dual horizontal bars from top and bottom meeting at center.
// reverse inverts the gradient.
func GenLetterbox(reverse bool) GeneratorFunc {
	return func(w, h int) []byte {
		buf := make([]byte, w*h)
		halfH := float64(h) / 2.0
		if halfH < 1 {
			halfH = 1
		}
		for y := 0; y < h; y++ {
			dist := math.Abs(float64(y) - halfH)
			// Distance from center: 0 at center, halfH at edges
			// We want edges low (revealed first), center high
			v := (1.0 - dist/halfH) * 255.0
			if reverse {
				v = 255.0 - v
			}
			b := clampByte(v)
			row := buf[y*w : y*w+w]
			for i := range row {
				row[i] = b
			}
		}
		return buf
	}
}

// GenCurtain creates a barn door with cosine-curved edges instead of straight.
// reverse inverts the gradient.
func GenCurtain(reverse bool) GeneratorFunc {
	return func(w, h int) []byte {
		buf := make([]byte, w*h)
		halfW := float64(w) / 2.0
		if halfW < 1 {
			halfW = 1
		}
		for y := 0; y < h; y++ {
			// Cosine modulation: edge position varies with y
			yNorm := float64(y) / float64(max(h-1, 1))
			edgeMod := 0.15 * math.Cos(yNorm*math.Pi*2)
			rowOff := y * w
			for x := 0; x < w; x++ {
				xNorm := float64(x) / float64(max(w-1, 1))
				dist := math.Abs(xNorm - 0.5)
				// Apply cosine curve to the edge
				adjusted := dist - edgeMod*0.5
				v := (1.0 - adjusted/0.5) * 255.0
				if reverse {
					v = 255.0 - v
				}
				buf[rowOff+x] = clampByte(v)
			}
		}
		return buf
	}
}

func clampByte(v float64) byte {
	if v < 0 {
		return 0
	}
	if v > 255 {
		return 255
	}
	return byte(v)
}
