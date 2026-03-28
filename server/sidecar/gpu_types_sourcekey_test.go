package sidecar

import "testing"

func TestGPUPoolControlSourceKey_RoundTrip(t *testing.T) {
	// Create a control region with 4 slots.
	size := GPUPoolControlSize(4)
	data := make([]byte, size)
	ctrl, err := NewGPUPoolControl(data, 4)
	if err != nil {
		t.Fatal(err)
	}
	ctrl.InitHeader(GPUPoolConfig{SlotCount: 4, Width: 64, Height: 64})

	// Write and read back a key.
	ctrl.SetSourceKey(0, "cam1")
	if got := ctrl.LoadSourceKey(0); got != "cam1" {
		t.Errorf("slot 0: got %q, want %q", got, "cam1")
	}

	// Different slots are independent.
	ctrl.SetSourceKey(1, "cam2")
	ctrl.SetSourceKey(2, "__ar__")
	ctrl.SetSourceKey(3, "__track:cam3")
	if got := ctrl.LoadSourceKey(1); got != "cam2" {
		t.Errorf("slot 1: got %q, want %q", got, "cam2")
	}
	if got := ctrl.LoadSourceKey(2); got != "__ar__" {
		t.Errorf("slot 2: got %q, want %q", got, "__ar__")
	}
	if got := ctrl.LoadSourceKey(3); got != "__track:cam3" {
		t.Errorf("slot 3: got %q, want %q", got, "__track:cam3")
	}

	// Slot 0 still has its original value.
	if got := ctrl.LoadSourceKey(0); got != "cam1" {
		t.Errorf("slot 0 after other writes: got %q, want %q", got, "cam1")
	}
}

func TestGPUPoolControlSourceKey_Truncation(t *testing.T) {
	size := GPUPoolControlSize(1)
	data := make([]byte, size)
	ctrl, err := NewGPUPoolControl(data, 1)
	if err != nil {
		t.Fatal(err)
	}
	ctrl.InitHeader(GPUPoolConfig{SlotCount: 1, Width: 64, Height: 64})

	// Key longer than 31 bytes should be truncated.
	longKey := "abcdefghijklmnopqrstuvwxyz123456789" // 35 chars
	ctrl.SetSourceKey(0, longKey)
	got := ctrl.LoadSourceKey(0)
	if len(got) != 31 {
		t.Errorf("expected truncated length 31, got %d (%q)", len(got), got)
	}
	if got != longKey[:31] {
		t.Errorf("truncation mismatch: got %q, want %q", got, longKey[:31])
	}
}

func TestGPUPoolControlSourceKey_Empty(t *testing.T) {
	size := GPUPoolControlSize(1)
	data := make([]byte, size)
	ctrl, err := NewGPUPoolControl(data, 1)
	if err != nil {
		t.Fatal(err)
	}
	ctrl.InitHeader(GPUPoolConfig{SlotCount: 1, Width: 64, Height: 64})

	// Empty key by default.
	if got := ctrl.LoadSourceKey(0); got != "" {
		t.Errorf("expected empty key, got %q", got)
	}

	// Write empty key explicitly.
	ctrl.SetSourceKey(0, "cam1")
	ctrl.SetSourceKey(0, "")
	if got := ctrl.LoadSourceKey(0); got != "" {
		t.Errorf("expected empty after clear, got %q", got)
	}
}

func TestGPUPoolControlSourceKey_Overwrite(t *testing.T) {
	size := GPUPoolControlSize(1)
	data := make([]byte, size)
	ctrl, err := NewGPUPoolControl(data, 1)
	if err != nil {
		t.Fatal(err)
	}
	ctrl.InitHeader(GPUPoolConfig{SlotCount: 1, Width: 64, Height: 64})

	ctrl.SetSourceKey(0, "first_key")
	ctrl.SetSourceKey(0, "second")
	if got := ctrl.LoadSourceKey(0); got != "second" {
		t.Errorf("overwrite failed: got %q, want %q", got, "second")
	}

	// Overwrite with longer key should work (no leftover bytes).
	ctrl.SetSourceKey(0, "ab")
	ctrl.SetSourceKey(0, "much_longer_key_here")
	if got := ctrl.LoadSourceKey(0); got != "much_longer_key_here" {
		t.Errorf("long overwrite: got %q, want %q", got, "much_longer_key_here")
	}

	// Overwrite with shorter key should not have leftover.
	ctrl.SetSourceKey(0, "xy")
	if got := ctrl.LoadSourceKey(0); got != "xy" {
		t.Errorf("short overwrite: got %q, want %q", got, "xy")
	}
}

func TestGPUPoolControlSourceKey_OutOfBounds(t *testing.T) {
	size := GPUPoolControlSize(2)
	data := make([]byte, size)
	ctrl, err := NewGPUPoolControl(data, 2)
	if err != nil {
		t.Fatal(err)
	}
	ctrl.InitHeader(GPUPoolConfig{SlotCount: 2, Width: 64, Height: 64})

	// Out of bounds should not panic.
	ctrl.SetSourceKey(-1, "bad")
	ctrl.SetSourceKey(2, "bad")
	ctrl.SetSourceKey(100, "bad")

	if got := ctrl.LoadSourceKey(-1); got != "" {
		t.Error("expected empty for out of bounds")
	}
	if got := ctrl.LoadSourceKey(2); got != "" {
		t.Error("expected empty for out of bounds")
	}
}
