package dve

import "math"

// ============================================================================
// 2D Transition Library — additional pre-canned DVE transitions
// ============================================================================

// SwingTransition creates a pendulum swing effect.
// Program swings off to one side, preview swings in from the other.
func SwingTransition(direction string) DVETransitionFunc {
	return func(pos float64, fw, fh int) (Transform, Transform) {
		w, h := float64(fw), float64(fh)

		// Pendulum-like easing: accelerate then decelerate
		eased := math.Sin(pos * math.Pi / 2)

		prog := DefaultTransform(0, 0, w, h)
		prev := DefaultTransform(0, 0, w, h)

		angle := 45.0 * eased
		switch direction {
		case "left":
			prog.RotationDeg = -angle
			prog.PositionX = -w * 0.3 * eased
			prev.RotationDeg = 45 - angle
			prev.PositionX = w*(1-eased) + w*0.3*(1-eased)
		default: // right
			prog.RotationDeg = angle
			prog.PositionX = w * 0.3 * eased
			prev.RotationDeg = -(45 - angle)
			prev.PositionX = -w*(1-eased) - w*0.3*(1-eased)
		}

		prog.AnchorX = 0.5
		prog.AnchorY = 1.0 // pivot at bottom
		prev.AnchorX = 0.5
		prev.AnchorY = 1.0
		prog.Opacity = 1.0 - pos*0.5
		prev.Opacity = 0.5 + pos*0.5

		return prog, prev
	}
}

// SlideRevealTransition slides the program aside to reveal preview underneath.
// Only the program moves; preview is static fullscreen underneath.
func SlideRevealTransition(direction string) DVETransitionFunc {
	return func(pos float64, fw, fh int) (Transform, Transform) {
		w, h := float64(fw), float64(fh)
		eased := smoothstepF(pos)

		prev := DefaultTransform(0, 0, w, h)
		prog := DefaultTransform(0, 0, w, h)

		switch direction {
		case "left":
			prog.PositionX = -w * eased
		case "right":
			prog.PositionX = w * eased
		case "up":
			prog.PositionY = -h * eased
		default:
			prog.PositionY = h * eased
		}

		// Shadow on the sliding piece (disabled for now — needs GPU shadow support during transitions)
		_ = eased

		return prog, prev
	}
}

// ScaleRotateTransition scales and rotates both sources simultaneously.
func ScaleRotateTransition() DVETransitionFunc {
	return func(pos float64, fw, fh int) (Transform, Transform) {
		w, h := float64(fw), float64(fh)

		progScale := 1.0 - pos
		prevScale := pos

		prog := DefaultTransform(
			w*(1-progScale)/2, h*(1-progScale)/2,
			w*progScale, h*progScale,
		)
		prog.RotationDeg = -180 * pos
		prog.AnchorX = 0.5
		prog.AnchorY = 0.5
		prog.Opacity = 1.0 - pos

		prev := DefaultTransform(
			w*(1-prevScale)/2, h*(1-prevScale)/2,
			w*prevScale, h*prevScale,
		)
		prev.RotationDeg = 180 * (1 - pos)
		prev.AnchorX = 0.5
		prev.AnchorY = 0.5
		prev.Opacity = pos

		return prog, prev
	}
}

// CrossZoomTransition zooms program out while zooming preview in.
// Creates a "through the lens" effect popular in vlogs.
func CrossZoomTransition() DVETransitionFunc {
	return func(pos float64, fw, fh int) (Transform, Transform) {
		w, h := float64(fw), float64(fh)

		// Program zooms out to ~200% then fades
		progZoom := 1.0 + pos*1.5
		progW := w * progZoom
		progH := h * progZoom
		prog := DefaultTransform(
			(w-progW)/2, (h-progH)/2, progW, progH,
		)
		prog.Opacity = math.Max(0, 1.0-pos*2)
		prog.DefocusRadius = pos * 8

		// Preview zooms from ~50% to 100%
		prevZoom := 0.3 + pos*0.7
		prevW := w * prevZoom
		prevH := h * prevZoom
		prev := DefaultTransform(
			(w-prevW)/2, (h-prevH)/2, prevW, prevH,
		)
		prev.Opacity = math.Min(1.0, pos*2)

		return prog, prev
	}
}

