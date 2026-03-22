//go:build !cgo || !whisper

package asr

import "errors"

// ErrASRNotAvailable indicates whisper.cpp is not available.
var ErrASRNotAvailable = errors.New("asr: whisper.cpp not available (build without cgo/whisper)")

// WhisperCpp is a stub for non-whisper builds.
type WhisperCpp struct{}

// WhisperCppConfig configures the whisper.cpp wrapper.
type WhisperCppConfig struct {
	ModelPath string
	UseGPU    bool
}

// NewWhisperCpp returns ErrASRNotAvailable on non-whisper builds.
func NewWhisperCpp(cfg WhisperCppConfig) (*WhisperCpp, error) {
	return nil, ErrASRNotAvailable
}

// Transcribe returns ErrASRNotAvailable on non-whisper builds.
func (w *WhisperCpp) Transcribe(samples []float32, language string) (string, error) {
	return "", ErrASRNotAvailable
}

// Close is a no-op on non-whisper builds.
func (w *WhisperCpp) Close() {}
