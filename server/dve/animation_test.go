package dve

import (
	"math"
	"testing"
)

// ---------- cubicBezier ----------

func TestCubicBezierLinear(t *testing.T) {
	// Linear: control points (0,0) and (1,1) should give y ≈ t.
	for _, tt := range []float64{0, 0.1, 0.25, 0.5, 0.75, 0.9, 1.0} {
		got := cubicBezier(tt, 0, 0, 1, 1)
		if math.Abs(got-tt) > 1e-4 {
			t.Errorf("cubicBezier(%v, 0,0,1,1) = %v, want ≈ %v", tt, got, tt)
		}
	}
}

func TestCubicBezierEaseInOut(t *testing.T) {
	// CSS ease-in-out: cubic-bezier(0.42, 0, 0.58, 1.0)
	// At t=0.5, the curve should be approximately 0.5 (symmetric).
	got := cubicBezier(0.5, 0.42, 0, 0.58, 1.0)
	if math.Abs(got-0.5) > 0.02 {
		t.Errorf("cubicBezier(0.5, ease-in-out) = %v, want ≈ 0.5", got)
	}

	// At t=0.25 (early phase) ease-in-out should be < 0.25 (slower start).
	got25 := cubicBezier(0.25, 0.42, 0, 0.58, 1.0)
	if got25 >= 0.25 {
		t.Errorf("cubicBezier(0.25, ease-in-out) = %v, want < 0.25 (slow start)", got25)
	}
}

func TestCubicBezierBoundaries(t *testing.T) {
	got0 := cubicBezier(0, 0.42, 0, 0.58, 1.0)
	if math.Abs(got0) > 1e-6 {
		t.Errorf("cubicBezier(0, ...) = %v, want 0", got0)
	}
	got1 := cubicBezier(1, 0.42, 0, 0.58, 1.0)
	if math.Abs(got1-1) > 1e-6 {
		t.Errorf("cubicBezier(1, ...) = %v, want 1", got1)
	}
}

// ---------- Track.Evaluate ----------

func TestTrackEvaluateZeroKeyframes(t *testing.T) {
	tr := Track{Param: "positionX", Keyframes: nil}
	if got := tr.Evaluate(500); got != 0 {
		t.Errorf("Evaluate with 0 keyframes = %v, want 0", got)
	}
}

func TestTrackEvaluateOneKeyframe(t *testing.T) {
	tr := Track{
		Param:     "positionX",
		Keyframes: []Keyframe{{TimeMs: 100, Value: 42.0}},
	}
	// Should always return the single keyframe value regardless of time.
	for _, ms := range []int{0, 50, 100, 200, 9999} {
		if got := tr.Evaluate(ms); got != 42.0 {
			t.Errorf("Evaluate(%d) with 1 keyframe = %v, want 42.0", ms, got)
		}
	}
}

func TestTrackEvaluateBeforeFirst(t *testing.T) {
	tr := Track{
		Param: "positionX",
		Keyframes: []Keyframe{
			{TimeMs: 500, Value: 10.0},
			{TimeMs: 1000, Value: 20.0},
		},
	}
	if got := tr.Evaluate(0); got != 10.0 {
		t.Errorf("Evaluate(0) before first keyframe = %v, want 10.0", got)
	}
	if got := tr.Evaluate(499); got != 10.0 {
		t.Errorf("Evaluate(499) before first keyframe = %v, want 10.0", got)
	}
}

func TestTrackEvaluateAfterLast(t *testing.T) {
	tr := Track{
		Param: "positionX",
		Keyframes: []Keyframe{
			{TimeMs: 0, Value: 10.0},
			{TimeMs: 500, Value: 90.0},
		},
	}
	if got := tr.Evaluate(500); got != 90.0 {
		t.Errorf("Evaluate(500) at last keyframe = %v, want 90.0", got)
	}
	if got := tr.Evaluate(9999); got != 90.0 {
		t.Errorf("Evaluate(9999) after last keyframe = %v, want 90.0", got)
	}
}

func TestTrackEvaluateLinear(t *testing.T) {
	// Linear easing: P1=(0,0) implicit, P2=(EaseOutX, EaseOutY).
	// (0,0)→(1,1) = linear per CSS cubic-bezier convention.
	tr := Track{
		Param: "positionX",
		Keyframes: []Keyframe{
			{TimeMs: 0, Value: 0.0, EaseOutX: 1.0, EaseOutY: 1.0},
			{TimeMs: 1000, Value: 100.0},
		},
	}
	got := tr.Evaluate(500)
	if math.Abs(got-50.0) > 0.5 {
		t.Errorf("Evaluate(500) linear = %v, want ≈ 50.0", got)
	}
}

