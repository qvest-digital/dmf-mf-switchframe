//go:build !cgo || !cuda

package asr

// SileroVAD is a stub for non-CUDA builds.
// The real implementation uses ONNX Runtime inference on the Silero VAD model.
type SileroVAD struct {
	speaking bool
}

// NewSileroVAD is a stub that returns a no-op SileroVAD.
func NewSileroVAD(session *OnnxSession, threshold float64, hangoverMs int) *SileroVAD {
	return &SileroVAD{}
}

// Process is a no-op on non-CUDA builds.
func (v *SileroVAD) Process(samples []float32) {}

// IsSpeaking always returns false on non-CUDA builds.
func (v *SileroVAD) IsSpeaking() bool { return false }

// Reset is a no-op on non-CUDA builds.
func (v *SileroVAD) Reset() {}
