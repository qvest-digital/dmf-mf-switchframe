package dve

import (
	"fmt"
	"image"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"github.com/zsiec/switchframe/server/internal/video"
	"github.com/zsiec/switchframe/server/transition"
)

// DefaultTransitionDurationMs is the default slot transition duration.
const DefaultTransitionDurationMs = 300

// fillEntry holds a cached source frame.
type fillEntry struct {
	yuv           []byte
	width, height int
	pts           int64 // last ingested PTS

	// Per-slot cache of scaled output. Invalidated when source PTS changes.
	scaledCache map[int]*scaledCacheEntry
}

// scaledCacheEntry caches a scaled YUV420 frame for a specific slot.
type scaledCacheEntry struct {
	yuv        []byte // cached scaled YUV420
	srcW, srcH int    // source dimensions when scaled
	dstW, dstH int    // slot dimensions when scaled
	pts        int64  // source PTS when scaled
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

// slotAnimation represents a slot transition animation.
type slotAnimation struct {
	SlotIndex  int
	StartTime  time.Time
	Duration   time.Duration
	FromRect   image.Rectangle
	ToRect     image.Rectangle
	FromAlpha  float64
	ToAlpha    float64
	Easing     func(float64) float64
	OnComplete func()
	now        func() time.Time // test hook; nil means time.Now
}

// Progress returns the animation progress [0, 1].
func (a *slotAnimation) Progress() float64 {
	now := time.Now()
	if a.now != nil {
		now = a.now()
	}
	elapsed := now.Sub(a.StartTime)
	t := float64(elapsed) / float64(a.Duration)
	if t >= 1.0 {
		return 1.0
	}
	if t <= 0 {
		return 0
	}
	if a.Easing != nil {
		return a.Easing(t)
	}
	return t
}

// InterpolateRect returns the interpolated rectangle at progress t.
func (a *slotAnimation) InterpolateRect(t float64) image.Rectangle {
	return image.Rect(
		EvenAlign(lerpInt(a.FromRect.Min.X, a.ToRect.Min.X, t)),
		EvenAlign(lerpInt(a.FromRect.Min.Y, a.ToRect.Min.Y, t)),
		EvenAlign(lerpInt(a.FromRect.Max.X, a.ToRect.Max.X, t)),
		EvenAlign(lerpInt(a.FromRect.Max.Y, a.ToRect.Max.Y, t)),
	)
}

// InterpolateAlpha returns the interpolated alpha at progress t.
// Clamps to endpoint values to avoid float imprecision causing visual pops.
func (a *slotAnimation) InterpolateAlpha(t float64) float64 {
	alpha := a.FromAlpha + (a.ToAlpha-a.FromAlpha)*t
	// Clamp to target endpoints to prevent 255/256 blend residue.
	if a.ToAlpha >= 1.0 && alpha > 0.99 {
		return 1.0
	}
	if a.ToAlpha <= 0.0 && alpha < 0.01 {
		return 0.0
	}
	return alpha
}

func lerpInt(a, b int, t float64) int {
	return a + int(float64(b-a)*t+0.5)
}

// flyInOrigin computes the off-screen starting position for a fly-in animation.
// The PIP flies in from the nearest frame edge based on the rectangle's position.
func flyInOrigin(target image.Rectangle, frameW, frameH int) image.Rectangle {
	// Use edge distances (rectangle edge to frame edge)
	distLeft := target.Min.X
	distRight := frameW - target.Max.X
	distTop := target.Min.Y
	distBottom := frameH - target.Max.Y

	minDist := distLeft
	dx, dy := -(target.Max.X), 0 // slide fully off-screen left
	if distRight < minDist {
		minDist = distRight
		dx, dy = frameW-target.Min.X, 0 // slide fully off-screen right
	}
	if distTop < minDist {
		minDist = distTop
		dx, dy = 0, -(target.Max.Y) // slide fully off-screen top
	}
	if distBottom < minDist {
		dx, dy = 0, frameH-target.Min.Y // slide fully off-screen bottom
	}

	return image.Rect(
		EvenAlign(target.Min.X+dx),
		EvenAlign(target.Min.Y+dy),
		EvenAlign(target.Max.X+dx),
		EvenAlign(target.Max.Y+dy),
	)
}

// Compositor manages the DVE composition pipeline.
// Frame delivery via IngestSourceFrame (called from handleRawVideoFrame).
// Compositing via ProcessFrame (called from pipeline node).
type Compositor struct {
	mu     sync.Mutex
	layout atomic.Pointer[Layout]

	// Per-source cached frames (8-bit YUV420)
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

	// Reusable snapshot slice (avoids per-frame allocation in ProcessFrame)
	snapCache []slotSnapshot

	// Reusable DVE warp buffers (single-threaded pipeline goroutine)
	dveWarpBuf  []byte
	dveAlphaBuf []byte

	// Active animations
	animations []*slotAnimation

	// Slot-level transition configs (indexed by slot)
	slotTransitions []SlotTransition

	// Frame dimensions
	frameW, frameH int

	// OnActiveChange is called when SetLayout changes the Active() state.
	// Used by the switcher to trigger pipeline rebuilds.
	OnActiveChange func()
}

// NewCompositor creates a new DVE compositor.
func NewCompositor(frameW, frameH int) *Compositor {
	return &Compositor{
		fills:  make(map[string]*fillEntry),
		frameW: frameW,
		frameH: frameH,
	}
}

// SetLayout atomically sets the current layout. nil clears the layout.
// Clears stale source fills that are no longer needed by any slot to
// prevent flashes of old frame data when layouts change.
//
// The layout pointer is stored AFTER buffer allocation and fill pruning
// so that a concurrent ProcessFrame never sees a layout whose buffers
// haven't been prepared yet.
func (c *Compositor) SetLayout(l *Layout) {
	c.mu.Lock()
	wasBefore := c.hasEnabledSlots(c.layout.Load())
	if l != nil {
		c.allocateBuffers(l)
		// Prune fills for sources not in the new layout.
		needed := make(map[string]bool, len(l.Slots))
		for _, slot := range l.Slots {
			if slot.SourceKey != "" {
				needed[slot.SourceKey] = true
			}
		}
		for key := range c.fills {
			if !needed[key] {
				delete(c.fills, key)
			}
		}
	} else {
		// Layout cleared -- drop all fills.
		c.fills = make(map[string]*fillEntry)
	}
	c.layout.Store(l)
	wasAfter := c.hasEnabledSlots(l)
	c.mu.Unlock()
	if wasBefore != wasAfter && c.OnActiveChange != nil {
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
	c.slotTransitions = make([]SlotTransition, len(l.Slots))
	for i, slot := range l.Slots {
		rect := slot.Transform.ToRect()
		w := rect.Dx()
		h := rect.Dy()
		size := video.YUV420FrameSize(w, h)
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
	return video.NewBlackFrame(video.YUV420_8bit, w, h)
}

// FrameSize returns the compositor's configured frame dimensions.
func (c *Compositor) FrameSize() (int, int) { return c.frameW, c.frameH }

// Active returns true if a layout is configured with at least one enabled slot.
func (c *Compositor) Active() bool {
	return c.hasEnabledSlots(c.layout.Load())
}

// hasEnabledSlots checks if a layout has at least one enabled slot.
func (c *Compositor) hasEnabledSlots(l *Layout) bool {
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
func (c *Compositor) IngestSourceFrame(sourceKey string, yuv []byte, width, height int, pts int64) {
	c.mu.Lock()
	defer c.mu.Unlock()

	yuvSize := video.YUV420FrameSize(width, height)
	if len(yuv) < yuvSize {
		return
	}

	entry := c.fills[sourceKey]
	if entry == nil || len(entry.yuv) != yuvSize {
		entry = &fillEntry{yuv: make([]byte, yuvSize)}
	}

	// Invalidate scaled cache when PTS changes or dimensions change.
	if entry.pts != pts || entry.width != width || entry.height != height {
		clear(entry.scaledCache)
	}

	copy(entry.yuv, yuv[:yuvSize])
	entry.width = width
	entry.height = height
	entry.pts = pts
	c.fills[sourceKey] = entry
}

// slotSnapshot holds source data for a single slot, captured under lock.
type slotSnapshot struct {
	slot         Slot
	idx          int
	srcYUV       []byte
	srcW         int
	srcH         int
	rect         image.Rectangle
	alpha        float64
	hasFill      bool
	hasGray      bool
	scaleBuf     []byte
	cropBuf      []byte            // non-nil when fill mode needs cropping
	fillPTS      int64             // PTS of the fill entry at snapshot time
	cachedScaled *scaledCacheEntry // cached scaled frame (may be nil)
	fillKey      string            // source key for cache writeback
	scaleMode    string            // resolved scale mode
	cropAnchor   [2]float64        // resolved crop anchor
}

// effectiveScaleMode returns the slot's effective scale mode, defaulting to "stretch".
func effectiveScaleMode(t Transform) string {
	if t.ScaleMode == ScaleModeFill {
		return ScaleModeFill
	}
	return ScaleModeStretch
}

// effectiveCropAnchor returns the slot's effective crop anchor, defaulting to center (0.5, 0.5).
func effectiveCropAnchor(t Transform) (float64, float64) {
	ax, ay := t.CropAnchor[0], t.CropAnchor[1]
	// In fill mode, always use raw values (including [0,0] = top-left).
	// In non-fill mode, zero-value means unset -- default to center.
	if t.ScaleMode != ScaleModeFill && ax == 0 && ay == 0 {
		return 0.5, 0.5
	}
	return clamp01(ax), clamp01(ay)
}

// ProcessFrame composites all enabled layout slots onto the frame.
// Called from the pipeline goroutine (single-threaded).
func (c *Compositor) ProcessFrame(yuv []byte, width, height int) []byte {
	l := c.layout.Load()
	if l == nil {
		return yuv
	}

	// Compute scale factors if frame dimensions differ from configured dimensions.
	scaleX, scaleY := 1.0, 1.0
	needsScale := false
	if width != c.frameW || height != c.frameH {
		if c.frameW > 0 && c.frameH > 0 {
			scaleX = float64(width) / float64(c.frameW)
			scaleY = float64(height) / float64(c.frameH)
			needsScale = true
		}
	}

	// Phase 1: Under lock -- tick animations, snapshot source data, release lock.
	c.mu.Lock()
	deactivated := c.tickAnimations()

	sortedSlots := c.sortedSlots
	if len(sortedSlots) == 0 {
		sortedSlots = make([]int, len(l.Slots))
		for i := range l.Slots {
			sortedSlots[i] = i
		}
	}

	c.snapCache = c.snapCache[:0]
	for _, idx := range sortedSlots {
		if idx >= len(l.Slots) {
			continue
		}
		slot := l.Slots[idx]
		if !slot.Enabled && !c.isAnimating(idx) {
			continue
		}

		// Resolve RectMode: if the slot uses rect mode, resolve the transform
		// using cached source dimensions (or 0,0 if unknown).
		transform := slot.Transform
		srcW, srcH := 0, 0
		if entry, ok := c.fills[slot.SourceKey]; ok {
			srcW = entry.width
			srcH = entry.height
		}
		if transform.RectMode {
			transform = transform.ResolveRect(srcW, srcH)
		}

		slotRect := transform.ToRect()
		rect, alpha := c.effectiveRectAndAlpha(idx, slotRect, slot.Enabled)
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

		// Apply DVE opacity on top of animation alpha.
		dveOpacity := transform.EffectiveOpacity()
		if dveOpacity < 1.0 {
			alpha *= dveOpacity
		}

		snap := slotSnapshot{
			slot:       slot,
			idx:        idx,
			rect:       rect,
			alpha:      alpha,
			scaleMode:  effectiveScaleMode(transform),
			cropAnchor: [2]float64{transform.CropAnchor[0], transform.CropAnchor[1]},
		}

		if entry, ok := c.fills[slot.SourceKey]; ok {
			yuvSize := video.YUV420FrameSize(entry.width, entry.height)
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
			snap.fillPTS = entry.pts
			snap.fillKey = slot.SourceKey
			if entry.scaledCache != nil {
				snap.cachedScaled = entry.scaledCache[idx]
			}
		} else if idx < len(c.grayBufs) {
			snap.srcYUV = c.grayBufs[idx]
			// Use the actual gray buffer dimensions from the slot transform rect.
			grayRect := slot.Transform.ToRect()
			grayW := grayRect.Dx()
			grayH := grayRect.Dy()
			if len(snap.srcYUV) == video.YUV420FrameSize(grayW, grayH) {
				snap.srcW = grayW
				snap.srcH = grayH
			} else {
				continue
			}
			snap.hasGray = true
		} else {
			continue
		}

		// Grab the scale buffer reference (pre-allocated).
		slotW := rect.Dx()
		slotH := rect.Dy()
		neededSize := video.YUV420FrameSize(slotW, slotH)
		if idx < len(c.scaleBufs) && len(c.scaleBufs[idx]) >= neededSize {
			snap.scaleBuf = c.scaleBufs[idx][:neededSize]
		} else {
			buf := make([]byte, neededSize)
			if idx < len(c.scaleBufs) {
				c.scaleBufs[idx] = buf
			} else {
				for len(c.scaleBufs) <= idx {
					c.scaleBufs = append(c.scaleBufs, nil)
				}
				c.scaleBufs[idx] = buf
			}
			snap.scaleBuf = buf[:neededSize]
		}

		// For fill-mode slots with a real source, allocate a crop buffer.
		if snap.hasFill && snap.scaleMode == ScaleModeFill {
			cropNeeded := video.YUV420FrameSize(snap.srcW, snap.srcH)
			for len(c.cropBufs) <= idx {
				c.cropBufs = append(c.cropBufs, nil)
			}
			if len(c.cropBufs[idx]) < cropNeeded {
				c.cropBufs[idx] = make([]byte, cropNeeded)
			}
			snap.cropBuf = c.cropBufs[idx][:cropNeeded]
		}

		c.snapCache = append(c.snapCache, snap)
	}
	c.mu.Unlock()

	// A dissolve/fly completion that disabled the last enabled slot transitions
	// the compositor to inactive; notify the switcher (outside the lock) so it
	// can rebuild the pipeline and drop the now-idle DVE node.
	if deactivated && c.OnActiveChange != nil {
		c.OnActiveChange()
	}

	// Phase 2: Lock-free -- crop (if fill mode), scale, and composite each slot.
	for i := range c.snapCache {
		snap := &c.snapCache[i]
		slotW := snap.rect.Dx()
		slotH := snap.rect.Dy()

		// Gray (no-signal) slots at full opacity: fill directly with broadcast black.
		if snap.hasGray && snap.alpha >= 1.0 {
			FillRectBlack(yuv, width, height, snap.rect)
			borderW := int(snap.slot.Transform.Border.Width)
			if borderW > 0 {
				color := [3]byte{snap.slot.Transform.Border.ColorY, snap.slot.Transform.Border.ColorCb, snap.slot.Transform.Border.ColorCr}
				DrawBorderYUV(yuv, width, height, snap.rect, color, borderW)
			}
			continue
		}

		srcYUV := snap.srcYUV
		srcW := snap.srcW
		srcH := snap.srcH

		// Crop-to-fill: extract aspect-matching sub-region before scaling.
		if snap.cropBuf != nil {
			anchorX, anchorY := effectiveCropAnchor(snap.slot.Transform)
			cropX, cropY, cropW, cropH := ComputeCropRect(srcW, srcH, slotW, slotH, anchorX, anchorY)
			if cropW > 0 && cropH > 0 && (cropW != srcW || cropH != srcH) {
				cropSize := video.YUV420FrameSize(cropW, cropH)
				CropYUV420Region(snap.cropBuf[:cropSize], srcYUV, srcW, srcH, cropX, cropY, cropW, cropH)
				srcYUV = snap.cropBuf[:cropSize]
				srcW = cropW
				srcH = cropH
			}
		}

		// DVE warp path: for rotated/perspective slots, use inverse-warp compositing.
		if needsWarp(snap.slot.Transform) {
			snap.srcYUV = srcYUV
			snap.srcW = srcW
			snap.srcH = srcH
			c.processSlotDVEWarp(yuv, width, height, snap)
			borderW := int(snap.slot.Transform.Border.Width)
			if borderW > 0 {
				color := [3]byte{snap.slot.Transform.Border.ColorY, snap.slot.Transform.Border.ColorCb, snap.slot.Transform.Border.ColorCr}
				DrawBorderYUV(yuv, width, height, snap.rect, color, borderW)
			}
			continue
		}

		// Scale source to slot dimensions, using cached result when possible.
		var scaled []byte
		if snap.cachedScaled != nil && snap.cropBuf == nil &&
			snap.cachedScaled.pts == snap.fillPTS &&
			snap.cachedScaled.srcW == srcW && snap.cachedScaled.srcH == srcH &&
			snap.cachedScaled.dstW == slotW && snap.cachedScaled.dstH == slotH {
			scaled = snap.cachedScaled.yuv
		} else if srcW == slotW && srcH == slotH {
			scaled = srcYUV
		} else {
			transition.ScaleYUV420WithQuality(srcYUV, srcW, srcH, snap.scaleBuf, slotW, slotH, transition.ScaleQualityFast)
			scaled = snap.scaleBuf

			// Write scaled result back to cache (under lock since fillEntry is shared).
			if snap.hasFill && snap.cropBuf == nil {
				c.mu.Lock()
				if entry, ok := c.fills[snap.fillKey]; ok {
					if entry.scaledCache == nil {
						entry.scaledCache = make(map[int]*scaledCacheEntry)
					}
					cacheEntry := entry.scaledCache[snap.idx]
					scaledSize := video.YUV420FrameSize(slotW, slotH)
					if cacheEntry == nil || len(cacheEntry.yuv) != scaledSize {
						cacheEntry = &scaledCacheEntry{
							yuv: make([]byte, scaledSize),
						}
					}
					copy(cacheEntry.yuv, scaled[:scaledSize])
					cacheEntry.srcW = srcW
					cacheEntry.srcH = srcH
					cacheEntry.dstW = slotW
					cacheEntry.dstH = slotH
					cacheEntry.pts = snap.fillPTS
					entry.scaledCache[snap.idx] = cacheEntry
				}
				c.mu.Unlock()
			}
		}

		// Composite onto frame.
		if snap.alpha < 1.0 {
			BlendRegion(yuv, width, height, scaled, slotW, slotH, snap.rect, snap.alpha)
		} else {
			ComposePIPOpaque(yuv, width, height, scaled, slotW, slotH, snap.rect)
		}

		// Draw border.
		borderW := int(snap.slot.Transform.Border.Width)
		if borderW > 0 {
			color := [3]byte{snap.slot.Transform.Border.ColorY, snap.slot.Transform.Border.ColorCb, snap.slot.Transform.Border.ColorCr}
			DrawBorderYUV(yuv, width, height, snap.rect, color, borderW)
		}
	}

	return yuv
}

// selectScaleQuality always uses bilinear for PIP compositing. Lanczos-3 is
// 5-15x slower and imperceptible at PIP overlay scale.
func (c *Compositor) selectScaleQuality() transition.ScaleQuality {
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

// SlotSnap is a deep-copied snapshot of one layout slot's state.
// Used by the GPU layout node to avoid holding locks during GPU dispatch.
type SlotSnap struct {
	Index                                      int
	Enabled                                    bool
	SourceKey                                  string
	RectX, RectY                               int
	RectW, RectH                               int
	FillYUV                                    []byte // YUV420p source frame (deep copy, nil if no signal)
	FillW, FillH                               int
	FillPTS                                    int64
	BorderColorY, BorderColorCb, BorderColorCr uint8
	BorderThickness                            int
	Alpha                                      float32
	ScaleMode                                  string     // "stretch" (default) or "fill"
	CropAnchor                                 [2]float64 // [x,y] 0.0-1.0, anchor point for crop (0.5,0.5 = center)

	// DVE fields (always populated since this is the DVE compositor).
	DVETransform  *Transform // full DVE transform
	DVEInvMatrix  Matrix3x3  // pre-computed inverse transform matrix
	DVEIsIdentity bool       // true if no DVE effects needed (fast path)
}

// SnapshotSlots returns a deep-copied snapshot of all enabled layout slots
// and their source fill frames. The GPU layout node calls this once per frame,
// then dispatches GPU kernels without holding any locks.
func (c *Compositor) SnapshotSlots() []SlotSnap {
	l := c.layout.Load()
	if l == nil {
		return nil
	}

	// A dissolve/fly completion that disabled the last enabled slot transitions
	// the compositor to inactive; notify the switcher outside the lock. The
	// notify defer is registered first so it runs after the unlock defer (LIFO).
	var deactivated bool
	defer func() {
		if deactivated && c.OnActiveChange != nil {
			c.OnActiveChange()
		}
	}()

	c.mu.Lock()
	defer c.mu.Unlock()

	// Tick animations under lock.
	deactivated = c.tickAnimations()

	var result []SlotSnap
	for _, idx := range c.sortedSlots {
		if idx >= len(l.Slots) {
			continue
		}
		slot := l.Slots[idx]
		if !slot.Enabled && !c.isAnimating(idx) {
			continue
		}

		// Resolve RectMode using cached source dimensions.
		transform := slot.Transform
		srcW, srcH := 0, 0
		if entry, ok := c.fills[slot.SourceKey]; ok {
			srcW = entry.width
			srcH = entry.height
		}
		if transform.RectMode {
			transform = transform.ResolveRect(srcW, srcH)
		}

		slotRect := transform.ToRect()
		rect, alpha := c.effectiveRectAndAlpha(idx, slotRect, slot.Enabled)
		if rect.Dx() <= 0 || rect.Dy() <= 0 {
			continue
		}

		// Apply DVE opacity.
		dveOpacity := transform.EffectiveOpacity()
		if dveOpacity < 1.0 {
			alpha *= dveOpacity
		}

		snap := SlotSnap{
			Index:           idx,
			Enabled:         slot.Enabled,
			SourceKey:       slot.SourceKey,
			RectX:           rect.Min.X,
			RectY:           rect.Min.Y,
			RectW:           rect.Dx(),
			RectH:           rect.Dy(),
			Alpha:           float32(alpha),
			BorderColorY:    transform.Border.ColorY,
			BorderColorCb:   transform.Border.ColorCb,
			BorderColorCr:   transform.Border.ColorCr,
			BorderThickness: int(transform.Border.Width),
			ScaleMode:       effectiveScaleMode(transform),
			CropAnchor:      transform.CropAnchor,
			DVETransform:    &transform,
			DVEIsIdentity:   isSimple(transform),
		}

		if entry, ok := c.fills[slot.SourceKey]; ok {
			yuvSize := video.YUV420FrameSize(entry.width, entry.height)
			if len(entry.yuv) >= yuvSize {
				fillCopy := make([]byte, yuvSize)
				copy(fillCopy, entry.yuv[:yuvSize])
				snap.FillYUV = fillCopy
				snap.FillW = entry.width
				snap.FillH = entry.height
				snap.FillPTS = entry.pts
			}
		}

		// Compute inverse matrix for GPU warp path.
		invSrcW := snap.FillW
		invSrcH := snap.FillH
		if invSrcW <= 0 || invSrcH <= 0 {
			invSrcW = int(transform.Width)
			invSrcH = int(transform.Height)
		}
		if invSrcW > 0 && invSrcH > 0 {
			snap.DVEInvMatrix = ComputeInverseTransform(transform, invSrcW, invSrcH)
		} else {
			snap.DVEInvMatrix = Identity()
		}

		result = append(result, snap)
	}

	return result
}

// effectiveRectAndAlpha returns the current rect and alpha for a slot,
// accounting for any active animation.
func (c *Compositor) effectiveRectAndAlpha(slotIdx int, slotRect image.Rectangle, enabled bool) (image.Rectangle, float64) {
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
	if enabled {
		return slotRect, 1.0
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

// tickAnimations removes completed animations and runs callbacks. It returns
// true when running the completion callbacks transitioned the compositor from
// active (at least one enabled slot) to inactive -- e.g. the last enabled slot
// finished dissolving or flying off. Callers must invoke OnActiveChange outside
// the lock in that case so the switcher can drop the now-idle DVE node.
// Must be called with c.mu held.
func (c *Compositor) tickAnimations() (deactivated bool) {
	// Progress() re-reads the wall clock, so it must be evaluated exactly once
	// per animation: a separate precheck scan could miss an animation that
	// crosses 1.0 between scans, running OnComplete without having captured
	// wasActive and losing the active-to-inactive transition. Instead, capture
	// the active state lazily, right before the first completion callback runs.
	wasActive := false
	capturedActive := false
	remaining := c.animations[:0]
	for _, anim := range c.animations {
		if anim.Progress() >= 1.0 {
			if anim.OnComplete != nil {
				if !capturedActive {
					capturedActive = true
					wasActive = c.hasEnabledSlots(c.layout.Load())
				}
				anim.OnComplete()
			}
			continue
		}
		remaining = append(remaining, anim)
	}
	c.animations = remaining

	return wasActive && !c.hasEnabledSlots(c.layout.Load())
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
func (c *Compositor) UpdateSlot(slotIdx int, fn func(*Slot)) {
	c.mu.Lock()
	defer c.mu.Unlock()
	l := c.layout.Load()
	if l == nil || slotIdx < 0 || slotIdx >= len(l.Slots) {
		return
	}
	updated := c.cloneLayout(l)
	fn(&updated.Slots[slotIdx])
	c.computeSortedSlots(updated)
	c.layout.Store(updated)
}

// UpdateSlotTransform applies a mutation function to a slot's Transform in the
// current layout. The layout is updated atomically. No state broadcast is
// triggered -- fast-control is fire-and-forget; the REST confirmation path
// triggers broadcasts separately.
func (c *Compositor) UpdateSlotTransform(slotIdx int, fn func(*Transform)) {
	c.mu.Lock()
	defer c.mu.Unlock()

	l := c.layout.Load()
	if l == nil || slotIdx < 0 || slotIdx >= len(l.Slots) {
		return
	}

	// Deep-copy the layout so concurrent readers see a consistent snapshot.
	updated := c.cloneLayout(l)
	fn(&updated.Slots[slotIdx].Transform)
	c.computeSortedSlots(updated)
	c.layout.Store(updated)
}

// UpdateSlotRect updates only the position and size of a slot via the transform.
// This is the fast path used by datagram handlers -- it does NOT trigger a state
// broadcast, allowing the compositor to pick up position changes at frame rate
// without flooding the control channel.
func (c *Compositor) UpdateSlotRect(slotIdx int, rect image.Rectangle) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	l := c.layout.Load()
	if l == nil || slotIdx < 0 || slotIdx >= len(l.Slots) {
		return fmt.Errorf("slot %d: no layout or out of range", slotIdx)
	}
	// Snap to even alignment for YUV420 compatibility.
	rect.Min.X &^= 1
	rect.Min.Y &^= 1
	rect.Max.X = (rect.Max.X + 1) &^ 1
	rect.Max.Y = (rect.Max.Y + 1) &^ 1
	if rect.Min.X < 0 || rect.Min.Y < 0 || rect.Max.X > c.frameW || rect.Max.Y > c.frameH {
		return fmt.Errorf("rect %v exceeds frame bounds %dx%d", rect, c.frameW, c.frameH)
	}
	updated := c.cloneLayout(l)
	updated.Slots[slotIdx].Transform.PositionX = float64(rect.Min.X)
	updated.Slots[slotIdx].Transform.PositionY = float64(rect.Min.Y)
	updated.Slots[slotIdx].Transform.Width = float64(rect.Dx())
	updated.Slots[slotIdx].Transform.Height = float64(rect.Dy())
	c.computeSortedSlots(updated)
	c.layout.Store(updated)
	return nil
}

// SlotOn brings a slot on-air with its configured transition.
func (c *Compositor) SlotOn(slotIdx int) {
	c.mu.Lock()

	l := c.layout.Load()
	if l == nil || slotIdx < 0 || slotIdx >= len(l.Slots) {
		c.mu.Unlock()
		return
	}

	wasBefore := c.hasEnabledSlots(l)

	// Clone layout and enable the slot
	updated := c.cloneLayout(l)
	slot := &updated.Slots[slotIdx]
	slot.Enabled = true
	c.layout.Store(updated)

	wasAfter := c.hasEnabledSlots(updated)

	// Get the slot rect for animation.
	slotRect := slot.Transform.ToRect()

	// Determine transition type from slot transitions config.
	transType := "cut"
	var transDuration time.Duration
	if slotIdx < len(c.slotTransitions) {
		transType = c.slotTransitions[slotIdx].Type
		transDuration = c.slotTransitions[slotIdx].TransitionDuration()
	}

	switch transType {
	case "dissolve":
		c.animations = append(c.animations, &slotAnimation{
			SlotIndex: slotIdx,
			StartTime: time.Now(),
			Duration:  transDuration,
			FromRect:  slotRect,
			ToRect:    slotRect,
			FromAlpha: 0.0,
			ToAlpha:   1.0,
		})
	case "fly":
		origin := flyInOrigin(slotRect, c.frameW, c.frameH)
		c.animations = append(c.animations, &slotAnimation{
			SlotIndex: slotIdx,
			StartTime: time.Now(),
			Duration:  transDuration,
			FromRect:  origin,
			ToRect:    slotRect,
			FromAlpha: 1.0,
			ToAlpha:   1.0,
		})
	}
	c.mu.Unlock()
	// "cut" = no animation, slot is just enabled

	if wasBefore != wasAfter && c.OnActiveChange != nil {
		c.OnActiveChange()
	}
}

// SlotOff takes a slot off-air with its configured transition.
func (c *Compositor) SlotOff(slotIdx int) {
	c.mu.Lock()

	l := c.layout.Load()
	if l == nil || slotIdx < 0 || slotIdx >= len(l.Slots) {
		c.mu.Unlock()
		return
	}

	wasBefore := c.hasEnabledSlots(l)

	slot := l.Slots[slotIdx]
	slotRect := slot.Transform.ToRect()

	// Determine transition type from slot transitions config.
	transType := "cut"
	var transDuration time.Duration
	if slotIdx < len(c.slotTransitions) {
		transType = c.slotTransitions[slotIdx].Type
		transDuration = c.slotTransitions[slotIdx].TransitionDuration()
	}

	var wasAfter bool
	switch transType {
	case "dissolve":
		c.animations = append(c.animations, &slotAnimation{
			SlotIndex: slotIdx,
			StartTime: time.Now(),
			Duration:  transDuration,
			FromRect:  slotRect,
			ToRect:    slotRect,
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
		wasAfter = wasBefore
	case "fly":
		dest := flyInOrigin(slotRect, c.frameW, c.frameH)
		c.animations = append(c.animations, &slotAnimation{
			SlotIndex: slotIdx,
			StartTime: time.Now(),
			Duration:  transDuration,
			FromRect:  slotRect,
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
		wasAfter = wasBefore
	default: // "cut"
		updated := c.cloneLayout(l)
		updated.Slots[slotIdx].Enabled = false
		c.layout.Store(updated)
		wasAfter = c.hasEnabledSlots(updated)
	}
	c.mu.Unlock()

	if wasBefore != wasAfter && c.OnActiveChange != nil {
		c.OnActiveChange()
	}
}

// SetSlotTransition configures the transition type for a slot.
func (c *Compositor) SetSlotTransition(slotIdx int, st SlotTransition) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for len(c.slotTransitions) <= slotIdx {
		c.slotTransitions = append(c.slotTransitions, SlotTransition{})
	}
	c.slotTransitions[slotIdx] = st
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
			slotRect := slot.Transform.ToRect()
			c.animations = append(c.animations, &slotAnimation{
				SlotIndex: idx,
				StartTime: time.Now(),
				Duration:  200 * time.Millisecond,
				FromRect:  slotRect,
				ToRect:    slotRect,
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
	cp := &Layout{Name: l.Name, Slots: make([]Slot, len(l.Slots))}
	copy(cp.Slots, l.Slots)
	return cp
}

// needsWarp returns true when the DVE transform has rotation or perspective
// that requires inverse-warp compositing instead of simple rect copy.
func needsWarp(t Transform) bool {
	return t.RotationDeg != 0 || t.PerspectiveX != 0 || t.PerspectiveY != 0
}

// isSimple returns true when the DVE transform only uses position/size
// (no rotation, no perspective, no crop-softness, no border corner radius,
// no shadow, no defocus). This is the fast path that matches legacy compositing.
func isSimple(t Transform) bool {
	return !needsWarp(t) &&
		t.CropSoftness == 0 &&
		t.Border.CornerRadius == 0 &&
		!t.Shadow.Enabled &&
		t.DefocusRadius == 0
}

// processSlotDVEWarp handles DVE-aware compositing for slots with rotation.
func (c *Compositor) processSlotDVEWarp(yuv []byte, width, height int, snap *slotSnapshot) {
	tr := snap.slot.Transform

	srcYUV := snap.srcYUV
	srcW := snap.srcW
	srcH := snap.srcH
	rect := snap.rect

	// Compute the inverse transform for this source.
	invM := ComputeInverseTransform(tr, srcW, srcH)

	// For each pixel in the AABB (rect), inverse-transform to find source coordinate,
	// bilinear sample from source YUV, and write to output.
	slotW := rect.Dx()
	slotH := rect.Dy()
	if slotW <= 0 || slotH <= 0 {
		return
	}

	// Reuse pooled buffers (safe: called from single-threaded pipeline goroutine).
	warpSize := video.YUV420FrameSize(slotW, slotH)
	if len(c.dveWarpBuf) < warpSize {
		c.dveWarpBuf = make([]byte, warpSize)
	}
	warpBuf := c.dveWarpBuf[:warpSize]

	// Initialize to transparent (BT.709 black).
	yPlaneSize := slotW * slotH
	for i := 0; i < yPlaneSize; i++ {
		warpBuf[i] = 16
	}
	chromaSize := (slotW / 2) * (slotH / 2)
	for i := 0; i < chromaSize*2; i++ {
		warpBuf[yPlaneSize+i] = 128
	}

	// Alpha mask for coverage tracking.
	if len(c.dveAlphaBuf) < yPlaneSize {
		c.dveAlphaBuf = make([]byte, yPlaneSize)
	}
	alphaMask := c.dveAlphaBuf[:yPlaneSize]
	for i := range alphaMask {
		alphaMask[i] = 0
	}

	// Inverse warp the Y plane.
	srcYSize := srcW * srcH
	fSrcW := float32(srcW)
	fSrcH := float32(srcH)

	for dy := 0; dy < slotH; dy++ {
		outY := float32(rect.Min.Y + dy)
		for dx := 0; dx < slotW; dx++ {
			outX := float32(rect.Min.X + dx)

			sx := invM[0]*outX + invM[1]*outY + invM[2]
			sy := invM[3]*outX + invM[4]*outY + invM[5]
			sw := invM[6]*outX + invM[7]*outY + invM[8]
			if sw != 0 && sw != 1 {
				sx /= sw
				sy /= sw
			}

			if sx < 0 || sy < 0 || sx >= fSrcW || sy >= fSrcH {
				continue
			}

			// Bilinear interpolation for Y plane.
			ix := int(sx)
			iy := int(sy)
			fx := sx - float32(ix)
			fy := sy - float32(iy)

			ix1 := ix + 1
			iy1 := iy + 1
			if ix1 >= srcW {
				ix1 = srcW - 1
			}
			if iy1 >= srcH {
				iy1 = srcH - 1
			}

			y00 := float32(srcYUV[iy*srcW+ix])
			y10 := float32(srcYUV[iy*srcW+ix1])
			y01 := float32(srcYUV[iy1*srcW+ix])
			y11 := float32(srcYUV[iy1*srcW+ix1])
			yVal := y00*(1-fx)*(1-fy) + y10*fx*(1-fy) + y01*(1-fx)*fy + y11*fx*fy

			warpBuf[dy*slotW+dx] = byte(yVal + 0.5)
			alphaMask[dy*slotW+dx] = 255
		}
	}

	// Inverse warp chroma planes (at half resolution).
	chromaW := slotW / 2
	chromaH := slotH / 2
	srcChromaW := srcW / 2
	fSrcChromaW := float32(srcChromaW)
	fSrcChromaH := float32(srcH / 2)

	for cdy := 0; cdy < chromaH; cdy++ {
		outY := float32(rect.Min.Y+cdy*2) + 0.5
		for cdx := 0; cdx < chromaW; cdx++ {
			outX := float32(rect.Min.X+cdx*2) + 0.5

			sx := invM[0]*outX + invM[1]*outY + invM[2]
			sy := invM[3]*outX + invM[4]*outY + invM[5]
			sw := invM[6]*outX + invM[7]*outY + invM[8]
			if sw != 0 && sw != 1 {
				sx /= sw
				sy /= sw
			}

			scx := sx / 2
			scy := sy / 2

			if scx < 0 || scy < 0 || scx >= fSrcChromaW || scy >= fSrcChromaH {
				continue
			}

			ix := int(scx)
			iy := int(scy)
			fx := scx - float32(ix)
			fy := scy - float32(iy)

			ix1 := ix + 1
			iy1 := iy + 1
			if ix1 >= srcChromaW {
				ix1 = srcChromaW - 1
			}
			srcChromaH := srcH / 2
			if iy1 >= srcChromaH {
				iy1 = srcChromaH - 1
			}

			// Cb plane.
			cbBase := srcYSize
			cb00 := float32(srcYUV[cbBase+iy*srcChromaW+ix])
			cb10 := float32(srcYUV[cbBase+iy*srcChromaW+ix1])
			cb01 := float32(srcYUV[cbBase+iy1*srcChromaW+ix])
			cb11 := float32(srcYUV[cbBase+iy1*srcChromaW+ix1])
			cbVal := cb00*(1-fx)*(1-fy) + cb10*fx*(1-fy) + cb01*(1-fx)*fy + cb11*fx*fy

			// Cr plane.
			crBase := srcYSize + srcChromaW*(srcH/2)
			cr00 := float32(srcYUV[crBase+iy*srcChromaW+ix])
			cr10 := float32(srcYUV[crBase+iy*srcChromaW+ix1])
			cr01 := float32(srcYUV[crBase+iy1*srcChromaW+ix])
			cr11 := float32(srcYUV[crBase+iy1*srcChromaW+ix1])
			crVal := cr00*(1-fx)*(1-fy) + cr10*fx*(1-fy) + cr01*(1-fx)*fy + cr11*fx*fy

			dstCbBase := yPlaneSize
			dstCrBase := yPlaneSize + chromaW*chromaH
			warpBuf[dstCbBase+cdy*chromaW+cdx] = byte(cbVal + 0.5)
			warpBuf[dstCrBase+cdy*chromaW+cdx] = byte(crVal + 0.5)
		}
	}

	// Composite warped buffer onto output frame using the effective opacity.
	// Both paths honor the coverage mask: pixels of the AABB never touched
	// by the rotated quad are black padding, not source content, and must
	// not be blended over the program.
	alpha := snap.alpha
	if alpha < 1.0 {
		blendWithMaskAlpha(yuv, width, height, warpBuf, alphaMask, slotW, slotH, rect, alpha)
	} else {
		blendWithMask(yuv, width, height, warpBuf, alphaMask, slotW, slotH, rect)
	}
}
