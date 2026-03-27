package arinfer

import (
	"errors"
	"testing"
)

func TestSAM2ModelInterface(t *testing.T) {
	t.Run("implements samModel", func(t *testing.T) {
		// Verify at compile time that *sam2Model satisfies samModel.
		var _ samModel = (*sam2Model)(nil)
	})
}

func TestNewSAM2Model_InvalidPath(t *testing.T) {
	t.Run("fails with nonexistent paths", func(t *testing.T) {
		m, err := newSAM2Model("/nonexistent/encoder.plan", "/nonexistent/decoder.plan")
		if m != nil {
			t.Errorf("expected nil model, got %v", m)
		}
		if err == nil {
			t.Error("expected error for nonexistent model paths")
		}
	})
}

func TestSAM2EncodeStub(t *testing.T) {
	t.Run("Encode returns ErrDisabled on nil receiver", func(t *testing.T) {
		var m *sam2Model
		err := m.Encode(0x1000, 1920, 1080, 2048)
		if !errors.Is(err, ErrDisabled) {
			t.Errorf("expected ErrDisabled, got %v", err)
		}
	})
}

func TestSAM2PropagateStub(t *testing.T) {
	t.Run("Propagate returns ErrDisabled on nil receiver", func(t *testing.T) {
		var m *sam2Model
		err := m.Propagate(0x1000, 1920, 1080, 2048, 0x2000)
		if !errors.Is(err, ErrDisabled) {
			t.Errorf("expected ErrDisabled, got %v", err)
		}
	})
}

