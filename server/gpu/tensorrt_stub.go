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
func (c *TRTContext) Infer(input, output unsafe.Pointer, batchSize int, stream uintptr) error {
	return ErrTensorRTNotAvailable
}

// Close is a no-op on non-TensorRT builds.
func (c *TRTContext) Close() {}
