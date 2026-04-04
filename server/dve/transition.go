package dve

import (
	"math"
	"strings"
)

// DVETransitionFunc computes the DVE transforms for program and preview
// sources at a given transition position (0.0-1.0).
// Returns (programTransform, previewTransform) both as full-frame transforms.
type DVETransitionFunc func(position float64, frameW, frameH int) (program Transform, preview Transform)

// PushTransition creates a push DVE transition in the specified direction.
// Directions: "left", "right", "up", "down".
// Program slides off in the specified direction, preview slides on from the
// opposite direction. Both sources remain full frame size.
func PushTransition(direction string) DVETransitionFunc {
	return func(position float64, frameW, frameH int) (Transform, Transform) {
		w := float64(frameW)
		h := float64(frameH)

		prog := DefaultTransform(0, 0, w, h)
		prev := DefaultTransform(0, 0, w, h)

		switch direction {
		case "left":
			// Program slides left, preview enters from right
			prog.PositionX = -w * position
			prev.PositionX = w * (1 - position)
		case "right":
			// Program slides right, preview enters from left
			prog.PositionX = w * position
			prev.PositionX = -w * (1 - position)
		case "up":
			// Program slides up, preview enters from bottom
			prog.PositionY = -h * position
			prev.PositionY = h * (1 - position)
		case "down":
			// Program slides down, preview enters from top
			prog.PositionY = h * position
			prev.PositionY = -h * (1 - position)
		}

		return prog, prev
	}
}

// SqueezeTransition creates a squeeze DVE transition.
// Program scales from 100% to 0% (centered), preview scales from 0% to 100%
// (centered). At position=0.5, both are at 50% size.
func SqueezeTransition() DVETransitionFunc {
	return func(position float64, frameW, frameH int) (Transform, Transform) {
		w := float64(frameW)
		h := float64(frameH)

		progScale := 1.0 - position
		prevScale := position

		const minSize = 0.001
		progW := w * progScale
		progH := h * progScale
		if progW < minSize {
			progW = minSize
		}
		if progH < minSize {
			progH = minSize
		}
		prevW := w * prevScale
		prevH := h * prevScale
		if prevW < minSize {
			prevW = minSize
		}
		if prevH < minSize {
			prevH = minSize
		}

		// Center both in the frame
		prog := DefaultTransform((w-progW)/2, (h-progH)/2, progW, progH)
		prev := DefaultTransform((w-prevW)/2, (h-prevH)/2, prevW, prevH)

		return prog, prev
	}
}

// SpinTransition creates a spin DVE transition.
// Program rotates 0->360 while scaling 100%->0% and fading 1.0->0.0.
// Preview rotates -360->0 while scaling 0%->100% and fading 0.0->1.0.
// Both rotate around center anchor (0.5, 0.5).
func SpinTransition() DVETransitionFunc {
	return func(position float64, frameW, frameH int) (Transform, Transform) {
		w := float64(frameW)
		h := float64(frameH)

		progScale := 1.0 - position
		prevScale := position

		progW := w * progScale
		progH := h * progScale
		prevW := w * prevScale
		prevH := h * prevScale

		prog := DefaultTransform((w-progW)/2, (h-progH)/2, progW, progH)
		prog.RotationDeg = 360 * position
		prog.Opacity = 1.0 - position
		prog.AnchorX = 0.5
		prog.AnchorY = 0.5

		prev := DefaultTransform((w-prevW)/2, (h-prevH)/2, prevW, prevH)
		prev.RotationDeg = -360 * (1 - position)
		prev.Opacity = position
		prev.AnchorX = 0.5
		prev.AnchorY = 0.5

		return prog, prev
	}
}

// ZoomTransition creates a zoom DVE transition.
// Preview zooms from 0% at center to 100% fullscreen.
// Program stays fullscreen but fades 1.0->0.0.
// Creates a "zoom in to new source" effect.
func ZoomTransition() DVETransitionFunc {
	return func(position float64, frameW, frameH int) (Transform, Transform) {
		w := float64(frameW)
		h := float64(frameH)

		prog := DefaultTransform(0, 0, w, h)
		prog.Opacity = 1.0 - position

		prevW := w * position
		prevH := h * position
		prev := DefaultTransform((w-prevW)/2, (h-prevH)/2, prevW, prevH)

		return prog, prev
	}
}

