package sidecar

import (
	"testing"
)

func TestGPUPoolConfigValidate(t *testing.T) {
	tests := []struct {
		name    string
		cfg     GPUPoolConfig
		wantErr bool
	}{
		{
			name:    "valid 1080p",
			cfg:     GPUPoolConfig{SlotCount: 8, Width: 1920, Height: 1080},
			wantErr: false,
		},
		{
			name:    "valid with explicit pitch",
			cfg:     GPUPoolConfig{SlotCount: 4, Width: 1920, Height: 1080, Pitch: 2048},
			wantErr: false,
		},
		{
			name:    "zero slots",
			cfg:     GPUPoolConfig{SlotCount: 0, Width: 1920, Height: 1080},
			wantErr: true,
		},
		{
			name:    "zero width",
			cfg:     GPUPoolConfig{SlotCount: 4, Width: 0, Height: 1080},
			wantErr: true,
		},
		{
			name:    "pitch less than width",
			cfg:     GPUPoolConfig{SlotCount: 4, Width: 1920, Height: 1080, Pitch: 1000},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.cfg.Validate()
			if (err != nil) != tt.wantErr {
				t.Errorf("Validate() err = %v, wantErr = %v", err, tt.wantErr)
			}
		})
	}
}

func TestGPUPoolConfigPitch(t *testing.T) {
	// 1920 is already 256-aligned (1920 / 256 = 7.5 → rounds to 2048).
	cfg := GPUPoolConfig{SlotCount: 4, Width: 1920, Height: 1080}
	if p := cfg.pitch(); p != 2048 {
		t.Errorf("pitch() = %d, want 2048 (256-aligned)", p)
	}

	// 1280 → (1280+255) &^ 255 = 1535 &^ 255 = 1280 (already aligned).
	cfg2 := GPUPoolConfig{SlotCount: 4, Width: 1280, Height: 720}
	if p := cfg2.pitch(); p != 1280 {
		t.Errorf("pitch() = %d, want 1280 (already aligned)", p)
	}

	// Explicit pitch takes precedence.
	cfg3 := GPUPoolConfig{SlotCount: 4, Width: 1920, Height: 1080, Pitch: 4096}
	if p := cfg3.pitch(); p != 4096 {
		t.Errorf("pitch() = %d, want 4096 (explicit)", p)
	}
}

func TestGPUPoolConfigNV12FrameSize(t *testing.T) {
	cfg := GPUPoolConfig{SlotCount: 4, Width: 1920, Height: 1080, Pitch: 2048}
	// NV12: pitch * height * 3/2
	want := 2048 * 1080 * 3 / 2
	if got := cfg.NV12FrameSize(); got != want {
		t.Errorf("NV12FrameSize() = %d, want %d", got, want)
	}
}

func TestGPUPoolSlotStateString(t *testing.T) {
	if SlotIdle.String() != "idle" {
		t.Errorf("SlotIdle.String() = %q", SlotIdle.String())
	}
	if SlotReady.String() != "ready" {
		t.Errorf("SlotReady.String() = %q", SlotReady.String())
	}
	if SlotProcessing.String() != "processing" {
		t.Errorf("SlotProcessing.String() = %q", SlotProcessing.String())
	}
	if SlotDone.String() != "done" {
		t.Errorf("SlotDone.String() = %q", SlotDone.String())
	}
	if GPUPoolSlotState(99).String() != "unknown(99)" {
		t.Errorf("unknown state String() = %q", GPUPoolSlotState(99).String())
	}
}

func TestGPUPoolControlSize(t *testing.T) {
	// 8 slots: 64 header + 8*64 slots = 576
	if got := GPUPoolControlSize(8); got != 576 {
		t.Errorf("GPUPoolControlSize(8) = %d, want 576", got)
	}
}

func TestGPUPoolControlInitAndValidate(t *testing.T) {
	cfg := GPUPoolConfig{SlotCount: 4, Width: 1920, Height: 1080, Pitch: 2048}
	size := GPUPoolControlSize(4)
	data := make([]byte, size)

	ctrl, err := NewGPUPoolControl(data, 4)
	if err != nil {
		t.Fatalf("NewGPUPoolControl: %v", err)
	}

	// Before init, validate should fail (magic is 0).
	if err := ctrl.ValidateHeader(); err == nil {
		t.Fatal("expected error before InitHeader")
	}

	ctrl.InitHeader(cfg)

	if err := ctrl.ValidateHeader(); err != nil {
		t.Fatalf("ValidateHeader after init: %v", err)
	}
}

