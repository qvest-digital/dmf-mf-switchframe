package layout

import (
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

	// Per-slot gray "no signal" frames
	grayBufs [][]byte

	// Active animations
	animations []*Animation

	// Frame dimensions
	frameW, frameH int
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
	c.layout.Store(l)
	if l != nil {
		c.mu.Lock()
		c.allocateBuffers(l)
		c.mu.Unlock()
	}
}

// GetLayout returns the current layout (may be nil).
func (c *Compositor) GetLayout() *Layout {
	return c.layout.Load()
}

// allocateBuffers pre-allocates scale and gray buffers for each slot.
func (c *Compositor) allocateBuffers(l *Layout) {
	c.scaleBufs = make([][]byte, len(l.Slots))
	c.grayBufs = make([][]byte, len(l.Slots))
	for i, slot := range l.Slots {
		w := slot.Rect.Dx()
		h := slot.Rect.Dy()
		size := w * h * 3 / 2
		c.scaleBufs[i] = make([]byte, size)
		c.grayBufs[i] = makeGrayFrame(w, h)
	}
}

// makeGrayFrame creates a "no signal" YUV420 frame (Y=128, Cb=128, Cr=128).
func makeGrayFrame(w, h int) []byte {
	ySize := w * h
	cbSize := (w / 2) * (h / 2)
	buf := make([]byte, ySize+cbSize*2)
	for i := 0; i < ySize; i++ {
		buf[i] = 128
	}
	for i := 0; i < cbSize*2; i++ {
		buf[ySize+i] = 128
	}
	return buf
}

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

// ProcessFrame composites all enabled layout slots onto the frame.
// Called from the pipeline goroutine (single-threaded).
func (c *Compositor) ProcessFrame(yuv []byte, width, height int) []byte {
	l := c.layout.Load()
	if l == nil {
		return yuv
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	// Process animations
	c.tickAnimations()

	// Sort slots by ZOrder
	sorted := make([]int, 0, len(l.Slots))
	for i := range l.Slots {
		sorted = append(sorted, i)
	}
	sort.Slice(sorted, func(a, b int) bool {
		return l.Slots[sorted[a]].ZOrder < l.Slots[sorted[b]].ZOrder
	})

	// Composite each slot
	for _, idx := range sorted {
		slot := l.Slots[idx]
		if !slot.Enabled && !c.isAnimating(idx) {
			continue
		}

		// Get the effective rect (may be modified by animation)
		rect, alpha := c.effectiveRectAndAlpha(idx, slot)
		if rect.Dx() <= 0 || rect.Dy() <= 0 {
			continue
		}

		slotW := rect.Dx()
		slotH := rect.Dy()

		// Get source frame or gray fallback
		var srcYUV []byte
		var srcW, srcH int
		if entry, ok := c.fills[slot.SourceKey]; ok {
			srcYUV = entry.yuv
			srcW = entry.width
			srcH = entry.height
		} else if idx < len(c.grayBufs) {
			srcYUV = c.grayBufs[idx]
			srcW = slot.Rect.Dx()
			srcH = slot.Rect.Dy()
		} else {
			continue
		}

		// Scale source to slot dimensions
		var scaled []byte
		if srcW == slotW && srcH == slotH {
			scaled = srcYUV
		} else {
			if idx >= len(c.scaleBufs) || len(c.scaleBufs[idx]) < slotW*slotH*3/2 {
				c.scaleBufs = append(c.scaleBufs, make([]byte, slotW*slotH*3/2))
			}
			buf := c.scaleBufs[idx][:slotW*slotH*3/2]
			quality := c.selectScaleQuality(srcW, srcH, slotW, slotH, width, height)
			transition.ScaleYUV420WithQuality(srcYUV, srcW, srcH, buf, slotW, slotH, quality)
			scaled = buf
		}

		// Composite onto frame
		if alpha < 1.0 {
			BlendRegion(yuv, width, height, scaled, slotW, slotH, rect, alpha)
		} else {
			ComposePIPOpaque(yuv, width, height, scaled, slotW, slotH, rect)
		}

		// Draw border
		if slot.Border.Width > 0 {
			color := [3]byte{slot.Border.ColorY, slot.Border.ColorCb, slot.Border.ColorCr}
			DrawBorderYUV(yuv, width, height, rect, color, slot.Border.Width)
		}
	}

	return yuv
}

// selectScaleQuality chooses Lanczos for small PIPs, bilinear for large.
func (c *Compositor) selectScaleQuality(srcW, srcH, dstW, dstH, frameW, frameH int) transition.ScaleQuality {
	pipArea := dstW * dstH
	frameArea := frameW * frameH
	if pipArea*4 <= frameArea { // PIP is <=25% of frame area
		return transition.ScaleQualityHigh
	}
	return transition.ScaleQualityFast
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
	c.layout.Store(updated)
}

// SlotOn brings a slot on-air with its configured transition.
func (c *Compositor) SlotOn(slotIdx int) {
	c.mu.Lock()
	defer c.mu.Unlock()

	l := c.layout.Load()
	if l == nil || slotIdx >= len(l.Slots) {
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
			Duration:  slot.Transition.Duration,
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
			Duration:  slot.Transition.Duration,
			FromRect:  origin,
			ToRect:    slot.Rect,
			FromAlpha: 1.0,
			ToAlpha:   1.0,
		})
	}
	// "cut" = no animation, slot is just enabled
}

// SlotOff takes a slot off-air with its configured transition.
func (c *Compositor) SlotOff(slotIdx int) {
	c.mu.Lock()
	defer c.mu.Unlock()

	l := c.layout.Load()
	if l == nil || slotIdx >= len(l.Slots) {
		return
	}

	slot := l.Slots[slotIdx]

	switch slot.Transition.Type {
	case "dissolve":
		c.animations = append(c.animations, &Animation{
			SlotIndex: slotIdx,
			StartTime: time.Now(),
			Duration:  slot.Transition.Duration,
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
			Duration:  slot.Transition.Duration,
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
}

// cloneLayout creates a deep copy of a Layout.
func (c *Compositor) cloneLayout(l *Layout) *Layout {
	cp := &Layout{Name: l.Name, Slots: make([]LayoutSlot, len(l.Slots))}
	copy(cp.Slots, l.Slots)
	return cp
}
