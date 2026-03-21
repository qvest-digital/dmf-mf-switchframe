//go:build !cgo || !cuda || !tensorrt

package gpu

// NewGPUAISegmentNode returns nil on non-TensorRT builds.
// The pipeline filters nil nodes, so this is safely excluded from the chain.
func NewGPUAISegmentNode(ctx *Context, pool *FramePool, state SegmentationState) GPUPipelineNode {
	return nil
}
