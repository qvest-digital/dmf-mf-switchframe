package dve

import (
	"math"
	"testing"
)

const floatTol = 1e-9

func assertClose(t *testing.T, name string, got, want float64) {
	t.Helper()
	if math.Abs(got-want) > floatTol {
		t.Errorf("%s = %v, want %v", name, got, want)
	}
}

func TestScaleModeFitConstant(t *testing.T) {
	if ScaleModeFit != "fit" {
		t.Errorf("ScaleModeFit = %q, want %q", ScaleModeFit, "fit")
	}
}

func TestValidateTransformAcceptsFit(t *testing.T) {
	tr := DefaultTransform(0, 0, 1920, 1080)
	tr.ScaleMode = ScaleModeFit
	if err := ValidateTransform(tr); err != nil {
		t.Errorf("ScaleModeFit should be valid, got error: %v", err)
	}
}

func TestValidateTransformRectMode(t *testing.T) {
	t.Run("rectMode with valid rect passes", func(t *testing.T) {
		tr := DefaultTransform(0, 0, 1920, 1080)
		tr.RectMode = true
		tr.OutputRect = Rect{X: 100, Y: 100, W: 480, H: 270}
		if err := ValidateTransform(tr); err != nil {
			t.Errorf("unexpected error: %v", err)
		}
	})

	t.Run("rectMode with zero width fails", func(t *testing.T) {
		tr := DefaultTransform(0, 0, 1920, 1080)
		tr.RectMode = true
		tr.OutputRect = Rect{X: 100, Y: 100, W: 0, H: 270}
		if err := ValidateTransform(tr); err == nil {
			t.Error("expected error for zero-width OutputRect in RectMode")
		}
	})

	t.Run("rectMode with zero height fails", func(t *testing.T) {
		tr := DefaultTransform(0, 0, 1920, 1080)
		tr.RectMode = true
		tr.OutputRect = Rect{X: 100, Y: 100, W: 480, H: 0}
		if err := ValidateTransform(tr); err == nil {
			t.Error("expected error for zero-height OutputRect in RectMode")
		}
	})

	t.Run("rectMode with negative width fails", func(t *testing.T) {
		tr := DefaultTransform(0, 0, 1920, 1080)
		tr.RectMode = true
		tr.OutputRect = Rect{X: 100, Y: 100, W: -10, H: 270}
		if err := ValidateTransform(tr); err == nil {
			t.Error("expected error for negative-width OutputRect in RectMode")
		}
	})

	t.Run("non-rectMode ignores OutputRect validation", func(t *testing.T) {
		tr := DefaultTransform(0, 0, 1920, 1080)
		tr.RectMode = false
		tr.OutputRect = Rect{X: 100, Y: 100, W: 0, H: 0}
		if err := ValidateTransform(tr); err != nil {
			t.Errorf("unexpected error when rectMode is false: %v", err)
		}
	})
}

func TestResolveRectStretch(t *testing.T) {
	tr := DefaultTransform(0, 0, 1920, 1080)
	tr.RectMode = true
	tr.ScaleMode = ScaleModeStretch
	tr.OutputRect = Rect{X: 960, Y: 540, W: 480, H: 270}

	resolved := tr.ResolveRect(1920, 1080) // source is 16:9

	if resolved.PositionX != 960 {
		t.Errorf("PositionX = %v, want 960", resolved.PositionX)
	}
	if resolved.PositionY != 540 {
		t.Errorf("PositionY = %v, want 540", resolved.PositionY)
	}
	if resolved.Width != 480 {
		t.Errorf("Width = %v, want 480", resolved.Width)
	}
	if resolved.Height != 270 {
		t.Errorf("Height = %v, want 270", resolved.Height)
	}
	// Stretch mode: no crop changes
	if resolved.CropTop != 0 || resolved.CropBottom != 0 || resolved.CropLeft != 0 || resolved.CropRight != 0 {
		t.Errorf("Stretch mode should not set crops, got T=%v B=%v L=%v R=%v",
			resolved.CropTop, resolved.CropBottom, resolved.CropLeft, resolved.CropRight)
	}
	// RectMode should be cleared on resolved transform
	if resolved.RectMode {
		t.Error("RectMode should be false on resolved transform")
	}
}

