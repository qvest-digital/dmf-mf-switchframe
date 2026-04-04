package wipemap

import "math"

// GenZigzag creates a linear ramp modulated by a sawtooth wave.
// axis: "h" = horizontal ramp + vertical teeth, "v" = vertical ramp + horizontal teeth,
// "diag" = diagonal ramp.
// teeth is the number of zigzag teeth.
func GenZigzag(axis string, teeth int) GeneratorFunc {
	if teeth < 1 {
		teeth = 1
	}
	return func(w, h int) []byte {
		buf := make([]byte, w*h)
		fTeeth := float64(teeth)

		for y := 0; y < h; y++ {
			yNorm := float64(y) / float64(max(h-1, 1))
			rowOff := y * w
			for x := 0; x < w; x++ {
				xNorm := float64(x) / float64(max(w-1, 1))

				var ramp, perp float64
				switch axis {
				case "v":
					ramp = yNorm
					perp = xNorm
				case "diag":
					ramp = (xNorm + yNorm) / 2.0
					perp = (xNorm - yNorm + 1.0) / 2.0
				default: // "h"
					ramp = xNorm
					perp = yNorm
				}

				// Sawtooth offset: triangular wave along perpendicular axis.
				saw := math.Mod(perp*fTeeth, 1.0)
				if saw > 0.5 {
					saw = 1.0 - saw
				}
				offset := saw * 0.3 // 30% amplitude modulation

				v := (ramp + offset - 0.15) / 1.0
				buf[rowOff+x] = clampByte(v * 255.0)
			}
		}
		return buf
	}
}

// GenSpiral creates a spiral pattern from center.
// clockwise controls the spiral direction.
func GenSpiral(clockwise bool) GeneratorFunc {
	return func(w, h int) []byte {
		buf := make([]byte, w*h)
		cx := float64(w) / 2.0
		cy := float64(h) / 2.0
		maxR := math.Sqrt(cx*cx + cy*cy)
		if maxR < 1 {
			maxR = 1
		}

		for y := 0; y < h; y++ {
			dy := float64(y) - cy
			rowOff := y * w
			for x := 0; x < w; x++ {
				dx := float64(x) - cx
				r := math.Sqrt(dx*dx+dy*dy) / maxR
				angle := math.Atan2(dy, dx)
				if angle < 0 {
					angle += 2 * math.Pi
				}
				normAngle := angle / (2 * math.Pi)

				if !clockwise {
					normAngle = 1.0 - normAngle
				}

				// Combine angle and radius for spiral effect.
				v := math.Mod(normAngle+r, 1.0) * 255.0
				buf[rowOff+x] = clampByte(v)
			}
		}
		return buf
	}
}

// GenWave creates a linear ramp with sinusoidal edge modulation.
// axis: "h" = horizontal, "v" = vertical.
// cycles is the number of sine wave cycles.
func GenWave(axis string, cycles int) GeneratorFunc {
	if cycles < 1 {
		cycles = 1
	}
	return func(w, h int) []byte {
		buf := make([]byte, w*h)
		amplitude := 0.15 // 15% sine amplitude
		fCycles := float64(cycles)

		for y := 0; y < h; y++ {
			yNorm := float64(y) / float64(max(h-1, 1))
			rowOff := y * w
			for x := 0; x < w; x++ {
				xNorm := float64(x) / float64(max(w-1, 1))

				var ramp, perp float64
				if axis == "v" {
					ramp = yNorm
					perp = xNorm
				} else {
					ramp = xNorm
					perp = yNorm
				}

				offset := amplitude * math.Sin(perp*fCycles*2*math.Pi)
				v := ramp + offset
				buf[rowOff+x] = clampByte(v * 255.0)
			}
		}
		return buf
	}
}

// GenBinoculars creates two overlapping circles (binocular view).
// Left eye at 35% x, right eye at 65% x, both at 50% y.
// Gradient = min distance to either circle center.
func GenBinoculars() GeneratorFunc {
	return func(w, h int) []byte {
		buf := make([]byte, w*h)
		fw := float64(w)
		fh := float64(h)

		leftCX := fw * 0.35
		rightCX := fw * 0.65
		eyeCY := fh * 0.5
		radius := math.Min(fw, fh) * 0.35

		maxDist := math.Max(fw, fh) * 0.5
		if maxDist < 1 {
			maxDist = 1
		}

		for y := 0; y < h; y++ {
			fy := float64(y)
			rowOff := y * w
			for x := 0; x < w; x++ {
				fx := float64(x)

				// Distance to left eye center.
				dlx := fx - leftCX
				dly := fy - eyeCY
				distLeft := math.Sqrt(dlx*dlx+dly*dly) - radius

				// Distance to right eye center.
				drx := fx - rightCX
				dry := fy - eyeCY
				distRight := math.Sqrt(drx*drx+dry*dry) - radius

				// Take minimum (union of both circles).
				dist := math.Min(distLeft, distRight)
				v := (dist/maxDist + 0.5) * 255.0
				buf[rowOff+x] = clampByte(v)
			}
		}
		return buf
	}
}