func TestTrackEvaluateCubicBezier(t *testing.T) {
	// Ease-out curve: P1=(0,0), P2=(0.58, 1.0). Starts fast, decelerates.
	tr := Track{
		Param: "positionX",
		Keyframes: []Keyframe{
			{TimeMs: 0, Value: 0.0, EaseOutX: 0.58, EaseOutY: 1.0},
			{TimeMs: 1000, Value: 100.0},
		},
	}

	// At t=0.25, ease-out should be > 25 (faster start).
	got25 := tr.Evaluate(250)
	if got25 <= 25.0 {
		t.Errorf("Evaluate(250) with ease-out should be > 25, got %v", got25)
	}

	// At midpoint, ease-out should be > 50.
	got50 := tr.Evaluate(500)
	if got50 <= 50.0 {
		t.Errorf("Evaluate(500) with ease-out should be > 50, got %v", got50)
	}
}

func TestTrackEvaluateMultiKeyframe(t *testing.T) {
	tr := Track{
		Param: "opacity",
		Keyframes: []Keyframe{
			{TimeMs: 0, Value: 0.0, EaseOutX: 1.0, EaseOutY: 1.0},
			{TimeMs: 500, Value: 1.0, EaseOutX: 1.0, EaseOutY: 1.0},
			{TimeMs: 1000, Value: 0.5},
		},
	}

	// Between first and second: linear from 0→1.
	got := tr.Evaluate(250)
	if math.Abs(got-0.5) > 0.05 {
		t.Errorf("Evaluate(250) between kf0 and kf1 = %v, want ≈ 0.5", got)
	}

	// Between second and third: linear from 1→0.5.
	got2 := tr.Evaluate(750)
	if math.Abs(got2-0.75) > 0.05 {
		t.Errorf("Evaluate(750) between kf1 and kf2 = %v, want ≈ 0.75", got2)
	}
}

// ---------- EvaluateTransform ----------

func TestEvaluateTransform(t *testing.T) {
	anim := Animation{
		Name:       "slide-in",
		DurationMs: 1000,
		Tracks: []Track{
			{
				Param:     "positionX",
				SlotIndex: 0,
				Keyframes: []Keyframe{
					{TimeMs: 0, Value: -100.0, EaseOutX: 1.0, EaseOutY: 1.0},
					{TimeMs: 1000, Value: 0.0},
				},
			},
			{
				Param:     "rotationDeg",
				SlotIndex: 0,
				Keyframes: []Keyframe{
					{TimeMs: 0, Value: 45.0, EaseOutX: 1.0, EaseOutY: 1.0},
					{TimeMs: 1000, Value: 0.0},
				},
			},
		},
	}

	base := DefaultTransform(0, 0, 1920, 1080)
	result := anim.EvaluateTransform(0, 500, base)

	// At t=500ms (midpoint), linear interpolation should give -50 for posX
	// and 22.5 for rotation.
	if math.Abs(result.PositionX-(-50.0)) > 1.0 {
		t.Errorf("PositionX at 500ms = %v, want ≈ -50.0", result.PositionX)
	}
	if math.Abs(result.RotationDeg-22.5) > 1.0 {
		t.Errorf("RotationDeg at 500ms = %v, want ≈ 22.5", result.RotationDeg)
	}

	// Width and Height should remain from base.
	if result.Width != 1920 {
		t.Errorf("Width should be unchanged, got %v", result.Width)
	}
	if result.Height != 1080 {
		t.Errorf("Height should be unchanged, got %v", result.Height)
	}
}

func TestEvaluateTransformUnknownParam(t *testing.T) {
	anim := Animation{
		Name:       "test",
		DurationMs: 1000,
		Tracks: []Track{
			{
				Param:     "unknownField",
				SlotIndex: 0,
				Keyframes: []Keyframe{
					{TimeMs: 0, Value: 100.0},
				},
			},
		},
	}

	base := DefaultTransform(10, 20, 1920, 1080)
	result := anim.EvaluateTransform(0, 500, base)

	// Unknown param should not change any field.
	if result.PositionX != 10 || result.PositionY != 20 {
		t.Errorf("Unknown param modified transform: %+v", result)
	}
}

func TestEvaluateTransformWrongSlot(t *testing.T) {
	anim := Animation{
		Name:       "test",
		DurationMs: 1000,
		Tracks: []Track{
			{
				Param:     "positionX",
				SlotIndex: 1,
				Keyframes: []Keyframe{
					{TimeMs: 0, Value: 999.0},
				},
			},
		},
	}

	base := DefaultTransform(10, 20, 1920, 1080)
	result := anim.EvaluateTransform(0, 500, base)

	// Track for slot 1 should not affect slot 0.
	if result.PositionX != 10 {
		t.Errorf("Track for slot 1 affected slot 0: positionX = %v", result.PositionX)
	}
}

