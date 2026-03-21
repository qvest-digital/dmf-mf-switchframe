//go:build !cgo || !cuda || !tensorrt

package gpu

import (
	"errors"
	"unsafe"
)

// ErrTensorRTNotAvailable indicates TensorRT is not available.
var ErrTensorRTNotAvailable = errors.New("gpu: TensorRT not available")

// TRTEngine is a stub for non-TensorRT builds.
type TRTEngine struct{}

// TRTContext is a stub for non-TensorRT builds.
type TRTContext struct{}

// TRTEngineOpts configures engine building.
type TRTEngineOpts struct {
	MaxBatchSize  int
	UseFP16       bool
	UseINT8       bool
	PlanCachePath string
}

// NewTRTEngine returns ErrTensorRTNotAvailable on non-TensorRT builds.
func NewTRTEngine(onnxPath string, opts TRTEngineOpts) (*TRTEngine, error) {
	return nil, ErrTensorRTNotAvailable
}

// NewContext returns ErrTensorRTNotAvailable on non-TensorRT builds.
func (e *TRTEngine) NewContext() (*TRTContext, error) {
	return nil, ErrTensorRTNotAvailable
}

// InputSize returns 0 on non-TensorRT builds.
func (e *TRTEngine) InputSize() int { return 0 }

// OutputSize returns 0 on non-TensorRT builds.
func (e *TRTEngine) OutputSize() int { return 0 }

// Close is a no-op on non-TensorRT builds.
func (e *TRTEngine) Close() {}

// Infer returns ErrTensorRTNotAvailable on non-TensorRT builds.
func (c *TRTContext) Infer(input, output unsafe.Pointer, batchSize int, stream unsafe.Pointer) error {
	return ErrTensorRTNotAvailable
}

// Close is a no-op on non-TensorRT builds.
func (c *TRTContext) Close() {}

// TensorInfo describes an I/O tensor in a TensorRT engine.
type TensorInfo struct {
	Name    string
	IsInput bool
	DType   int
	Dims    []int
}

// NumIOTensors returns 0 on non-TensorRT builds.
func (e *TRTEngine) NumIOTensors() int { return 0 }

// TensorInfoAt returns ErrTensorRTNotAvailable on non-TensorRT builds.
func (e *TRTEngine) TensorInfoAt(index int) (TensorInfo, error) {
	return TensorInfo{}, ErrTensorRTNotAvailable
}

// TRTEngineOptsV2 extends TRTEngineOpts with sequence length for decoder models.
type TRTEngineOptsV2 struct {
	MaxBatchSize  int
	MaxSeqLen     int
	UseFP16       bool
	UseINT8       bool
	PlanCachePath string
}

// NewTRTEngineV2 returns ErrTensorRTNotAvailable on non-TensorRT builds.
func NewTRTEngineV2(onnxPath string, opts TRTEngineOptsV2) (*TRTEngine, error) {
	return nil, ErrTensorRTNotAvailable
}

// TRTBinding describes a single named tensor binding for multi-input inference.
type TRTBinding struct {
	Name   string
	DevPtr unsafe.Pointer
	Dims   []int
}

// InferMulti returns ErrTensorRTNotAvailable on non-TensorRT builds.
func (c *TRTContext) InferMulti(bindings []TRTBinding, stream unsafe.Pointer) error {
	return ErrTensorRTNotAvailable
}
