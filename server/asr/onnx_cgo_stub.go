//go:build !cgo || !cuda

package asr

import "fmt"

// OnnxSession is a stub for non-CUDA builds.
type OnnxSession struct{}

// OnnxValue is a stub for non-CUDA builds.
type OnnxValue struct{}

// NewOnnxSession returns an error on non-CUDA builds.
func NewOnnxSession(modelPath string, useGPU bool) (*OnnxSession, error) {
	return nil, fmt.Errorf("onnxruntime: not available (requires cgo + cuda build)")
}

// Run is a stub.
func (s *OnnxSession) Run(inputNames []string, inputValues []*OnnxValue, outputNames []string) ([]*OnnxValue, error) {
	return nil, fmt.Errorf("onnxruntime: not available")
}

// NewOnnxValueFloat32 is a stub.
func (s *OnnxSession) NewOnnxValueFloat32(data []float32, shape []int64) (*OnnxValue, error) {
	return nil, fmt.Errorf("onnxruntime: not available")
}

// NewOnnxValueInt64 is a stub.
func (s *OnnxSession) NewOnnxValueInt64(data []int64, shape []int64) (*OnnxValue, error) {
	return nil, fmt.Errorf("onnxruntime: not available")
}

// NewOnnxValueInt32 is a stub.
func (s *OnnxSession) NewOnnxValueInt32(data []int32, shape []int64) (*OnnxValue, error) {
	return nil, fmt.Errorf("onnxruntime: not available")
}

// GetFloat32Data is a stub.
func (v *OnnxValue) GetFloat32Data() ([]float32, error) {
	return nil, fmt.Errorf("onnxruntime: not available")
}

// GetInt64Data is a stub.
func (v *OnnxValue) GetInt64Data() ([]int64, error) {
	return nil, fmt.Errorf("onnxruntime: not available")
}

// GetInt32Data is a stub.
func (v *OnnxValue) GetInt32Data() ([]int32, error) {
	return nil, fmt.Errorf("onnxruntime: not available")
}

// Shape is a stub.
func (v *OnnxValue) Shape() ([]int64, error) {
	return nil, fmt.Errorf("onnxruntime: not available")
}

// Close is a no-op stub.
func (v *OnnxValue) Close() {}

// Close is a no-op stub.
func (s *OnnxSession) Close() {}
