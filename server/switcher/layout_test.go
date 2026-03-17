package switcher

import (
	"image"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/zsiec/switchframe/server/layout"
)

func TestLayoutCompositor_InSwitcher(t *testing.T) {
	// Create a layout compositor
	lc := layout.NewCompositor(8, 8)

	// Set a layout with a PIP slot
	l := &layout.Layout{
		Name: "pip",
		Slots: []layout.Slot{
			{SourceKey: "cam2", Rect: image.Rect(4, 0, 8, 4), Enabled: true},
		},
	}
	lc.SetLayout(l)

	// Verify NeedsSource works
	require.True(t, lc.NeedsSource("cam2"))
	require.False(t, lc.NeedsSource("cam1"))

	// Ingest a white source frame
	src := make([]byte, 4*4*3/2) // 4x4 YUV420
	for i := 0; i < 4*4; i++ {
		src[i] = 235 // white Y
	}
	for i := 4 * 4; i < len(src); i++ {
		src[i] = 128 // neutral chroma
	}
	lc.IngestSourceFrame("cam2", src, 4, 4, 0)
	require.True(t, lc.HasFrame("cam2"))

	// Process on a black background
	bg := make([]byte, 8*8*3/2)
	for i := 0; i < 8*8; i++ {
		bg[i] = 16 // black Y
	}
	for i := 8 * 8; i < len(bg); i++ {
		bg[i] = 128 // neutral chroma
	}

	result := lc.ProcessFrame(bg, 8, 8)

	// PIP region (4,0) should be white
	require.Equal(t, byte(235), result[0*8+4], "PIP at (4,0) should be white Y")
	// Background (0,0) should be black
	require.Equal(t, byte(16), result[0], "background should be black Y")
}

func TestSetLayoutCompositor(t *testing.T) {
	programRelay := newTestRelay()
	sw := New(programRelay)
	lc := layout.NewCompositor(1920, 1080)
	sw.SetLayoutCompositor(lc)

	// Verify the compositor is set
	sw.mu.RLock()
	got := sw.layoutCompositor
	sw.mu.RUnlock()
	require.Equal(t, lc, got)
}
