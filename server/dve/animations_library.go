package dve

// ============================================================================
// DVE Editor Animation Library
//
// Pre-canned keyframe animations for slots in the DVE editor.
// Categories: Entrances, Exits, Emphasis/Loops, and 3D effects.
// Each returns an Animation that can be applied to a slot.
// ============================================================================

// --- Entrance Animations (slot appears) ---

// AnimFlyInLeft flies a slot in from off-screen left.
func AnimFlyInLeft(slotIdx int, durationMs int) *Animation {
	return &Animation{
		Name: "fly-in-left", DurationMs: durationMs,
		Tracks: []Track{
			{Param: "positionX", SlotIndex: slotIdx, Keyframes: []Keyframe{
				{TimeMs: 0, Value: -1920, EaseOutX: 0.25, EaseOutY: 0.1},
				{TimeMs: durationMs, Value: 0},
			}},
			{Param: "opacity", SlotIndex: slotIdx, Keyframes: []Keyframe{
				{TimeMs: 0, Value: 0},
				{TimeMs: durationMs / 4, Value: 1, EaseOutX: 1, EaseOutY: 1},
			}},
		},
	}
}

// AnimFlyInRight flies a slot in from off-screen right.
func AnimFlyInRight(slotIdx int, durationMs int) *Animation {
	return &Animation{
		Name: "fly-in-right", DurationMs: durationMs,
		Tracks: []Track{
			{Param: "positionX", SlotIndex: slotIdx, Keyframes: []Keyframe{
				{TimeMs: 0, Value: 1920, EaseOutX: 0.25, EaseOutY: 0.1},
				{TimeMs: durationMs, Value: 0},
			}},
			{Param: "opacity", SlotIndex: slotIdx, Keyframes: []Keyframe{
				{TimeMs: 0, Value: 0},
				{TimeMs: durationMs / 4, Value: 1, EaseOutX: 1, EaseOutY: 1},
			}},
		},
	}
}

// AnimFlyInTop flies a slot in from off-screen top.
func AnimFlyInTop(slotIdx int, durationMs int) *Animation {
	return &Animation{
		Name: "fly-in-top", DurationMs: durationMs,
		Tracks: []Track{
			{Param: "positionY", SlotIndex: slotIdx, Keyframes: []Keyframe{
				{TimeMs: 0, Value: -1080, EaseOutX: 0.25, EaseOutY: 0.1},
				{TimeMs: durationMs, Value: 0},
			}},
			{Param: "opacity", SlotIndex: slotIdx, Keyframes: []Keyframe{
				{TimeMs: 0, Value: 0},
				{TimeMs: durationMs / 4, Value: 1, EaseOutX: 1, EaseOutY: 1},
			}},
		},
	}
}

// AnimFlyInBottom flies a slot in from off-screen bottom.
func AnimFlyInBottom(slotIdx int, durationMs int) *Animation {
	return &Animation{
		Name: "fly-in-bottom", DurationMs: durationMs,
		Tracks: []Track{
			{Param: "positionY", SlotIndex: slotIdx, Keyframes: []Keyframe{
				{TimeMs: 0, Value: 1080, EaseOutX: 0.25, EaseOutY: 0.1},
				{TimeMs: durationMs, Value: 0},
			}},
			{Param: "opacity", SlotIndex: slotIdx, Keyframes: []Keyframe{
				{TimeMs: 0, Value: 0},
				{TimeMs: durationMs / 4, Value: 1, EaseOutX: 1, EaseOutY: 1},
			}},
		},
	}
}

