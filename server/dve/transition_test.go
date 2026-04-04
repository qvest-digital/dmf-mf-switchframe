package dve

import (
	"math"
	"testing"
)

const (
	testW = 1920
	testH = 1080
)

func approxEqual(a, b, epsilon float64) bool {
	return math.Abs(a-b) < epsilon
}

// --- Push transitions ---

func TestPushLeft_Position0(t *testing.T) {
	fn := PushTransition("left")
	prog, prev := fn(0, testW, testH)

	// Program at (0,0) fullscreen
	if prog.PositionX != 0 {
		t.Errorf("program PositionX = %v, want 0", prog.PositionX)
	}
	if prog.PositionY != 0 {
		t.Errorf("program PositionY = %v, want 0", prog.PositionY)
	}
	if prog.Width != testW {
		t.Errorf("program Width = %v, want %v", prog.Width, testW)
	}
	if prog.Height != testH {
		t.Errorf("program Height = %v, want %v", prog.Height, testH)
	}

	// Preview at (frameW, 0) — off-screen right
	if prev.PositionX != float64(testW) {
		t.Errorf("preview PositionX = %v, want %v", prev.PositionX, testW)
	}
	if prev.PositionY != 0 {
		t.Errorf("preview PositionY = %v, want 0", prev.PositionY)
	}
}

func TestPushLeft_Position1(t *testing.T) {
	fn := PushTransition("left")
	prog, prev := fn(1, testW, testH)

	// Program at (-frameW, 0) — off-screen left
	if prog.PositionX != float64(-testW) {
		t.Errorf("program PositionX = %v, want %v", prog.PositionX, -testW)
	}
	if prog.PositionY != 0 {
		t.Errorf("program PositionY = %v, want 0", prog.PositionY)
	}

	// Preview at (0, 0) fullscreen
	if prev.PositionX != 0 {
		t.Errorf("preview PositionX = %v, want 0", prev.PositionX)
	}
	if prev.PositionY != 0 {
		t.Errorf("preview PositionY = %v, want 0", prev.PositionY)
	}
}

func TestPushLeft_Position05(t *testing.T) {
	fn := PushTransition("left")
	prog, prev := fn(0.5, testW, testH)

	// Program at (-frameW/2, 0)
	wantProgX := float64(-testW) * 0.5
	if !approxEqual(prog.PositionX, wantProgX, 1) {
		t.Errorf("program PositionX = %v, want %v", prog.PositionX, wantProgX)
	}
	// Preview at (frameW/2, 0)
	wantPrevX := float64(testW) * 0.5
	if !approxEqual(prev.PositionX, wantPrevX, 1) {
		t.Errorf("preview PositionX = %v, want %v", prev.PositionX, wantPrevX)
	}
}

func TestPushRight(t *testing.T) {
	fn := PushTransition("right")

	prog0, prev0 := fn(0, testW, testH)
	// Program at (0,0), preview at (-frameW, 0)
	if prog0.PositionX != 0 {
		t.Errorf("pos=0: program PositionX = %v, want 0", prog0.PositionX)
	}
	if prev0.PositionX != float64(-testW) {
		t.Errorf("pos=0: preview PositionX = %v, want %v", prev0.PositionX, -testW)
	}

	prog1, prev1 := fn(1, testW, testH)
	// Program at (frameW, 0), preview at (0, 0)
	if prog1.PositionX != float64(testW) {
		t.Errorf("pos=1: program PositionX = %v, want %v", prog1.PositionX, testW)
	}
	if prev1.PositionX != 0 {
		t.Errorf("pos=1: preview PositionX = %v, want 0", prev1.PositionX)
	}
}

func TestPushUp(t *testing.T) {
	fn := PushTransition("up")

	prog0, prev0 := fn(0, testW, testH)
	if prog0.PositionY != 0 {
		t.Errorf("pos=0: program PositionY = %v, want 0", prog0.PositionY)
	}
	if prev0.PositionY != float64(testH) {
		t.Errorf("pos=0: preview PositionY = %v, want %v", prev0.PositionY, testH)
	}

	prog1, prev1 := fn(1, testW, testH)
	if prog1.PositionY != float64(-testH) {
		t.Errorf("pos=1: program PositionY = %v, want %v", prog1.PositionY, -testH)
	}
	if prev1.PositionY != 0 {
		t.Errorf("pos=1: preview PositionY = %v, want 0", prev1.PositionY)
	}
}

