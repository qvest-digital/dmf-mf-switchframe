package layout

import "image"

// PIPPreset creates a standard PIP layout with a secondary source in a corner.
// size is the PIP width as a fraction of frame width (e.g., 0.25 = quarter width).
func PIPPreset(frameW, frameH int, source, position string, size float64) *Layout {
	pipW := EvenAlign(int(float64(frameW) * size))
	pipH := EvenAlign(pipW * frameH / frameW)
	margin := EvenAlign(int(float64(frameW) * 0.02))

	var rect image.Rectangle
	switch position {
	case "top-left":
		rect = image.Rect(margin, margin, margin+pipW, margin+pipH)
	case "top-right":
		rect = image.Rect(frameW-margin-pipW, margin, frameW-margin, margin+pipH)
	case "bottom-left":
		rect = image.Rect(margin, frameH-margin-pipH, margin+pipW, frameH-margin)
	case "bottom-right":
		rect = image.Rect(frameW-margin-pipW, frameH-margin-pipH, frameW-margin, frameH-margin)
	default:
		rect = image.Rect(frameW-margin-pipW, margin, frameW-margin, margin+pipH) // default top-right
	}

	return &Layout{
		Name: "pip-" + position,
		Slots: []LayoutSlot{{
			SourceKey:  source,
			Rect:       rect,
			ZOrder:     1,
			Enabled:    true,
			Border:     BorderConfig{Width: 2, ColorY: 235, ColorCb: 128, ColorCr: 128},
			Transition: SlotTransition{Type: "cut"},
		}},
	}
}

// SideBySidePreset creates a 50/50 split layout.
func SideBySidePreset(frameW, frameH int, leftSource, rightSource string, gap int) *Layout {
	gap = EvenAlign(gap)
	leftW := EvenAlign((frameW - gap) / 2)
	rightW := EvenAlign(frameW - gap - leftW)

	return &Layout{
		Name: "side-by-side",
		Slots: []LayoutSlot{
			{SourceKey: leftSource, Rect: image.Rect(0, 0, leftW, frameH), ZOrder: 0, Enabled: true},
			{SourceKey: rightSource, Rect: image.Rect(leftW+gap, 0, leftW+gap+rightW, frameH), ZOrder: 0, Enabled: true},
		},
	}
}

// QuadPreset creates a 2x2 grid layout.
func QuadPreset(frameW, frameH int, sources [4]string, gap int) *Layout {
	gap = EvenAlign(gap)
	slotW := EvenAlign((frameW - gap) / 2)
	slotH := EvenAlign((frameH - gap) / 2)

	return &Layout{
		Name: "quad",
		Slots: []LayoutSlot{
			{SourceKey: sources[0], Rect: image.Rect(0, 0, slotW, slotH), ZOrder: 0, Enabled: true},
			{SourceKey: sources[1], Rect: image.Rect(slotW+gap, 0, slotW+gap+slotW, slotH), ZOrder: 0, Enabled: true},
			{SourceKey: sources[2], Rect: image.Rect(0, slotH+gap, slotW, slotH+gap+slotH), ZOrder: 0, Enabled: true},
			{SourceKey: sources[3], Rect: image.Rect(slotW+gap, slotH+gap, slotW+gap+slotW, slotH+gap+slotH), ZOrder: 0, Enabled: true},
		},
	}
}

// BuiltinPresets returns the names of all built-in layout presets.
func BuiltinPresets() []string {
	return []string{
		"full",
		"pip-top-right",
		"pip-top-left",
		"pip-bottom-right",
		"pip-bottom-left",
		"side-by-side",
		"quad",
	}
}