// AnimZoomIn zooms a slot from small to full size at center.
func AnimZoomIn(slotIdx int, targetW, targetH, targetX, targetY float64, durationMs int) *Animation {
	return &Animation{
		Name: "zoom-in", DurationMs: durationMs,
		Tracks: []Track{
			{Param: "width", SlotIndex: slotIdx, Keyframes: []Keyframe{
				{TimeMs: 0, Value: targetW * 0.1, EaseOutX: 0.2, EaseOutY: 0},
				{TimeMs: durationMs, Value: targetW},
			}},
			{Param: "height", SlotIndex: slotIdx, Keyframes: []Keyframe{
				{TimeMs: 0, Value: targetH * 0.1, EaseOutX: 0.2, EaseOutY: 0},
				{TimeMs: durationMs, Value: targetH},
			}},
			{Param: "positionX", SlotIndex: slotIdx, Keyframes: []Keyframe{
				{TimeMs: 0, Value: targetX + targetW*0.45},
				{TimeMs: durationMs, Value: targetX},
			}},
			{Param: "positionY", SlotIndex: slotIdx, Keyframes: []Keyframe{
				{TimeMs: 0, Value: targetY + targetH*0.45},
				{TimeMs: durationMs, Value: targetY},
			}},
			{Param: "opacity", SlotIndex: slotIdx, Keyframes: []Keyframe{
				{TimeMs: 0, Value: 0},
				{TimeMs: durationMs / 3, Value: 1, EaseOutX: 1, EaseOutY: 1},
			}},
		},
	}
}

// AnimSpinIn spins and scales a slot from nothing to full.
func AnimSpinIn(slotIdx int, targetW, targetH, targetX, targetY float64, durationMs int) *Animation {
	return &Animation{
		Name: "spin-in", DurationMs: durationMs,
		Tracks: []Track{
			{Param: "rotationDeg", SlotIndex: slotIdx, Keyframes: []Keyframe{
				{TimeMs: 0, Value: 360, EaseOutX: 0.2, EaseOutY: 0},
				{TimeMs: durationMs, Value: 0},
			}},
			{Param: "width", SlotIndex: slotIdx, Keyframes: []Keyframe{
				{TimeMs: 0, Value: 0},
				{TimeMs: durationMs, Value: targetW},
			}},
			{Param: "height", SlotIndex: slotIdx, Keyframes: []Keyframe{
				{TimeMs: 0, Value: 0},
				{TimeMs: durationMs, Value: targetH},
			}},
			{Param: "positionX", SlotIndex: slotIdx, Keyframes: []Keyframe{
				{TimeMs: 0, Value: targetX + targetW/2},
				{TimeMs: durationMs, Value: targetX},
			}},
			{Param: "positionY", SlotIndex: slotIdx, Keyframes: []Keyframe{
				{TimeMs: 0, Value: targetY + targetH/2},
				{TimeMs: durationMs, Value: targetY},
			}},
			{Param: "opacity", SlotIndex: slotIdx, Keyframes: []Keyframe{
				{TimeMs: 0, Value: 0},
				{TimeMs: durationMs / 2, Value: 1, EaseOutX: 1, EaseOutY: 1},
			}},
		},
	}
}

// AnimBounceIn bounces a slot in from above with overshoot.
func AnimBounceIn(slotIdx int, targetY float64, durationMs int) *Animation {
	b1 := durationMs * 60 / 100
	b2 := durationMs * 80 / 100
	return &Animation{
		Name: "bounce-in", DurationMs: durationMs,
		Tracks: []Track{
			{Param: "positionY", SlotIndex: slotIdx, Keyframes: []Keyframe{
				{TimeMs: 0, Value: -300},
				{TimeMs: b1, Value: targetY + 20, EaseOutX: 0.4, EaseOutY: 0},
				{TimeMs: b2, Value: targetY - 8, EaseOutX: 1, EaseOutY: 1},
				{TimeMs: durationMs, Value: targetY, EaseOutX: 1, EaseOutY: 1},
			}},
			{Param: "opacity", SlotIndex: slotIdx, Keyframes: []Keyframe{
				{TimeMs: 0, Value: 0},
				{TimeMs: durationMs / 5, Value: 1, EaseOutX: 1, EaseOutY: 1},
			}},
		},
	}
}

// AnimFadeIn fades a slot from transparent to opaque.
func AnimFadeIn(slotIdx int, durationMs int) *Animation {
	return &Animation{
		Name: "fade-in", DurationMs: durationMs,
		Tracks: []Track{
			{Param: "opacity", SlotIndex: slotIdx, Keyframes: []Keyframe{
				{TimeMs: 0, Value: 0, EaseOutX: 0.42, EaseOutY: 0},
				{TimeMs: durationMs, Value: 1},
			}},
		},
	}
}