// ShrinkGrowTransition shrinks program to a pip then grows preview from pip.
func ShrinkGrowTransition() DVETransitionFunc {
	return func(pos float64, fw, fh int) (Transform, Transform) {
		w, h := float64(fw), float64(fh)

		if pos < 0.5 {
			// Phase 1: program shrinks to PIP in corner
			t := pos * 2 // 0→1 over first half
			eased := smoothstepF(t)
			pipW := w * (1 - 0.75*eased)
			pipH := h * (1 - 0.75*eased)
			pipX := (w - pipW) * eased * 0.9
			pipY := (h - pipH) * eased * 0.1

			prog := DefaultTransform(pipX, pipY, pipW, pipH)
			prog.Border = BorderConfig{Width: 2 * eased, ColorY: 235, ColorCb: 128, ColorCr: 128, CornerRadius: 6 * eased}
			prog.Shadow = ShadowConfig{Enabled: true, OffsetX: 3, OffsetY: 3, Blur: 6, Opacity: 0.4 * eased}

			prev := DefaultTransform(0, 0, w, h)
			prev.Opacity = eased * 0.3 // start fading in behind

			return prog, prev
		}

		// Phase 2: preview grows from PIP to fullscreen
		t := (pos - 0.5) * 2 // 0→1 over second half
		eased := smoothstepF(t)

		prog := DefaultTransform(w*0.9-w*0.25, h*0.1, w*0.25, h*0.25)
		prog.Opacity = 1.0 - eased
		prog.Border = BorderConfig{Width: 2, ColorY: 235, ColorCb: 128, ColorCr: 128, CornerRadius: 6}

		prevW := w * (0.25 + 0.75*eased)
		prevH := h * (0.25 + 0.75*eased)
		prev := DefaultTransform(
			(w-prevW)/2*(1-eased), (h-prevH)/2*(1-eased),
			prevW, prevH,
		)
		prev.Opacity = 0.3 + 0.7*eased

		return prog, prev
	}
}

// BarnDoorTransition splits the program into two halves that slide apart.
// Simulated by moving the full frame with crop.
func BarnDoorTransition(direction string) DVETransitionFunc {
	return func(pos float64, fw, fh int) (Transform, Transform) {
		w, h := float64(fw), float64(fh)
		eased := smoothstepF(pos)

		prev := DefaultTransform(0, 0, w, h) // preview underneath

		prog := DefaultTransform(0, 0, w, h)
		if direction == "vertical" {
			prog.CropTop = h / 2 * eased
			prog.CropBottom = h / 2 * eased
		} else {
			prog.CropLeft = w / 2 * eased
			prog.CropRight = w / 2 * eased
		}
		prog.CropSoftness = 4

		return prog, prev
	}
}

// MosaicTransition creates a defocus-dissolve effect.
// Program blurs and fades out while preview sharpens and fades in.
func MosaicTransition() DVETransitionFunc {
	return func(pos float64, fw, fh int) (Transform, Transform) {
		w, h := float64(fw), float64(fh)

		prog := DefaultTransform(0, 0, w, h)
		prog.DefocusRadius = pos * 15
		prog.Opacity = 1.0 - pos

		prev := DefaultTransform(0, 0, w, h)
		prev.DefocusRadius = (1 - pos) * 15
		prev.Opacity = pos

		return prog, prev
	}
}

