//go:build !cgo || (!cuda && !darwin)

package gpu

// NewGPUSqueezeNode returns nil on non-GPU builds.
func NewGPUSqueezeNode(ctx *Context, pool *FramePool, state SqueezeState) GPUPipelineNode {
	return nil
}
