package dve

import "math"

// ============================================================================
// B2 Extended DVE Transitions
// ============================================================================

// PageTurnTransition creates a 3D page-turn effect.
// The program "turns" like a book page, rotating around the left edge.
// Preview is revealed behind the turning page.
func PageTurnTransition() DVETransitionFunc {
	return func(pos float64, fw, fh int) (Transform, Transform) {
		w, h := float64(fw), float64(fh)

		prog := DefaultTransform(0, 0, w, h)
		prog.Mode3D = "3d"
		prog.AnchorX = 0.0 // pivot at left edge
		prog.AnchorY = 0.5
		prog.Transform3D = &Transform3D{
			RotateY:      -120 * pos, // turn away to the left
			FOV:          30,
			Distance:     2.5,
			Shape:        "plane",
			LightEnabled: true,
			LightX:       0.4,
			LightY:       0.3,
			Ambient:      0.35,
			Specular:     0.15,
		}
		// Opacity fades in second half of the turn
		if pos > 0.5 {
			prog.Opacity = 1.0 - (pos-0.5)*2
		} else {
			prog.Opacity = 1.0
		}

		prev := DefaultTransform(0, 0, w, h)
		prev.Mode3D = "3d"
		prev.AnchorX = 0.0
		prev.AnchorY = 0.5
		prev.Transform3D = &Transform3D{
			RotateY:      90 - 90*pos, // starts behind (90), ends at 0
			FOV:          30,
			Distance:     2.5,
			Shape:        "plane",
			LightEnabled: true,
			LightX:       0.4,
			LightY:       0.3,
			Ambient:      0.35,
			Specular:     0.15,
		}
		prev.Opacity = math.Min(1.0, pos*1.5)

		return prog, prev
	}
}

// BounceTransition bounces the program off-screen with damped spring physics.
// Preview grows from center with slight overshoot for an elastic feel.
func BounceTransition() DVETransitionFunc {
	return func(pos float64, fw, fh int) (Transform, Transform) {
		w, h := float64(fw), float64(fh)

		// Damped spring parameters for program bounce
		damping := 3.0
		freq := 8.0

		// Program: bounces downward with damped oscillation
		prog := DefaultTransform(0, 0, w, h)
		envelope := math.Exp(-damping * pos)
		bounce := envelope * math.Cos(freq*pos)
		// Position: starts at 0, decays to large offset
		prog.PositionY = h * pos * (1.0 + 0.3*(1.0-bounce))
		// Scale shrinks slightly during bounce
		progScale := math.Max(0.3, 1.0-pos*0.7)
		prog.Width = w * progScale
		prog.Height = h * progScale
		prog.PositionX = (w - prog.Width) / 2
		// Opacity fades
		prog.Opacity = math.Max(0, 1.0-pos*1.2)

		// Preview: grows from center with elastic overshoot
		prevT := pos
		// Elastic ease-out: overshoot then settle
		elasticScale := 1.0
		if prevT < 1.0 {
			// Simple overshoot using damped sine
			overshoot := math.Sin(prevT*math.Pi*1.5) * math.Exp(-prevT*2) * 0.08
			elasticScale = prevT + overshoot
			if elasticScale > 1.0 {
				elasticScale = 1.0
			}
			if elasticScale < 0 {
				elasticScale = 0
			}
		}
		prevW := w * elasticScale
		prevH := h * elasticScale
		prev := DefaultTransform((w-prevW)/2, (h-prevH)/2, prevW, prevH)
		prev.Opacity = math.Min(1.0, pos*2)

		return prog, prev
	}
}

// BlurDissolveTransition crossfades through defocus.
// Program blurs and fades out in the first half;
// preview deblurs and fades in during the second half.
// Both sources remain full-screen (no position/scale change).
func BlurDissolveTransition() DVETransitionFunc {
	const maxBlur = 20.0

	return func(pos float64, fw, fh int) (Transform, Transform) {
		w, h := float64(fw), float64(fh)

		prog := DefaultTransform(0, 0, w, h)
		prev := DefaultTransform(0, 0, w, h)

		if pos <= 0.5 {
			// First half: program defocuses and fades
			t := pos / 0.5 // 0 -> 1 over first half
			prog.DefocusRadius = maxBlur * t
			prog.Opacity = 1.0 - t
			prev.DefocusRadius = maxBlur
			prev.Opacity = 0.0
		} else {
			// Second half: preview sharpens and fades in
			t := (pos - 0.5) / 0.5 // 0 -> 1 over second half
			prog.DefocusRadius = maxBlur
			prog.Opacity = 0.0
			prev.DefocusRadius = maxBlur * (1.0 - t)
			prev.Opacity = t
		}

		return prog, prev
	}
}
