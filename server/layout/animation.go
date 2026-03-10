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
func (a *Animation) InterpolateAlpha(t float64) float64 {
	return a.FromAlpha + (a.ToAlpha-a.FromAlpha)*t
}

func lerp(a, b int, t float64) int {
	return a + int(float64(b-a)*t+0.5)
}