// --- Exit Animations (slot disappears) ---

// AnimFlyOutLeft flies a slot off-screen to the left.
func AnimFlyOutLeft(slotIdx int, durationMs int) *Animation {
	return &Animation{
		Name: "fly-out-left", DurationMs: durationMs,
		Tracks: []Track{
			{Param: "positionX", SlotIndex: slotIdx, Keyframes: []Keyframe{
				{TimeMs: 0, Value: 0, EaseOutX: 0.6, EaseOutY: 0},
				{TimeMs: durationMs, Value: -1920},
			}},
			{Param: "opacity", SlotIndex: slotIdx, Keyframes: []Keyframe{
				{TimeMs: durationMs * 3 / 4, Value: 1},
				{TimeMs: durationMs, Value: 0, EaseOutX: 1, EaseOutY: 1},
			}},
		},
	}
}

// AnimFlyOutRight flies a slot off-screen to the right.
func AnimFlyOutRight(slotIdx int, durationMs int) *Animation {
	return &Animation{
		Name: "fly-out-right", DurationMs: durationMs,
		Tracks: []Track{
			{Param: "positionX", SlotIndex: slotIdx, Keyframes: []Keyframe{
				{TimeMs: 0, Value: 0, EaseOutX: 0.6, EaseOutY: 0},
				{TimeMs: durationMs, Value: 1920},
			}},
			{Param: "opacity", SlotIndex: slotIdx, Keyframes: []Keyframe{
				{TimeMs: durationMs * 3 / 4, Value: 1},
				{TimeMs: durationMs, Value: 0, EaseOutX: 1, EaseOutY: 1},
			}},
		},
	}
}

// AnimZoomOut zooms a slot from full size to nothing at center.
func AnimZoomOut(slotIdx int, fromW, fromH, fromX, fromY float64, durationMs int) *Animation {
	return &Animation{
		Name: "zoom-out", DurationMs: durationMs,
		Tracks: []Track{
			{Param: "width", SlotIndex: slotIdx, Keyframes: []Keyframe{
				{TimeMs: 0, Value: fromW},
				{TimeMs: durationMs, Value: 0},
			}},
			{Param: "height", SlotIndex: slotIdx, Keyframes: []Keyframe{
				{TimeMs: 0, Value: fromH},
				{TimeMs: durationMs, Value: 0},
			}},
			{Param: "positionX", SlotIndex: slotIdx, Keyframes: []Keyframe{
				{TimeMs: 0, Value: fromX},
				{TimeMs: durationMs, Value: fromX + fromW/2},
			}},
			{Param: "positionY", SlotIndex: slotIdx, Keyframes: []Keyframe{
				{TimeMs: 0, Value: fromY},
				{TimeMs: durationMs, Value: fromY + fromH/2},
			}},
			{Param: "opacity", SlotIndex: slotIdx, Keyframes: []Keyframe{
				{TimeMs: durationMs * 2 / 3, Value: 1},
				{TimeMs: durationMs, Value: 0},
			}},
		},
	}
}

// AnimSpinOut spins and shrinks a slot to nothing.
func AnimSpinOut(slotIdx int, fromW, fromH, fromX, fromY float64, durationMs int) *Animation {
	return &Animation{
		Name: "spin-out", DurationMs: durationMs,
		Tracks: []Track{
			{Param: "rotationDeg", SlotIndex: slotIdx, Keyframes: []Keyframe{
				{TimeMs: 0, Value: 0},
				{TimeMs: durationMs, Value: -360},
			}},
			{Param: "width", SlotIndex: slotIdx, Keyframes: []Keyframe{
				{TimeMs: 0, Value: fromW},
				{TimeMs: durationMs, Value: 0},
			}},
			{Param: "height", SlotIndex: slotIdx, Keyframes: []Keyframe{
				{TimeMs: 0, Value: fromH},
				{TimeMs: durationMs, Value: 0},
			}},
			{Param: "positionX", SlotIndex: slotIdx, Keyframes: []Keyframe{
				{TimeMs: 0, Value: fromX},
				{TimeMs: durationMs, Value: fromX + fromW/2},
			}},
			{Param: "positionY", SlotIndex: slotIdx, Keyframes: []Keyframe{
				{TimeMs: 0, Value: fromY},
				{TimeMs: durationMs, Value: fromY + fromH/2},
			}},
			{Param: "opacity", SlotIndex: slotIdx, Keyframes: []Keyframe{
				{TimeMs: durationMs / 2, Value: 1},
				{TimeMs: durationMs, Value: 0},
			}},
		},
	}
}

