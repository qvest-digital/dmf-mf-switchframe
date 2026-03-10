package layout

import (
	"fmt"
	"image"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"github.com/zsiec/switchframe/server/transition"
)

// fillEntry holds a cached source frame.
type fillEntry struct {
	yuv           []byte
	width, height int
}

// Compositor manages the layout composition pipeline.
// Frame delivery via IngestSourceFrame (called from handleRawVideoFrame).
// Compositing via ProcessFrame (called from pipeline node).
type Compositor struct {
	mu     sync.Mutex
	layout atomic.Pointer[Layout]

	// Per-source cached frames
	fills map[string]*fillEntry

	// Per-slot pre-allocated scale buffers
	scaleBufs [][]byte

	// Per-slot crop buffers (lazily allocated for fill-mode slots)
	cropBufs [][]byte

	// Per-slot gray "no signal" frames
	grayBufs [][]byte

	// Per-slot snapshot buffers for safe fill data copies (avoids aliasing fills map entries)
	snapBufs [][]byte

	// Pre-computed z-order sorted slot indices (avoids per-frame allocation)
	sortedSlots []int

	// Active animations
	animations []*Animation

	// Frame dimensions
	frameW, frameH int

	// OnActiveChange is called when SetLayout changes the Active() state.
	// Used by the switcher to trigger pipeline rebuilds.
	OnActiveChange func()
}

// NewCompositor creates a new layout compositor.
func NewCompositor(frameW, frameH int) *Compositor {
	return &Compositor{
		fills:  make(map[string]*fillEntry),
		frameW: frameW,
		frameH: frameH,
	}
}

// SetLayout atomically sets the current layout. nil clears the layout.
func (c *Compositor) SetLayout(l *Layout) {
	wasBefore := c.Active()
	c.layout.Store(l)
	if l != nil {
		c.mu.Lock()
		c.allocateBuffers(l)
		c.mu.Unlock()
	}
	if wasBefore != c.Active() && c.OnActiveChange != nil {
		c.OnActiveChange()
	}
}

// GetLayout returns the current layout (may be nil).
func (c *Compositor) GetLayout() *Layout {
	return c.layout.Load()
}

// allocateBuffers pre-allocates scale and gray buffers for each slot.
func (c *Compositor) allocateBuffers(l *Layout) {
	c.scaleBufs = make([][]byte, len(l.Slots))
	c.cropBufs = make([][]byte, len(l.Slots))
	c.grayBufs = make([][]byte, len(l.Slots))
	c.snapBufs = make([][]byte, len(l.Slots))
	for i, slot := range l.Slots {
		w := slot.Rect.Dx()
		h := slot.Rect.Dy()
		size := w * h * 3 / 2
		c.scaleBufs[i] = make([]byte, size)
		c.grayBufs[i] = makeGrayFrame(w, h)
		// cropBufs allocated lazily in ProcessFrame when needed.
	}
	c.computeSortedSlots(l)
}

// computeSortedSlots pre-computes z-order sorted indices for a layout.
func (c *Compositor) computeSortedSlots(l *Layout) {
	c.sortedSlots = make([]int, len(l.Slots))
	for i := range l.Slots {
		c.sortedSlots[i] = i
	}
	sort.Slice(c.sortedSlots, func(a, b int) bool {
		return l.Slots[c.sortedSlots[a]].ZOrder < l.Slots[c.sortedSlots[b]].ZOrder
	})
}

// makeGrayFrame creates a "no signal" YUV420 frame.
// Uses BT.709 limited range black (Y=16, Cb=128, Cr=128).
func makeGrayFrame(w, h int) []byte {
	ySize := w * h
	cbSize := (w / 2) * (h / 2)
	buf := make([]byte, ySize+cbSize*2)
	for i := 0; i < ySize; i++ {
		buf[i] = 16 // BT.709 limited range black
	}
	for i := 0; i < cbSize*2; i++ {
		buf[ySize+i] = 128 // neutral chroma
	}
	return buf
}