func TestPushDown(t *testing.T) {
	fn := PushTransition("down")

	prog0, prev0 := fn(0, testW, testH)
	if prog0.PositionY != 0 {
		t.Errorf("pos=0: program PositionY = %v, want 0", prog0.PositionY)
	}
	if prev0.PositionY != float64(-testH) {
		t.Errorf("pos=0: preview PositionY = %v, want %v", prev0.PositionY, -testH)
	}

	prog1, prev1 := fn(1, testW, testH)
	if prog1.PositionY != float64(testH) {
		t.Errorf("pos=1: program PositionY = %v, want %v", prog1.PositionY, testH)
	}
	if prev1.PositionY != 0 {
		t.Errorf("pos=1: preview PositionY = %v, want 0", prev1.PositionY)
	}
}

// --- Squeeze transition ---

func TestSqueeze_Position0(t *testing.T) {
	fn := SqueezeTransition()
	prog, prev := fn(0, testW, testH)

	// Program fullscreen
	if prog.Width != float64(testW) {
		t.Errorf("program Width = %v, want %v", prog.Width, testW)
	}
	if prog.Height != float64(testH) {
		t.Errorf("program Height = %v, want %v", prog.Height, testH)
	}
	if prog.Opacity != 1.0 {
		t.Errorf("program Opacity = %v, want 1.0", prog.Opacity)
	}

	// Preview clamped to minimum (avoids zero-width transforms)
	if prev.Width != 0.001 {
		t.Errorf("preview Width = %v, want 0.001", prev.Width)
	}
	if prev.Height != 0.001 {
		t.Errorf("preview Height = %v, want 0.001", prev.Height)
	}
}

func TestSqueeze_Position1(t *testing.T) {
	fn := SqueezeTransition()
	prog, prev := fn(1, testW, testH)

	// Program clamped to minimum (avoids zero-width transforms)
	if prog.Width != 0.001 {
		t.Errorf("program Width = %v, want 0.001", prog.Width)
	}
	if prog.Height != 0.001 {
		t.Errorf("program Height = %v, want 0.001", prog.Height)
	}

	// Preview fullscreen
	if prev.Width != float64(testW) {
		t.Errorf("preview Width = %v, want %v", prev.Width, testW)
	}
	if prev.Height != float64(testH) {
		t.Errorf("preview Height = %v, want %v", prev.Height, testH)
	}
	if prev.Opacity != 1.0 {
		t.Errorf("preview Opacity = %v, want 1.0", prev.Opacity)
	}
}

func TestSqueeze_Position05(t *testing.T) {
	fn := SqueezeTransition()
	prog, prev := fn(0.5, testW, testH)

	// Both at 50%
	halfW := float64(testW) * 0.5
	halfH := float64(testH) * 0.5
	if !approxEqual(prog.Width, halfW, 1) {
		t.Errorf("program Width = %v, want ~%v", prog.Width, halfW)
	}
	if !approxEqual(prog.Height, halfH, 1) {
		t.Errorf("program Height = %v, want ~%v", prog.Height, halfH)
	}
	if !approxEqual(prev.Width, halfW, 1) {
		t.Errorf("preview Width = %v, want ~%v", prev.Width, halfW)
	}
	if !approxEqual(prev.Height, halfH, 1) {
		t.Errorf("preview Height = %v, want ~%v", prev.Height, halfH)
	}
}

// --- Spin transition ---

func TestSpin_Position0(t *testing.T) {
	fn := SpinTransition()
	prog, prev := fn(0, testW, testH)

	// Program no rotation, full size, full opacity
	if prog.RotationDeg != 0 {
		t.Errorf("program RotationDeg = %v, want 0", prog.RotationDeg)
	}
	if prog.Width != float64(testW) {
		t.Errorf("program Width = %v, want %v", prog.Width, testW)
	}
	if prog.Opacity != 1.0 {
		t.Errorf("program Opacity = %v, want 1.0", prog.Opacity)
	}

	// Preview at -360 rotation, zero size, zero opacity
	if prev.RotationDeg != -360 {
		t.Errorf("preview RotationDeg = %v, want -360", prev.RotationDeg)
	}
	if prev.Width != 0 {
		t.Errorf("preview Width = %v, want 0", prev.Width)
	}
	if prev.Opacity != 0 {
		t.Errorf("preview Opacity = %v, want 0", prev.Opacity)
	}

	// Anchors at center
	if prog.AnchorX != 0.5 || prog.AnchorY != 0.5 {
		t.Errorf("program anchor = (%v,%v), want (0.5,0.5)", prog.AnchorX, prog.AnchorY)
	}
	if prev.AnchorX != 0.5 || prev.AnchorY != 0.5 {
		t.Errorf("preview anchor = (%v,%v), want (0.5,0.5)", prev.AnchorX, prev.AnchorY)
	}
}