// AnimFadeOut fades a slot from opaque to transparent.
func AnimFadeOut(slotIdx int, durationMs int) *Animation {
	return &Animation{
		Name: "fade-out", DurationMs: durationMs,
		Tracks: []Track{
			{Param: "opacity", SlotIndex: slotIdx, Keyframes: []Keyframe{
				{TimeMs: 0, Value: 1, EaseOutX: 0, EaseOutY: 0},
				{TimeMs: durationMs, Value: 0},
			}},
		},
	}
}

// AnimDropOut drops a slot downward with slight rotation.
func AnimDropOut(slotIdx int, durationMs int) *Animation {
	return &Animation{
		Name: "drop-out", DurationMs: durationMs,
		Tracks: []Track{
			{Param: "positionY", SlotIndex: slotIdx, Keyframes: []Keyframe{
				{TimeMs: 0, Value: 0, EaseOutX: 0.4, EaseOutY: 0},
				{TimeMs: durationMs, Value: 1200},
			}},
			{Param: "rotationDeg", SlotIndex: slotIdx, Keyframes: []Keyframe{
				{TimeMs: 0, Value: 0},
				{TimeMs: durationMs, Value: 15},
			}},
			{Param: "opacity", SlotIndex: slotIdx, Keyframes: []Keyframe{
				{TimeMs: durationMs * 3 / 4, Value: 1},
				{TimeMs: durationMs, Value: 0},
			}},
		},
	}
}

// --- Emphasis / Loop Animations (ongoing effects) ---

// AnimPulse pulses opacity in a loop.
func AnimPulse(slotIdx int, periodMs int) *Animation {
	half := periodMs / 2
	return &Animation{
		Name: "pulse", DurationMs: periodMs, Loop: true,
		Tracks: []Track{
			{Param: "opacity", SlotIndex: slotIdx, Keyframes: []Keyframe{
				{TimeMs: 0, Value: 1, EaseOutX: 0.42, EaseOutY: 0},
				{TimeMs: half, Value: 0.4},
				{TimeMs: periodMs, Value: 1, EaseOutX: 0.42, EaseOutY: 0},
			}},
		},
	}
}

// AnimBreathScale gently pulses the scale (broadcast "breathing" effect).
func AnimBreathScale(slotIdx int, baseW, baseH float64, periodMs int) *Animation {
	half := periodMs / 2
	scaleAmt := 0.03 // 3% scale variation
	return &Animation{
		Name: "breathe", DurationMs: periodMs, Loop: true,
		Tracks: []Track{
			{Param: "width", SlotIndex: slotIdx, Keyframes: []Keyframe{
				{TimeMs: 0, Value: baseW, EaseOutX: 0.42, EaseOutY: 0},
				{TimeMs: half, Value: baseW * (1 + scaleAmt)},
				{TimeMs: periodMs, Value: baseW, EaseOutX: 0.42, EaseOutY: 0},
			}},
			{Param: "height", SlotIndex: slotIdx, Keyframes: []Keyframe{
				{TimeMs: 0, Value: baseH, EaseOutX: 0.42, EaseOutY: 0},
				{TimeMs: half, Value: baseH * (1 + scaleAmt)},
				{TimeMs: periodMs, Value: baseH, EaseOutX: 0.42, EaseOutY: 0},
			}},
		},
	}
}

