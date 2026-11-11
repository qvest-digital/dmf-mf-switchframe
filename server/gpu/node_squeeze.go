//go:build (cgo && darwin) || (cgo && cuda)

package gpu

import (
	"log/slog"
	"sync/atomic"
	"time"
)

// gpuSqueezeNode is the GPU squeeze-back: it scales the program into a
// pool frame, draws the background over the whole frame and composites
// the scaled program into its rect. It sits after gpu_layout and before
// gpu_compositor, so keys and PiPs shrink with the program and graphics
// stay full frame.
type gpuSqueezeNode struct {
	ctx     *Context
	pool    *FramePool
	state   SqueezeState
	lastErr atomic.Value
}

// NewGPUSqueezeNode creates a GPU squeeze-back pipeline node.
func NewGPUSqueezeNode(ctx *Context, pool *FramePool, state SqueezeState) GPUPipelineNode {
	return &gpuSqueezeNode{ctx: ctx, pool: pool, state: state}
}

func (n *gpuSqueezeNode) Name() string                             { return "gpu_squeezeback" }
func (n *gpuSqueezeNode) Configure(width, height, pitch int) error { return nil }
func (n *gpuSqueezeNode) Active() bool                             { return n.state != nil }
func (n *gpuSqueezeNode) Latency() time.Duration                   { return 500 * time.Microsecond }
func (n *gpuSqueezeNode) Close() error                             { return nil }

func (n *gpuSqueezeNode) Err() error {
	if v := n.lastErr.Load(); v != nil {
		return v.(error)
	}
	return nil
}

// ProcessGPU returns at once while the program fills the frame.
func (n *gpuSqueezeNode) ProcessGPU(frame *GPUFrame) error {
	rect, background, ok := n.state.Squeeze(frame.Width, frame.Height)
	if !ok {
		return nil
	}
	scaled, err := n.pool.Acquire()
	if err != nil {
		slog.Warn("gpu_squeezeback: pool acquire failed, skipping", "error", err)
		n.lastErr.Store(err)
		return nil
	}
	defer scaled.Release()
	scaled.Width, scaled.Height = rect.W, rect.H
	if err := GPUScaleDispatch(n.ctx, scaled, frame); err != nil {
		n.lastErr.Store(err)
		return err
	}
	if err := n.drawBackground(frame, background); err != nil {
		n.lastErr.Store(err)
		return err
	}
	if err := GPUPIPCompositeDispatch(n.ctx, frame, scaled, rect, 1.0); err != nil {
		n.lastErr.Store(err)
		return err
	}
	return nil
}

// drawBackground fills frame with the background source scaled to it, or
// black when there is none or it has no frame yet.
func (n *gpuSqueezeNode) drawBackground(frame *GPUFrame, background string) error {
	if background != "" {
		if fill := n.state.GPUFill(background); fill != nil {
			defer fill.Release()
			return GPUScaleDispatch(n.ctx, frame, fill)
		}
	}
	return GPUFillBlackDispatch(n.ctx, frame)
}
