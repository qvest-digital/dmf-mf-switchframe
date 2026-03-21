//go:build darwin || (cgo && cuda)

package gpu

import (
	"log/slog"
	"sync/atomic"
	"time"
)

// gpuKeyNode applies upstream chroma/luma keying on the GPU.
// It reads key processor state via the KeyBridge interface, uploads
// fill frames, generates alpha masks, and composites fills onto
// the program frame using per-pixel alpha blending.
type gpuKeyNode struct {
	ctx    *Context
	pool   *FramePool
	bridge KeyBridge

	// Per-source cached fill frames (GPU NV12).
	// Keyed by source key string; replaced when dimensions change.
	fills map[string]*cachedFill

	// Reusable mask workspace (same dimensions as program frame).
	maskBuf *GPUFrame

	width, height int
	lastErr       atomic.Value
}

// cachedFill holds a GPU-resident fill frame and the dimensions
// it was uploaded at. When the source dimensions change, the old
// frame is released and a new one is uploaded.
type cachedFill struct {
	frame *GPUFrame
	w, h  int
}

// NewGPUKeyNode creates a GPU upstream key pipeline node.
func NewGPUKeyNode(ctx *Context, pool *FramePool, bridge KeyBridge) GPUPipelineNode {
	return &gpuKeyNode{
		ctx:    ctx,
		pool:   pool,
		bridge: bridge,
		fills:  make(map[string]*cachedFill),
	}
}

func (n *gpuKeyNode) Name() string { return "gpu_key" }

func (n *gpuKeyNode) Configure(width, height, pitch int) error {
	n.width = width
	n.height = height

	// Allocate or reallocate mask buffer.
	if n.maskBuf != nil {
		n.maskBuf.Release()
		n.maskBuf = nil
	}
	mask, err := n.pool.Acquire()
	if err != nil {
		return err
	}
	n.maskBuf = mask

	// Invalidate cached fills (dimensions may have changed).
	for k, cf := range n.fills {
		cf.frame.Release()
		delete(n.fills, k)
	}

	return nil
}

func (n *gpuKeyNode) Active() bool {
	return n.bridge != nil && n.bridge.HasEnabledKeys()
}

func (n *gpuKeyNode) Latency() time.Duration { return 500 * time.Microsecond }

func (n *gpuKeyNode) Err() error {
	if v := n.lastErr.Load(); v != nil {
		return v.(error)
	}
	return nil
}

func (n *gpuKeyNode) Close() error {
	if n.maskBuf != nil {
		n.maskBuf.Release()
		n.maskBuf = nil
	}
	for k, cf := range n.fills {
		cf.frame.Release()
		delete(n.fills, k)
	}
	return nil
}

// ProcessGPU applies all enabled upstream keys to the program frame.
// For each key:
//  1. Upload the fill YUV420p to GPU (cached per source, invalidated on dimension change).
//  2. Scale fill to frame dimensions if needed.
//  3. Generate an alpha mask (ChromaKey or LumaKey).
//  4. Composite the fill onto the program frame using BlendStinger.
func (n *gpuKeyNode) ProcessGPU(frame *GPUFrame) error {
	keys := n.bridge.SnapshotEnabledKeys()
	if len(keys) == 0 {
		return nil
	}

	for i := range keys {
		if err := n.processKey(frame, &keys[i]); err != nil {
			slog.Warn("gpu_key: key processing failed, skipping",
				"source", keys[i].SourceKey, "error", err)
			n.lastErr.Store(err)
			// Continue with remaining keys — don't fail the whole pipeline.
		}
	}

	return nil
}

// processKey handles a single upstream key.
func (n *gpuKeyNode) processKey(frame *GPUFrame, key *EnabledKeySnapshot) error {
	// Try GPU cache first (already NV12 in VRAM, no upload needed).
	fillGPU := n.bridge.GPUFill(key.SourceKey)
	if fillGPU != nil {
		defer fillGPU.Release()
		return n.applyKey(frame, fillGPU, key)
	}

	// CPU fallback: upload from snapshot.
	if len(key.FillYUV) == 0 || key.FillW == 0 || key.FillH == 0 {
		return nil
	}

	uploaded, err := n.getOrUploadFill(key)
	if err != nil {
		return err
	}
	return n.applyKey(frame, uploaded, key)
}

