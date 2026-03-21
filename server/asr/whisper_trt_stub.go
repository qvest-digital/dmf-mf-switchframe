//go:build !cgo || !cuda || !tensorrt

package asr

import "errors"

// ErrASRNotAvailable indicates TensorRT-based ASR is not available in this build.
var ErrASRNotAvailable = errors.New("asr: TensorRT not available (build without cgo/cuda/tensorrt)")

// WhisperTRT wraps TensorRT engines for Whisper encoder and decoder inference.
// This is the stub implementation for non-TensorRT builds.
type WhisperTRT struct{}

// WhisperTRTConfig configures the TensorRT Whisper inference wrapper.
type WhisperTRTConfig struct {
	ModelDir string // directory containing encoder.onnx, decoder.onnx, and vocab.json
	UseFP16  bool   // enable FP16 precision (requires GPU with FP16 support)
}

// NewWhisperTRT returns ErrASRNotAvailable on non-TensorRT builds.
func NewWhisperTRT(cfg WhisperTRTConfig) (*WhisperTRT, error) {
	return nil, ErrASRNotAvailable
}

// Encode runs the Whisper encoder on mel spectrogram features.
// Returns ErrASRNotAvailable on non-TensorRT builds.
func (w *WhisperTRT) Encode(mel []float32) ([]float32, error) {
	return nil, ErrASRNotAvailable
}

// Decode runs autoregressive greedy decoding using the Whisper decoder.
// Returns ErrASRNotAvailable on non-TensorRT builds.
func (w *WhisperTRT) Decode(encoderOutput []float32, initialTokens []int) ([]int, error) {
	return nil, ErrASRNotAvailable
}

// Close releases all GPU resources. No-op on non-TensorRT builds.
func (w *WhisperTRT) Close() {}
