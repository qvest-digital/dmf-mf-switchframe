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
	// Populated on first cycle from pre-built mmap'd .st files, reused thereafter.
	animCache   []*GPUSTMap
	animMapName string // for cache invalidation on map change

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

	// Acquire an atomic snapshot of the program map state. The release
	// function holds the registry's RLock, preventing Delete() from
	// munmapping the S/T backing memory while we read it for GPU upload.
	name, isAnimated, idx, sOrig, tOrig, release := n.registry.ProgramGPUSnapshot()
	if name == "" {
		release()
		return nil
	}

	// Determine the GPU ST map to use for this frame.
	var gpuMap *GPUSTMap

	if isAnimated {
		// Invalidate cache if map changed.
		if name != n.animMapName {
			n.freeAnimCache()
			n.animMapName = name
		}

		// Check GPU texture cache.
		if idx >= 0 && idx < len(n.animCache) && n.animCache[idx] != nil {
			gpuMap = n.animCache[idx]
			release() // S/T arrays not needed — cached GPU texture
		} else {
			// Cache miss — read from pre-built .st files and upload to GPU.
			if sOrig == nil || tOrig == nil {
				release()
				return nil // .st files not available — skip
			}
			m, err := UploadSTMap(n.ctx, sOrig, tOrig, frame.Width, frame.Height)
			release() // S/T arrays copied to GPU — safe to release
			if err != nil {
				n.lastErr.Store(err)
				slog.Warn("gpu_stmap: animated upload failed, skipping", "error", err)
				return nil
			}
			for len(n.animCache) <= idx {
				n.animCache = append(n.animCache, nil)
			}
			n.animCache[idx] = m
			gpuMap = m
		}
	} else {
		// Static map — check cache.
		if n.gpuMap == nil || n.mapName != name {
			// Cache miss — upload new S/T arrays.
			if n.gpuMap != nil {
				n.gpuMap.Free()
				n.gpuMap = nil
			}
			if sOrig == nil || tOrig == nil {
				release()
				return nil
			}
			m, err := UploadSTMap(n.ctx, sOrig, tOrig, frame.Width, frame.Height)
			release() // S/T arrays copied to GPU — safe to release
			if err != nil {
				n.lastErr.Store(err)
				slog.Warn("gpu_stmap: static upload failed, skipping", "error", err)
				return nil
			}
			n.gpuMap = m
			n.mapName = name
		} else {
			release() // S/T arrays not needed — cached GPU texture
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
