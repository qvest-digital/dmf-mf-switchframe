package dve

import "testing"

func TestValidateTransform_MosaicSize(t *testing.T) {
	tr := DefaultTransform(0, 0, 100, 100)
	tr.MosaicSize = 8
	if err := ValidateTransform(tr); err != nil {
		t.Fatalf("valid mosaic: %v", err)
	}
	tr.MosaicSize = -1
	if err := ValidateTransform(tr); err == nil {
		t.Fatal("expected error for negative mosaic")
	}
	tr.MosaicSize = 300
	if err := ValidateTransform(tr); err == nil {
		t.Fatal("expected error for mosaic > 256")
	}
}

func TestValidateTransform_Saturation(t *testing.T) {
	tr := DefaultTransform(0, 0, 100, 100)
	tr.Saturation = 0.0
	if err := ValidateTransform(tr); err != nil {
		t.Fatalf("valid saturation 0: %v", err)
	}
	tr.Saturation = 2.0
	if err := ValidateTransform(tr); err != nil {
		t.Fatalf("valid saturation 2: %v", err)
	}
	tr.Saturation = -0.1
	if err := ValidateTransform(tr); err == nil {
		t.Fatal("expected error for negative saturation")
	}
	tr.Saturation = 2.1
	if err := ValidateTransform(tr); err == nil {
		t.Fatal("expected error for saturation > 2")
	}
}

func TestValidateTransform_Brightness(t *testing.T) {
	tr := DefaultTransform(0, 0, 100, 100)
	tr.Brightness = -1.0
	if err := ValidateTransform(tr); err != nil {
		t.Fatalf("valid brightness -1: %v", err)
	}
	tr.Brightness = 1.0
	if err := ValidateTransform(tr); err != nil {
		t.Fatalf("valid brightness 1: %v", err)
	}
	tr.Brightness = -1.1
	if err := ValidateTransform(tr); err == nil {
		t.Fatal("expected error for brightness < -1")
	}
}

func TestValidateTransform_Contrast(t *testing.T) {
	tr := DefaultTransform(0, 0, 100, 100)
	tr.Contrast = 0.0
	if err := ValidateTransform(tr); err != nil {
		t.Fatalf("valid contrast 0: %v", err)
	}
	tr.Contrast = 3.0
	if err := ValidateTransform(tr); err != nil {
		t.Fatalf("valid contrast 3: %v", err)
	}
	tr.Contrast = -0.1
	if err := ValidateTransform(tr); err == nil {
		t.Fatal("expected error for negative contrast")
	}
}

func TestValidateTransform_Mirror(t *testing.T) {
	tr := DefaultTransform(0, 0, 100, 100)
	tr.MirrorH = true
	tr.MirrorV = true
	if err := ValidateTransform(tr); err != nil {
		t.Fatalf("valid mirror: %v", err)
	}
}

func TestValidateSlot_StrobeFrames(t *testing.T) {
	s := Slot{
		SourceKey: "cam1",
		Transform: DefaultTransform(0, 0, 100, 100),
		Enabled:   true,
	}
	s.StrobeFrames = 3
	if err := ValidateSlot(s); err != nil {
		t.Fatalf("valid strobe: %v", err)
	}
	s.StrobeFrames = -1
	if err := ValidateSlot(s); err == nil {
		t.Fatal("expected error for negative strobe")
	}
}

func TestDefaultTransform_ColorDefaults(t *testing.T) {
	tr := DefaultTransform(0, 0, 100, 100)
	if tr.Saturation != 1.0 {
		t.Errorf("default saturation: got %f, want 1.0", tr.Saturation)
	}
	if tr.Contrast != 1.0 {
		t.Errorf("default contrast: got %f, want 1.0", tr.Contrast)
	}
	if tr.MirrorH || tr.MirrorV {
		t.Error("default mirror should be false")
	}
	if tr.MosaicSize != 0 {
		t.Error("default mosaicSize should be 0")
	}
	if tr.Brightness != 0 {
		t.Error("default brightness should be 0")
	}
}

func TestEvaluateTransform_NewParams(t *testing.T) {
	base := DefaultTransform(0, 0, 100, 100)
	anim := Animation{
		DurationMs: 1000,
		Tracks: []Track{
			{SlotIndex: 0, Param: "mosaicSize", Keyframes: []Keyframe{{TimeMs: 0, Value: 0}, {TimeMs: 1000, Value: 32}}},
			{SlotIndex: 0, Param: "saturation", Keyframes: []Keyframe{{TimeMs: 0, Value: 1.0}, {TimeMs: 1000, Value: 0.0}}},
			{SlotIndex: 0, Param: "brightness", Keyframes: []Keyframe{{TimeMs: 0, Value: 0.0}, {TimeMs: 1000, Value: 0.5}}},
			{SlotIndex: 0, Param: "contrast", Keyframes: []Keyframe{{TimeMs: 0, Value: 1.0}, {TimeMs: 1000, Value: 2.0}}},
		},
	}

	result := anim.EvaluateTransform(0, 500, base) // midpoint
	if result.MosaicSize != 16 {
		t.Errorf("mosaicSize at 500ms: got %d, want 16", result.MosaicSize)
	}
	if abs64(result.Saturation-0.5) > 0.05 {
		t.Errorf("saturation at 500ms: got %f, want ~0.5", result.Saturation)
	}
	if abs64(result.Brightness-0.25) > 0.05 {
		t.Errorf("brightness at 500ms: got %f, want ~0.25", result.Brightness)
	}
	if abs64(result.Contrast-1.5) > 0.05 {
		t.Errorf("contrast at 500ms: got %f, want ~1.5", result.Contrast)
	}
}

func TestIsIdentity_NewFields(t *testing.T) {
	tr := DefaultTransform(0, 0, 1920, 1080)
	if !tr.IsIdentity(1920, 1080) {
		t.Fatal("default full-frame should be identity")
	}
	tr.MirrorH = true
	if tr.IsIdentity(1920, 1080) {
		t.Fatal("mirrorH should break identity")
	}
	tr.MirrorH = false
	tr.MosaicSize = 4
	if tr.IsIdentity(1920, 1080) {
		t.Fatal("mosaicSize should break identity")
	}
	tr.MosaicSize = 0
	tr.Saturation = 0.5
	if tr.IsIdentity(1920, 1080) {
		t.Fatal("non-default saturation should break identity")
	}
}

func abs64(x float64) float64 {
	if x < 0 {
		return -x
	}
	return x
}
