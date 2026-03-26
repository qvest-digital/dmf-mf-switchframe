//go:build !cgo || !cuda

package asr

import "errors"

// ErrNemotronNotAvailable is returned when the Nemotron backend is requested
// but the binary was built without cgo + CUDA + ONNX Runtime support.
var ErrNemotronNotAvailable = errors.New("asr: nemotron backend not available (requires cgo + cuda + onnxruntime)")

// NewNemotronBackend is a stub that always returns ErrNemotronNotAvailable.
// The real implementation requires cgo with CUDA and ONNX Runtime.
func NewNemotronBackend(cfg NemotronConfig) (Backend, error) {
	return nil, ErrNemotronNotAvailable
}