// AnimRock gently rocks a slot side to side (pendulum).
func AnimRock(slotIdx int, periodMs int) *Animation {
	q1 := periodMs / 4
	q3 := periodMs * 3 / 4
	return &Animation{
		Name: "rock", DurationMs: periodMs, Loop: true,
		Tracks: []Track{
			{Param: "rotationDeg", SlotIndex: slotIdx, Keyframes: []Keyframe{
				{TimeMs: 0, Value: 0, EaseOutX: 0.42, EaseOutY: 0},
				{TimeMs: q1, Value: 3},
				{TimeMs: periodMs / 2, Value: 0, EaseOutX: 0.42, EaseOutY: 0},
				{TimeMs: q3, Value: -3},
				{TimeMs: periodMs, Value: 0, EaseOutX: 0.42, EaseOutY: 0},
			}},
		},
	}
}

// AnimFloat gently floats a slot up and down.
func AnimFloat(slotIdx int, baseY float64, periodMs int) *Animation {
	half := periodMs / 2
	return &Animation{
		Name: "float", DurationMs: periodMs, Loop: true,
		Tracks: []Track{
			{Param: "positionY", SlotIndex: slotIdx, Keyframes: []Keyframe{
				{TimeMs: 0, Value: baseY, EaseOutX: 0.42, EaseOutY: 0},
				{TimeMs: half, Value: baseY - 10},
				{TimeMs: periodMs, Value: baseY, EaseOutX: 0.42, EaseOutY: 0},
			}},
		},
	}
}

// AnimShake shakes a slot rapidly (alert/attention).
func AnimShake(slotIdx int, durationMs int) *Animation {
	step := durationMs / 8
	return &Animation{
		Name: "shake", DurationMs: durationMs,
		Tracks: []Track{
			{Param: "positionX", SlotIndex: slotIdx, Keyframes: []Keyframe{
				{TimeMs: 0, Value: 0},
				{TimeMs: step, Value: -10},
				{TimeMs: step * 2, Value: 10},
				{TimeMs: step * 3, Value: -8},
				{TimeMs: step * 4, Value: 8},
				{TimeMs: step * 5, Value: -5},
				{TimeMs: step * 6, Value: 5},
				{TimeMs: step * 7, Value: -2},
				{TimeMs: durationMs, Value: 0},
			}},
		},
	}
}

// AnimGlowPulse pulses the border glow effect.
func AnimGlowPulse(slotIdx int, periodMs int) *Animation {
	half := periodMs / 2
	return &Animation{
		Name: "glow-pulse", DurationMs: periodMs, Loop: true,
		Tracks: []Track{
			{Param: "border.width", SlotIndex: slotIdx, Keyframes: []Keyframe{
				{TimeMs: 0, Value: 2, EaseOutX: 0.42, EaseOutY: 0},
				{TimeMs: half, Value: 6},
				{TimeMs: periodMs, Value: 2, EaseOutX: 0.42, EaseOutY: 0},
			}},
			{Param: "border.softness", SlotIndex: slotIdx, Keyframes: []Keyframe{
				{TimeMs: 0, Value: 1, EaseOutX: 0.42, EaseOutY: 0},
				{TimeMs: half, Value: 4},
				{TimeMs: periodMs, Value: 1, EaseOutX: 0.42, EaseOutY: 0},
			}},
		},
	}
}

// AnimShadowDance animates the shadow offset in a circle.
func AnimShadowDance(slotIdx int, periodMs int) *Animation {
	q1 := periodMs / 4
	q2 := periodMs / 2
	q3 := periodMs * 3 / 4
	return &Animation{
		Name: "shadow-dance", DurationMs: periodMs, Loop: true,
		Tracks: []Track{
			{Param: "shadow.offsetX", SlotIndex: slotIdx, Keyframes: []Keyframe{
				{TimeMs: 0, Value: 6, EaseOutX: 0.42, EaseOutY: 0},
				{TimeMs: q1, Value: 0},
				{TimeMs: q2, Value: -6},
				{TimeMs: q3, Value: 0},
				{TimeMs: periodMs, Value: 6, EaseOutX: 0.42, EaseOutY: 0},
			}},
			{Param: "shadow.offsetY", SlotIndex: slotIdx, Keyframes: []Keyframe{
				{TimeMs: 0, Value: 0, EaseOutX: 0.42, EaseOutY: 0},
				{TimeMs: q1, Value: 6},
				{TimeMs: q2, Value: 0},
				{TimeMs: q3, Value: -6},
				{TimeMs: periodMs, Value: 0, EaseOutX: 0.42, EaseOutY: 0},
			}},
		},
	}
}

