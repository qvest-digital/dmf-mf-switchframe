//go:build (!cgo || !cuda) && !darwin

package gpu

// NewGPUKeyNode returns nil on non-GPU builds.
func NewGPUKeyNode(ctx *Context, pool *FramePool, bridge KeyBridge) GPUPipelineNode {
	return nil
}
