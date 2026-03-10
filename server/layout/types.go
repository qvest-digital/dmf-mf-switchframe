package layout

import (
	"fmt"
	"image"
	"time"
)

// DefaultTransitionDurationMs is the default slot transition duration.
const DefaultTransitionDurationMs = 300

// MaxSlots is the maximum number of layout slots.
const MaxSlots = 4

// LayoutSlot defines one source's position within a layout.
type LayoutSlot struct {
	SourceKey  string          `json:"sourceKey"`
	Rect       image.Rectangle `json:"rect"`
	ZOrder     int             `json:"zOrder"`
	Border     BorderConfig    `json:"border"`
	Transition SlotTransition  `json:"transition"`
	Enabled    bool            `json:"enabled"`
}

// BorderConfig describes the visual border around a PIP slot.
type BorderConfig struct {
	Width   int  `json:"width"`   // luma pixels, must be even, 0 = no border
	ColorY  byte `json:"colorY"`  // BT.709 limited range
	ColorCb byte `json:"colorCb"`
	ColorCr byte `json:"colorCr"`
}

// SlotTransition configures how a slot animates on/off.
type SlotTransition struct {
	Type       string `json:"type"`       // "cut", "dissolve", "fly"
	DurationMs int    `json:"durationMs"` // milliseconds
}

// TransitionDuration returns the Duration as a time.Duration.
func (t SlotTransition) TransitionDuration() time.Duration {
	if t.DurationMs <= 0 {
		return time.Duration(DefaultTransitionDurationMs) * time.Millisecond
	}
	return time.Duration(t.DurationMs) * time.Millisecond
}

// Layout is the complete multi-source layout configuration.
type Layout struct {
	Name  string       `json:"name"`
	Slots []LayoutSlot `json:"slots"`
}

// EvenAlign rounds down to the nearest even number (YUV420 alignment).
func EvenAlign(v int) int { return v &^ 1 }

// ValidateSlot checks that a slot has valid even-aligned dimensions within frame bounds.
func ValidateSlot(slot LayoutSlot, frameW, frameH int) error {
	// SourceKey may be empty — user assigns sources after selecting a preset.
	// The compositor renders broadcast black for slots with no source.
	r := slot.Rect
	if r.Min.X%2 != 0 || r.Min.Y%2 != 0 {
		return fmt.Errorf("slot rect origin (%d,%d) must be even-aligned", r.Min.X, r.Min.Y)
	}
	if r.Dx()%2 != 0 || r.Dy()%2 != 0 {
		return fmt.Errorf("slot rect size (%dx%d) must be even-aligned", r.Dx(), r.Dy())
	}
	if r.Min.X < 0 || r.Min.Y < 0 || r.Max.X > frameW || r.Max.Y > frameH {
		return fmt.Errorf("slot rect %v exceeds frame bounds %dx%d", r, frameW, frameH)
	}
	if r.Dx() <= 0 || r.Dy() <= 0 {
		return fmt.Errorf("slot rect has zero or negative dimensions")
	}
	if slot.Border.Width%2 != 0 {
		return fmt.Errorf("border width %d must be even", slot.Border.Width)
	}
	return nil
}

// ValidateLayout checks all slots in a layout.
func ValidateLayout(l *Layout, frameW, frameH int) error {
	if len(l.Slots) > MaxSlots {
		return fmt.Errorf("layout has %d slots, max is %d", len(l.Slots), MaxSlots)
	}
	for i, slot := range l.Slots {
		if err := ValidateSlot(slot, frameW, frameH); err != nil {
			return fmt.Errorf("slot %d: %w", i, err)
		}
	}
	return nil
}
