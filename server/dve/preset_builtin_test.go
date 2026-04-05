package dve

import (
	"math"
	"testing"
)

// TestBuiltinPIPPresetsRectMode verifies all four PIP corner presets use RectMode
// with correct OutputRect coordinates at 1920x1080.
func TestBuiltinPIPPresetsRectMode(t *testing.T) {
	const (
		fw     = 1920.0
		fh     = 1080.0
		pipW   = fw * 0.25 // 480
		pipH   = fh * 0.25 // 270
		margin = fw * 0.02 // 38.4
	)

	tests := []struct {
		id   string
		wantX, wantY float64
	}{
		{"pip-top-right", fw - pipW - margin, margin},
		{"pip-top-left", margin, margin},
		{"pip-bottom-right", fw - pipW - margin, fh - pipH - margin},
		{"pip-bottom-left", margin, fh - pipH - margin},
	}

	for _, tc := range tests {
		t.Run(tc.id, func(t *testing.T) {
			p := ResolveBuiltinPreset(tc.id, 1920, 1080)
			if p == nil {
				t.Fatalf("preset %q not found", tc.id)
			}
			if len(p.Slots) != 1 {
				t.Fatalf("expected 1 slot, got %d", len(p.Slots))
			}

			slot := p.Slots[0]
			tr := slot.Transform

			// Must use RectMode.
			if !tr.RectMode {
				t.Fatal("expected RectMode=true")
			}

			// OutputRect coordinates.
			if math.Abs(tr.OutputRect.X-tc.wantX) > 0.01 {
				t.Errorf("OutputRect.X = %v, want %v", tr.OutputRect.X, tc.wantX)
			}
			if math.Abs(tr.OutputRect.Y-tc.wantY) > 0.01 {
				t.Errorf("OutputRect.Y = %v, want %v", tr.OutputRect.Y, tc.wantY)
			}
			if math.Abs(tr.OutputRect.W-pipW) > 0.01 {
				t.Errorf("OutputRect.W = %v, want %v", tr.OutputRect.W, pipW)
			}
			if math.Abs(tr.OutputRect.H-pipH) > 0.01 {
				t.Errorf("OutputRect.H = %v, want %v", tr.OutputRect.H, pipH)
			}

			// ScaleMode and CropAnchor.
			if tr.ScaleMode != ScaleModeFill {
				t.Errorf("ScaleMode = %q, want %q", tr.ScaleMode, ScaleModeFill)
			}
			if tr.CropAnchor != [2]float64{0.5, 0.5} {
				t.Errorf("CropAnchor = %v, want [0.5, 0.5]", tr.CropAnchor)
			}

			// Border should still be present.
			if tr.Border.Width == 0 {
				t.Error("expected border on PIP preset")
			}

			// Slot should be enabled with ZOrder 1.
			if !slot.Enabled {
				t.Error("slot should be enabled")
			}
			if slot.ZOrder != 1 {
				t.Errorf("ZOrder = %d, want 1", slot.ZOrder)
			}

			// ResolveRect with a 16:9 source should produce same-aspect output (no crop).
			resolved := tr.ResolveRect(1920, 1080)
			if math.Abs(resolved.PositionX-tc.wantX) > 0.01 {
				t.Errorf("resolved PositionX = %v, want %v", resolved.PositionX, tc.wantX)
			}
			if math.Abs(resolved.PositionY-tc.wantY) > 0.01 {
				t.Errorf("resolved PositionY = %v, want %v", resolved.PositionY, tc.wantY)
			}
			if math.Abs(resolved.Width-pipW) > 0.01 {
				t.Errorf("resolved Width = %v, want %v", resolved.Width, pipW)
			}
			if math.Abs(resolved.Height-pipH) > 0.01 {
				t.Errorf("resolved Height = %v, want %v", resolved.Height, pipH)
			}
			// 16:9 source into 16:9 PIP (480/270 = 16:9): no crop.
			if resolved.CropTop != 0 || resolved.CropBottom != 0 || resolved.CropLeft != 0 || resolved.CropRight != 0 {
				t.Errorf("16:9 into 16:9 should not crop, got T=%v B=%v L=%v R=%v",
					resolved.CropTop, resolved.CropBottom, resolved.CropLeft, resolved.CropRight)
			}
		})
	}
}

// TestBuiltinPIPResolveRect_4x3Source verifies that PIP presets correctly
// crop a 4:3 source into a 16:9 output rect.
func TestBuiltinPIPResolveRect_4x3Source(t *testing.T) {
	p := ResolveBuiltinPreset("pip-top-right", 1920, 1080)
	if p == nil {
		t.Fatal("preset not found")
	}

	tr := p.Slots[0].Transform

	// 4:3 source (640x480) into 16:9 PIP (480x270).
	// Source aspect 1.333, output aspect 1.778.
	// Source is taller → crop top/bottom.
	// Excess: 480 - 640*(270/480) = 480 - 360 = 120 px.
	// Anchor 0.5 → CropTop=60, CropBottom=60.
	resolved := tr.ResolveRect(640, 480)

	if resolved.CropTop != 60 {
		t.Errorf("CropTop = %v, want 60", resolved.CropTop)
	}
	if resolved.CropBottom != 60 {
		t.Errorf("CropBottom = %v, want 60", resolved.CropBottom)
	}
	if resolved.CropLeft != 0 {
		t.Errorf("CropLeft = %v, want 0", resolved.CropLeft)
	}
	if resolved.CropRight != 0 {
		t.Errorf("CropRight = %v, want 0", resolved.CropRight)
	}
}

