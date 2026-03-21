package main

import (
	"log/slog"

	"github.com/zsiec/switchframe/server/gpu"
	"github.com/zsiec/switchframe/server/switcher"
)

// gpuState holds all GPU subsystem resources. Nil fields mean the GPU
// subsystem is not active and the CPU pipeline is used instead.
type gpuState struct {
	ctx  *gpu.Context
	pool *gpu.FramePool
}

// initGPU attempts to initialize the GPU pipeline. If the GPU is not
// available or initialization fails, logs a message and returns nil
// (CPU fallback). The caller should store the returned state and call
// closeGPU in cleanup.
func initGPU(sw *switcher.Switcher, stmapRegistry interface{ HasProgramMap() bool }) *gpuState {
	ctx, err := gpu.NewContext()
	if err != nil {
		slog.Info("GPU not available, using CPU pipeline", "error", err)
		return nil
	}

	slog.Info("GPU detected",
		"backend", ctx.Backend(),
		"device", ctx.DeviceName(),
	)

	// Create GPU frame pool sized for the pipeline format.
	pf := sw.PipelineFormat()
	pool, err := gpu.NewFramePool(ctx, pf.Width, pf.Height, 8)
	if err != nil {
		slog.Warn("GPU frame pool failed, falling back to CPU", "error", err)
		ctx.Close()
		return nil
	}
	ctx.SetPool(pool)

	return &gpuState{ctx: ctx, pool: pool}
}

// wireGPUPipeline creates GPU pipeline nodes and registers them on the
// switcher. Call after initGPU returns a non-nil state, and before
// sw.BuildPipeline().
//
// The GPU path only accelerates the ST map warp (upload → stmap → download).
// CPU nodes (upstream key, layout compositor, DSK compositor) run before
// GPU upload and operate on YUV420p data as usual.
func wireGPUPipeline(gs *gpuState, sw *switcher.Switcher, app *App) {
	if gs == nil {
		return
	}

	// Build GPU-accelerated node chain: upload → stmap → download.
	// The switcher prepends CPU processing nodes (key, layout, compositor)
	// and appends raw-sinks + encode around this chain.
	nodes := []switcher.PipelineNode{
		gpu.NewUploadNode(gs.ctx, gs.pool),
		gpu.NewSTMapNode(gs.ctx, gs.pool, app.stmapRegistry),
		gpu.NewDownloadNode(gs.ctx),
	}

	sw.SetGPUNodes(nodes)

	slog.Info("GPU pipeline active",
		"backend", gs.ctx.Backend(),
		"device", gs.ctx.DeviceName(),
		"pool_frames", 8,
	)
}

// closeGPU releases all GPU resources.
func closeGPU(gs *gpuState) {
	if gs == nil {
		return
	}
	if gs.pool != nil {
		gs.pool.Close()
	}
	if gs.ctx != nil {
		gs.ctx.Close()
	}
}