func TestSAM2PromptManagement(t *testing.T) {
	t.Run("add and list prompts", func(t *testing.T) {
		m := &sam2Model{}

		p1 := SAMPrompt{
			ID:     "prompt-1",
			Type:   "point",
			Points: [][2]int{{100, 200}},
			Label:  "person",
		}
		p2 := SAMPrompt{
			ID:    "prompt-2",
			Type:  "box",
			Box:   [4]int{10, 20, 300, 400},
			Label: "table",
		}

		if err := m.AddPrompt(p1); err != nil {
			t.Fatalf("AddPrompt p1: %v", err)
		}
		if err := m.AddPrompt(p2); err != nil {
			t.Fatalf("AddPrompt p2: %v", err)
		}

		prompts := m.Prompts()
		if len(prompts) != 2 {
			t.Fatalf("expected 2 prompts, got %d", len(prompts))
		}
		if prompts[0].ID != "prompt-1" {
			t.Errorf("expected first prompt ID %q, got %q", "prompt-1", prompts[0].ID)
		}
		if prompts[1].ID != "prompt-2" {
			t.Errorf("expected second prompt ID %q, got %q", "prompt-2", prompts[1].ID)
		}
	})

	t.Run("duplicate ID is rejected", func(t *testing.T) {
		m := &sam2Model{}

		p := SAMPrompt{
			ID:     "dup-id",
			Type:   "point",
			Points: [][2]int{{50, 50}},
			Label:  "test",
		}

		if err := m.AddPrompt(p); err != nil {
			t.Fatalf("first AddPrompt: %v", err)
		}
		err := m.AddPrompt(p)
		if err == nil {
			t.Fatal("expected error for duplicate ID, got nil")
		}
		if !errors.Is(err, ErrPromptFailed) {
			t.Errorf("expected ErrPromptFailed, got %v", err)
		}
	})

	t.Run("empty ID is rejected", func(t *testing.T) {
		m := &sam2Model{}

		p := SAMPrompt{
			ID:   "",
			Type: "point",
		}
		err := m.AddPrompt(p)
		if err == nil {
			t.Fatal("expected error for empty ID, got nil")
		}
		if !errors.Is(err, ErrPromptFailed) {
			t.Errorf("expected ErrPromptFailed, got %v", err)
		}
	})

	t.Run("invalid type is rejected", func(t *testing.T) {
		m := &sam2Model{}

		p := SAMPrompt{
			ID:   "bad-type",
			Type: "polygon",
		}
		err := m.AddPrompt(p)
		if err == nil {
			t.Fatal("expected error for invalid type, got nil")
		}
		if !errors.Is(err, ErrPromptFailed) {
			t.Errorf("expected ErrPromptFailed, got %v", err)
		}
	})

	t.Run("remove prompt", func(t *testing.T) {
		m := &sam2Model{}

		p1 := SAMPrompt{ID: "r1", Type: "point", Points: [][2]int{{10, 10}}, Label: "a"}
		p2 := SAMPrompt{ID: "r2", Type: "point", Points: [][2]int{{20, 20}}, Label: "b"}
		p3 := SAMPrompt{ID: "r3", Type: "box", Box: [4]int{0, 0, 100, 100}, Label: "c"}

		_ = m.AddPrompt(p1)
		_ = m.AddPrompt(p2)
		_ = m.AddPrompt(p3)

		if err := m.RemovePrompt("r2"); err != nil {
			t.Fatalf("RemovePrompt r2: %v", err)
		}

		prompts := m.Prompts()
		if len(prompts) != 2 {
			t.Fatalf("expected 2 prompts after removal, got %d", len(prompts))
		}
		for _, p := range prompts {
			if p.ID == "r2" {
				t.Error("removed prompt r2 should not be present")
			}
		}
	})

	t.Run("remove nonexistent prompt", func(t *testing.T) {
		m := &sam2Model{}

		err := m.RemovePrompt("nonexistent")
		if err == nil {
			t.Fatal("expected error for nonexistent prompt, got nil")
		}
		if !errors.Is(err, ErrPromptFailed) {
			t.Errorf("expected ErrPromptFailed, got %v", err)
		}
	})

	t.Run("prompts returns copy", func(t *testing.T) {
		m := &sam2Model{}

		_ = m.AddPrompt(SAMPrompt{ID: "orig", Type: "point", Points: [][2]int{{1, 1}}, Label: "x"})

		p1 := m.Prompts()
		p1[0].ID = "mutated"

		p2 := m.Prompts()
		if p2[0].ID != "orig" {
			t.Errorf("Prompts() did not return a copy: got ID %q, want %q", p2[0].ID, "orig")
		}
	})

	t.Run("empty prompts returns nil", func(t *testing.T) {
		m := &sam2Model{}
		prompts := m.Prompts()
		if prompts != nil {
			t.Errorf("expected nil for empty prompts, got %v", prompts)
		}
	})
}

func TestSAM2PromptOnNilReceiver(t *testing.T) {
	t.Run("AddPrompt on nil returns ErrDisabled", func(t *testing.T) {
		var m *sam2Model
		err := m.AddPrompt(SAMPrompt{ID: "test", Type: "point"})
		if !errors.Is(err, ErrDisabled) {
			t.Errorf("expected ErrDisabled, got %v", err)
		}
	})

	t.Run("RemovePrompt on nil returns ErrDisabled", func(t *testing.T) {
		var m *sam2Model
		err := m.RemovePrompt("test")
		if !errors.Is(err, ErrDisabled) {
			t.Errorf("expected ErrDisabled, got %v", err)
		}
	})

	t.Run("Prompts on nil returns nil", func(t *testing.T) {
		var m *sam2Model
		prompts := m.Prompts()
		if prompts != nil {
			t.Errorf("expected nil from nil receiver, got %v", prompts)
		}
	})
}

func TestSAM2CloseStub(t *testing.T) {
	t.Run("close on nil is safe", func(t *testing.T) {
		var m *sam2Model
		m.Close() // must not panic
	})

	t.Run("close on zero struct is safe", func(t *testing.T) {
		m := &sam2Model{}
		m.Close() // must not panic
	})

	t.Run("double close is safe", func(t *testing.T) {
		m := &sam2Model{}
		m.Close()
		m.Close() // must not panic
	})
}