func TestResolveRectStretchDefaultScaleMode(t *testing.T) {
	// When ScaleMode is empty, stretch is the default behavior
	tr := DefaultTransform(0, 0, 1920, 1080)
	tr.RectMode = true
	tr.ScaleMode = ""
	tr.OutputRect = Rect{X: 100, Y: 200, W: 640, H: 480}

	resolved := tr.ResolveRect(1920, 1080)

	if resolved.PositionX != 100 {
		t.Errorf("PositionX = %v, want 100", resolved.PositionX)
	}
	if resolved.PositionY != 200 {
		t.Errorf("PositionY = %v, want 200", resolved.PositionY)
	}
	if resolved.Width != 640 {
		t.Errorf("Width = %v, want 640", resolved.Width)
	}
	if resolved.Height != 480 {
		t.Errorf("Height = %v, want 480", resolved.Height)
	}
}

func TestResolveRectFillSameAspect(t *testing.T) {
	// 16:9 source into 16:9 output rect — no crop needed
	tr := DefaultTransform(0, 0, 1920, 1080)
	tr.RectMode = true
	tr.ScaleMode = ScaleModeFill
	tr.CropAnchor = [2]float64{0.5, 0.5}
	tr.OutputRect = Rect{X: 0, Y: 0, W: 960, H: 540}

	resolved := tr.ResolveRect(1920, 1080)

	if resolved.PositionX != 0 {
		t.Errorf("PositionX = %v, want 0", resolved.PositionX)
	}
	if resolved.PositionY != 0 {
		t.Errorf("PositionY = %v, want 0", resolved.PositionY)
	}
	if resolved.Width != 960 {
		t.Errorf("Width = %v, want 960", resolved.Width)
	}
	if resolved.Height != 540 {
		t.Errorf("Height = %v, want 540", resolved.Height)
	}
	// Same aspect: no crop
	if resolved.CropTop != 0 || resolved.CropBottom != 0 || resolved.CropLeft != 0 || resolved.CropRight != 0 {
		t.Errorf("Same aspect fill should not crop, got T=%v B=%v L=%v R=%v",
			resolved.CropTop, resolved.CropBottom, resolved.CropLeft, resolved.CropRight)
	}
}

func TestResolveRectFill_WideSourceIntoTallOutput(t *testing.T) {
	// 16:9 source (1920x1080) into 4:3 output rect (480x360)
	// Source aspect 1.778, output aspect 1.333
	// Source is wider → crop left/right
	// Scale source to cover: match height → scaled source = 480*(1920/1080) x 360 = 853.33 x 360
	// Excess width in source pixels: 1920 - 1080*(480/360) = 1920 - 1440 = 480 px
	// CropAnchor 0.5 → split evenly: CropLeft=240, CropRight=240
	tr := DefaultTransform(0, 0, 1920, 1080)
	tr.RectMode = true
	tr.ScaleMode = ScaleModeFill
	tr.CropAnchor = [2]float64{0.5, 0.5}
	tr.OutputRect = Rect{X: 100, Y: 100, W: 480, H: 360}

	resolved := tr.ResolveRect(1920, 1080)

	assertClose(t, "PositionX", resolved.PositionX, 100)
	assertClose(t, "PositionY", resolved.PositionY, 100)
	assertClose(t, "Width", resolved.Width, 480)
	assertClose(t, "Height", resolved.Height, 360)
	assertClose(t, "CropLeft", resolved.CropLeft, 240)
	assertClose(t, "CropRight", resolved.CropRight, 240)
	assertClose(t, "CropTop", resolved.CropTop, 0)
	assertClose(t, "CropBottom", resolved.CropBottom, 0)
}