// CornerPeelTransition peels the program from a corner, revealing preview.
func CornerPeelTransition() DVETransitionFunc {
	return func(pos float64, fw, fh int) (Transform, Transform) {
		w, h := float64(fw), float64(fh)
		eased := smoothstepF(pos)

		prev := DefaultTransform(0, 0, w, h)

		prog := DefaultTransform(0, 0, w, h)
		prog.RotationDeg = -45 * eased
		prog.AnchorX = 0
		prog.AnchorY = 0
		prog.Opacity = 1.0 - eased*0.7
		prog.Shadow = ShadowConfig{
			Enabled: true,
			OffsetX: -5 * eased,
			OffsetY: 5 * eased,
			Blur:    12,
			Opacity: 0.5 * (1 - pos),
		}

		return prog, prev
	}
}

// ============================================================================
// 3D Transition Library — additional 3D transitions
// ============================================================================

// CarouselTransition rotates both sources around a vertical carousel.
func CarouselTransition() DVETransitionFunc {
	return func(pos float64, fw, fh int) (Transform, Transform) {
		w, h := float64(fw), float64(fh)

		prog := DefaultTransform(0, 0, w, h)
		prog.Mode3D = "3d"
		prog.Transform3D = &Transform3D{
			RotateY:      -120 * pos,
			FOV:          35,
			Distance:     2.0,
			Shape:        "plane",
			LightEnabled: true,
			LightX:       0,
			LightY:       0.3,
			Ambient:      0.35,
			Specular:     0.15,
		}
		prog.Opacity = math.Max(0, 1.0-pos*1.5)

		prev := DefaultTransform(0, 0, w, h)
		prev.Mode3D = "3d"
		prev.Transform3D = &Transform3D{
			RotateY:      120 - 120*pos,
			FOV:          35,
			Distance:     2.0,
			Shape:        "plane",
			LightEnabled: true,
			LightX:       0,
			LightY:       0.3,
			Ambient:      0.35,
			Specular:     0.15,
		}
		prev.Opacity = math.Min(1.0, pos*1.5)

		return prog, prev
	}
}

// TumbleTransition tumbles the program off-screen in 3D.
func TumbleTransition() DVETransitionFunc {
	return func(pos float64, fw, fh int) (Transform, Transform) {
		w, h := float64(fw), float64(fh)

		prog := DefaultTransform(0, 0, w, h)
		prog.Mode3D = "3d"
		prog.Transform3D = &Transform3D{
			RotateX:      -60 * pos,
			RotateY:      45 * pos,
			RotateZ:      30 * pos,
			FOV:          25,
			Distance:     3.0 + pos*2, // move away
			Shape:        "plane",
			LightEnabled: true,
			LightX:       0.5,
			LightY:       0.5,
			Ambient:      0.3,
		}
		prog.Opacity = 1.0 - pos

		prev := DefaultTransform(0, 0, w, h)
		prev.Mode3D = "3d"
		prev.Transform3D = &Transform3D{
			RotateX:      60 * (1 - pos),
			RotateY:      -45 * (1 - pos),
			RotateZ:      -30 * (1 - pos),
			FOV:          25,
			Distance:     5.0 - pos*2,
			Shape:        "plane",
			LightEnabled: true,
			LightX:       0.5,
			LightY:       0.5,
			Ambient:      0.3,
		}
		prev.Opacity = pos

		return prog, prev
	}
}

// SwingDoorTransition swings the program open like a hinged door in 3D.
func SwingDoorTransition() DVETransitionFunc {
	return func(pos float64, fw, fh int) (Transform, Transform) {
		w, h := float64(fw), float64(fh)
		eased := smoothstepF(pos)

		prev := DefaultTransform(0, 0, w, h)

		prog := DefaultTransform(0, 0, w, h)
		prog.Mode3D = "3d"
		prog.Transform3D = &Transform3D{
			RotateY:      -110 * eased, // swing past 90° to show it "opening"
			FOV:          30,
			Distance:     2.5,
			Shape:        "plane",
			LightEnabled: true,
			LightX:       -0.7,
			LightY:       0.2,
			Ambient:      0.25,
			Specular:     0.3,
		}
		prog.Opacity = 1.0 - eased*0.6

		return prog, prev
	}
}

