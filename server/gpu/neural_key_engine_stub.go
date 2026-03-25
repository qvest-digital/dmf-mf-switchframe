//go:build !cgo || !cuda || !tensorrt

package gpu

// NeuralKeyEngine is a stub for non-TensorRT builds.
type NeuralKeyEngine struct{}

// NewNeuralKeyEngine returns ErrTensorRTNotAvailable on non-TensorRT builds.
func NewNeuralKeyEngine(ctx *Context, modelPath string, progW, progH int) (*NeuralKeyEngine, error) {
	return nil, ErrTensorRTNotAvailable
}

// Infer is a no-op on non-TensorRT builds.
func (nke *NeuralKeyEngine) Infer(frame *GPUFrame, keyCb, keyCr uint8, similarity, smoothness float32) error {
	return ErrTensorRTNotAvailable
}

// InferAndComposite is a no-op on non-TensorRT builds.
func (nke *NeuralKeyEngine) InferAndComposite(programFrame, fillFrame *GPUFrame, keyCb, keyCr uint8, similarity, smoothness float32) error {
	return ErrTensorRTNotAvailable
}

// WarmupDone returns false on non-TensorRT builds.
func (nke *NeuralKeyEngine) WarmupDone() bool { return false }

// Close is a no-op on non-TensorRT builds.
func (nke *NeuralKeyEngine) Close() {}
