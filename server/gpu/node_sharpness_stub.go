//go:build !cgo || !cuda

package gpu

// SharpnessState provides sharpness amount to the GPU sharpness node.
type SharpnessState interface {
	SharpnessAmount() float32
}

// NewGPUSharpnessNode returns nil on non-GPU builds.
func NewGPUSharpnessNode(ctx *Context, state SharpnessState) GPUPipelineNode {
	return nil
}
