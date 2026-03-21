//go:build (!cgo || !cuda) && !darwin

package gpu

// NewGPUSTMapNode returns nil on non-GPU builds.
// The stub constructors are compiled but never called at runtime because
// initGPU returns nil, so wireGPUPipeline (which creates nodes) is skipped.
func NewGPUSTMapNode(ctx *Context, pool *FramePool, registry STMapState) GPUPipelineNode {
	return nil
}
