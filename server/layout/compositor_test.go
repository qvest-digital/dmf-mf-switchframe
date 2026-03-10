package layout

import (
	"image"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestCompositor_IngestAndNeedsSource(t *testing.T) {
	c := NewCompositor(1920, 1080)

	l := &Layout{
		Name: "pip",
		Slots: []LayoutSlot{
			{SourceKey: "cam2", Rect: image.Rect(1440, 0, 1920, 270), Enabled: true},
		},
	}
	c.SetLayout(l)

	require.True(t, c.NeedsSource("cam2"))
	require.False(t, c.NeedsSource("cam3"))

	// Ingest a frame
	yuv := makeYUV420(1920, 1080, 235, 128, 128)
	c.IngestSourceFrame("cam2", yuv, 1920, 1080)

	require.True(t, c.HasFrame("cam2"))
}

func TestCompositor_ProcessFrame(t *testing.T) {
	c := NewCompositor(8, 8)

	// Layout: 4x4 PIP at (4,0)
	l := &Layout{
		Name: "pip",
		Slots: []LayoutSlot{
			{SourceKey: "cam2", Rect: image.Rect(4, 0, 8, 4), Enabled: true},
		},
	}
	c.SetLayout(l)

	// Ingest white source
	src := makeYUV420(4, 4, 235, 128, 128)
	c.IngestSourceFrame("cam2", src, 4, 4)

	// Process on black background
	bg := makeYUV420(8, 8, 16, 128, 128)
	result := c.ProcessFrame(bg, 8, 8)

	// PIP region (4,0) should be white
	require.Equal(t, byte(235), result[0*8+4], "PIP at (4,0) should be white Y")
	// Background (0,0) should be black
	require.Equal(t, byte(16), result[0], "background should be black Y")
}

func TestCompositor_InactiveWhenEmpty(t *testing.T) {
	c := NewCompositor(1920, 1080)
	require.False(t, c.Active())

	l := &Layout{Name: "pip", Slots: []LayoutSlot{
		{SourceKey: "cam2", Rect: image.Rect(0, 0, 480, 270), Enabled: true},
	}}
	c.SetLayout(l)
	require.True(t, c.Active())

	c.SetLayout(nil)
	require.False(t, c.Active())
}

func TestCompositor_NilSourceRendersGray(t *testing.T) {
	c := NewCompositor(8, 8)

	l := &Layout{
		Name: "pip",
		Slots: []LayoutSlot{
			{SourceKey: "missing", Rect: image.Rect(4, 0, 8, 4), Enabled: true},
		},
	}
	c.SetLayout(l)

	// No frame ingested for "missing" — should render gray
	bg := makeYUV420(8, 8, 16, 128, 128)
	result := c.ProcessFrame(bg, 8, 8)

	// PIP region should be gray (Y=128)
	require.Equal(t, byte(128), result[0*8+4], "missing source should render gray")
}

func TestCompositor_DisabledSlotSkipped(t *testing.T) {
	c := NewCompositor(8, 8)

	l := &Layout{
		Name: "pip",
		Slots: []LayoutSlot{
			{SourceKey: "cam2", Rect: image.Rect(4, 0, 8, 4), Enabled: false},
		},
	}
	c.SetLayout(l)

	src := makeYUV420(4, 4, 235, 128, 128)
	c.IngestSourceFrame("cam2", src, 4, 4)

	bg := makeYUV420(8, 8, 16, 128, 128)
	result := c.ProcessFrame(bg, 8, 8)

	// Disabled slot should not be composited
	require.Equal(t, byte(16), result[0*8+4], "disabled slot should not appear")
}

func TestCompositor_ZOrderSorting(t *testing.T) {
	c := NewCompositor(8, 8)

	l := &Layout{
		Name: "test",
		Slots: []LayoutSlot{
			// Higher z-order but listed first — should still render on top
			{SourceKey: "top", Rect: image.Rect(2, 2, 6, 6), ZOrder: 2, Enabled: true},
			{SourceKey: "bottom", Rect: image.Rect(0, 0, 8, 8), ZOrder: 0, Enabled: true},
		},
	}
	c.SetLayout(l)

	// Bottom source: mid-gray
	c.IngestSourceFrame("bottom", makeYUV420(8, 8, 100, 128, 128), 8, 8)
	// Top source: white
	c.IngestSourceFrame("top", makeYUV420(4, 4, 235, 128, 128), 4, 4)

	bg := makeYUV420(8, 8, 16, 128, 128)
	result := c.ProcessFrame(bg, 8, 8)

	// (3,3) is in both slots — top (white, z=2) should win
	require.Equal(t, byte(235), result[3*8+3], "higher z-order should be on top")
	// (0,0) is only in bottom — should be mid-gray
	require.Equal(t, byte(100), result[0], "lower z-order visible where not overlapped")
}