// FrameSize returns the compositor's configured frame dimensions.
func (c *Compositor) FrameSize() (int, int) { return c.frameW, c.frameH }

// Active returns true if a layout is configured with at least one enabled slot.
func (c *Compositor) Active() bool {
	l := c.layout.Load()
	if l == nil {
		return false
	}
	for _, slot := range l.Slots {
		if slot.Enabled {
			return true
		}
	}
	return false
}

// NeedsSource returns true if the source is assigned to any slot in the current layout.
func (c *Compositor) NeedsSource(sourceKey string) bool {
	l := c.layout.Load()
	if l == nil {
		return false
	}
	for _, slot := range l.Slots {
		if slot.SourceKey == sourceKey {
			return true
		}
	}
	return false
}

// HasFrame returns true if a cached frame exists for the source.
func (c *Compositor) HasFrame(sourceKey string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	_, ok := c.fills[sourceKey]
	return ok
}

// IngestSourceFrame caches a decoded YUV frame for a source.
func (c *Compositor) IngestSourceFrame(sourceKey string, yuv []byte, width, height int) {
	c.mu.Lock()
	defer c.mu.Unlock()

	yuvSize := width * height * 3 / 2
	if len(yuv) < yuvSize {
		return
	}

	entry := c.fills[sourceKey]
	if entry == nil || len(entry.yuv) != yuvSize {
		entry = &fillEntry{yuv: make([]byte, yuvSize)}
	}
	copy(entry.yuv, yuv[:yuvSize])
	entry.width = width
	entry.height = height
	c.fills[sourceKey] = entry
}

// slotSnapshot holds source data for a single slot, captured under lock.
type slotSnapshot struct {
	slot     LayoutSlot
	idx      int
	srcYUV   []byte
	srcW     int
	srcH     int
	rect     image.Rectangle
	alpha    float64
	hasFill  bool
	hasGray  bool
	scaleBuf []byte
	cropBuf  []byte // non-nil when fill mode needs cropping
}

