package dve

import (
	"math"
	"sort"
	"time"
)

// layoutMorph animates every slot from the layout on screen to a recalled
// one. slots is the target padded to the larger slot count of both sides;
// its transforms are the base anim's tracks are evaluated over.
type layoutMorph struct {
	start    time.Time
	duration time.Duration
	ease     func(float64) float64
	slots    []Slot
	anim     *Animation
}

// progress returns the linear progress [0, 1] at now.
func (m *layoutMorph) progress(now time.Time) float64 {
	return clamp01(float64(now.Sub(m.start)) / float64(m.duration))
}

// slotsAt returns the morph's slots with transforms evaluated at now.
func (m *layoutMorph) slotsAt(now time.Time) []Slot {
	t := m.progress(now)
	if m.ease != nil {
		t = m.ease(t)
	}
	timeMs := int(math.Round(t * float64(m.anim.DurationMs)))
	out := make([]Slot, len(m.slots))
	for i, s := range m.slots {
		s.Transform = m.anim.EvaluateTransform(i, timeMs, s.Transform)
		// EffectiveOpacity reads 0 as opaque; a slot faded out is not drawn.
		s.Enabled = s.Enabled && s.Transform.Opacity > 0
		out[i] = s
	}
	return out
}

// MaxMorphDurationMs caps a layout morph. Longer requests are rejected by
// the API and clamped here; an unbounded value overflows time.Duration and
// the morph would never complete.
const MaxMorphDurationMs = 5000

// RecallLayout moves to target over durationMs, morphing every slot from
// what is on screen now, an in-flight morph included. The target is stored
// at once, so state reports it while the drawn slots catch up. ease warps
// time; nil is linear. durationMs <= 0 is SetLayout; durationMs is capped at
// MaxMorphDurationMs.
func (c *Compositor) RecallLayout(target *Layout, durationMs int, ease func(float64) float64) {
	if target == nil || durationMs <= 0 {
		c.SetLayout(target)
		return
	}

	c.mu.Lock()
	wasBefore := c.Active()
	c.morphTo(target, durationMs, ease)
	wasAfter := c.Active()
	c.mu.Unlock()
	if wasBefore != wasAfter && c.OnActiveChange != nil {
		c.OnActiveChange()
	}
}

// AnimateSlot applies fn to slot slotIdx and morphs to the result over
// durationMs, as RecallLayout. fn runs on the layout current under the
// lock, so a concurrent edit to another slot is kept. durationMs <= 0 is
// UpdateSlot.
func (c *Compositor) AnimateSlot(slotIdx int, fn func(*Slot), durationMs int, ease func(float64) float64) {
	if durationMs <= 0 {
		c.UpdateSlot(slotIdx, fn)
		return
	}

	c.mu.Lock()
	l := c.layout.Load()
	if l == nil || slotIdx < 0 || slotIdx >= len(l.Slots) {
		c.mu.Unlock()
		return
	}
	wasBefore := c.Active()
	target := c.cloneLayout(l)
	fn(&target.Slots[slotIdx])
	c.morphTo(target, durationMs, ease)
	wasAfter := c.Active()
	c.mu.Unlock()
	if wasBefore != wasAfter && c.OnActiveChange != nil {
		c.OnActiveChange()
	}
}

// morphTo stores target and morphs to it from what is on screen.
// Must be called with c.mu held.
func (c *Compositor) morphTo(target *Layout, durationMs int, ease func(float64) float64) {
	durationMs = min(durationMs, MaxMorphDurationMs)
	now := c.clock()

	var cur []Slot
	if m := c.morph.Load(); m != nil {
		cur = m.slotsAt(now)
	} else if l := c.layout.Load(); l != nil {
		cur = l.Slots
	}

	final := c.cloneLayout(target)
	n := max(len(cur), len(final.Slots))
	from := &Layout{Slots: make([]Slot, n)}
	to := &Layout{Slots: make([]Slot, n)}
	visible := make([]Slot, n)
	for i := range n {
		var f, t Slot
		if i < len(cur) {
			f = cur[i]
		}
		if i < len(final.Slots) {
			// Built-in presets carry no sources; keep what the slot shows.
			if final.Slots[i].SourceKey == "" {
				final.Slots[i].SourceKey = f.SourceKey
			}
			t = final.Slots[i]
		} else {
			t = f
			t.Enabled = false
		}
		if i >= len(cur) {
			f = t
			f.Enabled = false
		}
		f.Transform = c.morphTransform(f)
		t.Transform = c.morphTransform(t)
		// A slot enabled on one side only fades in place.
		if !f.Enabled {
			f.Transform = t.Transform
		}
		if !t.Enabled {
			t.Transform = f.Transform
		}
		from.Slots[i] = f
		to.Slots[i] = t
		visible[i] = t
		visible[i].Enabled = t.Enabled || f.Enabled
	}

	c.allocateBuffers(final)
	c.pruneFills(visible)
	c.morph.Store(&layoutMorph{
		start:    now,
		duration: time.Duration(durationMs) * time.Millisecond,
		ease:     ease,
		slots:    visible,
		anim:     RecallPreset(from, to, durationMs),
	})
	c.layout.Store(final)
}

// stopMorph ends a recall in flight, so the layout is drawn as stored.
// Must be called with c.mu held.
func (c *Compositor) stopMorph() {
	if c.morph.Swap(nil) == nil {
		return
	}
	if l := c.layout.Load(); l != nil {
		c.pruneFills(l.Slots)
	}
}

// morphTransform resolves RectMode and makes opacity explicit, so tracks
// between two slots interpolate what is drawn rather than stored fields
// ProcessFrame would recompute or read as opaque.
func (c *Compositor) morphTransform(s Slot) Transform {
	srcW, srcH := 0, 0
	if entry, ok := c.fills[s.SourceKey]; ok {
		srcW, srcH = entry.width, entry.height
	}
	t := s.Transform.ResolveRect(srcW, srcH)
	t.Opacity = t.EffectiveOpacity()
	return t
}

// frameSlots returns the slots to draw this frame and their z-order: the
// layout itself, or during a morph its slots evaluated now.
// Must be called with c.mu held.
func (c *Compositor) frameSlots(l *Layout) ([]Slot, []int) {
	m := c.morph.Load()
	if m == nil {
		return l.Slots, c.sortedSlots
	}
	slots := m.slotsAt(c.clock())
	order := make([]int, len(slots))
	for i := range order {
		order[i] = i
	}
	sort.SliceStable(order, func(a, b int) bool {
		return slots[order[a]].ZOrder < slots[order[b]].ZOrder
	})
	return slots, order
}

// clock returns the compositor's current time.
func (c *Compositor) clock() time.Time {
	if c.now != nil {
		return c.now()
	}
	return time.Now()
}
