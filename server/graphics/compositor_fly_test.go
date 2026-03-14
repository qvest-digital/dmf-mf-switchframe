package graphics

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestDeactivateOnComplete(t *testing.T) {
	c := NewCompositor()
	defer c.Close()

	id, err := c.AddLayer()
	require.NoError(t, err)

	// Upload a minimal overlay so On() works
	rgba := make([]byte, 1920*1080*4)
	require.NoError(t, c.SetOverlay(id, rgba, 1920, 1080, "test"))
	require.NoError(t, c.On(id))

	// Start a short transition animation with DeactivateOnComplete
	cfg := AnimationConfig{
		Mode:                 "transition",
		ToAlpha:              float64Ptr(0.0),
		DurationMs:           50,
		DeactivateOnComplete: true,
	}
	require.NoError(t, c.Animate(id, cfg))

	// Wait for animation to complete
	time.Sleep(200 * time.Millisecond)

	// Layer should be deactivated
	c.mu.RLock()
	layer := c.layers[id]
	active := layer.active
	c.mu.RUnlock()
	require.False(t, active, "layer should be deactivated after animation with DeactivateOnComplete")
}

func TestDeactivateOnComplete_NotSet(t *testing.T) {
	c := NewCompositor()
	defer c.Close()

	id, err := c.AddLayer()
	require.NoError(t, err)

	rgba := make([]byte, 1920*1080*4)
	require.NoError(t, c.SetOverlay(id, rgba, 1920, 1080, "test"))
	require.NoError(t, c.On(id))

	// Start a short transition without DeactivateOnComplete
	cfg := AnimationConfig{
		Mode:       "transition",
		ToAlpha:    float64Ptr(0.5),
		DurationMs: 50,
	}
	require.NoError(t, c.Animate(id, cfg))

	time.Sleep(200 * time.Millisecond)

	// Layer should still be active
	c.mu.RLock()
	layer := c.layers[id]
	active := layer.active
	c.mu.RUnlock()
	require.True(t, active, "layer should remain active when DeactivateOnComplete is false")
}

func float64Ptr(v float64) *float64 { return &v }
