//go:build darwin

package gpu

/*
#include "metal_bridge.h"
#include <string.h>
*/
import "C"

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
	return nil
}

func (n *gpuSTMapNode) ProcessGPU(frame *GPUFrame) error {
	if n.registry == nil || !n.registry.HasProgramMap() {
		return nil
	}

	// Determine the GPU ST map to use for this frame.
	var gpuMap *GPUSTMap
	var freeAfter bool

	if n.registry.IsAnimated() {
		// Animated maps change every frame — upload fresh S/T arrays each time.
		idx := n.registry.AdvanceAnimatedIndex()
		sOrig, tOrig := n.registry.AnimatedSTArraysAt(idx)
		if sOrig == nil || tOrig == nil {
			return nil
		}
		// Deep copy to prevent aliasing with registry's internal arrays.
		s := make([]float32, len(sOrig))
		t := make([]float32, len(tOrig))
		copy(s, sOrig)
		copy(t, tOrig)
		m, err := UploadSTMap(n.ctx, s, t, frame.Width, frame.Height)
		if err != nil {
			n.lastErr.Store(err)
			slog.Warn("gpu_stmap: animated upload failed, skipping", "error", err)
			return nil
		}
		gpuMap = m
		freeAfter = true // per-frame upload must be freed after use
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
			// Deep copy to prevent aliasing with registry's internal arrays.
			s := make([]float32, len(sOrig))
			t := make([]float32, len(tOrig))
			copy(s, sOrig)
			copy(t, tOrig)
			m, err := UploadSTMap(n.ctx, s, t, frame.Width, frame.Height)
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
		if freeAfter {
			gpuMap.Free()
		}
		slog.Warn("gpu_stmap: pool acquire failed, skipping", "error", err)
		return nil
	}
	tempFrame.PTS = frame.PTS

	if err := STMapWarp(n.ctx, tempFrame, frame, gpuMap); err != nil {
		n.lastErr.Store(err)
		tempFrame.Release()
		if freeAfter {
			gpuMap.Free()
		}
		slog.Warn("gpu_stmap: warp failed, skipping", "error", err)
		return nil
	}

	// Copy warped result back to the original frame via unified memory memcpy.
	copyGPUFrameNV12(frame, tempFrame)

	tempFrame.Release()
	if freeAfter {
		gpuMap.Free()
	}

	return nil
}

// copyGPUFrameNV12 copies NV12 data from src to dst using unified memory.
// Both frames must have the same dimensions and pitch. On Apple Silicon,
// contentsPtr() returns a CPU-accessible pointer to the Metal buffer's
// unified memory.
func copyGPUFrameNV12(dst, src *GPUFrame) {
	if dst.Pitch != src.Pitch || dst.Height != src.Height {
		slog.Error("copyGPUFrameNV12: dimension mismatch",
			"dst", fmt.Sprintf("%dx%d p=%d", dst.Width, dst.Height, dst.Pitch),
			"src", fmt.Sprintf("%dx%d p=%d", src.Width, src.Height, src.Pitch))
		return
	}
	size := C.size_t(src.Pitch * src.Height * 3 / 2)
	C.memcpy(dst.contentsPtr(), src.contentsPtr(), size)
}
