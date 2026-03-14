package transition

import (
	"fmt"
	"math"
)

// EasingType identifies the easing curve used for transition timing.
type EasingType string

const (
	EasingLinear     EasingType = "linear"      // y = t
	EasingEase       EasingType = "ease"        // CSS: cubic-bezier(0.25, 0.1, 0.25, 1.0)
	EasingEaseIn     EasingType = "ease-in"     // CSS: cubic-bezier(0.42, 0, 1.0, 1.0)
	EasingEaseOut    EasingType = "ease-out"    // CSS: cubic-bezier(0, 0, 0.58, 1.0)
	EasingEaseInOut  EasingType = "ease-in-out" // CSS: cubic-bezier(0.42, 0, 0.58, 1.0)
	EasingSmoothstep EasingType = "smoothstep"  // Hermite: t*(3-2t)
	EasingCustom     EasingType = "custom"      // User-defined cubic-bezier
)

// ValidEasingTypes is the set of all recognized easing types.
var ValidEasingTypes = map[EasingType]bool{
	EasingLinear:     true,
	EasingEase:       true,
	EasingEaseIn:     true,
	EasingEaseOut:    true,
	EasingEaseInOut:  true,
	EasingSmoothstep: true,
	EasingCustom:     true,
}

// EasingCurve defines a timing curve for transition easing. It supports
// CSS-style cubic-bezier presets, the legacy smoothstep, and custom curves.
type EasingCurve struct {
	Type   EasingType
	X1, Y1 float64
	X2, Y2 float64
}

// NewEasingCurve returns an EasingCurve for the given preset type.
// Unknown types fall back to linear.
func NewEasingCurve(preset EasingType) *EasingCurve {
	switch preset {
	case EasingLinear:
		return &EasingCurve{Type: EasingLinear}
	case EasingEase:
		return &EasingCurve{Type: EasingEase, X1: 0.25, Y1: 0.1, X2: 0.25, Y2: 1.0}
	case EasingEaseIn:
		return &EasingCurve{Type: EasingEaseIn, X1: 0.42, Y1: 0.0, X2: 1.0, Y2: 1.0}
	case EasingEaseOut:
		return &EasingCurve{Type: EasingEaseOut, X1: 0.0, Y1: 0.0, X2: 0.58, Y2: 1.0}
	case EasingEaseInOut:
		return &EasingCurve{Type: EasingEaseInOut, X1: 0.42, Y1: 0.0, X2: 0.58, Y2: 1.0}
	case EasingSmoothstep:
		return &EasingCurve{Type: EasingSmoothstep}
	default:
		// Unknown type: fallback to linear.
		return &EasingCurve{Type: EasingLinear}
	}
}

// NewCustomEasingCurve creates a custom cubic-bezier easing curve.
// x1 and x2 must be in [0, 1]; y1 and y2 may be any value (allows overshoot).
func NewCustomEasingCurve(x1, y1, x2, y2 float64) (*EasingCurve, error) {
	if x1 < 0 || x1 > 1 {
		return nil, fmt.Errorf("easing: x1=%v outside [0, 1]", x1)
	}
	if x2 < 0 || x2 > 1 {
		return nil, fmt.Errorf("easing: x2=%v outside [0, 1]", x2)
	}
	return &EasingCurve{
		Type: EasingCustom,
		X1:   x1, Y1: y1,
		X2: x2, Y2: y2,
	}, nil
}

// Ease maps a linear time value t in [0,1] to an eased position.
// Input is clamped to [0,1]. A nil receiver returns t (linear fallback).
func (c *EasingCurve) Ease(t float64) float64 {
	if c == nil {
		return t
	}

	// Clamp input to [0, 1].
	if t <= 0 {
		return 0
	}
	if t >= 1 {
		return 1
	}

	switch c.Type {
	case EasingLinear:
		return t
	case EasingSmoothstep:
		return t * t * (3.0 - 2.0*t)
	default:
		return cubicBezierEase(t, c.X1, c.Y1, c.X2, c.Y2)
	}
}

// cubicBezierEase evaluates a cubic bezier curve defined by:
//
//	P0 = (0, 0), P1 = (x1, y1), P2 = (x2, y2), P3 = (1, 1)
//
// Given input time t, we find parameter s such that x(s) = t using
// Newton-Raphson iteration, then return y(s).
func cubicBezierEase(t, x1, y1, x2, y2 float64) float64 {
	// Initial guess: s = t is a reasonable starting point for most curves.
	s := t

	// Newton-Raphson: solve x(s) = t for s.
	const maxIter = 8
	const epsilon = 1e-7

	for i := 0; i < maxIter; i++ {
		x := sampleCurve(s, x1, x2)
		diff := x - t
		if math.Abs(diff) < epsilon {
			break
		}

		dx := sampleCurveDerivative(s, x1, x2)
		if math.Abs(dx) < 1e-12 {
			// Derivative near zero -- fall back to bisection.
			s = bisectCurve(t, x1, x2)
			break
		}

		s -= diff / dx

		// Keep s in [0, 1] to prevent divergence.
		if s < 0 {
			s = 0
		} else if s > 1 {
			s = 1
		}
	}

	return sampleCurve(s, y1, y2)
}

// sampleCurve evaluates the cubic bezier for one coordinate axis at parameter s.
//
// The parametric cubic bezier with endpoints P0=0, P3=1 and control points p1, p2:
//
//	B(s) = 3*(1-s)^2*s*p1 + 3*(1-s)*s^2*p2 + s^3
//
// Expanded using Horner's method for efficiency:
//
//	B(s) = s * (3*p1 + s*(-6*p1 + 3*p2 + s*(3*p1 - 3*p2 + 1)))
func sampleCurve(s, p1, p2 float64) float64 {
	// Coefficients of the cubic polynomial as^3 + bs^2 + cs:
	//   a = 1 - 3*p2 + 3*p1
	//   b = 3*p2 - 6*p1
	//   c = 3*p1
	// Horner form: ((a*s + b)*s + c)*s
	return (((1.0-3.0*p2+3.0*p1)*s+(3.0*p2-6.0*p1))*s + 3.0*p1) * s
}

// sampleCurveDerivative returns dB/ds for the cubic bezier on one axis.
//
//	dB/ds = 3*(1-s)^2*p1 + 6*(1-s)*s*(p2-p1) + 3*s^2*(1-p2)
func sampleCurveDerivative(s, p1, p2 float64) float64 {
	// Coefficients: 3a*s^2 + 2b*s + c where a,b,c from sampleCurve.
	a := 1.0 - 3.0*p2 + 3.0*p1
	b := 3.0*p2 - 6.0*p1
	c := 3.0 * p1
	return (3.0*a*s+2.0*b)*s + c
}

// bisectCurve finds parameter s such that x(s) ~ t using binary search.
// Used as a fallback when Newton-Raphson has a near-zero derivative.
func bisectCurve(t, x1, x2 float64) float64 {
	lo, hi := 0.0, 1.0
	for i := 0; i < 64; i++ {
		mid := (lo + hi) * 0.5
		if sampleCurve(mid, x1, x2) < t {
			lo = mid
		} else {
			hi = mid
		}
	}
	return (lo + hi) * 0.5
}