// ProcessFrame composites all enabled layout slots onto the frame.
// Called from the pipeline goroutine (single-threaded).
func (c *Compositor) ProcessFrame(yuv []byte, width, height int) []byte {
	l := c.layout.Load()
	if l == nil {
		return yuv
	}

	// Compute scale factors if frame dimensions differ from configured dimensions.
	// Layout rects are in configured (pipeline format) coordinates; scale them
	// to actual frame coordinates when the source is at a different resolution.
	scaleX, scaleY := 1.0, 1.0
	needsScale := false
	if width != c.frameW || height != c.frameH {
		if c.frameW > 0 && c.frameH > 0 {
			scaleX = float64(width) / float64(c.frameW)
			scaleY = float64(height) / float64(c.frameH)
			needsScale = true
		}
	}

	// Phase 1: Under lock — tick animations, snapshot source data, release lock.
	c.mu.Lock()
	c.tickAnimations()

	sortedSlots := c.sortedSlots
	if len(sortedSlots) == 0 {
		// Fallback if sorted slots not yet computed (shouldn't happen).
		sortedSlots = make([]int, len(l.Slots))
		for i := range l.Slots {
			sortedSlots[i] = i
		}
	}

	snapshots := make([]slotSnapshot, 0, len(sortedSlots))
	for _, idx := range sortedSlots {
		if idx >= len(l.Slots) {
			continue
		}
		slot := l.Slots[idx]
		if !slot.Enabled && !c.isAnimating(idx) {
			continue
		}

		rect, alpha := c.effectiveRectAndAlpha(idx, slot)
		if rect.Dx() <= 0 || rect.Dy() <= 0 {
			continue
		}

		// Scale rect to actual frame dimensions if needed.
		if needsScale {
			rect = image.Rect(
				EvenAlign(int(float64(rect.Min.X)*scaleX)),
				EvenAlign(int(float64(rect.Min.Y)*scaleY)),
				EvenAlign(int(float64(rect.Max.X)*scaleX)),
				EvenAlign(int(float64(rect.Max.Y)*scaleY)),
			)
		}

		// Clamp rect to frame bounds (fly animations can go off-screen).
		frameBounds := image.Rect(0, 0, width, height)
		rect = rect.Intersect(frameBounds)
		if rect.Empty() {
			continue
		}
		// Even-align after clamping.
		rect.Min.X = EvenAlign(rect.Min.X)
		rect.Min.Y = EvenAlign(rect.Min.Y)
		rect.Max.X = EvenAlign(rect.Max.X)
		rect.Max.Y = EvenAlign(rect.Max.Y)
		if rect.Dx() <= 0 || rect.Dy() <= 0 {
			continue
		}

		snap := slotSnapshot{
			slot:  slot,
			idx:   idx,
			rect:  rect,
			alpha: alpha,
		}

		if entry, ok := c.fills[slot.SourceKey]; ok {
			yuvSize := entry.width * entry.height * 3 / 2
			// Deep-copy fill data into per-slot snapshot buffer under lock
			// to avoid racing with IngestSourceFrame writes to entry.yuv.
			for len(c.snapBufs) <= idx {
				c.snapBufs = append(c.snapBufs, nil)
			}
			if len(c.snapBufs[idx]) < yuvSize {
				c.snapBufs[idx] = make([]byte, yuvSize)
			}
			copy(c.snapBufs[idx][:yuvSize], entry.yuv[:yuvSize])
			snap.srcYUV = c.snapBufs[idx][:yuvSize]
			snap.srcW = entry.width
			snap.srcH = entry.height
			snap.hasFill = true
		} else if idx < len(c.grayBufs) {
			snap.srcYUV = c.grayBufs[idx]
			// Use the actual gray buffer dimensions, not the slot rect
			// (they match at allocation but slot rect can change during animation).
			grayW := slot.Rect.Dx()
			grayH := slot.Rect.Dy()
			if len(snap.srcYUV) == grayW*grayH*3/2 {
				snap.srcW = grayW
				snap.srcH = grayH
			} else {
				// Gray buffer size doesn't match — skip to avoid scaler out-of-bounds.
				continue
			}
			snap.hasGray = true
		} else {
			continue
		}

		// Grab the scale buffer reference (pre-allocated).
		slotW := rect.Dx()
		slotH := rect.Dy()
		neededSize := slotW * slotH * 3 / 2
		if idx < len(c.scaleBufs) && len(c.scaleBufs[idx]) >= neededSize {
			snap.scaleBuf = c.scaleBufs[idx][:neededSize]
		} else {
			// Allocate once under lock; the buffer persists for future frames.
			buf := make([]byte, neededSize)
			if idx < len(c.scaleBufs) {
				c.scaleBufs[idx] = buf
			} else {
				// Extend slice to accommodate this index.
				for len(c.scaleBufs) <= idx {
					c.scaleBufs = append(c.scaleBufs, nil)
				}
				c.scaleBufs[idx] = buf
			}
			snap.scaleBuf = buf[:neededSize]
		}

		// For fill-mode slots with a real source, allocate a crop buffer.
		if snap.hasFill && slot.EffectiveScaleMode() == ScaleModeFill {
			cropNeeded := snap.srcW * snap.srcH * 3 / 2
			// Extend cropBufs slice if needed.
			for len(c.cropBufs) <= idx {
				c.cropBufs = append(c.cropBufs, nil)
			}
			if len(c.cropBufs[idx]) < cropNeeded {
				c.cropBufs[idx] = make([]byte, cropNeeded)
			}
			snap.cropBuf = c.cropBufs[idx][:cropNeeded]
		}

		snapshots = append(snapshots, snap)
	}
	c.mu.Unlock()

	// Phase 2: Lock-free — crop (if fill mode), scale, and composite each slot.
	for i := range snapshots {
		snap := &snapshots[i]
		slotW := snap.rect.Dx()
		slotH := snap.rect.Dy()

		// Gray (no-signal) slots at full opacity: fill directly with broadcast black.
		// Avoids scaling a uniform gray buffer through the scaler.
		if snap.hasGray && snap.alpha >= 1.0 {
			FillRectBlack(yuv, width, height, snap.rect)
			if snap.slot.Border.Width > 0 {
				color := [3]byte{snap.slot.Border.ColorY, snap.slot.Border.ColorCb, snap.slot.Border.ColorCr}
				DrawBorderYUV(yuv, width, height, snap.rect, color, snap.slot.Border.Width)
			}
			continue
		}

		srcYUV := snap.srcYUV
		srcW := snap.srcW
		srcH := snap.srcH

		// Crop-to-fill: extract aspect-matching sub-region before scaling.
		if snap.cropBuf != nil {
			anchorX, anchorY := snap.slot.EffectiveCropAnchor()
			cropX, cropY, cropW, cropH := ComputeCropRect(srcW, srcH, slotW, slotH, anchorX, anchorY)
			if cropW > 0 && cropH > 0 && (cropW != srcW || cropH != srcH) {
				cropSize := cropW * cropH * 3 / 2
				CropYUV420Region(snap.cropBuf[:cropSize], srcYUV, srcW, srcH, cropX, cropY, cropW, cropH)
				srcYUV = snap.cropBuf[:cropSize]
				srcW = cropW
				srcH = cropH
			}
		}

		// Scale source to slot dimensions.
		var scaled []byte
		if srcW == slotW && srcH == slotH {
			scaled = srcYUV
		} else {
			quality := c.selectScaleQuality(srcW, srcH, slotW, slotH, width, height)
			transition.ScaleYUV420WithQuality(srcYUV, srcW, srcH, snap.scaleBuf, slotW, slotH, quality)
			scaled = snap.scaleBuf
		}

		// Composite onto frame.
		if snap.alpha < 1.0 {
			BlendRegion(yuv, width, height, scaled, slotW, slotH, snap.rect, snap.alpha)
		} else {
			ComposePIPOpaque(yuv, width, height, scaled, slotW, slotH, snap.rect)
		}

		// Draw border.
		if snap.slot.Border.Width > 0 {
			color := [3]byte{snap.slot.Border.ColorY, snap.slot.Border.ColorCb, snap.slot.Border.ColorCr}
			DrawBorderYUV(yuv, width, height, snap.rect, color, snap.slot.Border.Width)
		}
	}

	return yuv
}

