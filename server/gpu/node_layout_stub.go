//go:build (!cgo || !cuda) && !darwin

package gpu

// NewGPULayoutNode returns nil on non-GPU builds.
// The stub constructors are compiled but never called at runtime because
// initGPU returns nil, so wireGPUPipeline (which creates nodes) is skipped.
func NewGPULayoutNode(ctx *Context, pool *FramePool, layout LayoutState) GPUPipelineNode {
	return nil
}
