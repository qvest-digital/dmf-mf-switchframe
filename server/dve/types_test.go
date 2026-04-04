package dve

import (
	"fmt"
	"image"
	"testing"
)

func TestDefaultTransform(t *testing.T) {
	tr := DefaultTransform(100, 200, 640, 480)

	if tr.PositionX != 100 {
		t.Errorf("PositionX = %v, want 100", tr.PositionX)
	}
	if tr.PositionY != 200 {
		t.Errorf("PositionY = %v, want 200", tr.PositionY)
	}
	if tr.Width != 640 {
		t.Errorf("Width = %v, want 640", tr.Width)
	}
	if tr.Height != 480 {
		t.Errorf("Height = %v, want 480", tr.Height)
	}
	if tr.Opacity != 1.0 {
		t.Errorf("Opacity = %v, want 1.0", tr.Opacity)
	}
	if tr.AnchorX != 0.5 {
		t.Errorf("AnchorX = %v, want 0.5", tr.AnchorX)
	}
	if tr.AnchorY != 0.5 {
		t.Errorf("AnchorY = %v, want 0.5", tr.AnchorY)
	}
	if tr.RotationDeg != 0 {
		t.Errorf("RotationDeg = %v, want 0", tr.RotationDeg)
	}
	if tr.PerspectiveX != 0 {
		t.Errorf("PerspectiveX = %v, want 0", tr.PerspectiveX)
	}
	if tr.PerspectiveY != 0 {
		t.Errorf("PerspectiveY = %v, want 0", tr.PerspectiveY)
	}
	if tr.Border.Width != 0 {
		t.Errorf("Border.Width = %v, want 0", tr.Border.Width)
	}
	if tr.Shadow.Enabled {
		t.Error("Shadow.Enabled = true, want false")
	}
	if tr.DefocusRadius != 0 {
		t.Errorf("DefocusRadius = %v, want 0", tr.DefocusRadius)
	}
}

