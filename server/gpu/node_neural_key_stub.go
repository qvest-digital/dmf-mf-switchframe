//go:build !cgo || !cuda || !tensorrt

package gpu

// NewGPUNeuralKeyNode returns nil on non-TensorRT builds.
// The pipeline filters nil nodes, so this is safely excluded from the chain.
func NewGPUNeuralKeyNode(ctx *Context, pool *FramePool, state NeuralKeyState, enginePath string) GPUPipelineNode {
	return nil
}