// FlyTransition creates a fly DVE transition with scale overshoot.
// exitDir: direction program flies out ("left", "right", "up", "down").
// enterDir: direction preview flies in from.
// Both have slight scale overshoot (110% at midpoint) for dynamic feel.
func FlyTransition(exitDir, enterDir string) DVETransitionFunc {
	return func(position float64, frameW, frameH int) (Transform, Transform) {
		w := float64(frameW)
		h := float64(frameH)

		// Scale overshoot: 1.0 -> 1.1 -> 1.0 using sine curve
		// peaks at position=0.5
		overshoot := 1.0 + 0.1*math.Sin(position*math.Pi)

		// Eased position for smoother movement
		eased := smoothstep(position)

		// Program exit
		prog := DefaultTransform(0, 0, w*overshoot, h*overshoot)
		// Center the oversized frame
		prog.PositionX = -(prog.Width - w) / 2
		prog.PositionY = -(prog.Height - h) / 2
		applyFlyOffset(&prog, exitDir, eased, w, h)

		// Preview enter
		prev := DefaultTransform(0, 0, w*overshoot, h*overshoot)
		prev.PositionX = -(prev.Width - w) / 2
		prev.PositionY = -(prev.Height - h) / 2
		applyFlyEnterOffset(&prev, enterDir, eased, w, h)

		return prog, prev
	}
}

// applyFlyOffset applies exit direction offset to a transform.
func applyFlyOffset(t *Transform, dir string, eased, w, h float64) {
	switch dir {
	case "left":
		t.PositionX -= w * eased
	case "right":
		t.PositionX += w * eased
	case "up":
		t.PositionY -= h * eased
	case "down":
		t.PositionY += h * eased
	}
}

// applyFlyEnterOffset applies enter direction offset to a transform.
func applyFlyEnterOffset(t *Transform, dir string, eased, w, h float64) {
	switch dir {
	case "left":
		t.PositionX -= w * (1 - eased)
	case "right":
		t.PositionX += w * (1 - eased)
	case "up":
		t.PositionY -= h * (1 - eased)
	case "down":
		t.PositionY += h * (1 - eased)
	}
}

// smoothstep provides ease-in-out curve: 3t^2 - 2t^3
func smoothstep(t float64) float64 {
	if t <= 0 {
		return 0
	}
	if t >= 1 {
		return 1
	}
	return t * t * (3 - 2*t)
}

// CubeSpinTransition creates a 3D cube spin transition.
// The program and preview are mapped onto adjacent faces of a cube that
// rotates 90° around the Y axis, revealing the preview on the next face.
func CubeSpinTransition() DVETransitionFunc {
	return func(position float64, frameW, frameH int) (Transform, Transform) {
		w := float64(frameW)
		h := float64(frameH)

		// Program: visible face, rotating away
		prog := DefaultTransform(0, 0, w, h)
		prog.Mode3D = "3d"
		prog.Transform3D = &Transform3D{
			RotateY:      -90 * position, // rotate left, revealing preview
			FOV:          30,
			Distance:     2.5,
			Shape:        "plane",
			LightEnabled: true,
			LightX:       0.3,
			LightY:       0.3,
			Ambient:      0.4,
		}
		prog.Opacity = 1.0

		// Preview: entering from the right via rotation
		prev := DefaultTransform(0, 0, w, h)
		prev.Mode3D = "3d"
		prev.Transform3D = &Transform3D{
			RotateY:      90 - 90*position, // starts at +90 (hidden), ends at 0
			FOV:          30,
			Distance:     2.5,
			Shape:        "plane",
			LightEnabled: true,
			LightX:       0.3,
			LightY:       0.3,
			Ambient:      0.4,
		}
		prev.Opacity = 1.0

		return prog, prev
	}
}

