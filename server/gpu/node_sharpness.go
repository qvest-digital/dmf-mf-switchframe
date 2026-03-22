//go:build cgo && cuda

package gpu

import (
	"time"
)

// SharpnessState provides sharpness amount to the GPU sharpness node.
type SharpnessState interface {
	SharpnessAmount() float32 // 0.0 = off, 1.0 = full sharpening
}

// gpuSharpnessNode implements GPUPipelineNode for unsharp mask sharpening.
// It operates on the Y (luma) plane only — sharpening chroma causes color artifacts.
type gpuSharpnessNode struct {
	ctx   *Context
	state SharpnessState

	width, height, pitch int
}

// NewGPUSharpnessNode creates a GPU sharpness pipeline node.
// Pass nil state to disable.
func NewGPUSharpnessNode(ctx *Context, state SharpnessState) GPUPipelineNode {
	if state == nil {
		return nil
	}
	return &gpuSharpnessNode{
		ctx:   ctx,
		state: state,
	}
}

func (n *gpuSharpnessNode) Name() string { return "gpu_sharpness" }

func (n *gpuSharpnessNode) Configure(width, height, pitch int) error {
	n.width = width
	n.height = height
	n.pitch = pitch
	return nil
}

func (n *gpuSharpnessNode) Active() bool {
	return n.state != nil && n.state.SharpnessAmount() > 0
}

func (n *gpuSharpnessNode) ProcessGPU(frame *GPUFrame) error {
	if n.state == nil {
		return nil
	}
	amount := n.state.SharpnessAmount()
	if amount <= 0 {
		return nil
	}
	return SharpenLuma(n.ctx, frame, amount)
}

func (n *gpuSharpnessNode) Err() error { return nil }

func (n *gpuSharpnessNode) Latency() time.Duration { return 200 * time.Microsecond }

func (n *gpuSharpnessNode) Close() error { return nil }
