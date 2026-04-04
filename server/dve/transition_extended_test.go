package dve

import (
	"math"
	"testing"
)

// ============================================================================
// Page Turn Transition
// ============================================================================

func TestPageTurnTransition(t *testing.T) {
	t.Parallel()
	fn := PageTurnTransition()
	if fn == nil {
		t.Fatal("PageTurnTransition returned nil")
	}

	// At 0: program should be full-screen, preview behind
	prog0, prev0 := fn(0.0, 1920, 1080)
	if prog0.Width < 0.9*1920 {
		t.Errorf("program at 0 should be full-screen, got width=%f", prog0.Width)
	}
	if prog0.Opacity < 0.9 {
		t.Errorf("program at 0 should be opaque, got opacity=%f", prog0.Opacity)
	}
	_ = prev0

	// At 1: preview should be full-screen
	prog1, prev1 := fn(1.0, 1920, 1080)
	if prev1.Width < 0.9*1920 {
		t.Errorf("preview at 1 should be full-screen, got width=%f", prev1.Width)
	}
	// Program should be turned away (3D rotation)
	if prog1.Mode3D != "3d" {
		t.Error("program should use 3D mode")
	}
	if prog1.Transform3D == nil {
		t.Fatal("program at 1 should have Transform3D set")
	}
	if prog1.Transform3D.RotateY > -30 {
		t.Errorf("program at 1 should have significant Y rotation, got %f", prog1.Transform3D.RotateY)
	}

	// At midpoint: both should have valid transforms
	progMid, prevMid := fn(0.5, 1920, 1080)
	if progMid.Width <= 0 || progMid.Height <= 0 {
		t.Error("program at 0.5 should have positive dimensions")
	}
	if prevMid.Width <= 0 || prevMid.Height <= 0 {
		t.Error("preview at 0.5 should have positive dimensions")
	}
}

func TestPageTurnTransition3DFields(t *testing.T) {
	t.Parallel()
	fn := PageTurnTransition()

	// Both sources should use 3D mode at midpoint
	prog, prev := fn(0.5, 1920, 1080)
	if prog.Mode3D != "3d" {
		t.Error("program at 0.5 should use 3D mode")
	}
	if prev.Mode3D != "3d" {
		t.Error("preview at 0.5 should use 3D mode")
	}
}

// ============================================================================
// Bounce Transition
// ============================================================================

func TestBounceTransition(t *testing.T) {
	t.Parallel()
	fn := BounceTransition()
	if fn == nil {
		t.Fatal("BounceTransition returned nil")
	}

	// At 0: program should be full-screen at origin
	prog0, prev0 := fn(0.0, 1920, 1080)
	if abs64(prog0.PositionY) > 1 {
		t.Errorf("program at 0 should be at origin, got posY=%f", prog0.PositionY)
	}
	if prog0.Width < 0.9*1920 {
		t.Errorf("program at 0 should be full-screen, got width=%f", prog0.Width)
	}
	_ = prev0

	// At 1: preview should be settled at identity
	_, prev1 := fn(1.0, 1920, 1080)
	if abs64(prev1.PositionY) > 0.01 {
		t.Errorf("preview at 1.0 not settled: posY=%f", prev1.PositionY)
	}
	if abs64(prev1.Width-1920) > 1 {
		t.Errorf("preview at 1.0 not full: width=%f", prev1.Width)
	}

	// Program should move off-screen during transition
	progMid, _ := fn(0.5, 1920, 1080)
	if progMid.PositionY < 100 {
		t.Errorf("program at 0.5 should be bouncing downward, got posY=%f", progMid.PositionY)
	}
}

func TestBounceTransitionSpringPhysics(t *testing.T) {
	t.Parallel()
	fn := BounceTransition()

	// Check that preview shows overshoot (elastic behavior)
	// At ~0.7 the preview scale should slightly overshoot or positionY undershoot
	_, prev := fn(0.7, 1920, 1080)
	// Preview should be approaching full frame
	if prev.Width < 0.5*1920 {
		t.Errorf("preview at 0.7 should be mostly visible, got width=%f", prev.Width)
	}
}

// ============================================================================
// Blur Dissolve Transition
// ============================================================================