func TestTransformIsIdentity(t *testing.T) {
	tests := []struct {
		name   string
		tr     Transform
		frameW int
		frameH int
		want   bool
	}{
		{
			name:   "full frame default",
			tr:     DefaultTransform(0, 0, 1920, 1080),
			frameW: 1920,
			frameH: 1080,
			want:   true,
		},
		{
			name:   "not full width",
			tr:     DefaultTransform(0, 0, 960, 1080),
			frameW: 1920,
			frameH: 1080,
			want:   false,
		},
		{
			name:   "not full height",
			tr:     DefaultTransform(0, 0, 1920, 540),
			frameW: 1920,
			frameH: 1080,
			want:   false,
		},
		{
			name:   "offset position",
			tr:     DefaultTransform(10, 0, 1920, 1080),
			frameW: 1920,
			frameH: 1080,
			want:   false,
		},
		{
			name: "with rotation",
			tr: func() Transform {
				tr := DefaultTransform(0, 0, 1920, 1080)
				tr.RotationDeg = 45
				return tr
			}(),
			frameW: 1920,
			frameH: 1080,
			want:   false,
		},
		{
			name: "with perspective",
			tr: func() Transform {
				tr := DefaultTransform(0, 0, 1920, 1080)
				tr.PerspectiveX = 10
				return tr
			}(),
			frameW: 1920,
			frameH: 1080,
			want:   false,
		},
		{
			name: "with crop",
			tr: func() Transform {
				tr := DefaultTransform(0, 0, 1920, 1080)
				tr.CropTop = 0.1
				return tr
			}(),
			frameW: 1920,
			frameH: 1080,
			want:   false,
		},
		{
			name: "with border",
			tr: func() Transform {
				tr := DefaultTransform(0, 0, 1920, 1080)
				tr.Border.Width = 2
				return tr
			}(),
			frameW: 1920,
			frameH: 1080,
			want:   false,
		},
		{
			name: "with shadow",
			tr: func() Transform {
				tr := DefaultTransform(0, 0, 1920, 1080)
				tr.Shadow.Enabled = true
				return tr
			}(),
			frameW: 1920,
			frameH: 1080,
			want:   false,
		},
		{
			name: "with defocus",
			tr: func() Transform {
				tr := DefaultTransform(0, 0, 1920, 1080)
				tr.DefocusRadius = 5
				return tr
			}(),
			frameW: 1920,
			frameH: 1080,
			want:   false,
		},
		{
			name: "reduced opacity",
			tr: func() Transform {
				tr := DefaultTransform(0, 0, 1920, 1080)
				tr.Opacity = 0.5
				return tr
			}(),
			frameW: 1920,
			frameH: 1080,
			want:   false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.tr.IsIdentity(tt.frameW, tt.frameH)
			if got != tt.want {
				t.Errorf("IsIdentity() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestTransformToRect(t *testing.T) {
	tests := []struct {
		name string
		tr   Transform
		want image.Rectangle
	}{
		{
			name: "integer values",
			tr:   DefaultTransform(100, 200, 640, 480),
			want: image.Rect(100, 200, 740, 680),
		},
		{
			name: "float truncation with even align",
			tr:   DefaultTransform(101.7, 203.9, 641.5, 481.2),
			want: image.Rect(100, 202, 100+640, 202+480),
		},
		{
			name: "zero origin",
			tr:   DefaultTransform(0, 0, 1920, 1080),
			want: image.Rect(0, 0, 1920, 1080),
		},
		{
			name: "odd values get even aligned",
			tr:   DefaultTransform(1, 1, 3, 3),
			want: image.Rect(0, 0, 0+2, 0+2),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.tr.ToRect()
			if got != tt.want {
				t.Errorf("ToRect() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestEffectiveOpacity(t *testing.T) {
	tests := []struct {
		name    string
		opacity float64
		want    float64
	}{
		{"zero defaults to 1.0", 0, 1.0},
		{"normal value", 0.5, 0.5},
		{"full opacity", 1.0, 1.0},
		{"negative clamped to 0", -0.5, 0.0},
		{"over 1 clamped", 1.5, 1.0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tr := Transform{Opacity: tt.opacity}
			got := tr.EffectiveOpacity()
			if got != tt.want {
				t.Errorf("EffectiveOpacity() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestEffectiveAnchor(t *testing.T) {
	tests := []struct {
		name    string
		anchorX float64
		anchorY float64
		wantX   float64
		wantY   float64
	}{
		{"zero defaults to center", 0, 0, 0.5, 0.5},
		{"explicit center", 0.5, 0.5, 0.5, 0.5},
		{"top-left non-zero-y", 0, 0.1, 0, 0.1},
		{"negative clamped", -1, -1, 0, 0},
		{"over 1 clamped", 2, 2, 1, 1},
		{"custom values", 0.3, 0.7, 0.3, 0.7},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tr := Transform{AnchorX: tt.anchorX, AnchorY: tt.anchorY}
			gotX, gotY := tr.EffectiveAnchor()
			if gotX != tt.wantX || gotY != tt.wantY {
				t.Errorf("EffectiveAnchor() = (%v, %v), want (%v, %v)", gotX, gotY, tt.wantX, tt.wantY)
			}
		})
	}
}

func TestValidateTransform(t *testing.T) {
	valid := DefaultTransform(0, 0, 1920, 1080)

	t.Run("valid transform passes", func(t *testing.T) {
		if err := ValidateTransform(valid); err != nil {
			t.Errorf("unexpected error: %v", err)
		}
	})

	t.Run("zero width fails", func(t *testing.T) {
		tr := valid
		tr.Width = 0
		if err := ValidateTransform(tr); err == nil {
			t.Error("expected error for zero width")
		}
	})

	t.Run("negative height fails", func(t *testing.T) {
		tr := valid
		tr.Height = -10
		if err := ValidateTransform(tr); err == nil {
			t.Error("expected error for negative height")
		}
	})

	t.Run("rotation out of range fails", func(t *testing.T) {
		tr := valid
		tr.RotationDeg = 400
		if err := ValidateTransform(tr); err == nil {
			t.Error("expected error for rotation > 360")
		}
	})

	t.Run("rotation -360 passes", func(t *testing.T) {
		tr := valid
		tr.RotationDeg = -360
		if err := ValidateTransform(tr); err != nil {
			t.Errorf("unexpected error: %v", err)
		}
	})

	t.Run("perspective X out of range fails", func(t *testing.T) {
		tr := valid
		tr.PerspectiveX = 70
		if err := ValidateTransform(tr); err == nil {
			t.Error("expected error for perspectiveX > 60")
		}
	})

	t.Run("perspective Y out of range fails", func(t *testing.T) {
		tr := valid
		tr.PerspectiveY = -70
		if err := ValidateTransform(tr); err == nil {
			t.Error("expected error for perspectiveY < -60")
		}
	})

	t.Run("opacity over 1 fails", func(t *testing.T) {
		tr := valid
		tr.Opacity = 1.5
		if err := ValidateTransform(tr); err == nil {
			t.Error("expected error for opacity > 1")
		}
	})

	t.Run("opacity negative fails", func(t *testing.T) {
		tr := valid
		tr.Opacity = -0.1
		if err := ValidateTransform(tr); err == nil {
			t.Error("expected error for opacity < 0")
		}
	})

	t.Run("opacity 0 passes (means default)", func(t *testing.T) {
		tr := valid
		tr.Opacity = 0
		if err := ValidateTransform(tr); err != nil {
			t.Errorf("unexpected error: %v", err)
		}
	})

	t.Run("negative cropTop fails", func(t *testing.T) {
		tr := valid
		tr.CropTop = -0.1
		if err := ValidateTransform(tr); err == nil {
			t.Error("expected error for negative cropTop")
		}
	})

	t.Run("negative cropBottom fails", func(t *testing.T) {
		tr := valid
		tr.CropBottom = -0.1
		if err := ValidateTransform(tr); err == nil {
			t.Error("expected error for negative cropBottom")
		}
	})

	t.Run("negative cropLeft fails", func(t *testing.T) {
		tr := valid
		tr.CropLeft = -0.1
		if err := ValidateTransform(tr); err == nil {
			t.Error("expected error for negative cropLeft")
		}
	})

	t.Run("negative cropRight fails", func(t *testing.T) {
		tr := valid
		tr.CropRight = -0.1
		if err := ValidateTransform(tr); err == nil {
			t.Error("expected error for negative cropRight")
		}
	})

	t.Run("negative crop softness fails", func(t *testing.T) {
		tr := valid
		tr.CropSoftness = -1
		if err := ValidateTransform(tr); err == nil {
			t.Error("expected error for negative crop softness")
		}
	})

	t.Run("negative border width fails", func(t *testing.T) {
		tr := valid
		tr.Border.Width = -1
		if err := ValidateTransform(tr); err == nil {
			t.Error("expected error for negative border width")
		}
	})

	t.Run("negative corner radius fails", func(t *testing.T) {
		tr := valid
		tr.Border.CornerRadius = -1
		if err := ValidateTransform(tr); err == nil {
			t.Error("expected error for negative corner radius")
		}
	})

	t.Run("negative border softness fails", func(t *testing.T) {
		tr := valid
		tr.Border.Softness = -1
		if err := ValidateTransform(tr); err == nil {
			t.Error("expected error for negative border softness")
		}
	})

	t.Run("negative border innerWidth fails", func(t *testing.T) {
		tr := valid
		tr.Border.InnerWidth = -1
		if err := ValidateTransform(tr); err == nil {
			t.Error("expected error for negative border innerWidth")
		}
	})

	t.Run("negative shadow blur fails", func(t *testing.T) {
		tr := valid
		tr.Shadow.Blur = -1
		if err := ValidateTransform(tr); err == nil {
			t.Error("expected error for negative shadow blur")
		}
	})

	t.Run("shadow opacity over 1 fails", func(t *testing.T) {
		tr := valid
		tr.Shadow.Opacity = 1.5
		if err := ValidateTransform(tr); err == nil {
			t.Error("expected error for shadow opacity > 1")
		}
	})

	t.Run("negative defocus radius fails", func(t *testing.T) {
		tr := valid
		tr.DefocusRadius = -1
		if err := ValidateTransform(tr); err == nil {
			t.Error("expected error for negative defocus radius")
		}
	})

	t.Run("invalid scale mode fails", func(t *testing.T) {
		tr := valid
		tr.ScaleMode = "bogus"
		if err := ValidateTransform(tr); err == nil {
			t.Error("expected error for invalid scale mode")
		}
	})

	t.Run("valid scale modes pass", func(t *testing.T) {
		for _, mode := range []string{"", ScaleModeStretch, ScaleModeFill} {
			tr := valid
			tr.ScaleMode = mode
			if err := ValidateTransform(tr); err != nil {
				t.Errorf("unexpected error for scaleMode %q: %v", mode, err)
			}
		}
	})

	t.Run("crop anchor out of range fails", func(t *testing.T) {
		tr := valid
		tr.CropAnchor = [2]float64{1.5, 0.5}
		if err := ValidateTransform(tr); err == nil {
			t.Error("expected error for crop anchor > 1")
		}
	})

	t.Run("crop anchor negative fails", func(t *testing.T) {
		tr := valid
		tr.CropAnchor = [2]float64{0.5, -0.1}
		if err := ValidateTransform(tr); err == nil {
			t.Error("expected error for crop anchor < 0")
		}
	})
}

func TestValidateSlot(t *testing.T) {
	t.Run("valid slot passes", func(t *testing.T) {
		s := Slot{
			SourceKey: "cam1",
			Transform: DefaultTransform(0, 0, 1920, 1080),
			ZOrder:    0,
			Enabled:   true,
		}
		if err := ValidateSlot(s); err != nil {
			t.Errorf("unexpected error: %v", err)
		}
	})

	t.Run("negative zorder fails", func(t *testing.T) {
		s := Slot{
			SourceKey: "cam1",
			Transform: DefaultTransform(0, 0, 1920, 1080),
			ZOrder:    -1,
			Enabled:   true,
		}
		if err := ValidateSlot(s); err == nil {
			t.Error("expected error for negative zorder")
		}
	})

	t.Run("invalid transform propagates", func(t *testing.T) {
		tr := DefaultTransform(0, 0, 1920, 1080)
		tr.RotationDeg = 500
		s := Slot{
			SourceKey: "cam1",
			Transform: tr,
			ZOrder:    0,
			Enabled:   true,
		}
		if err := ValidateSlot(s); err == nil {
			t.Error("expected error for invalid transform")
		}
	})
}

func TestValidateLayout(t *testing.T) {
	t.Run("valid layout passes", func(t *testing.T) {
		l := &Layout{
			Name: "test",
			Slots: []Slot{
				{Transform: DefaultTransform(0, 0, 960, 540), ZOrder: 0, Enabled: true},
				{Transform: DefaultTransform(960, 0, 960, 540), ZOrder: 1, Enabled: true},
			},
		}
		if err := ValidateLayout(l); err != nil {
			t.Errorf("unexpected error: %v", err)
		}
	})

	t.Run("too many slots fails", func(t *testing.T) {
		slots := make([]Slot, MaxSlots+1)
		for i := range slots {
			slots[i] = Slot{Transform: DefaultTransform(0, 0, 100, 100), ZOrder: i, Enabled: true}
		}
		l := &Layout{Name: "test", Slots: slots}
		if err := ValidateLayout(l); err == nil {
			t.Error("expected error for too many slots")
		}
	})

	t.Run("invalid slot propagates", func(t *testing.T) {
		tr := DefaultTransform(0, 0, 1920, 1080)
		tr.RotationDeg = 500
		l := &Layout{
			Name:  "test",
			Slots: []Slot{{Transform: tr, ZOrder: 0, Enabled: true}},
		}
		if err := ValidateLayout(l); err == nil {
			t.Error("expected error for invalid slot")
		}
	})
}

func TestEvenAlign(t *testing.T) {
	tests := []struct {
		in   int
		want int
	}{
		{0, 0},
		{1, 0},
		{2, 2},
		{3, 2},
		{4, 4},
		{100, 100},
		{101, 100},
		{1920, 1920},
		{1921, 1920},
		{-1, -2},
	}

	for _, tt := range tests {
		t.Run(fmt.Sprintf("%d→%d", tt.in, tt.want), func(t *testing.T) {
			got := EvenAlign(tt.in)
			if got != tt.want {
				t.Errorf("EvenAlign(%d) = %d, want %d", tt.in, got, tt.want)
			}
		})
	}
}

func TestMaxSlots(t *testing.T) {
	if MaxSlots != 8 {
		t.Errorf("MaxSlots = %d, want 8", MaxSlots)
	}
}

func TestScaleModeConstants(t *testing.T) {
	if ScaleModeStretch != "stretch" {
		t.Errorf("ScaleModeStretch = %q, want %q", ScaleModeStretch, "stretch")
	}
	if ScaleModeFill != "fill" {
		t.Errorf("ScaleModeFill = %q, want %q", ScaleModeFill, "fill")
	}
}
