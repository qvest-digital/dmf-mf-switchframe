//go:build darwin

package gpu

import (
	"log/slog"
	"sync/atomic"
	"time"
)

// gpuCompositorNode applies DSK graphics overlays on the GPU.
// It reads layer state from a CompositorState interface and dispatches
// DSKCompositeRect/DSKCompositeFullFrame GPU kernels per visible layer.
// Per-layer RGBA overlays are cached on the GPU and invalidated by
// generation counter — avoiding re-upload every frame.
type gpuCompositorNode struct {
	ctx        *Context
	compositor CompositorState
	overlays   map[int]*GPUOverlay // per-layer cached RGBA on GPU
	overlayGen map[int]uint64      // per-layer generation for invalidation
	visibleSet map[int]bool        // reusable set for cache eviction (avoids per-frame alloc)
	lastErr    atomic.Value
}

// NewGPUCompositorNode creates a GPU DSK compositor pipeline node.
// The compositor parameter provides snapshots of visible graphics layers.
func NewGPUCompositorNode(ctx *Context, compositor CompositorState) GPUPipelineNode {
	return &gpuCompositorNode{
		ctx:        ctx,
		compositor: compositor,
		overlays:   make(map[int]*GPUOverlay),
		overlayGen: make(map[int]uint64),
		visibleSet: make(map[int]bool, 8),
	}
}

func (n *gpuCompositorNode) Name() string                             { return "gpu_dsk" }
func (n *gpuCompositorNode) Configure(width, height, pitch int) error { return nil }

// Active returns true when a compositor is attached. The HasActiveLayers()
// check is a fast-path optimization inside ProcessGPU — we keep the node
// active so it participates in pipeline timing/metrics even when idle.
func (n *gpuCompositorNode) Active() bool {
	return n.compositor != nil
}

func (n *gpuCompositorNode) Latency() time.Duration { return 200 * time.Microsecond }

func (n *gpuCompositorNode) Err() error {
	if v := n.lastErr.Load(); v != nil {
		return v.(error)
	}
	return nil
}

func (n *gpuCompositorNode) ProcessGPU(frame *GPUFrame) error {
	if n.compositor == nil {
		return nil
	}

	// Fast path: no visible layers — skip entirely.
	if !n.compositor.HasActiveLayers() {
		return nil
	}

	layers := n.compositor.SnapshotVisibleLayers()
	if len(layers) == 0 {
		return nil
	}

	// Clear and reuse the visible set instead of allocating a new map per frame.
	for k := range n.visibleSet {
		delete(n.visibleSet, k)
	}

	// Process each layer in z-order (SnapshotVisibleLayers returns sorted).
	for i := range layers {
		layer := &layers[i]
		n.visibleSet[layer.ID] = true

		// Check overlay cache — upload or re-upload if generation changed.
		cached, hasCached := n.overlays[layer.ID]
		cachedGen := n.overlayGen[layer.ID]

		if !hasCached || cachedGen != layer.Gen || cached == nil {
			// Free old overlay if present.
			if cached != nil {
				FreeOverlay(cached)
			}

			// Upload new RGBA overlay to GPU.
			overlay, err := UploadOverlay(n.ctx, layer.Overlay, layer.OverlayW, layer.OverlayH)
			if err != nil {
				slog.Warn("gpu_dsk: overlay upload failed",
					"layer", layer.ID, "error", err)
				n.lastErr.Store(err)
				// Remove stale cache entry.
				delete(n.overlays, layer.ID)
				delete(n.overlayGen, layer.ID)
				continue
			}
			n.overlays[layer.ID] = overlay
			n.overlayGen[layer.ID] = layer.Gen
			cached = overlay
		}

		alpha := float64(layer.Alpha)

		// Check if this is a full-frame overlay (rect covers entire frame
		// and overlay dimensions match the frame).
		isFullFrame := layer.Rect.X == 0 && layer.Rect.Y == 0 &&
			layer.Rect.W == frame.Width && layer.Rect.H == frame.Height &&
			cached.Width == frame.Width && cached.Height == frame.Height

		var err error
		if isFullFrame {
			err = DSKCompositeFullFrame(n.ctx, frame, cached, alpha)
		} else {
			err = DSKCompositeRect(n.ctx, frame, cached, layer.Rect, alpha)
		}
		if err != nil {
			slog.Warn("gpu_dsk: composite failed",
				"layer", layer.ID, "fullFrame", isFullFrame, "error", err)
			n.lastErr.Store(err)
			// Continue processing remaining layers — partial overlay is
			// better than no overlay.
		}
	}

	// Evict cached overlays for layers that are no longer visible.
	for id, overlay := range n.overlays {
		if !n.visibleSet[id] {
			FreeOverlay(overlay)
			delete(n.overlays, id)
			delete(n.overlayGen, id)
		}
	}

	return nil
}

func (n *gpuCompositorNode) Close() error {
	// Free all cached GPU overlays.
	for id, overlay := range n.overlays {
		FreeOverlay(overlay)
		delete(n.overlays, id)
		delete(n.overlayGen, id)
	}
	return nil
}
