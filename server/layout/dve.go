package layout

import (
	"image"

	"github.com/zsiec/switchframe/server/dve"
	"github.com/zsiec/switchframe/server/internal/video"
)

// dveLayout holds the current DVE layout, stored atomically for lock-free
// reads by the frame processing path.
//
// Added as an atomic.Pointer field on Compositor via the extension below.

// SetDVELayout sets a DVE layout, replacing the current layout. Converts
// the DVE slots to legacy layout.Slot entries for the existing ProcessFrame
// pipeline and stores the DVE layout pointer for SnapshotSlots to use.
//
// Pass nil to clear both the DVE layout and the underlying legacy layout.
func (c *Compositor) SetDVELayout(dl *dve.Layout) {
	if dl == nil {
		c.dveLayout.Store(nil)
		c.SetLayout(nil)
		return
	}

	c.dveLayout.Store(dl)

	// Convert DVE slots to legacy layout for the existing CPU pipeline.
	legacySlots := make([]Slot, len(dl.Slots))
	for i, ds := range dl.Slots {
		legacySlots[i] = legacySlotFromDVE(ds, c.frameW, c.frameH)
	}
	legacy := &Layout{
		Name:  dl.Name,
		Slots: legacySlots,
	}

	c.mu.Lock()
	wasBefore := c.hasEnabledSlots(c.layout.Load())
	c.allocateBuffers(legacy)

	// Prune fills for sources not in the new layout.
	needed := make(map[string]bool, len(legacy.Slots))
	for _, slot := range legacy.Slots {
		if slot.SourceKey != "" {
			needed[slot.SourceKey] = true
		}
	}
	for key := range c.fills {
		if !needed[key] {
			delete(c.fills, key)
		}
	}

	c.layout.Store(legacy)
	wasAfter := c.hasEnabledSlots(legacy)
	c.mu.Unlock()

	if wasBefore != wasAfter && c.OnActiveChange != nil {
		c.OnActiveChange()
	}
}

// GetDVELayout returns the current DVE layout (may be nil).
func (c *Compositor) GetDVELayout() *dve.Layout {
	return c.dveLayout.Load()
}

// UpdateDVESlotTransform applies a mutation function to a slot's Transform in the
// current DVE layout. The DVE layout and underlying legacy layout are both updated
// atomically. No state broadcast is triggered — fast-control is fire-and-forget;
// the REST confirmation path triggers broadcasts separately.
func (c *Compositor) UpdateDVESlotTransform(slotIdx int, fn func(*dve.Transform)) {
	c.mu.Lock()
	defer c.mu.Unlock()

	dl := c.dveLayout.Load()
	if dl == nil || slotIdx < 0 || slotIdx >= len(dl.Slots) {
		return
	}

	// Deep-copy the DVE layout so concurrent readers see a consistent snapshot.
	updated := &dve.Layout{
		Name:  dl.Name,
		Slots: make([]dve.Slot, len(dl.Slots)),
	}
	copy(updated.Slots, dl.Slots)

	fn(&updated.Slots[slotIdx].Transform)
	c.dveLayout.Store(updated)

	// Update the corresponding legacy slot for the CPU pipeline.
	if leg := c.layout.Load(); leg != nil && slotIdx < len(leg.Slots) {
		legCopy := c.cloneLayout(leg)
		legCopy.Slots[slotIdx] = legacySlotFromDVE(updated.Slots[slotIdx], c.frameW, c.frameH)
		c.computeSortedSlots(legCopy)
		c.layout.Store(legCopy)
	}
}