// TestBuiltinSideBySideRectMode verifies the side-by-side preset uses RectMode
// with two 50/50 horizontal slots.
func TestBuiltinSideBySideRectMode(t *testing.T) {
	p := ResolveBuiltinPreset("side-by-side", 1920, 1080)
	if p == nil {
		t.Fatal("preset not found")
	}
	if len(p.Slots) != 2 {
		t.Fatalf("expected 2 slots, got %d", len(p.Slots))
	}

	const (
		fw   = 1920.0
		fh   = 1080.0
		gap  = 4.0
		slotW = (fw - gap) / 2 // 958
		slotH = fh             // 1080
	)

	// Slot 0: left half.
	s0 := p.Slots[0]
	if !s0.Transform.RectMode {
		t.Fatal("slot 0: expected RectMode=true")
	}
	if s0.Transform.OutputRect.X != 0 {
		t.Errorf("slot 0 OutputRect.X = %v, want 0", s0.Transform.OutputRect.X)
	}
	if s0.Transform.OutputRect.Y != 0 {
		t.Errorf("slot 0 OutputRect.Y = %v, want 0", s0.Transform.OutputRect.Y)
	}
	if math.Abs(s0.Transform.OutputRect.W-slotW) > 0.01 {
		t.Errorf("slot 0 OutputRect.W = %v, want %v", s0.Transform.OutputRect.W, slotW)
	}
	if math.Abs(s0.Transform.OutputRect.H-slotH) > 0.01 {
		t.Errorf("slot 0 OutputRect.H = %v, want %v", s0.Transform.OutputRect.H, slotH)
	}
	if s0.Transform.ScaleMode != ScaleModeFill {
		t.Errorf("slot 0 ScaleMode = %q, want %q", s0.Transform.ScaleMode, ScaleModeFill)
	}
	if s0.Transform.CropAnchor != [2]float64{0.5, 0.5} {
		t.Errorf("slot 0 CropAnchor = %v, want [0.5, 0.5]", s0.Transform.CropAnchor)
	}

	// Slot 1: right half.
	s1 := p.Slots[1]
	if !s1.Transform.RectMode {
		t.Fatal("slot 1: expected RectMode=true")
	}
	expectedX1 := slotW + gap
	if math.Abs(s1.Transform.OutputRect.X-expectedX1) > 0.01 {
		t.Errorf("slot 1 OutputRect.X = %v, want %v", s1.Transform.OutputRect.X, expectedX1)
	}
	if s1.Transform.OutputRect.Y != 0 {
		t.Errorf("slot 1 OutputRect.Y = %v, want 0", s1.Transform.OutputRect.Y)
	}
	if math.Abs(s1.Transform.OutputRect.W-slotW) > 0.01 {
		t.Errorf("slot 1 OutputRect.W = %v, want %v", s1.Transform.OutputRect.W, slotW)
	}
	if s1.Transform.ScaleMode != ScaleModeFill {
		t.Errorf("slot 1 ScaleMode = %q, want %q", s1.Transform.ScaleMode, ScaleModeFill)
	}

	// Resolve both slots with 16:9 source — should produce correct position/size.
	r0 := s0.Transform.ResolveRect(1920, 1080)
	if r0.PositionX != 0 || r0.PositionY != 0 {
		t.Errorf("slot 0 resolved position = (%v, %v), want (0, 0)", r0.PositionX, r0.PositionY)
	}
	if math.Abs(r0.Width-slotW) > 0.01 {
		t.Errorf("slot 0 resolved Width = %v, want %v", r0.Width, slotW)
	}

	r1 := s1.Transform.ResolveRect(1920, 1080)
	if math.Abs(r1.PositionX-expectedX1) > 0.01 {
		t.Errorf("slot 1 resolved PositionX = %v, want %v", r1.PositionX, expectedX1)
	}
}

