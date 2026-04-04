package wipemap

import "math"

// GenBoxIris creates a rectangular iris from center (cx, cy in 0-1 normalized coordinates).
// reverse inverts the gradient direction.
func GenBoxIris(cx, cy float64, reverse bool) GeneratorFunc {
	return func(w, h int) []byte {
		buf := make([]byte, w*h)
		centerX := cx * float64(w)
		centerY := cy * float64(h)
		halfW := float64(w) / 2.0
		halfH := float64(h) / 2.0
		if halfW < 1 {
			halfW = 1
		}
		if halfH < 1 {
			halfH = 1
		}

		for y := 0; y < h; y++ {
			dy := math.Abs(float64(y)-centerY) / halfH
			rowOff := y * w
			for x := 0; x < w; x++ {
				dx := math.Abs(float64(x)-centerX) / halfW
				d := math.Max(dx, dy)
				v := d * 255.0
				if reverse {
					v = 255.0 - v
				}
				buf[rowOff+x] = clampByte(v)
			}
		}
		return buf
	}
}

// GenCircleIris creates a circular/oval iris from center (cx, cy in 0-1 coords).
// aspectX and aspectY control the ellipse shape (1.0, 1.0 = circle).
// reverse inverts the gradient direction.
func GenCircleIris(cx, cy, aspectX, aspectY float64, reverse bool) GeneratorFunc {
	return func(w, h int) []byte {
		buf := make([]byte, w*h)
		centerX := cx * float64(w)
		centerY := cy * float64(h)

		// Compute max radius considering aspect ratios.
		maxR := 0.0
		corners := [][2]float64{{0, 0}, {float64(w), 0}, {0, float64(h)}, {float64(w), float64(h)}}
		for _, c := range corners {
			dx := (c[0] - centerX) * aspectX
			dy := (c[1] - centerY) * aspectY
			r := math.Sqrt(dx*dx + dy*dy)
			if r > maxR {
				maxR = r
			}
		}
		if maxR < 1 {
			maxR = 1
		}

		for y := 0; y < h; y++ {
			dy := (float64(y) - centerY) * aspectY
			rowOff := y * w
			for x := 0; x < w; x++ {
				dx := (float64(x) - centerX) * aspectX
				r := math.Sqrt(dx*dx + dy*dy)
				v := r / maxR * 255.0
				if reverse {
					v = 255.0 - v
				}
				buf[rowOff+x] = clampByte(v)
			}
		}
		return buf
	}
}

// GenDiamondIris creates a diamond-shaped iris from center (cx, cy in 0-1 coords).
// Uses Manhattan distance: |dx| + |dy|.
// reverse inverts the gradient direction.
func GenDiamondIris(cx, cy float64, reverse bool) GeneratorFunc {
	return func(w, h int) []byte {
		buf := make([]byte, w*h)
		centerX := cx * float64(w)
		centerY := cy * float64(h)

		// Max Manhattan distance from center to any corner.
		maxDist := 0.0
		corners := [][2]float64{{0, 0}, {float64(w), 0}, {0, float64(h)}, {float64(w), float64(h)}}
		for _, c := range corners {
			d := math.Abs(c[0]-centerX) + math.Abs(c[1]-centerY)
			if d > maxDist {
				maxDist = d
			}
		}
		if maxDist < 1 {
			maxDist = 1
		}

		for y := 0; y < h; y++ {
			dy := math.Abs(float64(y) - centerY)
			rowOff := y * w
			for x := 0; x < w; x++ {
				dx := math.Abs(float64(x) - centerX)
				d := (dx + dy) / maxDist * 255.0
				if reverse {
					d = 255.0 - d
				}
				buf[rowOff+x] = clampByte(d)
			}
		}
		return buf
	}
}

// GenPinhole creates a pinhole iris: center is revealed first (high value), edges last.
// This is effectively a reversed circle iris with center at max.
func GenPinhole(cx, cy float64) GeneratorFunc {
	return GenCircleIris(cx, cy, 1.0, 1.0, true)
}
