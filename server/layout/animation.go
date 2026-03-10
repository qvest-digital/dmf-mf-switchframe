package layout

import (
	"image"
	"time"
)

// Animation represents a slot transition animation.
type Animation struct {
	SlotIndex  int
	StartTime  time.Time
	Duration   time.Duration
	FromRect   image.Rectangle
	ToRect     image.Rectangle
	FromAlpha  float64
	ToAlpha    float64
	Easing     func(float64) float64
	OnComplete func()
}

// Progress returns the animation progress [0, 1].
func (a *Animation) Progress() float64 {
	elapsed := time.Since(a.StartTime)
	t := float64(elapsed) / float64(a.Duration)
	if t >= 1.0 {
		return 1.0
	}
	if t <= 0 {
		return 0
	}
	if a.Easing != nil {
		return a.Easing(t)
	}
	return t
}

// InterpolateRect returns the interpolated rectangle at progress t.
func (a *Animation) InterpolateRect(t float64) image.Rectangle {
	return image.Rect(
		EvenAlign(lerp(a.FromRect.Min.X, a.ToRect.Min.X, t)),
		EvenAlign(lerp(a.FromRect.Min.Y, a.ToRect.Min.Y, t)),
		EvenAlign(lerp(a.FromRect.Max.X, a.ToRect.Max.X, t)),
		EvenAlign(lerp(a.FromRect.Max.Y, a.ToRect.Max.Y, t)),
	)
}

// InterpolateAlpha returns the interpolated alpha at progress t.
// Clamps to endpoint values to avoid float imprecision causing visual pops.
func (a *Animation) InterpolateAlpha(t float64) float64 {
	alpha := a.FromAlpha + (a.ToAlpha-a.FromAlpha)*t
	// Clamp to target endpoints to prevent 255/256 blend residue.
	if a.ToAlpha >= 1.0 && alpha > 0.99 {
		return 1.0
	}
	if a.ToAlpha <= 0.0 && alpha < 0.01 {
		return 0.0
	}
	return alpha
}

func lerp(a, b int, t float64) int {
	return a + int(float64(b-a)*t+0.5)
}

// FlyInOrigin computes the off-screen starting position for a fly-in animation.
// The PIP flies in from the nearest frame edge based on the rectangle's position.
func FlyInOrigin(target image.Rectangle, frameW, frameH int) image.Rectangle {
	// Use edge distances (rectangle edge to frame edge)
	distLeft := target.Min.X
	distRight := frameW - target.Max.X
	distTop := target.Min.Y
	distBottom := frameH - target.Max.Y

	minDist := distLeft
	dx, dy := -(target.Max.X), 0 // slide fully off-screen left
	if distRight < minDist {
		minDist = distRight
		dx, dy = frameW-target.Min.X, 0 // slide fully off-screen right
	}
	if distTop < minDist {
		minDist = distTop
		dx, dy = 0, -(target.Max.Y) // slide fully off-screen top
	}
	if distBottom < minDist {
		dx, dy = 0, frameH-target.Min.Y // slide fully off-screen bottom
	}

	return image.Rect(
		EvenAlign(target.Min.X+dx),
		EvenAlign(target.Min.Y+dy),
		EvenAlign(target.Max.X+dx),
		EvenAlign(target.Max.Y+dy),
	)
}
