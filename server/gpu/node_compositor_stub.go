//go:build (!cgo || !cuda) && !darwin

package gpu

// NewGPUCompositorNode returns nil on non-GPU builds.
func NewGPUCompositorNode(ctx *Context, compositor CompositorState) GPUPipelineNode {
	return nil
}