// AnimSlowSpin continuously rotates a slot.
func AnimSlowSpin(slotIdx int, periodMs int) *Animation {
	return &Animation{
		Name: "slow-spin", DurationMs: periodMs, Loop: true,
		Tracks: []Track{
			{Param: "rotationDeg", SlotIndex: slotIdx, Keyframes: []Keyframe{
				{TimeMs: 0, Value: 0, EaseOutX: 1, EaseOutY: 1},
				{TimeMs: periodMs, Value: 360},
			}},
		},
	}
}

// --- Catalogue ---

// AnimationCatalogEntry describes a pre-canned animation.
type AnimationCatalogEntry struct {
	Name        string `json:"name"`
	Category    string `json:"category"`    // "entrance", "exit", "emphasis", "3d"
	Description string `json:"description"`
}

// AnimationCatalog returns all available pre-canned animations.
func AnimationCatalog() []AnimationCatalogEntry {
	return []AnimationCatalogEntry{
		// Entrances
		{Name: "fly-in-left", Category: "entrance", Description: "Fly in from the left"},
		{Name: "fly-in-right", Category: "entrance", Description: "Fly in from the right"},
		{Name: "fly-in-top", Category: "entrance", Description: "Fly in from the top"},
		{Name: "fly-in-bottom", Category: "entrance", Description: "Fly in from the bottom"},
		{Name: "zoom-in", Category: "entrance", Description: "Zoom in from center"},
		{Name: "spin-in", Category: "entrance", Description: "Spin and scale in"},
		{Name: "bounce-in", Category: "entrance", Description: "Bounce in from above"},
		{Name: "fade-in", Category: "entrance", Description: "Fade in from transparent"},
		// Exits
		{Name: "fly-out-left", Category: "exit", Description: "Fly out to the left"},
		{Name: "fly-out-right", Category: "exit", Description: "Fly out to the right"},
		{Name: "zoom-out", Category: "exit", Description: "Zoom out to center"},
		{Name: "spin-out", Category: "exit", Description: "Spin and shrink out"},
		{Name: "fade-out", Category: "exit", Description: "Fade out to transparent"},
		{Name: "drop-out", Category: "exit", Description: "Drop down with rotation"},
		// Emphasis / Loops
		{Name: "pulse", Category: "emphasis", Description: "Pulse opacity (loop)"},
		{Name: "breathe", Category: "emphasis", Description: "Gentle scale breathing (loop)"},
		{Name: "rock", Category: "emphasis", Description: "Rock side to side (loop)"},
		{Name: "float", Category: "emphasis", Description: "Float up and down (loop)"},
		{Name: "shake", Category: "emphasis", Description: "Rapid shake (attention)"},
		{Name: "glow-pulse", Category: "emphasis", Description: "Pulsing border glow (loop)"},
		{Name: "shadow-dance", Category: "emphasis", Description: "Animated shadow circle (loop)"},
		{Name: "slow-spin", Category: "emphasis", Description: "Continuous rotation (loop)"},
		// B3 extended entrances
		{Name: "slide-in-left", Category: "entrance", Description: "Slide in from the left"},
		{Name: "slide-in-right", Category: "entrance", Description: "Slide in from the right"},
		{Name: "slide-in-top", Category: "entrance", Description: "Slide in from the top"},
		{Name: "slide-in-bottom", Category: "entrance", Description: "Slide in from the bottom"},
		{Name: "elastic-in", Category: "entrance", Description: "Spring-overshoot entrance"},
		{Name: "flip-in", Category: "entrance", Description: "3D flip in (Y rotation)"},
		{Name: "blur-in", Category: "entrance", Description: "Defocus to sharp entrance"},
		{Name: "squish-in", Category: "entrance", Description: "Horizontal squash-stretch entrance"},
	}
}