func TestSpin_Position1(t *testing.T) {
	fn := SpinTransition()
	prog, prev := fn(1, testW, testH)

	// Program at 360 rotation, zero size, zero opacity
	if prog.RotationDeg != 360 {
		t.Errorf("program RotationDeg = %v, want 360", prog.RotationDeg)
	}
	if prog.Width != 0 {
		t.Errorf("program Width = %v, want 0", prog.Width)
	}
	if prog.Opacity != 0 {
		t.Errorf("program Opacity = %v, want 0", prog.Opacity)
	}

	// Preview no rotation, full size, full opacity
	if prev.RotationDeg != 0 {
		t.Errorf("preview RotationDeg = %v, want 0", prev.RotationDeg)
	}
	if prev.Width != float64(testW) {
		t.Errorf("preview Width = %v, want %v", prev.Width, testW)
	}
	if prev.Opacity != 1.0 {
		t.Errorf("preview Opacity = %v, want 1.0", prev.Opacity)
	}
}

// --- Zoom transition ---

func TestZoom_Position0(t *testing.T) {
	fn := ZoomTransition()
	prog, prev := fn(0, testW, testH)

	// Program fullscreen, full opacity
	if prog.Width != float64(testW) {
		t.Errorf("program Width = %v, want %v", prog.Width, testW)
	}
	if prog.Opacity != 1.0 {
		t.Errorf("program Opacity = %v, want 1.0", prog.Opacity)
	}

	// Preview zero size
	if prev.Width != 0 {
		t.Errorf("preview Width = %v, want 0", prev.Width)
	}
	if prev.Height != 0 {
		t.Errorf("preview Height = %v, want 0", prev.Height)
	}
}

func TestZoom_Position1(t *testing.T) {
	fn := ZoomTransition()
	prog, prev := fn(1, testW, testH)

	// Program faded out
	if prog.Opacity != 0 {
		t.Errorf("program Opacity = %v, want 0", prog.Opacity)
	}

	// Preview fullscreen
	if prev.Width != float64(testW) {
		t.Errorf("preview Width = %v, want %v", prev.Width, testW)
	}
	if prev.Height != float64(testH) {
		t.Errorf("preview Height = %v, want %v", prev.Height, testH)
	}
	if prev.PositionX != 0 {
		t.Errorf("preview PositionX = %v, want 0", prev.PositionX)
	}
	if prev.PositionY != 0 {
		t.Errorf("preview PositionY = %v, want 0", prev.PositionY)
	}
}

// --- Fly transition ---

func TestFly_Position0(t *testing.T) {
	fn := FlyTransition("left", "right")
	prog, prev := fn(0, testW, testH)

	// Program at (0,0) fullscreen
	if prog.PositionX != 0 {
		t.Errorf("program PositionX = %v, want 0", prog.PositionX)
	}
	if prog.Width != float64(testW) {
		t.Errorf("program Width = %v, want %v", prog.Width, testW)
	}

	// Preview off-screen right
	if prev.PositionX != float64(testW) {
		t.Errorf("preview PositionX = %v, want %v", prev.PositionX, testW)
	}
}

func TestFly_Position1(t *testing.T) {
	fn := FlyTransition("left", "right")
	prog, prev := fn(1, testW, testH)

	// Program off-screen left
	if prog.PositionX != float64(-testW) {
		t.Errorf("program PositionX = %v, want %v", prog.PositionX, -testW)
	}

	// Preview at (0,0) fullscreen
	if prev.PositionX != 0 {
		t.Errorf("preview PositionX = %v, want 0", prev.PositionX)
	}
	if prev.Width != float64(testW) {
		t.Errorf("preview Width = %v, want %v", prev.Width, testW)
	}
}

func TestFly_ScaleOvershoot(t *testing.T) {
	fn := FlyTransition("left", "right")
	prog, prev := fn(0.5, testW, testH)

	// At midpoint, both should have scale > 100% (overshoot)
	if prog.Width <= float64(testW) {
		t.Errorf("program Width at midpoint = %v, want > %v (overshoot)", prog.Width, testW)
	}
	if prev.Width <= float64(testW) {
		t.Errorf("preview Width at midpoint = %v, want > %v (overshoot)", prev.Width, testW)
	}
}

// --- DVETransitionForType ---

func TestDVETransitionForType_KnownTypes(t *testing.T) {
	knownTypes := []string{
		"dve-push-left",
		"dve-push-right",
		"dve-push-up",
		"dve-push-down",
		"dve-squeeze",
		"dve-spin",
		"dve-zoom",
		"dve-fly",
	}
	for _, typ := range knownTypes {
		t.Run(typ, func(t *testing.T) {
			fn := DVETransitionForType(typ)
			if fn == nil {
				t.Errorf("DVETransitionForType(%q) = nil, want non-nil", typ)
			}
		})
	}
}

