//go:build !cgo || !cuda || !tensorrt

// Package neuralkey wraps the CorridorKey neural green screen keyer,
// a TensorRT-based inference engine that produces an alpha matte and
// cleaned foreground from an NV12 input frame plus an optional hint mask.
//
// On builds without CUDA + TensorRT, all functions return
// ErrNeuralKeyNotAvailable.
package neuralkey

import (
	"errors"
	"unsafe"
)

// ErrNeuralKeyNotAvailable indicates the neural keyer is not available
// (requires CUDA + TensorRT build).
var ErrNeuralKeyNotAvailable = errors.New("neuralkey: not available (requires CUDA + TensorRT)")

// Engine wraps a TensorRT execution context for the CorridorKey model.
type Engine struct{}

// NewEngine loads an ONNX model and builds a TensorRT engine optimized
// for frames up to maxWidth x maxHeight. Returns ErrNeuralKeyNotAvailable
// on non-CUDA builds.
func NewEngine(onnxPath string, maxWidth, maxHeight int) (*Engine, error) {
	return nil, ErrNeuralKeyNotAvailable
}

// Infer runs the neural keyer on a single NV12 frame.
//
// Parameters:
//   - nv12:     device pointer to the input NV12 frame
//   - hint:     device pointer to the hint mask (single channel, same resolution; nil to skip)
//   - width:    frame width in pixels
//   - height:   frame height in pixels
//   - pitch:    row pitch in bytes of the NV12 surface
//   - alphaOut: device pointer to receive the alpha matte (single channel, uint8)
//   - fgOut:    device pointer to receive the cleaned foreground (NV12)
//   - stream:   CUDA stream for async execution (unsafe.Pointer to cudaStream_t)
//
// Returns ErrNeuralKeyNotAvailable on non-CUDA builds.
func (e *Engine) Infer(nv12 unsafe.Pointer, hint unsafe.Pointer, width, height, pitch int,
	alphaOut, fgOut unsafe.Pointer, stream unsafe.Pointer) error {
	return ErrNeuralKeyNotAvailable
}

// WarmupDone reports whether the engine has completed warmup inference
// passes. Returns false on non-CUDA builds.
func (e *Engine) WarmupDone() bool { return false }

// Close releases all TensorRT and CUDA resources. No-op on non-CUDA builds.
func (e *Engine) Close() {}