// FoldTransition folds the program in half horizontally, revealing preview.
func FoldTransition() DVETransitionFunc {
	return func(pos float64, fw, fh int) (Transform, Transform) {
		w, h := float64(fw), float64(fh)
		eased := smoothstepF(pos)

		prev := DefaultTransform(0, 0, w, h)

		prog := DefaultTransform(0, 0, w, h)
		prog.Mode3D = "3d"
		prog.Transform3D = &Transform3D{
			RotateX:      90 * eased, // fold upward
			FOV:          20,
			Distance:     3.0,
			Shape:        "plane",
			LightEnabled: true,
			LightX:       0,
			LightY:       -0.5,
			Ambient:      0.3,
		}
		prog.Opacity = 1.0 - eased

		return prog, prev
	}
}

// CubeRollTransition rolls a cube forward around the X axis.
func CubeRollTransition() DVETransitionFunc {
	return func(pos float64, fw, fh int) (Transform, Transform) {
		w, h := float64(fw), float64(fh)

		prog := DefaultTransform(0, 0, w, h)
		prog.Mode3D = "3d"
		prog.Transform3D = &Transform3D{
			RotateX:      90 * pos,
			FOV:          30,
			Distance:     2.5,
			Shape:        "plane",
			LightEnabled: true,
			LightX:       0,
			LightY:       0.5,
			Ambient:      0.4,
		}

		prev := DefaultTransform(0, 0, w, h)
		prev.Mode3D = "3d"
		prev.Transform3D = &Transform3D{
			RotateX:      -90 + 90*pos,
			FOV:          30,
			Distance:     2.5,
			Shape:        "plane",
			LightEnabled: true,
			LightX:       0,
			LightY:       0.5,
			Ambient:      0.4,
		}

		return prog, prev
	}
}

// GlobeSpinTransition wraps sources on a sphere and spins.
func GlobeSpinTransition() DVETransitionFunc {
	return func(pos float64, fw, fh int) (Transform, Transform) {
		w, h := float64(fw), float64(fh)

		prog := DefaultTransform(0, 0, w, h)
		prog.Mode3D = "3d"
		prog.Transform3D = &Transform3D{
			RotateY:      -180 * pos,
			FOV:          40,
			Distance:     1.8,
			Shape:        "cube", // cube approximates globe for this effect
			LightEnabled: true,
			LightX:       0.3,
			LightY:       0.2,
			Ambient:      0.3,
			Specular:     0.4,
		}
		prog.Opacity = math.Max(0, 1.0-pos*2)

		prev := DefaultTransform(0, 0, w, h)
		prev.Mode3D = "3d"
		prev.Transform3D = &Transform3D{
			RotateY:      180 - 180*pos,
			FOV:          40,
			Distance:     1.8,
			Shape:        "cube",
			LightEnabled: true,
			LightX:       0.3,
			LightY:       0.2,
			Ambient:      0.3,
			Specular:     0.4,
		}
		prev.Opacity = math.Min(1.0, pos*2)

		return prog, prev
	}
}

// smoothstepF is an alias for smoothstep (transition.go) for local readability.
var smoothstepF = smoothstep

// AllTransitionTypes returns all available DVE transition type strings.
func AllTransitionTypes() []string {
	return []string{
		"dve-push-left", "dve-push-right", "dve-push-up", "dve-push-down",
		"dve-squeeze", "dve-spin", "dve-zoom", "dve-fly",
		"dve-cube", "dve-flip", "dve-door",
		"dve-swing-left", "dve-swing-right",
		"dve-reveal-left", "dve-reveal-right", "dve-reveal-up", "dve-reveal-down",
		"dve-scale-rotate", "dve-cross-zoom", "dve-shrink-grow",
		"dve-barn-h", "dve-barn-v", "dve-mosaic", "dve-corner-peel",
		"dve-carousel", "dve-tumble", "dve-swing-door",
		"dve-fold", "dve-cube-roll", "dve-globe",
		"dve-page-turn", "dve-bounce", "dve-blur-dissolve",
	}
}
