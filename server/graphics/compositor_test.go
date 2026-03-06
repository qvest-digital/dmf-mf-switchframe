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
