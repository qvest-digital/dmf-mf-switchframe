package graphics

import (
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestCompositor_DSKFadeDurationDefaultsTo1000(t *testing.T) {
	c := NewCompositor()
	defer c.Close()
	require.Equal(t, 1000, c.DSKFadeDurationMs())
	require.Equal(t, 1000, c.Status().DSKFadeDurationMs)
}

func TestCompositor_SetDSKFadeDurationValidatesRange(t *testing.T) {
	c := NewCompositor()
	defer c.Close()

	for _, bad := range []int{99, 5001, 0, -100} {
		err := c.SetDSKFadeDuration(bad)
		require.True(t, errors.Is(err, ErrInvalidDSKFadeDuration),
			"SetDSKFadeDuration(%d): want ErrInvalidDSKFadeDuration, got %v", bad, err)
	}

	require.NoError(t, c.SetDSKFadeDuration(100))
	require.Equal(t, 100, c.DSKFadeDurationMs())
	require.Equal(t, 100, c.Status().DSKFadeDurationMs)

	require.NoError(t, c.SetDSKFadeDuration(5000))
	require.Equal(t, 5000, c.DSKFadeDurationMs())

	require.NoError(t, c.SetDSKFadeDuration(1500))
	require.Equal(t, 1500, c.DSKFadeDurationMs())
}

// TestGraphicsCompositor_DSKFadeHonorsConfiguredDuration validates that
// AutoOn's fade completes after the configured duration, not some other
// derived rate. AC #4.
func TestGraphicsCompositor_DSKFadeHonorsConfiguredDuration(t *testing.T) {
	c := NewCompositor()
	defer c.Close()

	require.NoError(t, c.SetDSKFadeDuration(600))

	id, err := c.AddLayer()
	require.NoError(t, err)
	overlay := makeTestOverlay(320, 240)
	require.NoError(t, c.SetOverlay(id, overlay, 320, 240, "test"))

	dur := time.Duration(c.DSKFadeDurationMs()) * time.Millisecond
	require.NoError(t, c.AutoOn(id, dur))

	// At half-duration the fade must still be in progress — alpha has
	// not yet landed at 1.0. We avoid asserting a specific lower bound
	// because goroutine scheduling latency can push the first alpha
	// update back by tens of ms under CI load; the only robust signal
	// is "not yet complete".
	time.Sleep(dur / 2)
	require.Less(t, c.Status().Layers[0].FadePosition, 0.99,
		"half-duration: alpha should not yet have completed")

	// After dur + margin the fade must have reached 1.0.
	time.Sleep(dur + 150*time.Millisecond)
	require.Equal(t, 1.0, c.Status().Layers[0].FadePosition)
}

// TestCompositor_AnimationDurationUnaffectedByDSKFade validates AC #6:
// the existing AnimationConfig.DurationMs (per-layer animation) is not
// coupled to dskFadeDurationMs.
func TestCompositor_AnimationDurationUnaffectedByDSKFade(t *testing.T) {
	c := NewCompositor()
	defer c.Close()

	require.NoError(t, c.SetDSKFadeDuration(5000)) // deliberately long

	id, err := c.AddLayer()
	require.NoError(t, err)
	overlay := makeTestOverlay(320, 240)
	require.NoError(t, c.SetOverlay(id, overlay, 320, 240, "test"))
	require.NoError(t, c.On(id))

	toAlpha := 0.0
	cfg := AnimationConfig{
		Mode:       "transition",
		ToAlpha:    &toAlpha,
		DurationMs: 300,
		Easing:     "linear",
	}
	require.NoError(t, c.Animate(id, cfg))

	// After 400ms (> 300ms AnimationConfig.DurationMs, ≪ 5000ms DSK fade)
	// the transition animation must have completed — alpha ramped from 1.0 → 0.0.
	time.Sleep(400 * time.Millisecond)
	status := c.Status()
	require.Less(t, status.Layers[0].FadePosition, 0.05,
		"transition animation should finish on its own DurationMs (300ms), not DSK fade (5000ms)")
}

func TestCompositor_DSKFadeDurationMidFlightDoesNotRetime(t *testing.T) {
	c := NewCompositor()
	defer c.Close()

	require.NoError(t, c.SetDSKFadeDuration(2000))

	id, err := c.AddLayer()
	require.NoError(t, err)
	overlay := makeTestOverlay(320, 240)
	require.NoError(t, c.SetOverlay(id, overlay, 320, 240, "test"))

	dur := time.Duration(c.DSKFadeDurationMs()) * time.Millisecond
	require.NoError(t, c.AutoOn(id, dur))

	// Change the duration while fade is in-flight; the running fade must
	// keep the rate it was started with.
	time.Sleep(50 * time.Millisecond)
	require.NoError(t, c.SetDSKFadeDuration(300))
	require.Equal(t, 300, c.DSKFadeDurationMs())

	// The initial 2000ms fade has not yet completed; alpha should still be
	// below 1.0 well before the original 2s window elapses.
	time.Sleep(200 * time.Millisecond) // total 250ms elapsed on the fade
	mid := c.Status().Layers[0].FadePosition
	require.Less(t, mid, 0.5,
		"in-flight fade must keep its original 2000ms rate, not retune to 300ms mid-flight")
}
