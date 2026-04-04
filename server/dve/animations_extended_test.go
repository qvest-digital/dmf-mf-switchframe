package dve

import "testing"

// ============================================================================
// B3: Extended Animation Tests
// ============================================================================

func TestAnimSlideInLeft(t *testing.T) {
	t.Parallel()
	anim := AnimSlideInLeft(0, 500)
	if anim == nil {
		t.Fatal("AnimSlideInLeft returned nil")
	}
	if anim.DurationMs != 500 {
		t.Errorf("duration = %d, want 500", anim.DurationMs)
	}
	if anim.Name != "slide-in-left" {
		t.Errorf("name = %q, want %q", anim.Name, "slide-in-left")
	}

	// Should have a positionX track
	hasPositionX := false
	for _, tr := range anim.Tracks {
		if tr.Param == "positionX" {
			hasPositionX = true
			// At t=0: positionX should be off-screen left (negative)
			val0 := tr.Evaluate(0)
			if val0 >= 0 {
				t.Errorf("positionX at t=0 = %f, want negative (off-screen left)", val0)
			}
			// At t=duration: positionX should be at target (0)
			valEnd := tr.Evaluate(500)
			if abs64(valEnd) > 1 {
				t.Errorf("positionX at t=500 = %f, want ~0", valEnd)
			}
		}
	}
	if !hasPositionX {
		t.Error("missing positionX track")
	}
}

func TestAnimSlideInRight(t *testing.T) {
	t.Parallel()
	anim := AnimSlideInRight(0, 500)
	if anim == nil {
		t.Fatal("AnimSlideInRight returned nil")
	}
	if anim.DurationMs != 500 {
		t.Errorf("duration = %d, want 500", anim.DurationMs)
	}
	if anim.Name != "slide-in-right" {
		t.Errorf("name = %q, want %q", anim.Name, "slide-in-right")
	}

	for _, tr := range anim.Tracks {
		if tr.Param == "positionX" {
			// At t=0: should be off-screen right (positive)
			val0 := tr.Evaluate(0)
			if val0 <= 0 {
				t.Errorf("positionX at t=0 = %f, want positive (off-screen right)", val0)
			}
			// At t=duration: should be at target
			valEnd := tr.Evaluate(500)
			if abs64(valEnd) > 1 {
				t.Errorf("positionX at t=500 = %f, want ~0", valEnd)
			}
		}
	}
}

func TestAnimSlideInTop(t *testing.T) {
	t.Parallel()
	anim := AnimSlideInTop(0, 600)
	if anim == nil {
		t.Fatal("AnimSlideInTop returned nil")
	}
	if anim.DurationMs != 600 {
		t.Errorf("duration = %d, want 600", anim.DurationMs)
	}
	if anim.Name != "slide-in-top" {
		t.Errorf("name = %q, want %q", anim.Name, "slide-in-top")
	}

	for _, tr := range anim.Tracks {
		if tr.Param == "positionY" {
			// At t=0: should be off-screen top (negative)
			val0 := tr.Evaluate(0)
			if val0 >= 0 {
				t.Errorf("positionY at t=0 = %f, want negative (off-screen top)", val0)
			}
			valEnd := tr.Evaluate(600)
			if abs64(valEnd) > 1 {
				t.Errorf("positionY at t=600 = %f, want ~0", valEnd)
			}
		}
	}
}

func TestAnimSlideInBottom(t *testing.T) {
	t.Parallel()
	anim := AnimSlideInBottom(0, 600)
	if anim == nil {
		t.Fatal("AnimSlideInBottom returned nil")
	}
	if anim.DurationMs != 600 {
		t.Errorf("duration = %d, want 600", anim.DurationMs)
	}
	if anim.Name != "slide-in-bottom" {
		t.Errorf("name = %q, want %q", anim.Name, "slide-in-bottom")
	}

	for _, tr := range anim.Tracks {
		if tr.Param == "positionY" {
			// At t=0: should be off-screen bottom (positive)
			val0 := tr.Evaluate(0)
			if val0 <= 0 {
				t.Errorf("positionY at t=0 = %f, want positive (off-screen bottom)", val0)
			}
			valEnd := tr.Evaluate(600)
			if abs64(valEnd) > 1 {
				t.Errorf("positionY at t=600 = %f, want ~0", valEnd)
			}
		}
	}
}