// selectScaleQuality picks bilinear for small PIP slots and Lanczos-3 for
// larger slots where the quality difference is visible. Threshold: 25% of
// frame area. Below that, bilinear is 5-15x faster and visually identical
// at small on-screen sizes.
func (c *Compositor) selectScaleQuality(_, _, dstW, dstH, frameW, frameH int) transition.ScaleQuality {
	frameArea := frameW * frameH
	if frameArea > 0 && dstW*dstH*4 < frameArea {
		return transition.ScaleQualityFast
	}
	return transition.ScaleQualityHigh
}

// Latency returns the estimated processing time for the current layout.
func (c *Compositor) Latency() time.Duration {
	l := c.layout.Load()
	if l == nil {
		return 0
	}
	count := 0
	for _, slot := range l.Slots {
		if slot.Enabled {
			count++
		}
	}
	return time.Duration(count) * time.Millisecond
}

// effectiveRectAndAlpha returns the current rect and alpha for a slot,
// accounting for any active animation.
func (c *Compositor) effectiveRectAndAlpha(slotIdx int, slot LayoutSlot) (image.Rectangle, float64) {
	for _, anim := range c.animations {
		if anim.SlotIndex == slotIdx {
			t := anim.Progress()
			if t >= 1.0 {
				continue // completed, will be cleaned up
			}
			rect := anim.InterpolateRect(t)
			alpha := anim.InterpolateAlpha(t)
			return rect, alpha
		}
	}
	if slot.Enabled {
		return slot.Rect, 1.0
	}
	return image.Rectangle{}, 0.0
}

