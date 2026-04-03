package layout

import (
	"image"

	"github.com/zsiec/switchframe/server/internal/video"

	"github.com/zsiec/switchframe/server/transition"
)

// fillEntry10 holds a cached 10-bit source frame.
type fillEntry10 struct {
	yuv           []byte
	width, height int
	pts           int64
}

// IngestSourceFrame10 caches a decoded YUV422P10LE frame for a source.
func (c *Compositor) IngestSourceFrame10(sourceKey string, yuv []byte, width, height int, pts int64) {
	yuvSize := video.YUV422_10bitFrameSize(width, height)
	if len(yuv) < yuvSize {
		return
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	if c.fills10 == nil {
		c.fills10 = make(map[string]*fillEntry10)
	}

	entry := c.fills10[sourceKey]
	if entry == nil || len(entry.yuv) != yuvSize {
		entry = &fillEntry10{yuv: make([]byte, yuvSize)}
	}

	copy(entry.yuv, yuv[:yuvSize])
	entry.width = width
	entry.height = height
	entry.pts = pts
	c.fills10[sourceKey] = entry
}

// HasFrame10 returns true if a cached 10-bit frame exists for the source.
func (c *Compositor) HasFrame10(sourceKey string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.fills10 == nil {
		return false
	}
	_, ok := c.fills10[sourceKey]
	return ok
}

// ProcessFrame10 composites all enabled layout slots onto a 10-bit YUV422P10LE frame.
// Called from the pipeline goroutine (single-threaded).
func (c *Compositor) ProcessFrame10(yuv []byte, width, height int) []byte {
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

	// Phase 1: Under lock — tick animations, snapshot source data, release lock.
	c.mu.Lock()
	c.tickAnimations()

	sortedSlots := c.sortedSlots
	if len(sortedSlots) == 0 {
		sortedSlots = make([]int, len(l.Slots))
		for i := range l.Slots {
			sortedSlots[i] = i
		}
	}

	type slot10Snap struct {
		slot    Slot
		idx     int
		srcYUV  []byte
		srcW    int
		srcH    int
		rect    image.Rectangle
		alpha   float64
		hasFill bool
		hasGray bool
	}

	var snaps []slot10Snap

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

		// Clamp rect to frame bounds.
		frameBounds := image.Rect(0, 0, width, height)
		rect = rect.Intersect(frameBounds)
		if rect.Empty() {
			continue
		}
		rect.Min.X = EvenAlign(rect.Min.X)
		rect.Min.Y = EvenAlign(rect.Min.Y)
		rect.Max.X = EvenAlign(rect.Max.X)
		rect.Max.Y = EvenAlign(rect.Max.Y)
		if rect.Dx() <= 0 || rect.Dy() <= 0 {
			continue
		}

		snap := slot10Snap{
			slot:  slot,
			idx:   idx,
			rect:  rect,
			alpha: alpha,
		}

		if c.fills10 != nil {
			if entry, ok := c.fills10[slot.SourceKey]; ok {
				yuvSize := video.YUV422_10bitFrameSize(entry.width, entry.height)
				// Deep-copy fill data to avoid racing with IngestSourceFrame10.
				snapBuf := make([]byte, yuvSize)
				copy(snapBuf, entry.yuv[:yuvSize])
				snap.srcYUV = snapBuf
				snap.srcW = entry.width
				snap.srcH = entry.height
				snap.hasFill = true
			}
		}

		if !snap.hasFill {
			snap.hasGray = true
		}

		snaps = append(snaps, snap)
	}
	c.mu.Unlock()

	// Phase 2: Lock-free — scale and composite each slot.
	for i := range snaps {
		snap := &snaps[i]
		slotW := snap.rect.Dx()
		slotH := snap.rect.Dy()

		// No-signal (gray) slots: fill with broadcast black.
		if snap.hasGray {
			FillRectBlack10(yuv, width, height, snap.rect.Min.X, snap.rect.Min.Y, slotW, slotH)
			if snap.slot.Border.Width > 0 {
				borderY10, borderCb10, borderCr10 := borderTo10bit(snap.slot.Border)
				DrawBorderYUV10(yuv, width, height,
					snap.rect.Min.X, snap.rect.Min.Y, slotW, slotH,
					snap.slot.Border.Width, borderY10, borderCb10, borderCr10)
			}
			continue
		}

		// Scale source to slot dimensions.
		var scaled []byte
		if snap.srcW == slotW && snap.srcH == slotH {
			scaled = snap.srcYUV
		} else {
			scaledSize := video.YUV422_10bitFrameSize(slotW, slotH)
			scaleBuf := make([]byte, scaledSize)
			if err := transition.ScaleYUV422P10WithQuality(snap.srcYUV, snap.srcW, snap.srcH,
				scaleBuf, slotW, slotH, transition.ScaleQualityFast); err != nil {
				// Scale failed — fill with black.
				FillRectBlack10(yuv, width, height, snap.rect.Min.X, snap.rect.Min.Y, slotW, slotH)
				continue
			}
			scaled = scaleBuf
		}

		// Composite onto frame.
		ComposePIPOpaque10(yuv, width, height, scaled, slotW, slotH,
			snap.rect.Min.X, snap.rect.Min.Y)

		// Draw border.
		if snap.slot.Border.Width > 0 {
			borderY10, borderCb10, borderCr10 := borderTo10bit(snap.slot.Border)
			DrawBorderYUV10(yuv, width, height,
				snap.rect.Min.X, snap.rect.Min.Y, slotW, slotH,
				snap.slot.Border.Width, borderY10, borderCb10, borderCr10)
		}
	}

	return yuv
}

// borderTo10bit converts 8-bit border colors to 10-bit by scaling by 4.
// Maps BT.709 8-bit limited range to 10-bit limited range.
func borderTo10bit(b BorderConfig) (uint16, uint16, uint16) {
	return uint16(b.ColorY) * 4, uint16(b.ColorCb) * 4, uint16(b.ColorCr) * 4
}