// legacySlotFromDVE converts a DVE slot to a legacy layout.Slot.
// For identity-like transforms (no rotation/perspective), the rect is simply
// derived from position/size. For transforms with rotation, we compute the AABB.
func legacySlotFromDVE(ds dve.Slot, frameW, frameH int) Slot {
	tr := ds.Transform

	// Compute the rect: use AABB for non-trivial transforms, simple rect for trivial.
	var rect image.Rectangle
	if tr.RotationDeg != 0 || tr.PerspectiveX != 0 || tr.PerspectiveY != 0 {
		// Use source dimensions equal to transform W/H for AABB computation.
		srcW := int(tr.Width)
		srcH := int(tr.Height)
		if srcW <= 0 {
			srcW = 2
		}
		if srcH <= 0 {
			srcH = 2
		}
		rect = dve.ComputeAABB(tr, srcW, srcH, frameW, frameH)
	} else {
		rect = tr.ToRect()
	}

	// Clamp to frame bounds.
	if rect.Min.X < 0 {
		rect.Min.X = 0
	}
	if rect.Min.Y < 0 {
		rect.Min.Y = 0
	}
	if rect.Max.X > frameW {
		rect.Max.X = EvenAlign(frameW)
	}
	if rect.Max.Y > frameH {
		rect.Max.Y = EvenAlign(frameH)
	}

	// Even-align.
	rect.Min.X = EvenAlign(rect.Min.X)
	rect.Min.Y = EvenAlign(rect.Min.Y)
	rect.Max.X = EvenAlign(rect.Max.X)
	rect.Max.Y = EvenAlign(rect.Max.Y)

	// Border: convert from DVE float to legacy int.
	border := BorderConfig{
		Width:   EvenAlign(int(tr.Border.Width)),
		ColorY:  tr.Border.ColorY,
		ColorCb: tr.Border.ColorCb,
		ColorCr: tr.Border.ColorCr,
	}

	return Slot{
		SourceKey:  ds.SourceKey,
		Rect:       rect,
		ZOrder:     ds.ZOrder,
		Enabled:    ds.Enabled,
		Border:     border,
		ScaleMode:  tr.ScaleMode,
		CropAnchor: tr.CropAnchor,
	}
}

// dveNeedsWarp returns true when the DVE transform has rotation or perspective
// that requires inverse-warp compositing instead of simple rect copy.
func dveNeedsWarp(t dve.Transform) bool {
	return t.RotationDeg != 0 || t.PerspectiveX != 0 || t.PerspectiveY != 0
}

// dveIsSimple returns true when the DVE transform only uses position/size
// (no rotation, no perspective, no crop-softness, no border corner radius,
// no shadow, no defocus). This is the fast path that matches legacy compositing.
func dveIsSimple(t dve.Transform) bool {
	return !dveNeedsWarp(t) &&
		t.CropSoftness == 0 &&
		t.Border.CornerRadius == 0 &&
		!t.Shadow.Enabled &&
		t.DefocusRadius == 0
}

// snapshotSlotsWithDVE augments SlotSnap entries with DVE transform data.
// Called from SnapshotSlots when a DVE layout is active.
func (c *Compositor) enrichSnapsWithDVE(snaps []SlotSnap, dl *dve.Layout) []SlotSnap {
	for i := range snaps {
		snap := &snaps[i]
		if snap.Index < len(dl.Slots) {
			ds := dl.Slots[snap.Index]
			t := &ds.Transform
			snap.DVETransform = t

			// Compute inverse matrix if the slot has fill data for source dimensions.
			srcW := snap.FillW
			srcH := snap.FillH
			if srcW <= 0 || srcH <= 0 {
				// No source frame yet — use transform dimensions.
				srcW = int(t.Width)
				srcH = int(t.Height)
			}
			if srcW > 0 && srcH > 0 {
				snap.DVEInvMatrix = dve.ComputeInverseTransform(*t, srcW, srcH)
			} else {
				snap.DVEInvMatrix = dve.Identity()
			}
			snap.DVEIsIdentity = dveIsSimple(*t)
		}
	}
	return snaps
}

