package graphics

import (
	"sync/atomic"
	"testing"
	"time"
)

func makeTestOverlay(w, h int) []byte {
	return make([]byte, w*h*4) // transparent black
}

func TestCompositor_Passthrough(t *testing.T) {
	c := NewCompositor()
	defer c.Close()

	// Without overlay set, Overlay() returns nil.
	rgba, w, h, alpha := c.Overlay()
	if rgba != nil {
		t.Errorf("expected nil overlay when inactive, got %d bytes", len(rgba))
	}
	if w != 0 || h != 0 {
		t.Errorf("expected 0x0 dimensions, got %dx%d", w, h)
	}
	if alpha != 0 {
		t.Errorf("expected alpha 0, got %f", alpha)
	}

	status := c.Status()
	if status.Active {
		t.Error("expected inactive status")
	}
}

func TestCompositor_ActiveBlend(t *testing.T) {
	c := NewCompositor()
	defer c.Close()

	overlay := makeTestOverlay(320, 240)
	// Set some visible pixels
	for i := 0; i < len(overlay); i += 4 {
		overlay[i] = 255   // R
		overlay[i+1] = 0   // G
		overlay[i+2] = 0   // B
		overlay[i+3] = 128 // A (50%)
	}

	if err := c.SetOverlay(overlay, 320, 240, "lower-third"); err != nil {
		t.Fatalf("SetOverlay: %v", err)
	}

	if err := c.On(); err != nil {
		t.Fatalf("On: %v", err)
	}

	rgba, w, h, alpha := c.Overlay()
	if rgba == nil {
		t.Fatal("expected overlay data when active")
	}
	if w != 320 || h != 240 {
		t.Errorf("dimensions = %dx%d, want 320x240", w, h)
	}
	if alpha != 1.0 {
		t.Errorf("alpha = %f, want 1.0", alpha)
	}

	status := c.Status()
	if !status.Active {
		t.Error("expected active status after On()")
	}
	if status.Template != "lower-third" {
		t.Errorf("template = %q, want %q", status.Template, "lower-third")
	}
	if status.FadePosition != 1.0 {
		t.Errorf("fadePosition = %f, want 1.0", status.FadePosition)
	}
}

func TestCompositor_Toggle(t *testing.T) {
	c := NewCompositor()
	defer c.Close()

	overlay := makeTestOverlay(320, 240)
	_ = c.SetOverlay(overlay, 320, 240, "full-screen")

	// CUT ON
	if err := c.On(); err != nil {
		t.Fatalf("On: %v", err)
	}
	if !c.Status().Active {
		t.Error("expected active after On")
	}

	// CUT OFF
	if err := c.Off(); err != nil {
		t.Fatalf("Off: %v", err)
	}
	if c.Status().Active {
		t.Error("expected inactive after Off")
	}

	// CUT ON again
	if err := c.On(); err != nil {
		t.Fatalf("On (second): %v", err)
	}
	if !c.Status().Active {
		t.Error("expected active after second On")
	}

	// Verify overlay data is accessible
	rgba, _, _, _ := c.Overlay()
	if rgba == nil {
		t.Error("expected overlay after re-enable")
	}
}

func TestCompositor_OnWithoutOverlayReturnsError(t *testing.T) {
	c := NewCompositor()
	defer c.Close()

	err := c.On()
	if err != ErrNoOverlay {
		t.Errorf("On without overlay: err = %v, want %v", err, ErrNoOverlay)
	}
}

func TestCompositor_AutoOnAutoOff(t *testing.T) {
	c := NewCompositor()
	defer c.Close()

	var stateChanges int32
	c.OnStateChange(func(_ State) {
		atomic.AddInt32(&stateChanges, 1)
	})

	overlay := makeTestOverlay(320, 240)
	_ = c.SetOverlay(overlay, 320, 240, "ticker")

	// AUTO ON with short duration for testing
	if err := c.AutoOn(50 * time.Millisecond); err != nil {
		t.Fatalf("AutoOn: %v", err)
	}

	// Should be active immediately, but fading in
	status := c.Status()
	if !status.Active {
		t.Error("expected active immediately after AutoOn")
	}

	// Wait for fade to complete
	time.Sleep(100 * time.Millisecond)

	status = c.Status()
	if !status.Active {
		t.Error("expected still active after fade-in completes")
	}
	if status.FadePosition != 1.0 {
		t.Errorf("fadePosition = %f, want 1.0 after fade-in", status.FadePosition)
	}

	// AUTO OFF
	if err := c.AutoOff(50 * time.Millisecond); err != nil {
		t.Fatalf("AutoOff: %v", err)
	}

	// Wait for fade to complete
	time.Sleep(100 * time.Millisecond)

	status = c.Status()
	if status.Active {
		t.Error("expected inactive after fade-out completes")
	}
	if status.FadePosition != 0.0 {
		t.Errorf("fadePosition = %f, want 0.0 after fade-out", status.FadePosition)
	}

	// State change callback should have been called multiple times.
	changes := atomic.LoadInt32(&stateChanges)
	if changes < 2 {
		t.Errorf("expected >= 2 state changes, got %d", changes)
	}
}

func TestCompositor_Close(t *testing.T) {
	c := NewCompositor()
	overlay := makeTestOverlay(320, 240)
	_ = c.SetOverlay(overlay, 320, 240, "test")
	_ = c.On()

	c.Close()

	// After close, operations should fail.
	err := c.On()
	if err == nil {
		t.Error("expected error after Close, got nil")
	}

	err = c.Off()
	if err == nil {
		t.Error("expected error after Close, got nil")
	}
}

func TestCompositor_SetOverlaySizeMismatch(t *testing.T) {
	c := NewCompositor()
	defer c.Close()

	// Wrong size: 10 bytes for a 320x240 overlay
	err := c.SetOverlay(make([]byte, 10), 320, 240, "test")
	if err == nil {
		t.Error("expected error for size mismatch")
	}
}

func TestCompositor_FadeActiveError(t *testing.T) {
	c := NewCompositor()
	defer c.Close()

	overlay := makeTestOverlay(320, 240)
	_ = c.SetOverlay(overlay, 320, 240, "test")

	// Start a long fade
	if err := c.AutoOn(5 * time.Second); err != nil {
		t.Fatalf("AutoOn: %v", err)
	}

	// Starting another fade should fail
	err := c.AutoOn(500 * time.Millisecond)
	if err != ErrFadeActive {
		t.Errorf("expected ErrFadeActive, got %v", err)
	}
}

func TestCompositor_CutDuringFade(t *testing.T) {
	c := NewCompositor()
	defer c.Close()

	overlay := makeTestOverlay(320, 240)
	_ = c.SetOverlay(overlay, 320, 240, "test")

	// Start a long fade
	if err := c.AutoOn(5 * time.Second); err != nil {
		t.Fatalf("AutoOn: %v", err)
	}

	// CUT OFF should cancel the fade
	if err := c.Off(); err != nil {
		t.Fatalf("Off during fade: %v", err)
	}

	status := c.Status()
	if status.Active {
		t.Error("expected inactive after CUT OFF during fade")
	}
}
