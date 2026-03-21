//go:build darwin || (cgo && cuda)

package gpu

import (
	"log/slog"
	"sync/atomic"
	"time"
)

// gpuLayoutNode applies PIP/multi-layout compositing on the GPU.
// It reads layout state via the LayoutState interface, uploads per-slot
// fill frames, and dispatches PIPComposite/DrawBorder/FillRect kernels.
type gpuLayoutNode struct {
	ctx    *Context
	pool   *FramePool
	layout LayoutState

	// Per-source cached fill frames (GPU NV12), keyed by SourceKey.
	// Invalidated when fill dimensions change.
	fills    map[string]*GPUFrame
	fillDims map[string][2]int // [width, height] for cache validation

	width, height int
	lastErr       atomic.Value
}

// NewGPULayoutNode creates a GPU layout compositor pipeline node.
func NewGPULayoutNode(ctx *Context, pool *FramePool, layout LayoutState) GPUPipelineNode {
	return &gpuLayoutNode{
		ctx:      ctx,
		pool:     pool,
		layout:   layout,
		fills:    make(map[string]*GPUFrame),
		fillDims: make(map[string][2]int),
	}
}

func (n *gpuLayoutNode) Name() string { return "gpu_layout" }

func (n *gpuLayoutNode) Configure(width, height, pitch int) error {
	n.width = width
	n.height = height

	// Invalidate cached fills (program dimensions may have changed).
	for k, f := range n.fills {
		f.Release()
		delete(n.fills, k)
		delete(n.fillDims, k)
	}

	return nil
}

func (n *gpuLayoutNode) Active() bool {
	return n.layout != nil && n.layout.Active()
}

func (n *gpuLayoutNode) Latency() time.Duration { return 500 * time.Microsecond }

func (n *gpuLayoutNode) Err() error {
	if v := n.lastErr.Load(); v != nil {
		return v.(error)
	}
	return nil
}

func (n *gpuLayoutNode) Close() error {
	for k, f := range n.fills {
		f.Release()
		delete(n.fills, k)
		delete(n.fillDims, k)
	}
	return nil
}

// ProcessGPU composites all enabled layout slots onto the program frame.
// For each slot:
//  1. No signal (FillYUV nil): FillRect with black, then draw border if configured.
//  2. Has signal: Upload fill to GPU (cached per source), PIPComposite
//     (handles scaling internally), then draw border if configured.
func (n *gpuLayoutNode) ProcessGPU(frame *GPUFrame) error {
	slots := n.layout.SnapshotSlots()
	if len(slots) == 0 {
		return nil
	}

	for i := range slots {
		slot := &slots[i]
		if !slot.Enabled {
			continue
		}
		if err := n.processSlot(frame, slot); err != nil {
			slog.Warn("gpu_layout: slot processing failed, skipping",
				"slot", slot.Index, "source", slot.SourceKey, "error", err)
			n.lastErr.Store(err)
			// Continue with remaining slots — don't fail the whole pipeline.
		}
	}

	return nil
}

// processSlot handles a single layout slot.
func (n *gpuLayoutNode) processSlot(frame *GPUFrame, slot *SlotSnapshot) error {
	rect := Rect{X: slot.Rect.X, Y: slot.Rect.Y, W: slot.Rect.W, H: slot.Rect.H}

	// Compute crop rect if fill mode is active.
	var cropX, cropY, cropW, cropH int
	alpha := float64(slot.Alpha)

	// Try GPU cache first (already NV12 in VRAM, no upload needed).
	fillGPU := n.layout.GPUFill(slot.SourceKey)
	if fillGPU != nil {
		defer fillGPU.Release()
		if slot.ScaleMode == "fill" {
			cropX, cropY, cropW, cropH = computeGPUCropRect(
				fillGPU.Width, fillGPU.Height, rect.W, rect.H, slot.CropAnchor)
		}
		if err := PIPCompositeWithCrop(n.ctx, frame, fillGPU, rect, alpha,
			cropX, cropY, cropW, cropH); err != nil {
			return err
		}
	} else if len(slot.FillYUV) == 0 || slot.FillW == 0 || slot.FillH == 0 {
		// No signal — fill with black.
		if err := FillRect(n.ctx, frame, rect, ColorBlack); err != nil {
			return err
		}
	} else {
		// CPU fallback: upload from snapshot.
		uploaded, err := n.getOrUploadFill(slot)
		if err != nil {
			return err
		}

		if slot.ScaleMode == "fill" {
			cropX, cropY, cropW, cropH = computeGPUCropRect(
				uploaded.Width, uploaded.Height, rect.W, rect.H, slot.CropAnchor)
		}
		if err := PIPCompositeWithCrop(n.ctx, frame, uploaded, rect, alpha,
			cropX, cropY, cropW, cropH); err != nil {
			return err
		}
	}

	// Draw border if configured.
	if slot.Border.Thickness > 0 {
		color := YUVColor{
			Y:  slot.Border.ColorY,
			Cb: slot.Border.ColorCb,
			Cr: slot.Border.ColorCr,
		}
		if err := DrawBorder(n.ctx, frame, rect, color, slot.Border.Thickness); err != nil {
			return err
		}
	}

	return nil
}

