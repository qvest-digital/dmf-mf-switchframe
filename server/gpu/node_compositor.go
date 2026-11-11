//go:build (cgo && darwin) || (cgo && cuda)

package gpu

import (
	"errors"
	"fmt"
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
	liveWarned map[int]bool        // live layers whose failure was logged
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
		liveWarned: make(map[int]bool),
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
		if layer.LiveFill != nil {
			if err := n.compositeLive(frame, layer); err != nil {
				n.lastErr.Store(err)
				if !n.liveWarned[layer.ID] {
					n.liveWarned[layer.ID] = true
					slog.Warn("gpu_dsk: live layer skipped", "layer", layer.ID, "error", err)
				}
			} else {
				delete(n.liveWarned, layer.ID)
			}
			continue
		}
		n.visibleSet[layer.ID] = true

		// Check overlay cache — upload or re-upload if generation changed.
		// When layer.Overlay is nil, the compositor signals "unchanged since
		// last snapshot" — reuse the cached GPU overlay (same gen = same data).
		cached, hasCached := n.overlays[layer.ID]
		cachedGen := n.overlayGen[layer.ID]

		if hasCached && cachedGen == layer.Gen && cached != nil {
			// Cache hit — overlay unchanged, skip upload entirely.
		} else if layer.Overlay != nil {
			// Cache miss or gen changed — upload new RGBA overlay.
			if cached != nil {
				FreeOverlay(cached)
			}
			overlay, err := UploadOverlay(n.ctx, layer.Overlay, layer.OverlayW, layer.OverlayH)
			if err != nil {
				slog.Warn("gpu_dsk: overlay upload failed",
					"layer", layer.ID, "error", err)
				n.lastErr.Store(err)
				delete(n.overlays, layer.ID)
				delete(n.overlayGen, layer.ID)
				continue
			}
			n.overlays[layer.ID] = overlay
			n.overlayGen[layer.ID] = layer.Gen
			cached = overlay
		} else if !hasCached || cached == nil {
			// Overlay is nil AND no cache — can't composite, skip.
			continue
		}

		alpha := float64(layer.Alpha)

		// Check if this is a full-frame overlay (rect covers entire frame
		// and overlay dimensions match the frame).
		isFullFrame := layer.Rect.X == 0 && layer.Rect.Y == 0 &&
			layer.Rect.W == frame.Width && layer.Rect.H == frame.Height &&
			cached.Width == frame.Width && cached.Height == frame.Height

		var err error
		if frame.Format == GPUFormatP210 {
			// P210 (10-bit) path: use P210-aware DSK overlay kernel.
			if isFullFrame {
				err = DSKCompositeFullFrameP210(n.ctx, frame, cached, alpha)
			} else {
				err = DSKCompositeRectP210(n.ctx, frame, cached, layer.Rect, alpha)
			}
		} else {
			// NV12 standard path.
			if isFullFrame {
				err = DSKCompositeFullFrame(n.ctx, frame, cached, alpha)
			} else {
				err = DSKCompositeRect(n.ctx, frame, cached, layer.Rect, alpha)
			}
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

// errLivePremultiplied: no GPU kernel adds a shaped fill; the live guard
// refuses one before it gets here.
var errLivePremultiplied = errors.New("gpu_dsk: premultiplied live fill not supported")

// compositeLive keys a live fill onto frame by its key's luma with the
// kernels the upstream keyer uses: both pictures are uploaded, the key
// becomes a mask through the layer's LUT, and the fill is blended by it.
func (n *gpuCompositorNode) compositeLive(frame *GPUFrame, layer *VisibleLayerSnapshot) error {
	if layer.Premultiplied {
		return errLivePremultiplied
	}
	if layer.LiveW != frame.Width || layer.LiveH != frame.Height || layer.LiveTenBit != (frame.Format == GPUFormatP210) {
		return fmt.Errorf("gpu_dsk: live fill and key %dx%d (10-bit %v) do not match the program %dx%d",
			layer.LiveW, layer.LiveH, layer.LiveTenBit, frame.Width, frame.Height)
	}
	pool := n.ctx.Pool()
	if pool == nil {
		return ErrGPUNotAvailable
	}
	var frames [3]*GPUFrame // fill, key, mask
	for i := range frames {
		f, err := pool.Acquire()
		if err != nil {
			return err
		}
		defer f.Release()
		frames[i] = f
	}
	fill, key, mask := frames[0], frames[1], frames[2]
	if err := GPUUploadDispatch(n.ctx, fill, layer.LiveFill, layer.LiveW, layer.LiveH); err != nil {
		return err
	}
	if err := GPUUploadDispatch(n.ctx, key, layer.LiveKey, layer.LiveW, layer.LiveH); err != nil {
		return err
	}
	if err := GPULumaKeyDispatch(n.ctx, key, mask, layer.LiveLUT); err != nil {
		return err
	}
	return GPUBlendStingerDispatch(n.ctx, frame, frame, fill, mask)
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