// applyKey generates the alpha mask and composites the fill onto the program frame.
func (n *gpuKeyNode) applyKey(frame, fillGPU *GPUFrame, key *EnabledKeySnapshot) error {
	// Scale fill to frame dimensions if needed.
	// If we scale, we own the scaled frame and must release it after use.
	needsScale := fillGPU.Width != frame.Width || fillGPU.Height != frame.Height
	if needsScale {
		scaled, scaleErr := n.pool.Acquire()
		if scaleErr != nil {
			return scaleErr
		}
		scaled.PTS = frame.PTS

		if scaleErr = ScaleBilinear(n.ctx, scaled, fillGPU); scaleErr != nil {
			scaled.Release()
			return scaleErr
		}
		fillGPU = scaled
		defer scaled.Release()
	}

	// Generate alpha mask.
	switch key.Type {
	case "chroma":
		cfg := ChromaKeyConfig{
			KeyCb:          key.KeyCb,
			KeyCr:          key.KeyCr,
			Similarity:     key.Similarity,
			Smoothness:     key.Smoothness,
			SpillSuppress:  key.SpillSuppress,
			SpillReplaceCb: key.SpillReplaceCb,
			SpillReplaceCr: key.SpillReplaceCr,
		}
		if err := ChromaKey(n.ctx, fillGPU, n.maskBuf, cfg); err != nil {
			return err
		}
	case "luma":
		lut := BuildLumaKeyLUT(key.LowClip, key.HighClip, key.Softness)
		if err := LumaKey(n.ctx, fillGPU, n.maskBuf, lut); err != nil {
			return err
		}
	default:
		return nil
	}

	// Composite fill onto program frame using alpha mask.
	// BlendStinger(ctx, dst, base, overlay, alpha) does:
	//   dst[i] = lerp(base[i], overlay[i], alpha[i])
	// Using frame as both dst and base is safe because blend_alpha
	// indexes by unique thread gid — no two threads access the same pixel.
	if err := BlendStinger(n.ctx, frame, frame, fillGPU, n.maskBuf); err != nil {
		return err
	}

	return nil
}

// getOrUploadFill returns a cached GPU fill frame for the given key,
// uploading from CPU YUV420p if not cached or if dimensions changed.
func (n *gpuKeyNode) getOrUploadFill(key *EnabledKeySnapshot) (*GPUFrame, error) {
	cached, ok := n.fills[key.SourceKey]
	if ok && cached.w == key.FillW && cached.h == key.FillH {
		// Cache hit — re-upload pixel data (fill content changes every frame
		// since it's the latest decoded frame from the source).
		if err := Upload(n.ctx, cached.frame, key.FillYUV, key.FillW, key.FillH); err != nil {
			return nil, err
		}
		return cached.frame, nil
	}

	// Cache miss or dimension change — allocate new GPU frame.
	if cached != nil {
		cached.frame.Release()
		delete(n.fills, key.SourceKey)
	}

	// Allocate a fill frame with the fill's native dimensions.
	// We can't always use pool.Acquire() because pool frames are sized to
	// the program dimensions. Use a standalone allocation for mismatched sizes.
	fillFrame, err := n.allocFillFrame(key.FillW, key.FillH)
	if err != nil {
		return nil, err
	}

	if err := Upload(n.ctx, fillFrame, key.FillYUV, key.FillW, key.FillH); err != nil {
		fillFrame.Release()
		return nil, err
	}

	n.fills[key.SourceKey] = &cachedFill{
		frame: fillFrame,
		w:     key.FillW,
		h:     key.FillH,
	}

	return fillFrame, nil
}

// allocFillFrame allocates a GPU frame for fill content.
// Always uses the pool — pool frames have enough memory even when source
// dimensions differ from program dimensions. Upload uses the actual width/height
// and the pool's pitch, so the extra space is just unused padding. We set the
// frame's Width/Height to the fill dimensions so that downstream scaling checks
// (e.g., needsScale in applyKey) work correctly.
func (n *gpuKeyNode) allocFillFrame(w, h int) (*GPUFrame, error) {
	frame, err := n.pool.Acquire()
	if err != nil {
		return nil, err
	}
	frame.Width = w
	frame.Height = h
	return frame, nil
}