// computeGPUCropRect computes the largest source sub-region that matches the
// destination slot's aspect ratio. The region is positioned using the anchor
// point (0.0-1.0 on each axis). All coordinates are even-aligned for YUV420.
func computeGPUCropRect(srcW, srcH, dstW, dstH int, anchor [2]float64) (cropX, cropY, cropW, cropH int) {
	if srcW <= 0 || srcH <= 0 || dstW <= 0 || dstH <= 0 {
		return 0, 0, 0, 0
	}

	slotAR := float64(dstW) / float64(dstH)
	srcAR := float64(srcW) / float64(srcH)

	if srcAR > slotAR {
		// Source is wider — crop horizontally.
		cropH = srcH
		cropW = int(float64(srcH) * slotAR)
	} else {
		// Source is taller — crop vertically.
		cropW = srcW
		cropH = int(float64(srcW) / slotAR)
	}

	// Even-align for YUV420.
	cropW &^= 1
	cropH &^= 1
	if cropW > srcW {
		cropW = srcW &^ 1
	}
	if cropH > srcH {
		cropH = srcH &^ 1
	}
	if cropW <= 0 || cropH <= 0 {
		return 0, 0, 0, 0
	}

	// No crop needed if crop region matches source.
	if cropW == srcW&^1 && cropH == srcH&^1 {
		return 0, 0, 0, 0
	}

	// Position using anchor.
	cropX = int(float64(srcW-cropW) * anchor[0])
	cropY = int(float64(srcH-cropH) * anchor[1])

	// Even-align offsets.
	cropX &^= 1
	cropY &^= 1

	return cropX, cropY, cropW, cropH
}

// getOrUploadFill returns a cached GPU fill frame for the given slot,
// uploading from CPU YUV420p if not cached or if dimensions changed.
func (n *gpuLayoutNode) getOrUploadFill(slot *SlotSnapshot) (*GPUFrame, error) {
	dims := [2]int{slot.FillW, slot.FillH}
	cached, ok := n.fills[slot.SourceKey]

	if ok && n.fillDims[slot.SourceKey] == dims {
		// Cache hit — re-upload pixel data (fill content changes every frame).
		if err := Upload(n.ctx, cached, slot.FillYUV, slot.FillW, slot.FillH); err != nil {
			return nil, err
		}
		return cached, nil
	}

	// Cache miss or dimension change — allocate new GPU frame.
	if cached != nil {
		cached.Release()
		delete(n.fills, slot.SourceKey)
		delete(n.fillDims, slot.SourceKey)
	}

	fillFrame, err := n.allocFillFrame(slot.FillW, slot.FillH)
	if err != nil {
		return nil, err
	}

	if err := Upload(n.ctx, fillFrame, slot.FillYUV, slot.FillW, slot.FillH); err != nil {
		fillFrame.Release()
		return nil, err
	}

	n.fills[slot.SourceKey] = fillFrame
	n.fillDims[slot.SourceKey] = dims

	return fillFrame, nil
}

// allocFillFrame allocates a GPU frame for fill content.
// Always uses the pool — pool frames have enough memory even when source
// dimensions differ from program dimensions. Upload uses the actual width/height
// and the pool's pitch, so the extra space is just unused padding. We set the
// frame's Width/Height to the fill dimensions so that downstream operations
// (e.g., PIPComposite) use the correct source dimensions.
func (n *gpuLayoutNode) allocFillFrame(w, h int) (*GPUFrame, error) {
	frame, err := n.pool.Acquire()
	if err != nil {
		return nil, err
	}
	frame.Width = w
	frame.Height = h
	return frame, nil
}