func TestBlurDissolveTransition(t *testing.T) {
	t.Parallel()
	fn := BlurDissolveTransition()
	if fn == nil {
		t.Fatal("BlurDissolveTransition returned nil")
	}

	// At 0: program no blur, full opacity
	prog0, prev0 := fn(0.0, 1920, 1080)
	if prog0.DefocusRadius > 0.1 {
		t.Errorf("program at 0 should have no blur, got %f", prog0.DefocusRadius)
	}
	if prog0.Opacity < 0.9 {
		t.Errorf("program at 0 should be opaque, got %f", prog0.Opacity)
	}
	if prev0.Opacity > 0.1 {
		t.Errorf("preview at 0 should be transparent, got %f", prev0.Opacity)
	}

	// At 0.5: both should have max blur
	prog5, prev5 := fn(0.5, 1920, 1080)
	if prog5.DefocusRadius < 5.0 {
		t.Errorf("program at 0.5 should be blurred, got %f", prog5.DefocusRadius)
	}
	if prev5.DefocusRadius < 5.0 {
		t.Errorf("preview at 0.5 should be blurred, got %f", prev5.DefocusRadius)
	}

	// At 1: preview no blur, full opacity
	prog1, prev1 := fn(1.0, 1920, 1080)
	if prev1.DefocusRadius > 0.1 {
		t.Errorf("preview at 1 should have no blur, got %f", prev1.DefocusRadius)
	}
	if prev1.Opacity < 0.9 {
		t.Errorf("preview at 1 should be opaque, got %f", prev1.Opacity)
	}
	if prog1.Opacity > 0.1 {
		t.Errorf("program at 1 should be transparent, got %f", prog1.Opacity)
	}
}

func TestBlurDissolvePositionsStayFullScreen(t *testing.T) {
	t.Parallel()
	fn := BlurDissolveTransition()

	// Both sources should remain full-screen throughout
	for _, pos := range []float64{0, 0.25, 0.5, 0.75, 1.0} {
		prog, prev := fn(pos, 1920, 1080)
		if abs64(prog.Width-1920) > 1 {
			t.Errorf("program at pos=%f should be full-screen width, got %f", pos, prog.Width)
		}
		if abs64(prev.Width-1920) > 1 {
			t.Errorf("preview at pos=%f should be full-screen width, got %f", pos, prev.Width)
		}
	}
}

// ============================================================================
// DVETransitionForType dispatch
// ============================================================================

func TestDVETransitionForTypePageTurn(t *testing.T) {
	t.Parallel()
	fn := DVETransitionForType("dve-page-turn")
	if fn == nil {
		t.Fatal("DVETransitionForType('dve-page-turn') returned nil")
	}
}

func TestDVETransitionForTypeBounce(t *testing.T) {
	t.Parallel()
	fn := DVETransitionForType("dve-bounce")
	if fn == nil {
		t.Fatal("DVETransitionForType('dve-bounce') returned nil")
	}
}

func TestDVETransitionForTypeBlurDissolve(t *testing.T) {
	t.Parallel()
	fn := DVETransitionForType("dve-blur-dissolve")
	if fn == nil {
		t.Fatal("DVETransitionForType('dve-blur-dissolve') returned nil")
	}
}

// ============================================================================
// AllTransitionTypes includes new types
// ============================================================================

func TestAllTransitionTypesIncludesNew(t *testing.T) {
	t.Parallel()
	types := AllTransitionTypes()
	expected := []string{"dve-page-turn", "dve-bounce", "dve-blur-dissolve"}
	for _, exp := range expected {
		found := false
		for _, typ := range types {
			if typ == exp {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("AllTransitionTypes() missing %q", exp)
		}
	}
}

// ============================================================================
// Edge cases: boundary positions
// ============================================================================

func TestNewTransitionsBoundaryPositions(t *testing.T) {
	t.Parallel()
	transitions := []struct {
		name string
		fn   DVETransitionFunc
	}{
		{"page-turn", PageTurnTransition()},
		{"bounce", BounceTransition()},
		{"blur-dissolve", BlurDissolveTransition()},
	}

	for _, tr := range transitions {
		tr := tr
		t.Run(tr.name, func(t *testing.T) {
			t.Parallel()
			for _, pos := range []float64{0.0, 0.001, 0.25, 0.5, 0.75, 0.999, 1.0} {
				prog, prev := tr.fn(pos, 1920, 1080)
				if math.IsNaN(prog.Width) || math.IsNaN(prog.Height) {
					t.Errorf("pos=%f: program has NaN dimensions", pos)
				}
				if math.IsNaN(prev.Width) || math.IsNaN(prev.Height) {
					t.Errorf("pos=%f: preview has NaN dimensions", pos)
				}
				if math.IsNaN(prog.Opacity) || math.IsNaN(prev.Opacity) {
					t.Errorf("pos=%f: has NaN opacity", pos)
				}
			}
		})
	}
}