// SlotAnimating returns true if a slot has an active animation (exported for state enrichment).
func (c *Compositor) SlotAnimating(slotIdx int) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.isAnimating(slotIdx)
}

// isAnimating returns true if a slot has an active animation.
func (c *Compositor) isAnimating(slotIdx int) bool {
	for _, anim := range c.animations {
		if anim.SlotIndex == slotIdx && anim.Progress() < 1.0 {
			return true
		}
	}
	return false
}

// tickAnimations removes completed animations and runs callbacks.
func (c *Compositor) tickAnimations() {
	remaining := c.animations[:0]
	for _, anim := range c.animations {
		if anim.Progress() >= 1.0 {
			if anim.OnComplete != nil {
				anim.OnComplete()
			}
			continue
		}
		remaining = append(remaining, anim)
	}
	c.animations = remaining
}

// UpdateFormat updates the frame dimensions (called on pipeline format change).
func (c *Compositor) UpdateFormat(frameW, frameH int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.frameW = frameW
	c.frameH = frameH
	if l := c.layout.Load(); l != nil {
		c.allocateBuffers(l)
	}
}

// UpdateSlot modifies a slot in-place using the given function, then atomically swaps the layout.
func (c *Compositor) UpdateSlot(slotIdx int, fn func(*LayoutSlot)) {
	c.mu.Lock()
	defer c.mu.Unlock()
	l := c.layout.Load()
	if l == nil || slotIdx >= len(l.Slots) {
		return
	}
	updated := c.cloneLayout(l)
	fn(&updated.Slots[slotIdx])
	c.computeSortedSlots(updated)
	c.layout.Store(updated)
}

// UpdateSlotRect updates only the position and size of a slot. This is the
// fast path used by datagram handlers — it does NOT trigger a state broadcast,
// allowing the compositor to pick up position changes at frame rate without
// flooding the control channel.
func (c *Compositor) UpdateSlotRect(slotIdx int, rect image.Rectangle) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	l := c.layout.Load()
	if l == nil || slotIdx >= len(l.Slots) {
		return fmt.Errorf("slot %d: no layout or out of range", slotIdx)
	}
	if rect.Min.X < 0 || rect.Min.Y < 0 || rect.Max.X > c.frameW || rect.Max.Y > c.frameH {
		return fmt.Errorf("rect %v exceeds frame bounds %dx%d", rect, c.frameW, c.frameH)
	}
	updated := c.cloneLayout(l)
	updated.Slots[slotIdx].Rect = rect
	c.computeSortedSlots(updated)
	c.layout.Store(updated)
	return nil
}

// SlotOn brings a slot on-air with its configured transition.
func (c *Compositor) SlotOn(slotIdx int) {
	wasBefore := c.Active()
	c.mu.Lock()

	l := c.layout.Load()
	if l == nil || slotIdx >= len(l.Slots) {
		c.mu.Unlock()
		return
	}

	// Clone layout and enable the slot
	updated := c.cloneLayout(l)
	slot := &updated.Slots[slotIdx]
	slot.Enabled = true
	c.layout.Store(updated)

	switch slot.Transition.Type {
	case "dissolve":
		c.animations = append(c.animations, &Animation{
			SlotIndex: slotIdx,
			StartTime: time.Now(),
			Duration:  slot.Transition.TransitionDuration(),
			FromRect:  slot.Rect,
			ToRect:    slot.Rect,
			FromAlpha: 0.0,
			ToAlpha:   1.0,
		})
	case "fly":
		origin := FlyInOrigin(slot.Rect, c.frameW, c.frameH)
		c.animations = append(c.animations, &Animation{
			SlotIndex: slotIdx,
			StartTime: time.Now(),
			Duration:  slot.Transition.TransitionDuration(),
			FromRect:  origin,
			ToRect:    slot.Rect,
			FromAlpha: 1.0,
			ToAlpha:   1.0,
		})
	}
	c.mu.Unlock()
	// "cut" = no animation, slot is just enabled

	if wasBefore != c.Active() && c.OnActiveChange != nil {
		c.OnActiveChange()
	}
}

