package dve

import "math"

// Keyframe is a single timed value with cubic bezier easing handles.
// The easing applies to the segment AFTER this keyframe. P1 is fixed at (0,0)
// and P2 is (EaseOutX, EaseOutY), making this a simplified single-handle model.
// For linear interpolation, use EaseOutX=1, EaseOutY=1 (maps to identity curve).
type Keyframe struct {
	TimeMs   int     `json:"timeMs"`
	Value    float64 `json:"value"`
	EaseOutX float64 `json:"easeOutX"` // P2.X of cubic bezier [0-1]
	EaseOutY float64 `json:"easeOutY"` // P2.Y of cubic bezier (can overshoot)
}

// Track animates a single parameter on a specific slot.
type Track struct {
	Param     string     `json:"param"`
	SlotIndex int        `json:"slotIndex"`
	Keyframes []Keyframe `json:"keyframes"`
}

// Evaluate returns the interpolated value at the given time in milliseconds.
func (tr Track) Evaluate(timeMs int) float64 {
	n := len(tr.Keyframes)
	if n == 0 {
		return 0
	}
	if n == 1 {
		return tr.Keyframes[0].Value
	}
	if timeMs <= tr.Keyframes[0].TimeMs {
		return tr.Keyframes[0].Value
	}
	if timeMs >= tr.Keyframes[n-1].TimeMs {
		return tr.Keyframes[n-1].Value
	}

	// Find the segment.
	for i := 0; i < n-1; i++ {
		k0 := tr.Keyframes[i]
		k1 := tr.Keyframes[i+1]
		if timeMs >= k0.TimeMs && timeMs < k1.TimeMs {
			dt := float64(k1.TimeMs - k0.TimeMs)
			if dt <= 0 {
				return k0.Value
			}
			t := float64(timeMs-k0.TimeMs) / dt
			easedT := cubicBezier(t, 0, 0, k0.EaseOutX, k0.EaseOutY)
			return k0.Value + (k1.Value-k0.Value)*easedT
		}
	}
	return tr.Keyframes[n-1].Value
}

// Animation is a named collection of tracks with a duration.
type Animation struct {
	Name       string  `json:"name"`
	DurationMs int     `json:"durationMs"`
	Loop       bool    `json:"loop"`
	Tracks     []Track `json:"tracks"`
}

// ResolveTimeMs resolves a raw elapsed time to an effective time,
// applying looping or clamping as appropriate.
func (a Animation) ResolveTimeMs(elapsed int) int {
	if elapsed < 0 {
		return 0
	}
	if a.Loop && a.DurationMs > 0 {
		return elapsed % a.DurationMs
	}
	if elapsed > a.DurationMs {
		return a.DurationMs
	}
	return elapsed
}

// IsComplete returns true if the animation has finished for a non-looping
// animation, or always false for looping animations.
func (a Animation) IsComplete(elapsed int) bool {
	if a.Loop {
		return false
	}
	return elapsed >= a.DurationMs
}

// EvaluateTransform applies all tracks targeting the given slot index
// at the given time to a base Transform, returning the modified copy.
func (a Animation) EvaluateTransform(slotIndex int, timeMs int, base Transform) Transform {
	result := base
	for _, track := range a.Tracks {
		if track.SlotIndex != slotIndex {
			continue
		}
		v := track.Evaluate(timeMs)
		switch track.Param {
		case "positionX":
			result.PositionX = v
		case "positionY":
			result.PositionY = v
		case "width":
			result.Width = v
		case "height":
			result.Height = v
		case "rotationDeg":
			result.RotationDeg = v
		case "perspectiveX":
			result.PerspectiveX = v
		case "perspectiveY":
			result.PerspectiveY = v
		case "opacity":
			result.Opacity = v
		case "anchorX":
			result.AnchorX = v
		case "anchorY":
			result.AnchorY = v
		case "cropTop":
			result.CropTop = v
		case "cropBottom":
			result.CropBottom = v
		case "cropLeft":
			result.CropLeft = v
		case "cropRight":
			result.CropRight = v
		case "cropSoftness":
			result.CropSoftness = v
		case "defocusRadius":
			result.DefocusRadius = v
		case "border.width":
			result.Border.Width = v
		case "border.cornerRadius":
			result.Border.CornerRadius = v
		case "border.softness":
			result.Border.Softness = v
		case "border.innerWidth":
			result.Border.InnerWidth = v
		case "shadow.offsetX":
			result.Shadow.OffsetX = v
		case "shadow.offsetY":
			result.Shadow.OffsetY = v
		case "shadow.blur":
			result.Shadow.Blur = v
		case "shadow.opacity":
			result.Shadow.Opacity = v
		case "mosaicSize":
			result.MosaicSize = int(v)
		case "saturation":
			result.Saturation = v
		case "brightness":
			result.Brightness = v
		case "contrast":
			result.Contrast = v
		}
	}
	return result
}

// cubicBezier evaluates a cubic bezier curve at parameter t in [0,1].
// Control points: P0=(0,0), P1=(x1,y1), P2=(x2,y2), P3=(1,1).
// Returns the Y value for the given t using Newton's method to solve for
// the X parameter first.
func cubicBezier(t, x1, y1, x2, y2 float64) float64 {
	if t <= 0 {
		return 0
	}
	if t >= 1 {
		return 1
	}

	// Solve for the bezier parameter s where bezierX(s) = t
	// using Newton-Raphson with bisection fallback.
	s := t
	for i := 0; i < 8; i++ {
		bx := bezierComponent(s, x1, x2)
		dx := bezierDerivative(s, x1, x2)
		if math.Abs(dx) < 1e-12 {
			break
		}
		s -= (bx - t) / dx
		if s < 0 {
			s = 0
		}
		if s > 1 {
			s = 1
		}
		if math.Abs(bezierComponent(s, x1, x2)-t) < 1e-7 {
			return bezierComponent(s, y1, y2)
		}
	}

	// Bisection fallback if Newton-Raphson didn't converge.
	lo, hi := 0.0, 1.0
	s = t
	for i := 0; i < 64; i++ {
		bx := bezierComponent(s, x1, x2)
		if math.Abs(bx-t) < 1e-7 {
			break
		}
		if bx < t {
			lo = s
		} else {
			hi = s
		}
		s = (lo + hi) / 2
	}

	return bezierComponent(s, y1, y2)
}

// bezierComponent evaluates one component of a cubic bezier:
// B(s) = 3*(1-s)^2*s*p1 + 3*(1-s)*s^2*p2 + s^3
func bezierComponent(s, p1, p2 float64) float64 {
	si := 1 - s
	return 3*si*si*s*p1 + 3*si*s*s*p2 + s*s*s
}

// bezierDerivative is the derivative of bezierComponent w.r.t. s.
func bezierDerivative(s, p1, p2 float64) float64 {
	si := 1 - s
	return 3*si*si*p1 + 6*si*s*(p2-p1) + 3*s*s*(1-p2)
}
