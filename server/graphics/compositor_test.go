package graphics

import (
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func makeTestOverlay(w, h int) []byte {
	return make([]byte, w*h*4) // transparent black
}

func TestCompositor_Passthrough(t *testing.T) {
	c := NewCompositor()
	defer c.Close()

	// Without overlay set, Overlay() returns nil.
	rgba, w, h, alpha := c.Overlay()
	require.Nil(t, rgba, "expected nil overlay when inactive")
	require.Equal(t, 0, w)
	require.Equal(t, 0, h)
	require.Equal(t, float64(0), alpha)

	status := c.Status()
	require.False(t, status.Active, "expected inactive status")
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

	require.NoError(t, c.SetOverlay(overlay, 320, 240, "lower-third"))
	require.NoError(t, c.On())

	rgba, w, h, alpha := c.Overlay()
	require.NotNil(t, rgba, "expected overlay data when active")
	require.Equal(t, 320, w)
	require.Equal(t, 240, h)
	require.Equal(t, float64(1.0), alpha)

	status := c.Status()
	require.True(t, status.Active, "expected active status after On()")
	require.Equal(t, "lower-third", status.Template)
	require.Equal(t, float64(1.0), status.FadePosition)
}

func TestCompositor_Toggle(t *testing.T) {
	c := NewCompositor()
	defer c.Close()

	overlay := makeTestOverlay(320, 240)
	_ = c.SetOverlay(overlay, 320, 240, "full-screen")

	// CUT ON
	require.NoError(t, c.On())
	require.True(t, c.Status().Active, "expected active after On")

	// CUT OFF
	require.NoError(t, c.Off())
	require.False(t, c.Status().Active, "expected inactive after Off")

	// CUT ON again
	require.NoError(t, c.On())
	require.True(t, c.Status().Active, "expected active after second On")

	// Verify overlay data is accessible
	rgba, _, _, _ := c.Overlay()
	require.NotNil(t, rgba, "expected overlay after re-enable")
}

func TestCompositor_OnWithoutOverlayReturnsError(t *testing.T) {
	c := NewCompositor()
	defer c.Close()

	err := c.On()
	require.ErrorIs(t, err, ErrNoOverlay)
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
	require.NoError(t, c.AutoOn(50*time.Millisecond))

	// Should be active immediately, but fading in
	status := c.Status()
	require.True(t, status.Active, "expected active immediately after AutoOn")

	// Wait for fade to complete
	time.Sleep(100 * time.Millisecond)

	status = c.Status()
	require.True(t, status.Active, "expected still active after fade-in completes")
	require.Equal(t, float64(1.0), status.FadePosition, "fadePosition should be 1.0 after fade-in")

	// AUTO OFF
	require.NoError(t, c.AutoOff(50*time.Millisecond))

	// Wait for fade to complete
	time.Sleep(100 * time.Millisecond)

	status = c.Status()
	require.False(t, status.Active, "expected inactive after fade-out completes")
	require.Equal(t, float64(0.0), status.FadePosition, "fadePosition should be 0.0 after fade-out")

	// State change callback should have been called multiple times.
	changes := atomic.LoadInt32(&stateChanges)
	require.GreaterOrEqual(t, changes, int32(2), "expected >= 2 state changes")
}

func TestCompositor_Close(t *testing.T) {
	c := NewCompositor()
	overlay := makeTestOverlay(320, 240)
	_ = c.SetOverlay(overlay, 320, 240, "test")
	_ = c.On()

	c.Close()

	// After close, operations should fail.
	err := c.On()
	require.Error(t, err, "expected error after Close")

	err = c.Off()
	require.Error(t, err, "expected error after Close")
}

func TestCompositor_ClosedReturnsSentinel(t *testing.T) {
	c := NewCompositor()
	c.Close()

	err := c.On()
	require.ErrorIs(t, err, ErrCompositorClosed)

	err = c.Off()
	require.ErrorIs(t, err, ErrCompositorClosed)
}

func TestCompositor_SetOverlaySizeMismatch(t *testing.T) {
	c := NewCompositor()
	defer c.Close()

	// Wrong size: 10 bytes for a 320x240 overlay
	err := c.SetOverlay(make([]byte, 10), 320, 240, "test")
	require.Error(t, err, "expected error for size mismatch")
}

func TestCompositor_FadeActiveError(t *testing.T) {
	c := NewCompositor()
	defer c.Close()

	overlay := makeTestOverlay(320, 240)
	_ = c.SetOverlay(overlay, 320, 240, "test")

	// Start a long fade
	require.NoError(t, c.AutoOn(5*time.Second))

	// Starting another fade should fail
	err := c.AutoOn(500 * time.Millisecond)
	require.ErrorIs(t, err, ErrFadeActive)
}

func TestCompositor_CutDuringFade(t *testing.T) {
	c := NewCompositor()
	defer c.Close()

	overlay := makeTestOverlay(320, 240)
	_ = c.SetOverlay(overlay, 320, 240, "test")

	// Start a long fade
	require.NoError(t, c.AutoOn(5*time.Second))

	// CUT OFF should cancel the fade
	require.NoError(t, c.Off())

	status := c.Status()
	require.False(t, status.Active, "expected inactive after CUT OFF during fade")
}

func TestCompositor_AnimatePulse(t *testing.T) {
	c := NewCompositor()
	defer c.Close()

	overlay := makeTestOverlay(320, 240)
	require.NoError(t, c.SetOverlay(overlay, 320, 240, "test"))
	require.NoError(t, c.On())

	cfg := AnimationConfig{
		Mode:     "pulse",
		MinAlpha: 0.3,
		MaxAlpha: 1.0,
		SpeedHz:  5.0, // fast oscillation for test
	}
	require.NoError(t, c.Animate(cfg))

	// Let the animation run a bit
	time.Sleep(200 * time.Millisecond)

	// fadePosition should have oscillated away from 1.0 at some point.
	// Read it — it may not be exactly 1.0.
	status := c.Status()
	require.True(t, status.Active, "should still be active during animation")
	// The fade position should be within [minAlpha, maxAlpha]
	require.GreaterOrEqual(t, status.FadePosition, 0.3, "fadePosition should be >= minAlpha")
	require.LessOrEqual(t, status.FadePosition, 1.0, "fadePosition should be <= maxAlpha")

	require.NoError(t, c.StopAnimation())
}

func TestCompositor_AnimateRequiresActive(t *testing.T) {
	c := NewCompositor()
	defer c.Close()

	overlay := makeTestOverlay(320, 240)
	require.NoError(t, c.SetOverlay(overlay, 320, 240, "test"))
	// Do NOT call On()

	cfg := AnimationConfig{
		Mode:     "pulse",
		MinAlpha: 0.3,
		MaxAlpha: 1.0,
		SpeedHz:  2.0,
	}
	err := c.Animate(cfg)
	require.ErrorIs(t, err, ErrNotActive)
}

func TestCompositor_AnimateStopsOnOff(t *testing.T) {
	c := NewCompositor()
	defer c.Close()

	overlay := makeTestOverlay(320, 240)
	require.NoError(t, c.SetOverlay(overlay, 320, 240, "test"))
	require.NoError(t, c.On())

	cfg := AnimationConfig{
		Mode:     "pulse",
		MinAlpha: 0.2,
		MaxAlpha: 0.9,
		SpeedHz:  3.0,
	}
	require.NoError(t, c.Animate(cfg))

	// Let the animation start
	time.Sleep(50 * time.Millisecond)

	// Off() should cancel animation
	require.NoError(t, c.Off())

	// Verify animation goroutine stopped
	c.mu.RLock()
	animDone := c.animDone
	c.mu.RUnlock()
	require.Nil(t, animDone, "animDone should be nil after Off()")
}

func TestCompositor_StopAnimation(t *testing.T) {
	c := NewCompositor()
	defer c.Close()

	overlay := makeTestOverlay(320, 240)
	require.NoError(t, c.SetOverlay(overlay, 320, 240, "test"))
	require.NoError(t, c.On())

	cfg := AnimationConfig{
		Mode:     "pulse",
		MinAlpha: 0.2,
		MaxAlpha: 0.8,
		SpeedHz:  3.0,
	}
	require.NoError(t, c.Animate(cfg))

	time.Sleep(50 * time.Millisecond)

	require.NoError(t, c.StopAnimation())

	// fadePosition should be restored to 1.0
	status := c.Status()
	require.True(t, status.Active, "should still be active after StopAnimation")
	require.Equal(t, 1.0, status.FadePosition, "fadePosition should be restored to 1.0 after StopAnimation")

	// Animation state should be cleared
	c.mu.RLock()
	require.Nil(t, c.animConfig, "animConfig should be nil after StopAnimation")
	require.Nil(t, c.animDone, "animDone should be nil after StopAnimation")
	c.mu.RUnlock()
}

func TestCompositor_AnimateWhileFading(t *testing.T) {
	c := NewCompositor()
	defer c.Close()

	overlay := makeTestOverlay(320, 240)
	require.NoError(t, c.SetOverlay(overlay, 320, 240, "test"))

	// Start a long fade
	require.NoError(t, c.AutoOn(5*time.Second))

	// Animate during fade should fail
	cfg := AnimationConfig{
		Mode:     "pulse",
		MinAlpha: 0.3,
		MaxAlpha: 1.0,
		SpeedHz:  2.0,
	}
	err := c.Animate(cfg)
	require.ErrorIs(t, err, ErrFadeActive)
}

func TestCompositor_AnimateStateCallback(t *testing.T) {
	c := NewCompositor()
	defer c.Close()

	var stateChanges int32
	c.OnStateChange(func(s State) {
		atomic.AddInt32(&stateChanges, 1)
	})

	overlay := makeTestOverlay(320, 240)
	require.NoError(t, c.SetOverlay(overlay, 320, 240, "test"))
	require.NoError(t, c.On())
	// Reset counter after On()
	atomic.StoreInt32(&stateChanges, 0)

	cfg := AnimationConfig{
		Mode:     "pulse",
		MinAlpha: 0.2,
		MaxAlpha: 1.0,
		SpeedHz:  4.0,
	}
	require.NoError(t, c.Animate(cfg))

	// Let the animation run to generate state callbacks
	time.Sleep(300 * time.Millisecond)

	require.NoError(t, c.StopAnimation())

	changes := atomic.LoadInt32(&stateChanges)
	// At ~15fps state broadcast, 300ms should generate ~4-5 callbacks + the StopAnimation one
	require.GreaterOrEqual(t, changes, int32(2), "expected at least 2 state change callbacks during animation")
}

func TestCompositor_CloseCancelsAnimation(t *testing.T) {
	c := NewCompositor()

	overlay := makeTestOverlay(320, 240)
	require.NoError(t, c.SetOverlay(overlay, 320, 240, "test"))
	require.NoError(t, c.On())

	cfg := AnimationConfig{
		Mode:     "pulse",
		MinAlpha: 0.2,
		MaxAlpha: 1.0,
		SpeedHz:  3.0,
	}
	require.NoError(t, c.Animate(cfg))

	time.Sleep(50 * time.Millisecond)

	// Close should cancel the animation without goroutine leak
	c.Close()

	// Verify the compositor is closed — operations should fail
	err := c.On()
	require.ErrorIs(t, err, ErrCompositorClosed)
}

func TestCompositor_AnimateWhileAnimating(t *testing.T) {
	c := NewCompositor()
	defer c.Close()

	overlay := makeTestOverlay(320, 240)
	require.NoError(t, c.SetOverlay(overlay, 320, 240, "test"))
	require.NoError(t, c.On())

	cfg := AnimationConfig{
		Mode:     "pulse",
		MinAlpha: 0.3,
		MaxAlpha: 1.0,
		SpeedHz:  2.0,
	}
	require.NoError(t, c.Animate(cfg))

	// Trying to animate again while already animating should return ErrFadeActive
	err := c.Animate(cfg)
	require.ErrorIs(t, err, ErrFadeActive)

	require.NoError(t, c.StopAnimation())
}

func TestCompositor_AutoOnWhileAnimating(t *testing.T) {
	c := NewCompositor()
	defer c.Close()

	overlay := makeTestOverlay(320, 240)
	require.NoError(t, c.SetOverlay(overlay, 320, 240, "test"))
	require.NoError(t, c.On())

	cfg := AnimationConfig{
		Mode:     "pulse",
		MinAlpha: 0.3,
		MaxAlpha: 1.0,
		SpeedHz:  2.0,
	}
	require.NoError(t, c.Animate(cfg))

	// AutoOn/AutoOff while animating should return ErrFadeActive
	err := c.AutoOff(500 * time.Millisecond)
	require.ErrorIs(t, err, ErrFadeActive)

	require.NoError(t, c.StopAnimation())
}

func TestCompositor_AnimateStateFields(t *testing.T) {
	c := NewCompositor()
	defer c.Close()

	overlay := makeTestOverlay(320, 240)
	require.NoError(t, c.SetOverlay(overlay, 320, 240, "test"))
	require.NoError(t, c.On())

	cfg := AnimationConfig{
		Mode:     "pulse",
		MinAlpha: 0.3,
		MaxAlpha: 1.0,
		SpeedHz:  2.5,
	}
	require.NoError(t, c.Animate(cfg))

	status := c.Status()
	require.Equal(t, "pulse", status.AnimationMode, "AnimationMode should be 'pulse' during animation")
	require.Equal(t, 2.5, status.AnimationHz, "AnimationHz should match configured speedHz")

	require.NoError(t, c.StopAnimation())

	status = c.Status()
	require.Empty(t, status.AnimationMode, "AnimationMode should be empty after StopAnimation")
	require.Equal(t, float64(0), status.AnimationHz, "AnimationHz should be 0 after StopAnimation")
}

func TestCompositor_AutoOn_AlreadyActive_NoFlash(t *testing.T) {
	c := NewCompositor()
	defer c.Close()

	overlay := makeTestOverlay(320, 240)
	require.NoError(t, c.SetOverlay(overlay, 320, 240, "test"))

	// Activate with CUT ON (fully active, fadePosition=1.0)
	require.NoError(t, c.On())

	status := c.Status()
	require.True(t, status.Active)
	require.Equal(t, 1.0, status.FadePosition)

	// AutoOn while already fully active should be a no-op (no flash)
	require.NoError(t, c.AutoOn(500*time.Millisecond))

	// fadePosition should still be 1.0 — not reset to 0.0
	status = c.Status()
	require.True(t, status.Active)
	require.Equal(t, 1.0, status.FadePosition, "AutoOn on fully active overlay should not reset fadePosition")
}
