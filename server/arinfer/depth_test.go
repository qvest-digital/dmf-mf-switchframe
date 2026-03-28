package arinfer

import (
	"errors"
	"testing"
)

func TestDepthAnythingV2Interface(t *testing.T) {
	t.Run("implements depthModel", func(t *testing.T) {
		// Verify at compile time that *depthAnythingV2 satisfies depthModel.
		var _ depthModel = (*depthAnythingV2)(nil)
	})
}

func TestNewDepthAnythingV2_InvalidPath(t *testing.T) {
	t.Run("fails with nonexistent path", func(t *testing.T) {
		d, err := newDepthAnythingV2("/nonexistent/model.plan", 518, 294)
		if d != nil {
			t.Errorf("expected nil model, got %v", d)
		}
		if err == nil {
			t.Error("expected error for nonexistent model path")
		}
	})

	t.Run("fails with zero dimensions", func(t *testing.T) {
		d, err := newDepthAnythingV2("/nonexistent/model.plan", 0, 0)
		if d != nil {
			t.Errorf("expected nil model, got %v", d)
		}
		if err == nil {
			t.Error("expected error for nonexistent model path")
		}
	})
}

func TestDepthAnythingV2InferStub(t *testing.T) {
	t.Run("Infer returns ErrDisabled on nil receiver", func(t *testing.T) {
		var d *depthAnythingV2
		err := d.Infer(0x1000, 1920, 1080, 2048, 0x2000, FormatNV12)
		if !errors.Is(err, ErrDisabled) {
			t.Errorf("expected ErrDisabled, got %v", err)
		}
	})
}

func TestDepthAnythingV2CloseStub(t *testing.T) {
	t.Run("close on nil is safe", func(t *testing.T) {
		var d *depthAnythingV2
		d.Close() // must not panic
	})

	t.Run("close on zero struct is safe", func(t *testing.T) {
		d := &depthAnythingV2{}
		d.Close() // must not panic
	})

	t.Run("double close is safe", func(t *testing.T) {
		d := &depthAnythingV2{}
		d.Close()
		d.Close() // must not panic
	})
}