// TestBuiltinQuadRectMode verifies the quad preset uses RectMode with four 25% slots
// in a 2x2 grid.
func TestBuiltinQuadRectMode(t *testing.T) {
	p := ResolveBuiltinPreset("quad", 1920, 1080)
	if p == nil {
		t.Fatal("preset not found")
	}
	if len(p.Slots) != 4 {
		t.Fatalf("expected 4 slots, got %d", len(p.Slots))
	}

	const (
		fw    = 1920.0
		fh    = 1080.0
		gap   = 4.0
		slotW = (fw - gap) / 2 // 958
		slotH = (fh - gap) / 2 // 538
	)

	expected := []struct {
		x, y float64
	}{
		{0, 0},                    // top-left
		{slotW + gap, 0},         // top-right
		{0, slotH + gap},         // bottom-left
		{slotW + gap, slotH + gap}, // bottom-right
	}

	for i, exp := range expected {
		s := p.Slots[i]
		if !s.Transform.RectMode {
			t.Errorf("slot %d: expected RectMode=true", i)
		}
		if math.Abs(s.Transform.OutputRect.X-exp.x) > 0.01 {
			t.Errorf("slot %d OutputRect.X = %v, want %v", i, s.Transform.OutputRect.X, exp.x)
		}
		if math.Abs(s.Transform.OutputRect.Y-exp.y) > 0.01 {
			t.Errorf("slot %d OutputRect.Y = %v, want %v", i, s.Transform.OutputRect.Y, exp.y)
		}
		if math.Abs(s.Transform.OutputRect.W-slotW) > 0.01 {
			t.Errorf("slot %d OutputRect.W = %v, want %v", i, s.Transform.OutputRect.W, slotW)
		}
		if math.Abs(s.Transform.OutputRect.H-slotH) > 0.01 {
			t.Errorf("slot %d OutputRect.H = %v, want %v", i, s.Transform.OutputRect.H, slotH)
		}
		if s.Transform.ScaleMode != ScaleModeFill {
			t.Errorf("slot %d ScaleMode = %q, want %q", i, s.Transform.ScaleMode, ScaleModeFill)
		}
		if s.Transform.CropAnchor != [2]float64{0.5, 0.5} {
			t.Errorf("slot %d CropAnchor = %v, want [0.5, 0.5]", i, s.Transform.CropAnchor)
		}
		if !s.Enabled {
			t.Errorf("slot %d should be enabled", i)
		}

		// Resolve and verify position/size.
		resolved := s.Transform.ResolveRect(1920, 1080)
		if math.Abs(resolved.PositionX-exp.x) > 0.01 {
			t.Errorf("slot %d resolved PositionX = %v, want %v", i, resolved.PositionX, exp.x)
		}
		if math.Abs(resolved.PositionY-exp.y) > 0.01 {
			t.Errorf("slot %d resolved PositionY = %v, want %v", i, resolved.PositionY, exp.y)
		}
		if math.Abs(resolved.Width-slotW) > 0.01 {
			t.Errorf("slot %d resolved Width = %v, want %v", i, resolved.Width, slotW)
		}
		if math.Abs(resolved.Height-slotH) > 0.01 {
			t.Errorf("slot %d resolved Height = %v, want %v", i, resolved.Height, slotH)
		}
	}
}

// TestBuiltinThreeBoxNotRectMode verifies three-box is not modified (no RectMode).
func TestBuiltinThreeBoxNotRectMode(t *testing.T) {
	p := ResolveBuiltinPreset("three-box", 1920, 1080)
	if p == nil {
		t.Fatal("preset not found")
	}
	if len(p.Slots) != 3 {
		t.Fatalf("expected 3 slots, got %d", len(p.Slots))
	}
	for i, s := range p.Slots {
		if s.Transform.RectMode {
			t.Errorf("slot %d: three-box should NOT use RectMode", i)
		}
		// Should still have fill mode.
		if s.Transform.ScaleMode != ScaleModeFill {
			t.Errorf("slot %d ScaleMode = %q, want %q", i, s.Transform.ScaleMode, ScaleModeFill)
		}
	}
}

// TestBuiltinOverShoulderNotRectMode verifies over-shoulder is not modified.
func TestBuiltinOverShoulderNotRectMode(t *testing.T) {
	p := ResolveBuiltinPreset("over-shoulder", 1920, 1080)
	if p == nil {
		t.Fatal("preset not found")
	}
	if len(p.Slots) != 1 {
		t.Fatalf("expected 1 slot, got %d", len(p.Slots))
	}

	tr := p.Slots[0].Transform
	if tr.RectMode {
		t.Error("over-shoulder should NOT use RectMode")
	}
	// Verify its DVE effects are still present.
	if tr.RotationDeg == 0 {
		t.Error("expected rotation on over-shoulder")
	}
	if !tr.Shadow.Enabled {
		t.Error("expected shadow on over-shoulder")
	}
	if tr.Border.Width == 0 {
		t.Error("expected border on over-shoulder")
	}
}

// TestBuiltinPresetsValidateAfterRectMode verifies all presets pass validation
// after the RectMode update.
func TestBuiltinPresetsValidateAfterRectMode(t *testing.T) {
	presets := BuiltinPresets(1920, 1080)
	for _, p := range presets {
		for i, slot := range p.Slots {
			if err := ValidateSlot(slot); err != nil {
				t.Errorf("preset %q slot %d: validation failed: %v", p.ID, i, err)
			}
		}
	}
}
