package tracking

import (
	"testing"
)

func TestVisualLocalizerStub(t *testing.T) {
	t.Run("NewVisualLocalizer returns error", func(t *testing.T) {
		intrinsics := CameraIntrinsics{
			PrincipalPoint:   [2]float64{0.5, 0.5},
			SensorWidth:      36.0,
			FocalLengthCurve: [][2]float64{{0, 50.0}},
		}
		_, err := NewVisualLocalizer("/nonexistent", "sp.plan", "lg.plan", intrinsics)
		if err == nil {
			t.Fatal("expected error from NewVisualLocalizer on non-CUDA build")
		}
	})

	t.Run("stub methods are safe", func(t *testing.T) {
		var v VisualLocalizer

		if v.Name() != "visual" {
			t.Errorf("Name() = %q, want %q", v.Name(), "visual")
		}
		if v.Type() != "visual" {
			t.Errorf("Type() = %q, want %q", v.Type(), "visual")
		}
		if v.Running() {
			t.Error("Running() should be false")
		}
		if v.LatestPose() != nil {
			t.Error("LatestPose() should be nil")
		}

		// ProcessFrame should not panic.
		v.ProcessFrame(0, 1920, 1080, 1920)

		if err := v.Close(); err != nil {
			t.Errorf("Close() = %v, want nil", err)
		}
	})

	t.Run("implements TrackingSource", func(t *testing.T) {
		var v VisualLocalizer
		// Compile-time interface check.
		var _ TrackingSource = &v
	})
}
