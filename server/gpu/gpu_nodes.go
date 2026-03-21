//go:build darwin

package gpu

import (
	"log/slog"
	"time"

	"github.com/zsiec/switchframe/server/stmap"
	"github.com/zsiec/switchframe/server/switcher"
)

// Compile-time interface checks.
var (
	_ switcher.PipelineNode = (*gpuUploadBridgeNode)(nil)
	_ switcher.PipelineNode = (*gpuDownloadBridgeNode)(nil)
	_ switcher.PipelineNode = (*gpuSTMapBridgeNode)(nil)
	_ switcher.PipelineNode = (*gpuKeyBridgeNode)(nil)
	_ switcher.PipelineNode = (*gpuLayoutBridgeNode)(nil)
	_ switcher.PipelineNode = (*gpuCompositorBridgeNode)(nil)
)

// gpuUploadBridgeNode implements switcher.PipelineNode. It uploads a CPU
// YUV420p ProcessingFrame to GPU NV12 and stores the GPUFrame on the
// ProcessingFrame.GPUData field. Subsequent GPU nodes read from GPUData.
type gpuUploadBridgeNode struct {
	ctx  *Context
	pool *FramePool
}

// NewUploadNode creates a GPU upload bridge node that converts CPU YUV420p
// frames to GPU NV12 frames.
func NewUploadNode(ctx *Context, pool *FramePool) switcher.PipelineNode {
	return &gpuUploadBridgeNode{ctx: ctx, pool: pool}
}

func (n *gpuUploadBridgeNode) Name() string                                  { return "gpu-upload" }
func (n *gpuUploadBridgeNode) Configure(format switcher.PipelineFormat) error { return nil }
func (n *gpuUploadBridgeNode) Active() bool                                  { return n.ctx != nil && n.pool != nil }
func (n *gpuUploadBridgeNode) Err() error                                    { return nil }
func (n *gpuUploadBridgeNode) Close() error                                  { return nil }

// Latency returns the estimated upload time. On Apple Silicon with unified
// memory this is just a memcpy + YUV420p→NV12 conversion kernel (~100us).
func (n *gpuUploadBridgeNode) Latency() time.Duration { return 100 * time.Microsecond }

func (n *gpuUploadBridgeNode) Process(dst, src *switcher.ProcessingFrame) *switcher.ProcessingFrame {
	frame, err := n.pool.Acquire()
	if err != nil {
		slog.Warn("gpu-upload: pool acquire failed, falling back to CPU", "error", err)
		return src
	}
	frame.PTS = src.PTS

	if err := Upload(n.ctx, frame, src.YUV, src.Width, src.Height); err != nil {
		slog.Warn("gpu-upload: upload failed, falling back to CPU", "error", err)
		frame.Release()
		return src
	}

	src.GPUData = frame
	return src
}

// gpuDownloadBridgeNode implements switcher.PipelineNode. It downloads the
// GPU NV12 frame (from ProcessingFrame.GPUData) back to CPU YUV420p,
// updating ProcessingFrame.YUV. The GPUFrame is released after download.
type gpuDownloadBridgeNode struct {
	ctx *Context
}

// NewDownloadNode creates a GPU download bridge node that converts GPU NV12
// frames back to CPU YUV420p.
func NewDownloadNode(ctx *Context) switcher.PipelineNode {
	return &gpuDownloadBridgeNode{ctx: ctx}
}

func (n *gpuDownloadBridgeNode) Name() string                                  { return "gpu-download" }
func (n *gpuDownloadBridgeNode) Configure(format switcher.PipelineFormat) error { return nil }
func (n *gpuDownloadBridgeNode) Active() bool                                  { return n.ctx != nil }
func (n *gpuDownloadBridgeNode) Err() error                                    { return nil }
func (n *gpuDownloadBridgeNode) Close() error                                  { return nil }
func (n *gpuDownloadBridgeNode) Latency() time.Duration                        { return 100 * time.Microsecond }

func (n *gpuDownloadBridgeNode) Process(dst, src *switcher.ProcessingFrame) *switcher.ProcessingFrame {
	gpuFrame, ok := src.GPUData.(*GPUFrame)
	if !ok || gpuFrame == nil {
		// No GPU frame — pass through (CPU fallback path)
		return src
	}

	if err := Download(n.ctx, src.YUV, gpuFrame, src.Width, src.Height); err != nil {
		slog.Warn("gpu-download: download failed", "error", err)
	}

	gpuFrame.Release()
	src.GPUData = nil
	return src
}

// gpuSTMapBridgeNode implements switcher.PipelineNode. It applies a program
// ST map warp on the GPU. This is the critical performance win: <1ms GPU
// vs ~29ms CPU for 1080p warps.
type gpuSTMapBridgeNode struct {
	ctx      *Context
	registry *stmap.Registry
	pool     *FramePool

	// Cached GPU ST map — invalidated when the registry's program map changes.
	cachedMap     *GPUSTMap
	cachedMapName string
}

// NewSTMapNode creates a GPU ST map warp pipeline node.
func NewSTMapNode(ctx *Context, pool *FramePool, registry *stmap.Registry) switcher.PipelineNode {
	return &gpuSTMapBridgeNode{
		ctx:      ctx,
		pool:     pool,
		registry: registry,
	}
}

