package gpu

import (
	"testing"
)

func TestDVEDefocusBlur_ZeroRadius(t *testing.T) {
	// Zero radius should return nil (no-op) on all builds
	err := DVEDefocusBlur(nil, nil, 0)
	if err != nil {
		t.Fatalf("DVEDefocusBlur(radius=0) should return nil, got: %v", err)
	}
}

func TestDVEDefocusBlur_NegativeRadius(t *testing.T) {
	// Negative radius should return nil (no-op) on all builds
	err := DVEDefocusBlur(nil, nil, -5.0)
	if err != nil {
		t.Fatalf("DVEDefocusBlur(radius=-5) should return nil, got: %v", err)
	}
}