func TestGPUPoolControlTooSmall(t *testing.T) {
	data := make([]byte, 10)
	_, err := NewGPUPoolControl(data, 4)
	if err == nil {
		t.Fatal("expected error for too-small region")
	}
}

func TestGPUPoolControlSlotLifecycle(t *testing.T) {
	cfg := GPUPoolConfig{SlotCount: 4, Width: 1920, Height: 1080}
	size := GPUPoolControlSize(4)
	data := make([]byte, size)

	ctrl, _ := NewGPUPoolControl(data, 4)
	ctrl.InitHeader(cfg)

	// Initial state is Idle (zero).
	for i := 0; i < 4; i++ {
		if s := ctrl.LoadState(i); s != SlotIdle {
			t.Errorf("slot %d initial state = %v, want idle", i, s)
		}
	}

	// Host writes frame to slot 0 and marks ready.
	if !ctrl.MarkReady(0, 12345) {
		t.Fatal("MarkReady(0) failed")
	}
	if s := ctrl.LoadState(0); s != SlotReady {
		t.Errorf("slot 0 state = %v, want ready", s)
	}
	if pts := ctrl.LoadPTS(0); pts != 12345 {
		t.Errorf("slot 0 PTS = %d, want 12345", pts)
	}

	// Double MarkReady should fail (already Ready, not Idle).
	if ctrl.MarkReady(0, 99999) {
		t.Fatal("double MarkReady should fail")
	}

	// Worker picks it up.
	if !ctrl.MarkProcessing(0) {
		t.Fatal("MarkProcessing(0) failed")
	}
	if s := ctrl.LoadState(0); s != SlotProcessing {
		t.Errorf("slot 0 state = %v, want processing", s)
	}

	// Worker finishes inference.
	if !ctrl.MarkDone(0) {
		t.Fatal("MarkDone(0) failed")
	}
	if s := ctrl.LoadState(0); s != SlotDone {
		t.Errorf("slot 0 state = %v, want done", s)
	}

	// Host reads results and reclaims.
	if !ctrl.MarkIdle(0) {
		t.Fatal("MarkIdle(0) failed")
	}
	if s := ctrl.LoadState(0); s != SlotIdle {
		t.Errorf("slot 0 state = %v, want idle", s)
	}
}

func TestGPUPoolControlFindSlot(t *testing.T) {
	cfg := GPUPoolConfig{SlotCount: 4, Width: 1920, Height: 1080}
	size := GPUPoolControlSize(4)
	data := make([]byte, size)
	ctrl, _ := NewGPUPoolControl(data, 4)
	ctrl.InitHeader(cfg)

	// All idle initially.
	if s := ctrl.FindSlot(SlotIdle); s != 0 {
		t.Errorf("FindSlot(Idle) = %d, want 0", s)
	}
	if s := ctrl.FindSlot(SlotReady); s != -1 {
		t.Errorf("FindSlot(Ready) = %d, want -1", s)
	}

	// Mark slots 0 and 1 ready.
	ctrl.MarkReady(0, 0)
	ctrl.MarkReady(1, 0)

	// FindSlot(Idle) should return 2 (first idle).
	if s := ctrl.FindSlot(SlotIdle); s != 2 {
		t.Errorf("FindSlot(Idle) = %d, want 2", s)
	}
	// FindSlot(Ready) should return 0 (first ready).
	if s := ctrl.FindSlot(SlotReady); s != 0 {
		t.Errorf("FindSlot(Ready) = %d, want 0", s)
	}
}

func TestGPUPoolControlConcurrentStateTransitions(t *testing.T) {
	cfg := GPUPoolConfig{SlotCount: 1, Width: 1920, Height: 1080}
	size := GPUPoolControlSize(1)
	data := make([]byte, size)
	ctrl, _ := NewGPUPoolControl(data, 1)
	ctrl.InitHeader(cfg)

	// Simulate host and worker racing on the same slot.
	// Only one CAS should succeed.
	ctrl.MarkReady(0, 0)

	successes := 0
	done := make(chan bool, 2)

	// Two goroutines try to mark processing.
	for i := 0; i < 2; i++ {
		go func() {
			done <- ctrl.MarkProcessing(0)
		}()
	}

	for i := 0; i < 2; i++ {
		if <-done {
			successes++
		}
	}

	if successes != 1 {
		t.Errorf("expected exactly 1 successful CAS, got %d", successes)
	}
}