// SlotOff takes a slot off-air with its configured transition.
func (c *Compositor) SlotOff(slotIdx int) {
	wasBefore := c.Active()
	c.mu.Lock()

	l := c.layout.Load()
	if l == nil || slotIdx >= len(l.Slots) {
		c.mu.Unlock()
		return
	}

	slot := l.Slots[slotIdx]

	switch slot.Transition.Type {
	case "dissolve":
		c.animations = append(c.animations, &Animation{
			SlotIndex: slotIdx,
			StartTime: time.Now(),
			Duration:  slot.Transition.TransitionDuration(),
			FromRect:  slot.Rect,
			ToRect:    slot.Rect,
			FromAlpha: 1.0,
			ToAlpha:   0.0,
			OnComplete: func() {
				if cur := c.layout.Load(); cur != nil {
					up := c.cloneLayout(cur)
					if slotIdx < len(up.Slots) {
						up.Slots[slotIdx].Enabled = false
					}
					c.layout.Store(up)
				}
			},
		})
	case "fly":
		dest := FlyInOrigin(slot.Rect, c.frameW, c.frameH)
		c.animations = append(c.animations, &Animation{
			SlotIndex: slotIdx,
			StartTime: time.Now(),
			Duration:  slot.Transition.TransitionDuration(),
			FromRect:  slot.Rect,
			ToRect:    dest,
			FromAlpha: 1.0,
			ToAlpha:   1.0,
			OnComplete: func() {
				if cur := c.layout.Load(); cur != nil {
					up := c.cloneLayout(cur)
					if slotIdx < len(up.Slots) {
						up.Slots[slotIdx].Enabled = false
					}
					c.layout.Store(up)
				}
			},
		})
	default: // "cut"
		updated := c.cloneLayout(l)
		updated.Slots[slotIdx].Enabled = false
		c.layout.Store(updated)
	}
	c.mu.Unlock()

	if wasBefore != c.Active() && c.OnActiveChange != nil {
		c.OnActiveChange()
	}
}

// AutoDissolveSource dissolves off any enabled slot whose source matches the given key.
// Used when a program cut changes to match a PIP source.
func (c *Compositor) AutoDissolveSource(sourceKey string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	l := c.layout.Load()
	if l == nil {
		return
	}
	for i, slot := range l.Slots {
		if slot.Enabled && slot.SourceKey == sourceKey {
			idx := i // capture for closure
			c.animations = append(c.animations, &Animation{
				SlotIndex: idx,
				StartTime: time.Now(),
				Duration:  200 * time.Millisecond,
				FromRect:  slot.Rect,
				ToRect:    slot.Rect,
				FromAlpha: 1.0,
				ToAlpha:   0.0,
				OnComplete: func() {
					if cur := c.layout.Load(); cur != nil {
						up := c.cloneLayout(cur)
						if idx < len(up.Slots) {
							up.Slots[idx].Enabled = false
						}
						c.layout.Store(up)
					}
				},
			})
		}
	}
}

// cloneLayout creates a deep copy of a Layout.
func (c *Compositor) cloneLayout(l *Layout) *Layout {
	cp := &Layout{Name: l.Name, Slots: make([]LayoutSlot, len(l.Slots))}
	copy(cp.Slots, l.Slots)
	return cp
}