// FlipTransition creates a 3D flip (card flip) transition around the X axis.
// The program flips up/away, revealing the preview on the "back side".
func FlipTransition() DVETransitionFunc {
	return func(position float64, frameW, frameH int) (Transform, Transform) {
		w := float64(frameW)
		h := float64(frameH)

		prog := DefaultTransform(0, 0, w, h)
		prev := DefaultTransform(0, 0, w, h)

		if position < 0.5 {
			// First half: program flips forward
			prog.Mode3D = "3d"
			prog.Transform3D = &Transform3D{
				RotateX:      -180 * position, // flip forward
				FOV:          25,
				Distance:     2.8,
				Shape:        "plane",
				LightEnabled: true,
				LightX:       0,
				LightY:       0.5,
				Ambient:      0.35,
			}
			prog.Opacity = 1.0
			prev.Opacity = 0.0 // hidden during first half
		} else {
			// Second half: preview flips in from behind
			prog.Opacity = 0.0 // program hidden
			prev.Mode3D = "3d"
			prev.Transform3D = &Transform3D{
				RotateX:      180 - 180*position, // starts at 90° (edge-on), ends at 0
				FOV:          25,
				Distance:     2.8,
				Shape:        "plane",
				LightEnabled: true,
				LightX:       0,
				LightY:       0.5,
				Ambient:      0.35,
			}
			prev.Opacity = 1.0
		}
		return prog, prev
	}
}

// DoorTransition creates a 3D door-open effect.
// The program splits into two halves that swing open like doors,
// revealing the preview behind.
func DoorTransition() DVETransitionFunc {
	return func(position float64, frameW, frameH int) (Transform, Transform) {
		w := float64(frameW)
		h := float64(frameH)

		// Preview: fullscreen behind the doors
		prev := DefaultTransform(0, 0, w, h)
		prev.Opacity = 1.0

		// Program: left half (door) — we simulate this with perspective tilt
		// Since we can only composite whole sources, use perspective to create
		// the door-open illusion with the full frame
		prog := DefaultTransform(0, 0, w, h)
		prog.Mode3D = "3d"
		prog.Transform3D = &Transform3D{
			RotateY:      -90 * position, // swing open to the left
			FOV:          30,
			Distance:     2.5,
			Shape:        "plane",
			LightEnabled: true,
			LightX:       -0.5,
			LightY:       0.3,
			Ambient:      0.3,
			Specular:     0.2,
		}
		prog.Opacity = 1.0 - position*0.3 // slight fade as it opens

		return prog, prev
	}
}

// DVETransitionForType maps transition type strings to DVE transition functions.
// Returns nil for unknown types.
func DVETransitionForType(transitionType string) DVETransitionFunc {
	switch transitionType {
	case "dve-push-left":
		return PushTransition("left")
	case "dve-push-right":
		return PushTransition("right")
	case "dve-push-up":
		return PushTransition("up")
	case "dve-push-down":
		return PushTransition("down")
	case "dve-squeeze":
		return SqueezeTransition()
	case "dve-spin":
		return SpinTransition()
	case "dve-zoom":
		return ZoomTransition()
	case "dve-fly":
		return FlyTransition("left", "right")
	case "dve-cube":
		return CubeSpinTransition()
	case "dve-flip":
		return FlipTransition()
	case "dve-door":
		return DoorTransition()
	// Extended 2D transitions
	case "dve-swing-left":
		return SwingTransition("left")
	case "dve-swing-right":
		return SwingTransition("right")
	case "dve-reveal-left":
		return SlideRevealTransition("left")
	case "dve-reveal-right":
		return SlideRevealTransition("right")
	case "dve-reveal-up":
		return SlideRevealTransition("up")
	case "dve-reveal-down":
		return SlideRevealTransition("down")
	case "dve-scale-rotate":
		return ScaleRotateTransition()
	case "dve-cross-zoom":
		return CrossZoomTransition()
	case "dve-shrink-grow":
		return ShrinkGrowTransition()
	case "dve-barn-h":
		return BarnDoorTransition("horizontal")
	case "dve-barn-v":
		return BarnDoorTransition("vertical")
	case "dve-mosaic":
		return MosaicTransition()
	case "dve-corner-peel":
		return CornerPeelTransition()
	// Extended 3D transitions
	case "dve-carousel":
		return CarouselTransition()
	case "dve-tumble":
		return TumbleTransition()
	case "dve-swing-door":
		return SwingDoorTransition()
	case "dve-fold":
		return FoldTransition()
	case "dve-cube-roll":
		return CubeRollTransition()
	case "dve-globe":
		return GlobeSpinTransition()
	// B2 extended transitions
	case "dve-page-turn":
		return PageTurnTransition()
	case "dve-bounce":
		return BounceTransition()
	case "dve-blur-dissolve":
		return BlurDissolveTransition()
	default:
		return nil
	}
}

// IsDVETransition returns true for any "dve-*" type string.
func IsDVETransition(transitionType string) bool {
	return strings.HasPrefix(transitionType, "dve-")
}