func TestAnimElasticIn(t *testing.T) {
	t.Parallel()
	anim := AnimElasticIn(0, 100, 200, 800)
	if anim == nil {
		t.Fatal("AnimElasticIn returned nil")
	}
	if anim.DurationMs != 800 {
		t.Errorf("duration = %d, want 800", anim.DurationMs)
	}
	if anim.Name != "elastic-in" {
		t.Errorf("name = %q, want %q", anim.Name, "elastic-in")
	}

	// Should have positionX and positionY tracks
	hasX, hasY := false, false
	for _, tr := range anim.Tracks {
		if tr.Param == "positionX" {
			hasX = true
			// At t=duration: should be at target X=100
			valEnd := tr.Evaluate(800)
			if abs64(valEnd-100) > 1 {
				t.Errorf("positionX at t=800 = %f, want ~100", valEnd)
			}
			// Should have 4 keyframes (start, overshoot, undershoot, settle)
			if len(tr.Keyframes) < 4 {
				t.Errorf("positionX should have >= 4 keyframes, got %d", len(tr.Keyframes))
			}
		}
		if tr.Param == "positionY" {
			hasY = true
			valEnd := tr.Evaluate(800)
			if abs64(valEnd-200) > 1 {
				t.Errorf("positionY at t=800 = %f, want ~200", valEnd)
			}
		}
	}
	if !hasX {
		t.Error("missing positionX track")
	}
	if !hasY {
		t.Error("missing positionY track")
	}
}

func TestAnimFlipIn(t *testing.T) {
	t.Parallel()
	anim := AnimFlipIn(0, 700)
	if anim == nil {
		t.Fatal("AnimFlipIn returned nil")
	}
	if anim.DurationMs != 700 {
		t.Errorf("duration = %d, want 700", anim.DurationMs)
	}
	if anim.Name != "flip-in" {
		t.Errorf("name = %q, want %q", anim.Name, "flip-in")
	}

	// Should have a rotateY track (3D)
	hasRotateY := false
	hasOpacity := false
	for _, tr := range anim.Tracks {
		if tr.Param == "rotateY" {
			hasRotateY = true
			// At t=0: rotateY should be 180
			val0 := tr.Evaluate(0)
			if abs64(val0-180) > 1 {
				t.Errorf("rotateY at t=0 = %f, want 180", val0)
			}
			// At t=duration: rotateY should be 0
			valEnd := tr.Evaluate(700)
			if abs64(valEnd) > 1 {
				t.Errorf("rotateY at t=700 = %f, want 0", valEnd)
			}
		}
		if tr.Param == "opacity" {
			hasOpacity = true
			// At t=0: opacity should be 0
			val0 := tr.Evaluate(0)
			if val0 > 0.1 {
				t.Errorf("opacity at t=0 = %f, want ~0", val0)
			}
		}
	}
	if !hasRotateY {
		t.Error("missing rotateY track")
	}
	if !hasOpacity {
		t.Error("missing opacity track")
	}
}