// ProcessFrameDVE handles DVE-aware compositing for slots with rotation.
// For slots without rotation, it falls through to the standard compositing path.
// Called from ProcessFrame when a DVE layout is active.
func (c *Compositor) processSlotDVEWarp(yuv []byte, width, height int, snap *slotSnapshot, dveSlot *dve.Slot) {
	tr := dveSlot.Transform

	srcYUV := snap.srcYUV
	srcW := snap.srcW
	srcH := snap.srcH
	rect := snap.rect

	// Compute the inverse transform for this source.
	invM := dve.ComputeInverseTransform(tr, srcW, srcH)

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

			// Apply inverse transform to get source coordinates.
			sx := invM[0]*outX + invM[1]*outY + invM[2]
			sy := invM[3]*outX + invM[4]*outY + invM[5]
			sw := invM[6]*outX + invM[7]*outY + invM[8]
			if sw != 0 && sw != 1 {
				sx /= sw
				sy /= sw
			}

			// Bounds check. Use fSrcW/fSrcH (not -1) to include the last
			// column/row; the bilinear sample at the edge clamps correctly.
			if sx < 0 || sy < 0 || sx >= fSrcW || sy >= fSrcH {
				continue
			}

			// Bilinear interpolation for Y plane.
			ix := int(sx)
			iy := int(sy)
			fx := sx - float32(ix)
			fy := sy - float32(iy)

			// Clamp to valid range.
			ix1 := ix + 1
			iy1 := iy + 1
			if ix1 >= srcW {
				ix1 = srcW - 1
			}
			if iy1 >= srcH {
				iy1 = srcH - 1
			}

			// Y plane bilinear.
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
	srcChromaH := srcH / 2
	_ = srcChromaH
	fSrcChromaW := float32(srcChromaW)
	fSrcChromaH := float32(srcChromaH)

	for cdy := 0; cdy < chromaH; cdy++ {
		// Chroma pixel center in output space.
		outY := float32(rect.Min.Y+cdy*2) + 0.5
		for cdx := 0; cdx < chromaW; cdx++ {
			outX := float32(rect.Min.X+cdx*2) + 0.5

			// Inverse transform to source.
			sx := invM[0]*outX + invM[1]*outY + invM[2]
			sy := invM[3]*outX + invM[4]*outY + invM[5]
			sw := invM[6]*outX + invM[7]*outY + invM[8]
			if sw != 0 && sw != 1 {
				sx /= sw
				sy /= sw
			}

			// Convert to chroma coordinates.
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
	alpha := snap.alpha
	if alpha < 1.0 {
		BlendRegion(yuv, width, height, warpBuf, slotW, slotH, rect, alpha)
	} else {
		// For full opacity with rotation, we need alpha-aware compositing
		// to avoid overwriting background with black where the rotated
		// source doesn't cover.
		blendWithMask(yuv, width, height, warpBuf, alphaMask, slotW, slotH, rect)
	}
}

// blendWithMask composites src onto dst at the given rect, only copying pixels
// where mask[i] > 0. This avoids overwriting background pixels where a rotated
// source has no coverage.
func blendWithMask(dst []byte, dstW, dstH int, src, mask []byte, srcW, srcH int, rect image.Rectangle) {
	copyW := srcW
	copyH := srcH
	if rect.Min.X+copyW > dstW {
		copyW = dstW - rect.Min.X
	}
	if rect.Min.Y+copyH > dstH {
		copyH = dstH - rect.Min.Y
	}
	if copyW <= 0 || copyH <= 0 || rect.Min.X < 0 || rect.Min.Y < 0 {
		return
	}

	// Y plane.
	for y := 0; y < copyH; y++ {
		for x := 0; x < copyW; x++ {
			if mask[y*srcW+x] > 0 {
				dst[(rect.Min.Y+y)*dstW+rect.Min.X+x] = src[y*srcW+x]
			}
		}
	}

	// Chroma planes.
	dstYSize := dstW * dstH
	srcYSize := srcW * srcH
	chromaDstW := dstW / 2
	chromaSrcW := srcW / 2
	chromaCopyW := copyW / 2
	chromaCopyH := copyH / 2
	chromaX := rect.Min.X / 2
	chromaY := rect.Min.Y / 2

	for plane := 0; plane < 2; plane++ {
		dstBase := dstYSize + plane*(chromaDstW*(dstH/2))
		srcBase := srcYSize + plane*(chromaSrcW*(srcH/2))
		for y := 0; y < chromaCopyH; y++ {
			for x := 0; x < chromaCopyW; x++ {
				// Check if any of the 4 luma pixels in this chroma block are covered.
				ly := y * 2
				lx := x * 2
				if ly < copyH && lx < copyW && mask[ly*srcW+lx] > 0 {
					dstOff := dstBase + (chromaY+y)*chromaDstW + chromaX + x
					srcOff := srcBase + y*chromaSrcW + x
					dst[dstOff] = src[srcOff]
				}
			}
		}
	}
}

// SlotFromLegacy converts a legacy layout.Slot to a DVE Slot, mapping
// Rect to PositionX/Y/Width/Height, BorderConfig fields, etc.
// This bridge function lives in the layout package to avoid a circular
// dependency between layout and dve packages.
func SlotFromLegacy(s Slot) dve.Slot {
	r := s.Rect
	tr := dve.DefaultTransform(float64(r.Min.X), float64(r.Min.Y), float64(r.Dx()), float64(r.Dy()))
	tr.Border = dve.BorderConfig{
		Width:   float64(s.Border.Width),
		ColorY:  s.Border.ColorY,
		ColorCb: s.Border.ColorCb,
		ColorCr: s.Border.ColorCr,
	}
	tr.ScaleMode = s.ScaleMode
	tr.CropAnchor = s.CropAnchor
	return dve.Slot{
		SourceKey: s.SourceKey,
		Transform: tr,
		ZOrder:    s.ZOrder,
		Enabled:   s.Enabled,
	}
}
