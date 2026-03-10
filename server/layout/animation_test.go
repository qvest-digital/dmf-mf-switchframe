package layout

import (
	"image"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestAnimation_Progress(t *testing.T) {
	a := &Animation{
		StartTime: time.Now().Add(-500 * time.Millisecond),
		Duration:  time.Second,
	}
	p := a.Progress()
	require.InDelta(t, 0.5, p, 0.1)
}

func TestAnimation_ProgressComplete(t *testing.T) {
	a := &Animation{
		StartTime: time.Now().Add(-2 * time.Second),
		Duration:  time.Second,
	}
	require.Equal(t, 1.0, a.Progress())
}

func TestAnimation_InterpolateRect(t *testing.T) {
	a := &Animation{
		FromRect: image.Rect(0, 0, 100, 100),
		ToRect:   image.Rect(200, 200, 400, 400),
	}
	r := a.InterpolateRect(0.5)
	require.Equal(t, 100, r.Min.X)
	require.Equal(t, 100, r.Min.Y)
	// Even-aligned
	require.Equal(t, 0, r.Min.X%2)
	require.Equal(t, 0, r.Min.Y%2)
}

func TestAnimation_InterpolateAlpha(t *testing.T) {
	a := &Animation{FromAlpha: 0.0, ToAlpha: 1.0}
	require.InDelta(t, 0.5, a.InterpolateAlpha(0.5), 0.001)
}

func TestFlyInOrigin(t *testing.T) {
	target := image.Rect(1440, 20, 1920, 290)
	origin := FlyInOrigin(target, 1920, 1080)
	// Should fly in from the right edge
	require.GreaterOrEqual(t, origin.Min.X, 1920, "should start off-screen right")
}

func TestFlyInOrigin_BottomLeft(t *testing.T) {
	target := image.Rect(38, 788, 518, 1058)
	origin := FlyInOrigin(target, 1920, 1080)
	// Should fly in from the bottom edge (closer to bottom)
	require.GreaterOrEqual(t, origin.Min.Y, 1080, "should start off-screen bottom")
}

func TestCompositor_SlotOn(t *testing.T) {
	c := NewCompositor(8, 8)
	l := &Layout{
		Name: "pip",
		Slots: []LayoutSlot{
			{SourceKey: "cam2", Rect: image.Rect(4, 0, 8, 4), Enabled: false,
				Transition: SlotTransition{Type: "cut"}},
		},
	}
	c.SetLayout(l)

	c.SlotOn(0)
	updated := c.layout.Load()
	require.True(t, updated.Slots[0].Enabled)
}

func TestCompositor_SlotOff(t *testing.T) {
	c := NewCompositor(8, 8)
	l := &Layout{
		Name: "pip",
		Slots: []LayoutSlot{
			{SourceKey: "cam2", Rect: image.Rect(4, 0, 8, 4), Enabled: true,
				Transition: SlotTransition{Type: "cut"}},
		},
	}
	c.SetLayout(l)

	c.SlotOff(0)
	updated := c.layout.Load()
	require.False(t, updated.Slots[0].Enabled)
}

func TestCompositor_SlotOn_Dissolve(t *testing.T) {
	c := NewCompositor(8, 8)
	l := &Layout{
		Name: "pip",
		Slots: []LayoutSlot{
			{SourceKey: "cam2", Rect: image.Rect(4, 0, 8, 4), Enabled: false,
				Transition: SlotTransition{Type: "dissolve", DurationMs: 200}},
		},
	}
	c.SetLayout(l)

	c.SlotOn(0)

	// Should have an animation in progress
	c.mu.Lock()
	require.Len(t, c.animations, 1)
	require.Equal(t, 0.0, c.animations[0].FromAlpha)
	require.Equal(t, 1.0, c.animations[0].ToAlpha)
	c.mu.Unlock()

	// Slot should be enabled
	updated := c.layout.Load()
	require.True(t, updated.Slots[0].Enabled)
}

func TestCompositor_SlotOn_Fly(t *testing.T) {
	c := NewCompositor(1920, 1080)
	l := &Layout{
		Name: "pip",
		Slots: []LayoutSlot{
			{SourceKey: "cam2", Rect: image.Rect(1440, 20, 1920, 290), Enabled: false,
				Transition: SlotTransition{Type: "fly", DurationMs: 300}},
		},
	}
	c.SetLayout(l)

	c.SlotOn(0)

	// Should have fly animation
	c.mu.Lock()
	require.Len(t, c.animations, 1)
	anim := c.animations[0]
	// FromRect should be off-screen
	require.GreaterOrEqual(t, anim.FromRect.Min.X, 1920)
	// Alpha stays 1.0 (position-based animation)
	require.Equal(t, 1.0, anim.FromAlpha)
	require.Equal(t, 1.0, anim.ToAlpha)
	c.mu.Unlock()
}
