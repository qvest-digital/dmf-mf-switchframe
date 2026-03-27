//go:build !cgo || !cuda || !tensorrt

package main

import (
	"fmt"
	"unsafe"
)

// stubBackend is the non-GPU fallback. The mel spectrogram runs in pure Go,
// but encoder/decoder require TensorRT and return errors.
type stubBackend struct {
	mel *melFrontend
}

// Compile-time interface check.
var _ inferenceBackend = (*stubBackend)(nil)

func newInferenceBackend(modelDir string, deviceID int, cfg *modelConfig) (inferenceBackend, error) {
	return &stubBackend{
		mel: newMelFrontend(),
	}, nil
}

func (s *stubBackend) runMel(audio []float32) ([]float32, int, error) {
	features, numFrames := s.mel.Compute(audio)
	return features, numFrames, nil
}

func (s *stubBackend) runEncoder(melFeatures []float32, melFrames int) ([]float32, int, error) {
	return nil, 0, fmt.Errorf("TensorRT not available (build without cgo/cuda/tensorrt)")
}

func (s *stubBackend) runDecoder(encoderFrame []float32, targets []int32, states1, states2 []float32) ([]float32, []float32, []float32, error) {
	return nil, nil, nil, fmt.Errorf("TensorRT not available (build without cgo/cuda/tensorrt)")
}

func (s *stubBackend) gpuStream() unsafe.Pointer { return nil }

func (s *stubBackend) close() {}

func buildTRTEngines(modelDir string, deviceID int) error {
	return fmt.Errorf("TensorRT not available (build without cgo/cuda/tensorrt tags)")
}
