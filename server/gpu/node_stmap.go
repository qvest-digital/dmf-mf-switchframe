//go:build (cgo && darwin) || (cgo && cuda)

package gpu

import (
	"fmt"
	"log/slog"
	"sync/atomic"
	"time"
)

// gpuSTMapNode implements GPUPipelineNode for ST map warping.
// It reads stmap.Registry state via the STMapState interface and dispatches
// STMapWarp GPU kernels. For animated maps, a new GPUSTMap is uploaded each
// frame. For static maps, the GPUSTMap is cached and only re-uploaded when
// the map name changes.
//
// Because STMapWarp requires separate src and dst buffers but ProcessGPU
// modifies the frame in-place, this node acquires a temporary frame from
// the pool, warps into it, then copies the result back via unified memory
// memcpy and releases the temporary frame.
type gpuSTMapNode struct {
	ctx      *Context
	pool     *FramePool
	registry STMapState

	// Cached GPU ST map for static (non-animated) maps.
	gpuMap  *GPUSTMap
	mapName string // for cache invalidation

	// Cached GPU ST maps for animated maps, keyed by frame index.
	// Populated on first cycle (90 frames = 90 uploads), reused thereafter.
	animCache    []*GPUSTMap
	animMapName  string // for cache invalidation on map change
	animPopulated bool  // true once all frames have been cached

	width, height, pitch int
	lastErr              atomic.Value
}

// NewGPUSTMapNode creates a GPU ST map warp pipeline node.
// The registry provides program map state; pass nil to disable.
func NewGPUSTMapNode(ctx *Context, pool *FramePool, registry STMapState) GPUPipelineNode {
	return &gpuSTMapNode{
		ctx:      ctx,
		pool:     pool,
		registry: registry,
	}
}

func (n *gpuSTMapNode) Name() string { return "gpu_stmap" }

func (n *gpuSTMapNode) Configure(width, height, pitch int) error {
	n.width = width
	n.height = height
	n.pitch = pitch
	return nil
}

func (n *gpuSTMapNode) Active() bool {
	return n.registry != nil && n.registry.HasProgramMap()
}

func (n *gpuSTMapNode) Err() error {
	if v := n.lastErr.Load(); v != nil {
		return v.(error)
	}
	return nil
}

func (n *gpuSTMapNode) Latency() time.Duration { return 500 * time.Microsecond }

func (n *gpuSTMapNode) Close() error {
	if n.gpuMap != nil {
		n.gpuMap.Free()
		n.gpuMap = nil
		n.mapName = ""
	}
	n.freeAnimCache()
	return nil
}

// freeAnimCache releases all cached GPU ST maps for animated frames.
func (n *gpuSTMapNode) freeAnimCache() {
	for i, m := range n.animCache {
		if m != nil {
			m.Free()
			n.animCache[i] = nil
		}
	}
	n.animCache = nil
	n.animMapName = ""
}

func (n *gpuSTMapNode) ProcessGPU(frame *GPUFrame) error {
	if n.registry == nil || !n.registry.HasProgramMap() {
		return nil
	}

	// Determine the GPU ST map to use for this frame.
	var gpuMap *GPUSTMap

	if n.registry.IsAnimated() {
		// Animated maps cycle through N frames. Cache GPU textures per index
		// so we only upload once per unique frame (not every pipeline tick).
		name := n.registry.ProgramMapName()
		idx := n.registry.AdvanceAnimatedIndex()

		// Invalidate cache if map changed.
		if name != n.animMapName {
			n.freeAnimCache()
			n.animMapName = name
		}

		// Check cache first.
		if idx >= 0 && idx < len(n.animCache) && n.animCache[idx] != nil {
			gpuMap = n.animCache[idx]
		} else {
			// Cache miss — generate S/T arrays and upload to GPU.
			sOrig, tOrig := n.registry.AnimatedSTArraysAt(idx)
			if sOrig == nil || tOrig == nil {
				return nil
			}
			m, err := UploadSTMap(n.ctx, sOrig, tOrig, frame.Width, frame.Height)
			if err != nil {
				n.lastErr.Store(err)
				slog.Warn("gpu_stmap: animated upload failed, skipping", "error", err)
				return nil
			}
			// Grow cache slice if needed.
			for len(n.animCache) <= idx {
				n.animCache = append(n.animCache, nil)
			}
			n.animCache[idx] = m
			gpuMap = m
		}
		// Do NOT free after use — cached for reuse.
	} else {
		// Static map — check cache.
		name := n.registry.ProgramMapName()
		if n.gpuMap == nil || n.mapName != name {
			// Cache miss — upload new S/T arrays.
			if n.gpuMap != nil {
				n.gpuMap.Free()
				n.gpuMap = nil
			}
			sOrig, tOrig := n.registry.ProgramSTArrays()
			if sOrig == nil || tOrig == nil {
				return nil
			}
			// UploadSTMap only reads S/T to copy to GPU — no deep copy needed.
			m, err := UploadSTMap(n.ctx, sOrig, tOrig, frame.Width, frame.Height)
			if err != nil {
				n.lastErr.Store(err)
				slog.Warn("gpu_stmap: static upload failed, skipping", "error", err)
				return nil
			}
			n.gpuMap = m
			n.mapName = name
		}
		gpuMap = n.gpuMap
	}

	// STMapWarp needs separate src and dst buffers. Acquire a temp frame,
	// warp into it, then copy the result back into the original frame.
	tempFrame, err := n.pool.Acquire()
	if err != nil {
		slog.Warn("gpu_stmap: pool acquire failed, skipping", "error", err)
		return nil
	}
	tempFrame.PTS = frame.PTS

	if err := STMapWarp(n.ctx, tempFrame, frame, gpuMap); err != nil {
		n.lastErr.Store(err)
		tempFrame.Release()
		slog.Warn("gpu_stmap: warp failed, skipping", "error", err)
		return nil
	}

	// Copy warped result back to the original frame.
	if err := CopyGPUFrame(frame, tempFrame); err != nil {
		tempFrame.Release()
		return fmt.Errorf("gpu stmap: copy back failed: %w", err)
	}

	tempFrame.Release()
	return nil
}