func TestResolveRectFill_TallSourceIntoWideOutput(t *testing.T) {
	// 4:3 source (640x480) into 16:9 output rect (960x540)
	// Source aspect 1.333, output aspect 1.778
	// Source is taller → crop top/bottom
	// Match width: scaled source = 960 x 960*(480/640) = 960 x 720
	// Excess height in source pixels: 480 - 640*(540/960) = 480 - 360 = 120 px
	// CropAnchor 0.5 → split evenly: CropTop=60, CropBottom=60
	tr := DefaultTransform(0, 0, 1920, 1080)
	tr.RectMode = true
	tr.ScaleMode = ScaleModeFill
	tr.CropAnchor = [2]float64{0.5, 0.5}
	tr.OutputRect = Rect{X: 0, Y: 0, W: 960, H: 540}

	resolved := tr.ResolveRect(640, 480)

	if resolved.Width != 960 {
		t.Errorf("Width = %v, want 960", resolved.Width)
	}
	if resolved.Height != 540 {
		t.Errorf("Height = %v, want 540", resolved.Height)
	}
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

func TestResolveRectFill_CropAnchorTopLeft(t *testing.T) {
	// 16:9 source (1920x1080) into 4:3 output (480x360)
	// Source wider → crop left/right, total crop = 480 px
	// Anchor (0, 0.5) → all crop on right side
	tr := DefaultTransform(0, 0, 1920, 1080)
	tr.RectMode = true
	tr.ScaleMode = ScaleModeFill
	tr.CropAnchor = [2]float64{0.0, 0.5}
	tr.OutputRect = Rect{X: 0, Y: 0, W: 480, H: 360}

	resolved := tr.ResolveRect(1920, 1080)

	assertClose(t, "CropLeft", resolved.CropLeft, 0)
	assertClose(t, "CropRight", resolved.CropRight, 480)
}

func TestResolveRectFill_CropAnchorBottomRight(t *testing.T) {
	// 16:9 source (1920x1080) into 4:3 output (480x360)
	// Source wider → crop left/right, total crop = 480 px
	// Anchor (1.0, 0.5) → all crop on left side
	tr := DefaultTransform(0, 0, 1920, 1080)
	tr.RectMode = true
	tr.ScaleMode = ScaleModeFill
	tr.CropAnchor = [2]float64{1.0, 0.5}
	tr.OutputRect = Rect{X: 0, Y: 0, W: 480, H: 360}

	resolved := tr.ResolveRect(1920, 1080)

	assertClose(t, "CropLeft", resolved.CropLeft, 480)
	assertClose(t, "CropRight", resolved.CropRight, 0)
}

func TestResolveRectFit_SameAspect(t *testing.T) {
	// 16:9 source into 16:9 output — fits perfectly, no letterbox
	tr := DefaultTransform(0, 0, 1920, 1080)
	tr.RectMode = true
	tr.ScaleMode = ScaleModeFit
	tr.OutputRect = Rect{X: 0, Y: 0, W: 960, H: 540}

	resolved := tr.ResolveRect(1920, 1080)

	if resolved.PositionX != 0 {
		t.Errorf("PositionX = %v, want 0", resolved.PositionX)
	}
	if resolved.PositionY != 0 {
		t.Errorf("PositionY = %v, want 0", resolved.PositionY)
	}
	if resolved.Width != 960 {
		t.Errorf("Width = %v, want 960", resolved.Width)
	}
	if resolved.Height != 540 {
		t.Errorf("Height = %v, want 540", resolved.Height)
	}
	// No crop in fit mode
	if resolved.CropTop != 0 || resolved.CropBottom != 0 || resolved.CropLeft != 0 || resolved.CropRight != 0 {
		t.Errorf("Fit mode should not crop, got T=%v B=%v L=%v R=%v",
			resolved.CropTop, resolved.CropBottom, resolved.CropLeft, resolved.CropRight)
	}
}

func TestResolveRectFit_WideSourceIntoTallOutput(t *testing.T) {
	// 16:9 source (1920x1080) into square output (400x400)
	// Source is wider → pillarbox (vertical letterbox)
	// Fit width: 400 wide → height = 400 * (1080/1920) = 225
	// Center vertically: Y offset = (400 - 225) / 2 = 87.5
	tr := DefaultTransform(0, 0, 1920, 1080)
	tr.RectMode = true
	tr.ScaleMode = ScaleModeFit
	tr.OutputRect = Rect{X: 100, Y: 100, W: 400, H: 400}

	resolved := tr.ResolveRect(1920, 1080)

	if resolved.Width != 400 {
		t.Errorf("Width = %v, want 400", resolved.Width)
	}
	if resolved.Height != 225 {
		t.Errorf("Height = %v, want 225", resolved.Height)
	}
	if resolved.PositionX != 100 {
		t.Errorf("PositionX = %v, want 100", resolved.PositionX)
	}
	expectedY := 100 + (400.0-225.0)/2.0
	if resolved.PositionY != expectedY {
		t.Errorf("PositionY = %v, want %v", resolved.PositionY, expectedY)
	}
}

func TestResolveRectFit_TallSourceIntoWideOutput(t *testing.T) {
	// 4:3 source (640x480) into 16:9 output (960x540)
	// Source is taller → pillarbox (horizontal letterbox)
	// Fit height: 540 high → width = 540 * (640/480) = 720
	// Center horizontally: X offset = (960 - 720) / 2 = 120
	tr := DefaultTransform(0, 0, 1920, 1080)
	tr.RectMode = true
	tr.ScaleMode = ScaleModeFit
	tr.OutputRect = Rect{X: 0, Y: 0, W: 960, H: 540}

	resolved := tr.ResolveRect(640, 480)

	if resolved.Width != 720 {
		t.Errorf("Width = %v, want 720", resolved.Width)
	}
	if resolved.Height != 540 {
		t.Errorf("Height = %v, want 540", resolved.Height)
	}
	if resolved.PositionX != 120 {
		t.Errorf("PositionX = %v, want 120", resolved.PositionX)
	}
	if resolved.PositionY != 0 {
		t.Errorf("PositionY = %v, want 0", resolved.PositionY)
	}
}

func TestResolveRectNonRectModePassthrough(t *testing.T) {
	// When RectMode is false, ResolveRect should return the transform unchanged
	tr := DefaultTransform(100, 200, 640, 480)
	tr.RectMode = false
	tr.OutputRect = Rect{X: 0, Y: 0, W: 960, H: 540}

	resolved := tr.ResolveRect(1920, 1080)

	if resolved.PositionX != 100 || resolved.PositionY != 200 {
		t.Errorf("Position = (%v, %v), want (100, 200)", resolved.PositionX, resolved.PositionY)
	}
	if resolved.Width != 640 || resolved.Height != 480 {
		t.Errorf("Size = (%v, %v), want (640, 480)", resolved.Width, resolved.Height)
	}
}

func TestResolveRectPreservesOtherFields(t *testing.T) {
	tr := DefaultTransform(0, 0, 1920, 1080)
	tr.RectMode = true
	tr.ScaleMode = ScaleModeStretch
	tr.OutputRect = Rect{X: 100, Y: 100, W: 480, H: 270}
	tr.RotationDeg = 15
	tr.Opacity = 0.8
	tr.Border.Width = 3
	tr.MirrorH = true

	resolved := tr.ResolveRect(1920, 1080)

	if resolved.RotationDeg != 15 {
		t.Errorf("RotationDeg = %v, want 15", resolved.RotationDeg)
	}
	if resolved.Opacity != 0.8 {
		t.Errorf("Opacity = %v, want 0.8", resolved.Opacity)
	}
	if resolved.Border.Width != 3 {
		t.Errorf("Border.Width = %v, want 3", resolved.Border.Width)
	}
	if !resolved.MirrorH {
		t.Error("MirrorH = false, want true")
	}
}

// The spec test from the task description:
// A 1920x1080 PIP at (960,540,480,270) with fill mode and anchor (0.5,0.5)
// for a 4:3 source (e.g. 640x480).
func TestResolveRectSpecExample(t *testing.T) {
	tr := DefaultTransform(0, 0, 1920, 1080)
	tr.RectMode = true
	tr.ScaleMode = ScaleModeFill
	tr.CropAnchor = [2]float64{0.5, 0.5}
	tr.OutputRect = Rect{X: 960, Y: 540, W: 480, H: 270}

	// 4:3 source
	resolved := tr.ResolveRect(640, 480)

	// Output rect aspect = 480/270 = 16:9 = 1.778
	// Source aspect = 640/480 = 4:3 = 1.333
	// Source is taller (narrower) than output → crop top/bottom
	// Excess height in source px: 480 - 640*(270/480) = 480 - 360 = 120
	// CropAnchor (0.5) → CropTop=60, CropBottom=60

	if resolved.PositionX != 960 {
		t.Errorf("PositionX = %v, want 960", resolved.PositionX)
	}
	if resolved.PositionY != 540 {
		t.Errorf("PositionY = %v, want 540", resolved.PositionY)
	}
	if resolved.Width != 480 {
		t.Errorf("Width = %v, want 480", resolved.Width)
	}
	if resolved.Height != 270 {
		t.Errorf("Height = %v, want 270", resolved.Height)
	}
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

func TestResolveRectFill_AsymmetricAnchor(t *testing.T) {
	// 4:3 source (640x480) into 16:9 output (960x540)
	// Source taller → crop top/bottom, total = 120 px
	// Anchor Y = 0.25 → CropTop = 120*0.25 = 30, CropBottom = 120*0.75 = 90
	tr := DefaultTransform(0, 0, 1920, 1080)
	tr.RectMode = true
	tr.ScaleMode = ScaleModeFill
	tr.CropAnchor = [2]float64{0.5, 0.25}
	tr.OutputRect = Rect{X: 0, Y: 0, W: 960, H: 540}

	resolved := tr.ResolveRect(640, 480)

	if resolved.CropTop != 30 {
		t.Errorf("CropTop = %v, want 30", resolved.CropTop)
	}
	if resolved.CropBottom != 90 {
		t.Errorf("CropBottom = %v, want 90", resolved.CropBottom)
	}
}

func TestRectType(t *testing.T) {
	r := Rect{X: 10, Y: 20, W: 100, H: 200}
	if r.X != 10 || r.Y != 20 || r.W != 100 || r.H != 200 {
		t.Errorf("Rect fields not stored correctly: %+v", r)
	}
}

func TestResolveRectZeroFrameDimensions(t *testing.T) {
	// When source dimensions are unknown (0x0), ResolveRect should fall back
	// to stretch behavior: use OutputRect directly, skip aspect ratio math.
	tr := DefaultTransform(0, 0, 1920, 1080)
	tr.RectMode = true
	tr.ScaleMode = ScaleModeFill // would normally trigger aspect ratio math
	tr.CropAnchor = [2]float64{0.5, 0.5}
	tr.OutputRect = Rect{X: 100, Y: 200, W: 480, H: 270}

	for _, tc := range []struct {
		name string
		w, h int
	}{
		{"zero/zero", 0, 0},
		{"negative/positive", -1, 1080},
		{"positive/zero", 1920, 0},
		{"zero/negative", 0, -1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			resolved := tr.ResolveRect(tc.w, tc.h)

			assertClose(t, "PositionX", resolved.PositionX, 100)
			assertClose(t, "PositionY", resolved.PositionY, 200)
			assertClose(t, "Width", resolved.Width, 480)
			assertClose(t, "Height", resolved.Height, 270)
			// No crop should be set
			assertClose(t, "CropTop", resolved.CropTop, 0)
			assertClose(t, "CropBottom", resolved.CropBottom, 0)
			assertClose(t, "CropLeft", resolved.CropLeft, 0)
			assertClose(t, "CropRight", resolved.CropRight, 0)
			if resolved.RectMode {
				t.Error("RectMode should be false on resolved transform")
			}
		})
	}
}

func TestResolveRectFit_ExtremeAspectRatios(t *testing.T) {
	// Very wide source (1920x100) into square output (400x400)
	// Source aspect = 19.2, output aspect = 1.0
	// Source is much wider → fit by width: W=400, H=400*(100/1920) ≈ 20.83
	tr := DefaultTransform(0, 0, 1920, 1080)
	tr.RectMode = true
	tr.ScaleMode = ScaleModeFit
	tr.OutputRect = Rect{X: 0, Y: 0, W: 400, H: 400}

	resolved := tr.ResolveRect(1920, 100)

	srcAspect := 1920.0 / 100.0
	expectedW := 400.0
	expectedH := expectedW / srcAspect

	if math.Abs(resolved.Width-expectedW) > 0.01 {
		t.Errorf("Width = %v, want %v", resolved.Width, expectedW)
	}
	if math.Abs(resolved.Height-expectedH) > 0.01 {
		t.Errorf("Height = %v, want ~%v", resolved.Height, expectedH)
	}
	// Should be centered vertically
	expectedY := (400.0 - expectedH) / 2.0
	if math.Abs(resolved.PositionY-expectedY) > 0.01 {
		t.Errorf("PositionY = %v, want ~%v", resolved.PositionY, expectedY)
	}
}