func TestEvaluateTransformAllParams(t *testing.T) {
	// Test every supported param to ensure the switch mapping works.
	params := []struct {
		name string
		get  func(Transform) float64
	}{
		{"positionX", func(tr Transform) float64 { return tr.PositionX }},
		{"positionY", func(tr Transform) float64 { return tr.PositionY }},
		{"width", func(tr Transform) float64 { return tr.Width }},
		{"height", func(tr Transform) float64 { return tr.Height }},
		{"rotationDeg", func(tr Transform) float64 { return tr.RotationDeg }},
		{"perspectiveX", func(tr Transform) float64 { return tr.PerspectiveX }},
		{"perspectiveY", func(tr Transform) float64 { return tr.PerspectiveY }},
		{"opacity", func(tr Transform) float64 { return tr.Opacity }},
		{"anchorX", func(tr Transform) float64 { return tr.AnchorX }},
		{"anchorY", func(tr Transform) float64 { return tr.AnchorY }},
		{"cropTop", func(tr Transform) float64 { return tr.CropTop }},
		{"cropBottom", func(tr Transform) float64 { return tr.CropBottom }},
		{"cropLeft", func(tr Transform) float64 { return tr.CropLeft }},
		{"cropRight", func(tr Transform) float64 { return tr.CropRight }},
		{"cropSoftness", func(tr Transform) float64 { return tr.CropSoftness }},
		{"defocusRadius", func(tr Transform) float64 { return tr.DefocusRadius }},
		{"border.width", func(tr Transform) float64 { return tr.Border.Width }},
		{"border.cornerRadius", func(tr Transform) float64 { return tr.Border.CornerRadius }},
		{"border.softness", func(tr Transform) float64 { return tr.Border.Softness }},
		{"shadow.offsetX", func(tr Transform) float64 { return tr.Shadow.OffsetX }},
		{"shadow.offsetY", func(tr Transform) float64 { return tr.Shadow.OffsetY }},
		{"shadow.blur", func(tr Transform) float64 { return tr.Shadow.Blur }},
		{"shadow.opacity", func(tr Transform) float64 { return tr.Shadow.Opacity }},
	}

	for _, p := range params {
		t.Run(p.name, func(t *testing.T) {
			anim := Animation{
				Name:       "test-" + p.name,
				DurationMs: 1000,
				Tracks: []Track{
					{
						Param:     p.name,
						SlotIndex: 0,
						Keyframes: []Keyframe{
							{TimeMs: 0, Value: 77.0},
						},
					},
				},
			}
			base := Transform{} // Zero transform.
			result := anim.EvaluateTransform(0, 500, base)
			got := p.get(result)
			if got != 77.0 {
				t.Errorf("param %q: got %v, want 77.0", p.name, got)
			}
		})
	}
}

// ---------- ResolveTimeMs ----------

func TestResolveTimeMsClamping(t *testing.T) {
	a := Animation{DurationMs: 1000, Loop: false}

	if got := a.ResolveTimeMs(-100); got != 0 {
		t.Errorf("ResolveTimeMs(-100) = %v, want 0", got)
	}
	if got := a.ResolveTimeMs(500); got != 500 {
		t.Errorf("ResolveTimeMs(500) = %v, want 500", got)
	}
	if got := a.ResolveTimeMs(1500); got != 1000 {
		t.Errorf("ResolveTimeMs(1500) = %v, want 1000", got)
	}
}

func TestResolveTimeMsLooping(t *testing.T) {
	a := Animation{DurationMs: 1000, Loop: true}

	if got := a.ResolveTimeMs(500); got != 500 {
		t.Errorf("ResolveTimeMs(500) = %v, want 500", got)
	}
	if got := a.ResolveTimeMs(1500); got != 500 {
		t.Errorf("ResolveTimeMs(1500) = %v, want 500", got)
	}
	if got := a.ResolveTimeMs(2000); got != 0 {
		t.Errorf("ResolveTimeMs(2000) = %v, want 0", got)
	}
}

// ---------- IsComplete ----------

func TestIsComplete(t *testing.T) {
	a := Animation{DurationMs: 1000, Loop: false}
	if a.IsComplete(999) {
		t.Error("IsComplete(999) should be false")
	}
	if !a.IsComplete(1000) {
		t.Error("IsComplete(1000) should be true")
	}
	if !a.IsComplete(2000) {
		t.Error("IsComplete(2000) should be true")
	}
}

func TestIsCompleteLooping(t *testing.T) {
	a := Animation{DurationMs: 1000, Loop: true}
	if a.IsComplete(0) {
		t.Error("Looping: IsComplete(0) should be false")
	}
	if a.IsComplete(1000) {
		t.Error("Looping: IsComplete(1000) should be false")
	}
	if a.IsComplete(999999) {
		t.Error("Looping: IsComplete(999999) should be false")
	}
}