func (n *gpuSTMapBridgeNode) Name() string                                  { return "gpu-stmap" }
func (n *gpuSTMapBridgeNode) Configure(format switcher.PipelineFormat) error { return nil }
func (n *gpuSTMapBridgeNode) Active() bool {
	return n.ctx != nil && n.registry != nil && n.registry.HasProgramMap()
}
func (n *gpuSTMapBridgeNode) Err() error             { return nil }
func (n *gpuSTMapBridgeNode) Latency() time.Duration { return 500 * time.Microsecond }
func (n *gpuSTMapBridgeNode) Close() error {
	if n.cachedMap != nil {
		n.cachedMap.Free()
		n.cachedMap = nil
	}
	return nil
}

func (n *gpuSTMapBridgeNode) Process(dst, src *switcher.ProcessingFrame) *switcher.ProcessingFrame {
	gpuFrame, ok := src.GPUData.(*GPUFrame)
	if !ok || gpuFrame == nil {
		// No GPU frame — fall through (will be handled by CPU node if present)
		return src
	}

	// Get the current program processor to access S/T arrays.
	proc := n.registry.ProgramProcessor()
	if proc == nil || !proc.Active() {
		return src
	}

	// Check if the cached GPU map matches the current program map.
	mapName := proc.Name()
	if n.cachedMap == nil || n.cachedMapName != mapName {
		// Cache miss — upload ST map to GPU.
		if n.cachedMap != nil {
			n.cachedMap.Free()
		}
		s, t := proc.STArrays()
		if s == nil || t == nil {
			return src
		}
		m, err := UploadSTMap(n.ctx, s, t, src.Width, src.Height)
		if err != nil {
			slog.Warn("gpu-stmap: upload failed, skipping", "error", err)
			return src
		}
		n.cachedMap = m
		n.cachedMapName = mapName
	}

	// Acquire a destination frame for the warp output.
	dstFrame, err := n.pool.Acquire()
	if err != nil {
		slog.Warn("gpu-stmap: pool acquire failed, skipping", "error", err)
		return src
	}
	dstFrame.PTS = gpuFrame.PTS

	if err := STMapWarp(n.ctx, dstFrame, gpuFrame, n.cachedMap); err != nil {
		slog.Warn("gpu-stmap: warp failed, skipping", "error", err)
		dstFrame.Release()
		return src
	}

	// Replace the GPU frame on the ProcessingFrame.
	gpuFrame.Release()
	src.GPUData = dstFrame
	return src
}

// gpuKeyBridgeNode is a placeholder for GPU upstream keying.
// Currently passes through — the CPU fallback handles keying when GPU data
// is nil (before upload) or this is a future expansion point.
type gpuKeyBridgeNode struct {
	active bool
}

// NewKeyNode creates a GPU upstream key bridge node (currently passthrough).
func NewKeyNode() switcher.PipelineNode {
	return &gpuKeyBridgeNode{active: false}
}

func (n *gpuKeyBridgeNode) Name() string                                  { return "gpu-key" }
func (n *gpuKeyBridgeNode) Configure(format switcher.PipelineFormat) error { return nil }
func (n *gpuKeyBridgeNode) Active() bool                                  { return n.active }
func (n *gpuKeyBridgeNode) Err() error                                    { return nil }
func (n *gpuKeyBridgeNode) Latency() time.Duration                        { return 0 }
func (n *gpuKeyBridgeNode) Close() error                                  { return nil }

func (n *gpuKeyBridgeNode) Process(dst, src *switcher.ProcessingFrame) *switcher.ProcessingFrame {
	return src
}

// gpuLayoutBridgeNode is a placeholder for GPU PIP/layout compositing.
type gpuLayoutBridgeNode struct {
	active bool
}

// NewLayoutNode creates a GPU layout compositor bridge node (currently passthrough).
func NewLayoutNode() switcher.PipelineNode {
	return &gpuLayoutBridgeNode{active: false}
}

func (n *gpuLayoutBridgeNode) Name() string                                  { return "gpu-layout" }
func (n *gpuLayoutBridgeNode) Configure(format switcher.PipelineFormat) error { return nil }
func (n *gpuLayoutBridgeNode) Active() bool                                  { return n.active }
func (n *gpuLayoutBridgeNode) Err() error                                    { return nil }
func (n *gpuLayoutBridgeNode) Latency() time.Duration                        { return 0 }
func (n *gpuLayoutBridgeNode) Close() error                                  { return nil }

func (n *gpuLayoutBridgeNode) Process(dst, src *switcher.ProcessingFrame) *switcher.ProcessingFrame {
	return src
}

// gpuCompositorBridgeNode is a placeholder for GPU DSK graphics compositing.
type gpuCompositorBridgeNode struct {
	active bool
}

// NewCompositorNode creates a GPU DSK compositor bridge node (currently passthrough).
func NewCompositorNode() switcher.PipelineNode {
	return &gpuCompositorBridgeNode{active: false}
}

func (n *gpuCompositorBridgeNode) Name() string                                  { return "gpu-compositor" }
func (n *gpuCompositorBridgeNode) Configure(format switcher.PipelineFormat) error { return nil }
func (n *gpuCompositorBridgeNode) Active() bool                                  { return n.active }
func (n *gpuCompositorBridgeNode) Err() error                                    { return nil }
func (n *gpuCompositorBridgeNode) Latency() time.Duration                        { return 0 }
func (n *gpuCompositorBridgeNode) Close() error                                  { return nil }

func (n *gpuCompositorBridgeNode) Process(dst, src *switcher.ProcessingFrame) *switcher.ProcessingFrame {
	return src
}