func TestDVETransitionForType_Unknown(t *testing.T) {
	fn := DVETransitionForType("mix")
	if fn != nil {
		t.Errorf("DVETransitionForType(\"mix\") = non-nil, want nil")
	}

	fn = DVETransitionForType("unknown")
	if fn != nil {
		t.Errorf("DVETransitionForType(\"unknown\") = non-nil, want nil")
	}
}

// --- IsDVETransition ---

func TestIsDVETransition(t *testing.T) {
	tests := []struct {
		typ  string
		want bool
	}{
		{"dve-push-left", true},
		{"dve-push-right", true},
		{"dve-push-up", true},
		{"dve-push-down", true},
		{"dve-squeeze", true},
		{"dve-spin", true},
		{"dve-zoom", true},
		{"dve-fly", true},
		{"mix", false},
		{"dip", false},
		{"wipe", false},
		{"stinger", false},
		{"", false},
	}
	for _, tt := range tests {
		t.Run(tt.typ, func(t *testing.T) {
			got := IsDVETransition(tt.typ)
			if got != tt.want {
				t.Errorf("IsDVETransition(%q) = %v, want %v", tt.typ, got, tt.want)
			}
		})
	}
}

// --- Push preserves full frame size ---

func TestPush_FullFrameSize(t *testing.T) {
	for _, dir := range []string{"left", "right", "up", "down"} {
		t.Run(dir, func(t *testing.T) {
			fn := PushTransition(dir)
			for _, pos := range []float64{0, 0.25, 0.5, 0.75, 1.0} {
				prog, prev := fn(pos, testW, testH)
				if prog.Width != float64(testW) || prog.Height != float64(testH) {
					t.Errorf("dir=%s pos=%v: program size = (%v,%v), want (%v,%v)",
						dir, pos, prog.Width, prog.Height, testW, testH)
				}
				if prev.Width != float64(testW) || prev.Height != float64(testH) {
					t.Errorf("dir=%s pos=%v: preview size = (%v,%v), want (%v,%v)",
						dir, pos, prev.Width, prev.Height, testW, testH)
				}
			}
		})
	}
}

// --- Squeeze centered ---

func TestSqueeze_Centered(t *testing.T) {
	fn := SqueezeTransition()
	prog, prev := fn(0.5, testW, testH)

	// Both should be centered in the frame
	halfW := float64(testW) * 0.5
	halfH := float64(testH) * 0.5

	// Program center
	progCenterX := prog.PositionX + prog.Width/2
	progCenterY := prog.PositionY + prog.Height/2
	if !approxEqual(progCenterX, float64(testW)/2, 1) {
		t.Errorf("program center X = %v, want ~%v", progCenterX, float64(testW)/2)
	}
	if !approxEqual(progCenterY, float64(testH)/2, 1) {
		t.Errorf("program center Y = %v, want ~%v", progCenterY, float64(testH)/2)
	}

	// Preview center
	prevCenterX := prev.PositionX + prev.Width/2
	prevCenterY := prev.PositionY + prev.Height/2
	if !approxEqual(prevCenterX, float64(testW)/2, 1) {
		t.Errorf("preview center X = %v, want ~%v", prevCenterX, float64(testW)/2)
	}
	if !approxEqual(prevCenterY, float64(testH)/2, 1) {
		t.Errorf("preview center Y = %v, want ~%v", prevCenterY, float64(testH)/2)
	}
	_ = halfW
	_ = halfH
}

// --- Spin anchors ---

func TestSpin_AnchorsCentered(t *testing.T) {
	fn := SpinTransition()
	for _, pos := range []float64{0, 0.25, 0.5, 0.75, 1.0} {
		prog, prev := fn(pos, testW, testH)
		if prog.AnchorX != 0.5 || prog.AnchorY != 0.5 {
			t.Errorf("pos=%v: program anchor = (%v,%v), want (0.5,0.5)", pos, prog.AnchorX, prog.AnchorY)
		}
		if prev.AnchorX != 0.5 || prev.AnchorY != 0.5 {
			t.Errorf("pos=%v: preview anchor = (%v,%v), want (0.5,0.5)", pos, prev.AnchorX, prev.AnchorY)
		}
	}
}

// --- Zoom centered preview ---

func TestZoom_PreviewCentered(t *testing.T) {
	fn := ZoomTransition()
	_, prev := fn(0.5, testW, testH)

	// Preview should be centered
	centerX := prev.PositionX + prev.Width/2
	centerY := prev.PositionY + prev.Height/2
	if !approxEqual(centerX, float64(testW)/2, 1) {
		t.Errorf("preview center X = %v, want ~%v", centerX, float64(testW)/2)
	}
	if !approxEqual(centerY, float64(testH)/2, 1) {
		t.Errorf("preview center Y = %v, want ~%v", centerY, float64(testH)/2)
	}
}
