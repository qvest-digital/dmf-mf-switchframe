package layout

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestPIPPreset(t *testing.T) {
	tests := []struct {
		position string
		expectX  int // expected Min.X
		expectY  int // expected Min.Y
	}{
		{"top-right", 1402, 22},
		{"top-left", 38, 22},
		{"bottom-right", 1402, 788},
		{"bottom-left", 38, 788},
	}

	for _, tt := range tests {
		t.Run(tt.position, func(t *testing.T) {
			l := PIPPreset(1920, 1080, "cam1", tt.position, 0.25)
			require.Len(t, l.Slots, 1)
			slot := l.Slots[0]
			require.Equal(t, "cam1", slot.SourceKey)
			require.Equal(t, 480, slot.Rect.Dx(), "PIP width = 25% of 1920")
			require.Equal(t, 270, slot.Rect.Dy(), "PIP height maintains 16:9")
			// Even alignment
			require.Equal(t, 0, slot.Rect.Min.X%2, "X must be even")
			require.Equal(t, 0, slot.Rect.Min.Y%2, "Y must be even")
			require.NoError(t, ValidateSlot(slot, 1920, 1080))
		})
	}
}

func TestSideBySidePreset(t *testing.T) {
	l := SideBySidePreset(1920, 1080, "cam1", "cam2", 4)
	require.Len(t, l.Slots, 2)
	// Both slots should cover the full height
	require.Equal(t, 1080, l.Slots[0].Rect.Dy())
	require.Equal(t, 1080, l.Slots[1].Rect.Dy())
	// Combined width should be ~1920 minus gap
	totalW := l.Slots[0].Rect.Dx() + l.Slots[1].Rect.Dx() + 4
	require.InDelta(t, 1920, totalW, 2) // allow rounding
	for i, slot := range l.Slots {
		require.NoError(t, ValidateSlot(slot, 1920, 1080), "slot %d", i)
	}
}

func TestQuadPreset(t *testing.T) {
	sources := [4]string{"cam1", "cam2", "cam3", "cam4"}
	l := QuadPreset(1920, 1080, sources, 4)
	require.Len(t, l.Slots, 4)
	for i, slot := range l.Slots {
		require.NoError(t, ValidateSlot(slot, 1920, 1080), "slot %d", i)
		require.Equal(t, sources[i], slot.SourceKey)
	}
}

func TestResolveBuiltinPreset(t *testing.T) {
	for _, name := range BuiltinPresets() {
		l := ResolveBuiltinPreset(name, 1920, 1080)
		require.NotNil(t, l, "preset %q should resolve", name)
		require.Equal(t, name, l.Name)
		require.NotEmpty(t, l.Slots, "preset %q should have slots", name)
	}

	// Unknown returns nil
	require.Nil(t, ResolveBuiltinPreset("nonexistent", 1920, 1080))
}

func TestBuiltinPresets(t *testing.T) {
	presets := BuiltinPresets()
	require.GreaterOrEqual(t, len(presets), 7)
	for _, name := range []string{"full", "pip-top-right", "pip-top-left", "pip-bottom-right", "pip-bottom-left", "side-by-side", "quad"} {
		found := false
		for _, p := range presets {
			if p == name {
				found = true
				break
			}
		}
		require.True(t, found, "preset %q should exist", name)
	}
}
