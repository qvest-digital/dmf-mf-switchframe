package wipemap

import "math"

// GenClockWipe creates a radial sweep (clock wipe).
// startAngleDeg is the starting angle in degrees (270 = 12 o'clock).
// hands is the number of simultaneous sweeps (1 = standard, 2 = two-handed).
// clockwise controls sweep direction.
func GenClockWipe(startAngleDeg float64, hands int, clockwise bool) GeneratorFunc {
	if hands < 1 {
		hands = 1
	}
	return func(w, h int) []byte {
		buf := make([]byte, w*h)
		cx := float64(w) / 2.0
		cy := float64(h) / 2.0
		startRad := startAngleDeg * math.Pi / 180.0

		for y := 0; y < h; y++ {
			dy := float64(y) - cy
			rowOff := y * w
			for x := 0; x < w; x++ {
				dx := float64(x) - cx
				angle := math.Atan2(dy, dx) // -π to π

				// Rotate so startAngle maps to 0.
				a := angle - startRad
				if !clockwise {
					a = -a
				}

				// Normalize to [0, 2π).
				a = math.Mod(a, 2*math.Pi)
				if a < 0 {
					a += 2 * math.Pi
				}

				// For multiple hands, multiply the angle range.
				a = math.Mod(a*float64(hands), 2*math.Pi)

				v := a / (2 * math.Pi) * 255.0
				buf[rowOff+x] = clampByte(v)
			}
		}
		return buf
	}
}

// GenVClockWipe creates a V-shaped clock wipe: two radial sweeps from the start
// angle going outward in both directions simultaneously.
func GenVClockWipe(startAngleDeg float64) GeneratorFunc {
	return func(w, h int) []byte {
		buf := make([]byte, w*h)
		cx := float64(w) / 2.0
		cy := float64(h) / 2.0
		startRad := startAngleDeg * math.Pi / 180.0

		for y := 0; y < h; y++ {
			dy := float64(y) - cy
			rowOff := y * w
			for x := 0; x < w; x++ {
				dx := float64(x) - cx
				angle := math.Atan2(dy, dx)

				// Angular distance from start angle.
				diff := angle - startRad
				diff = math.Mod(diff, 2*math.Pi)
				if diff < 0 {
					diff += 2 * math.Pi
				}
				// V-shape: distance from start in either direction.
				// min(diff, 2π - diff) gives the shorter arc.
				if diff > math.Pi {
					diff = 2*math.Pi - diff
				}

				v := diff / math.Pi * 255.0
				buf[rowOff+x] = clampByte(v)
			}
		}
		return buf
	}
}

// GenRadialBurst creates pie-slice segments, each with a sequential gradient value.
func GenRadialBurst(segments int) GeneratorFunc {
	if segments < 1 {
		segments = 1
	}
	return func(w, h int) []byte {
		buf := make([]byte, w*h)
		cx := float64(w) / 2.0
		cy := float64(h) / 2.0
		segAngle := 2 * math.Pi / float64(segments)

		for y := 0; y < h; y++ {
			dy := float64(y) - cy
			rowOff := y * w
			for x := 0; x < w; x++ {
				dx := float64(x) - cx
				angle := math.Atan2(dy, dx)
				if angle < 0 {
					angle += 2 * math.Pi
				}
				seg := int(angle / segAngle)
				if seg >= segments {
					seg = segments - 1
				}
				v := float64(seg) / float64(segments) * 255.0
				buf[rowOff+x] = clampByte(v)
			}
		}
		return buf
	}
}
