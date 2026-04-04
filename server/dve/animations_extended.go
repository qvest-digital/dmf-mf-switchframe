package dve

// ============================================================================
// B3: Extended DVE Editor Animation Library
//
// 8 new pre-canned keyframe animations for slots in the DVE editor.
// Categories: Entrances (slide, elastic, flip, blur, squish).
// ============================================================================

// AnimSlideInLeft slides a slot in from off-screen left. Pure horizontal slide,
// no opacity change.
func AnimSlideInLeft(slotIdx int, durationMs int) *Animation {
	return &Animation{
		Name: "slide-in-left", DurationMs: durationMs,
		Tracks: []Track{
			{Param: "positionX", SlotIndex: slotIdx, Keyframes: []Keyframe{
				{TimeMs: 0, Value: -1920, EaseOutX: 0.25, EaseOutY: 0.1},
				{TimeMs: durationMs, Value: 0},
			}},
		},
	}
}

// AnimSlideInRight slides a slot in from off-screen right.
func AnimSlideInRight(slotIdx int, durationMs int) *Animation {
	return &Animation{
		Name: "slide-in-right", DurationMs: durationMs,
		Tracks: []Track{
			{Param: "positionX", SlotIndex: slotIdx, Keyframes: []Keyframe{
				{TimeMs: 0, Value: 1920, EaseOutX: 0.25, EaseOutY: 0.1},
				{TimeMs: durationMs, Value: 0},
			}},
		},
	}
}

// AnimSlideInTop slides a slot in from off-screen top.
func AnimSlideInTop(slotIdx int, durationMs int) *Animation {
	return &Animation{
		Name: "slide-in-top", DurationMs: durationMs,
		Tracks: []Track{
			{Param: "positionY", SlotIndex: slotIdx, Keyframes: []Keyframe{
				{TimeMs: 0, Value: -1080, EaseOutX: 0.25, EaseOutY: 0.1},
				{TimeMs: durationMs, Value: 0},
			}},
		},
	}
}

// AnimSlideInBottom slides a slot in from off-screen bottom.
func AnimSlideInBottom(slotIdx int, durationMs int) *Animation {
	return &Animation{
		Name: "slide-in-bottom", DurationMs: durationMs,
		Tracks: []Track{
			{Param: "positionY", SlotIndex: slotIdx, Keyframes: []Keyframe{
				{TimeMs: 0, Value: 1080, EaseOutX: 0.25, EaseOutY: 0.1},
				{TimeMs: durationMs, Value: 0},
			}},
		},
	}
}

// AnimElasticIn animates position with spring overshoot.
// 4 keyframes: start far off-screen -> overshoot target -> undershoot -> settle.
func AnimElasticIn(slotIdx int, targetX, targetY float64, durationMs int) *Animation {
	// Timing breakpoints
	t1 := durationMs * 55 / 100 // first arrival (overshoot)
	t2 := durationMs * 75 / 100 // undershoot
	t3 := durationMs             // settle

	// Overshoot/undershoot amounts
	overshootX := targetX * 0.15
	overshootY := targetY * 0.15
	if targetX == 0 {
		overshootX = 30 // default overshoot in pixels
	}
	if targetY == 0 {
		overshootY = 30
	}

	return &Animation{
		Name: "elastic-in", DurationMs: durationMs,
		Tracks: []Track{
			{Param: "positionX", SlotIndex: slotIdx, Keyframes: []Keyframe{
				{TimeMs: 0, Value: targetX - 800, EaseOutX: 0.25, EaseOutY: 0.1},
				{TimeMs: t1, Value: targetX + overshootX, EaseOutX: 0.42, EaseOutY: 0},
				{TimeMs: t2, Value: targetX - overshootX*0.4, EaseOutX: 0.42, EaseOutY: 0},
				{TimeMs: t3, Value: targetX},
			}},
			{Param: "positionY", SlotIndex: slotIdx, Keyframes: []Keyframe{
				{TimeMs: 0, Value: targetY - 600, EaseOutX: 0.25, EaseOutY: 0.1},
				{TimeMs: t1, Value: targetY + overshootY, EaseOutX: 0.42, EaseOutY: 0},
				{TimeMs: t2, Value: targetY - overshootY*0.4, EaseOutX: 0.42, EaseOutY: 0},
				{TimeMs: t3, Value: targetY},
			}},
			{Param: "opacity", SlotIndex: slotIdx, Keyframes: []Keyframe{
				{TimeMs: 0, Value: 0},
				{TimeMs: durationMs / 4, Value: 1, EaseOutX: 1, EaseOutY: 1},
			}},
		},
	}
}

// AnimFlipIn flips a slot in from behind using 3D Y rotation.
// rotateY: 180 -> 0 with easing. Opacity 0->1 in first quarter.
func AnimFlipIn(slotIdx int, durationMs int) *Animation {
	return &Animation{
		Name: "flip-in", DurationMs: durationMs,
		Tracks: []Track{
			{Param: "rotateY", SlotIndex: slotIdx, Keyframes: []Keyframe{
				{TimeMs: 0, Value: 180, EaseOutX: 0.25, EaseOutY: 0.1},
				{TimeMs: durationMs, Value: 0},
			}},
			{Param: "opacity", SlotIndex: slotIdx, Keyframes: []Keyframe{
				{TimeMs: 0, Value: 0},
				{TimeMs: durationMs / 4, Value: 1, EaseOutX: 1, EaseOutY: 1},
			}},
		},
	}
}

// AnimBlurIn animates from blurred/transparent to sharp/opaque.
// defocusRadius: 20 -> 0. Opacity: 0 -> 1.
func AnimBlurIn(slotIdx int, durationMs int) *Animation {
	return &Animation{
		Name: "blur-in", DurationMs: durationMs,
		Tracks: []Track{
			{Param: "defocusRadius", SlotIndex: slotIdx, Keyframes: []Keyframe{
				{TimeMs: 0, Value: 20, EaseOutX: 0.25, EaseOutY: 0.1},
				{TimeMs: durationMs, Value: 0},
			}},
			{Param: "opacity", SlotIndex: slotIdx, Keyframes: []Keyframe{
				{TimeMs: 0, Value: 0, EaseOutX: 0.42, EaseOutY: 0},
				{TimeMs: durationMs, Value: 1},
			}},
		},
	}
}

// AnimSquishIn squishes a slot in horizontally (width: 0 -> targetW).
// Height stays constant, creating a squash-stretch effect.
func AnimSquishIn(slotIdx int, targetW float64, durationMs int) *Animation {
	return &Animation{
		Name: "squish-in", DurationMs: durationMs,
		Tracks: []Track{
			{Param: "width", SlotIndex: slotIdx, Keyframes: []Keyframe{
				{TimeMs: 0, Value: 0, EaseOutX: 0.2, EaseOutY: 0},
				{TimeMs: durationMs, Value: targetW},
			}},
			{Param: "opacity", SlotIndex: slotIdx, Keyframes: []Keyframe{
				{TimeMs: 0, Value: 0},
				{TimeMs: durationMs / 3, Value: 1, EaseOutX: 1, EaseOutY: 1},
			}},
		},
	}
}