func TestAnimBlurIn(t *testing.T) {
	t.Parallel()
	anim := AnimBlurIn(0, 500)
	if anim == nil {
		t.Fatal("AnimBlurIn returned nil")
	}
	if anim.DurationMs != 500 {
		t.Errorf("duration = %d, want 500", anim.DurationMs)
	}
	if anim.Name != "blur-in" {
		t.Errorf("name = %q, want %q", anim.Name, "blur-in")
	}

	// Should have defocusRadius and opacity tracks
	hasBlur := false
	hasOpacity := false
	for _, tr := range anim.Tracks {
		if tr.Param == "defocusRadius" {
			hasBlur = true
			// At t=0: max blur
			val0 := tr.Evaluate(0)
			if val0 < 10 {
				t.Errorf("defocusRadius at t=0 = %f, want >= 10", val0)
			}
			// At t=duration: no blur
			valEnd := tr.Evaluate(500)
			if valEnd > 0.1 {
				t.Errorf("defocusRadius at t=500 = %f, want ~0", valEnd)
			}
		}
		if tr.Param == "opacity" {
			hasOpacity = true
			val0 := tr.Evaluate(0)
			if val0 > 0.1 {
				t.Errorf("opacity at t=0 = %f, want ~0", val0)
			}
			valEnd := tr.Evaluate(500)
			if valEnd < 0.9 {
				t.Errorf("opacity at t=500 = %f, want ~1", valEnd)
			}
		}
	}
	if !hasBlur {
		t.Error("missing defocusRadius track")
	}
	if !hasOpacity {
		t.Error("missing opacity track")
	}
}

func TestAnimSquishIn(t *testing.T) {
	t.Parallel()
	anim := AnimSquishIn(0, 480, 600)
	if anim == nil {
		t.Fatal("AnimSquishIn returned nil")
	}
	if anim.DurationMs != 600 {
		t.Errorf("duration = %d, want 600", anim.DurationMs)
	}
	if anim.Name != "squish-in" {
		t.Errorf("name = %q, want %q", anim.Name, "squish-in")
	}

	// Should have a width track
	hasWidth := false
	for _, tr := range anim.Tracks {
		if tr.Param == "width" {
			hasWidth = true
			// At t=0: width should be 0 (or near 0)
			val0 := tr.Evaluate(0)
			if val0 > 1 {
				t.Errorf("width at t=0 = %f, want ~0", val0)
			}
			// At t=duration: width should be targetW (480)
			valEnd := tr.Evaluate(600)
			if abs64(valEnd-480) > 1 {
				t.Errorf("width at t=600 = %f, want ~480", valEnd)
			}
		}
	}
	if !hasWidth {
		t.Error("missing width track")
	}
}

// ============================================================================
// AnimationCatalog count
// ============================================================================

func TestAnimationCatalogCount(t *testing.T) {
	t.Parallel()
	catalog := AnimationCatalog()
	if len(catalog) < 30 {
		t.Errorf("AnimationCatalog() has %d entries, want >= 30", len(catalog))
	}
}

func TestAnimationCatalogIncludesNewEntries(t *testing.T) {
	t.Parallel()
	catalog := AnimationCatalog()
	expected := []string{
		"slide-in-left", "slide-in-right", "slide-in-top", "slide-in-bottom",
		"elastic-in", "flip-in", "blur-in", "squish-in",
	}
	names := make(map[string]bool, len(catalog))
	for _, entry := range catalog {
		names[entry.Name] = true
	}
	for _, exp := range expected {
		if !names[exp] {
			t.Errorf("AnimationCatalog() missing %q", exp)
		}
	}
}

// ============================================================================
// All new animations return non-nil
// ============================================================================

func TestNewAnimationsAllNonNil(t *testing.T) {
	t.Parallel()
	anims := []*Animation{
		AnimSlideInLeft(0, 500),
		AnimSlideInRight(0, 500),
		AnimSlideInTop(0, 500),
		AnimSlideInBottom(0, 500),
		AnimElasticIn(0, 100, 200, 500),
		AnimFlipIn(0, 500),
		AnimBlurIn(0, 500),
		AnimSquishIn(0, 480, 500),
	}
	for i, a := range anims {
		if a == nil {
			t.Errorf("animation %d returned nil", i)
			continue
		}
		if len(a.Tracks) == 0 {
			t.Errorf("animation %d (%s) has no tracks", i, a.Name)
		}
	}
}
