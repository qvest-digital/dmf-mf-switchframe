package dve

import "testing"

func TestTransform3D_PageTurnShape(t *testing.T) {
	t3d := Default3DTransform()
	t3d.Shape = "page_turn"
	t3d.CurlProgress = 0.5
	t3d.CurlRadius = 0.15

	tr := DefaultTransform(0, 0, 1920, 1080)
	tr.Mode3D = "3d"
	tr.Transform3D = t3d

	if !tr.Is3D() {
		t.Fatal("page turn transform should report Is3D=true")
	}
	if t3d.Shape != "page_turn" {
		t.Errorf("shape: got %q, want page_turn", t3d.Shape)
	}
	if t3d.CurlProgress != 0.5 {
		t.Errorf("curlProgress: got %f, want 0.5", t3d.CurlProgress)
	}
	if t3d.CurlRadius != 0.15 {
		t.Errorf("curlRadius: got %f, want 0.15", t3d.CurlRadius)
	}
}

func TestTransform3D_PageTurnDefaultCurl(t *testing.T) {
	t3d := Default3DTransform()
	// Default should have zero curl values
	if t3d.CurlProgress != 0 {
		t.Errorf("default curlProgress: got %f, want 0", t3d.CurlProgress)
	}
	if t3d.CurlRadius != 0 {
		t.Errorf("default curlRadius: got %f, want 0", t3d.CurlRadius)
	}
}

func TestTransform_DefocusValidation(t *testing.T) {
	tr := DefaultTransform(0, 0, 100, 100)

	// defocusRadius=0 is valid (disabled)
	tr.DefocusRadius = 0
	if err := ValidateTransform(tr); err != nil {
		t.Fatalf("defocusRadius=0 should be valid: %v", err)
	}

	// positive defocusRadius is valid
	tr.DefocusRadius = 10.5
	if err := ValidateTransform(tr); err != nil {
		t.Fatalf("defocusRadius=10.5 should be valid: %v", err)
	}

	// negative defocusRadius should fail
	tr.DefocusRadius = -1
	if err := ValidateTransform(tr); err == nil {
		t.Fatal("negative defocusRadius should be invalid")
	}
}
