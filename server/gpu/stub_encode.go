//go:build (!cgo || !cuda) && !darwin

package gpu

// GPUEncoder is a stub for non-GPU builds.
type GPUEncoder struct{}

// NewGPUEncoder returns ErrGPUNotAvailable on non-GPU builds.
func NewGPUEncoder(ctx *Context, width, height, fpsNum, fpsDen, bitrate int) (*GPUEncoder, error) {
	return nil, ErrGPUNotAvailable
}

// EncodeGPU returns ErrGPUNotAvailable on non-GPU builds.
func (e *GPUEncoder) EncodeGPU(frame *GPUFrame, forceIDR bool) ([]byte, bool, error) {
	return nil, false, ErrGPUNotAvailable
}

// EncodeGPUOn returns ErrGPUNotAvailable on non-GPU builds.
func (e *GPUEncoder) EncodeGPUOn(frame *GPUFrame, forceIDR bool, q *GPUWorkQueue) ([]byte, bool, error) {
	return nil, false, ErrGPUNotAvailable
}

// EncodeCPU returns ErrGPUNotAvailable on non-GPU builds.
func (e *GPUEncoder) EncodeCPU(yuv []byte, pts int64, forceIDR bool) ([]byte, bool, error) {
	return nil, false, ErrGPUNotAvailable
}

// IsNativeVT returns false on non-GPU builds.
func (e *GPUEncoder) IsNativeVT() bool { return false }

// IsHWFrames returns false on non-GPU builds.
func (e *GPUEncoder) IsHWFrames() bool { return false }

// Close is a no-op on non-GPU builds.
func (e *GPUEncoder) Close() {}

// GPUDecoder is a stub for non-GPU builds.
type GPUDecoder struct{}

// NewGPUDecoder returns ErrGPUNotAvailable on non-GPU builds.
func NewGPUDecoder(ctx *Context, threadCount int) (*GPUDecoder, error) {
	return nil, ErrGPUNotAvailable
}

// PreviewEncoder is a stub for non-GPU builds.
type PreviewEncoder struct{}

// NewPreviewEncoder returns ErrGPUNotAvailable on non-GPU builds.
func NewPreviewEncoder(ctx *Context, srcW, srcH, dstW, dstH, bitrate, fpsNum, fpsDen int) (*PreviewEncoder, error) {
	return nil, ErrGPUNotAvailable
}

// Encode returns ErrGPUNotAvailable on non-GPU builds.
func (p *PreviewEncoder) Encode(src *GPUFrame, forceIDR bool) ([]byte, bool, error) {
	return nil, false, ErrGPUNotAvailable
}

// Close is a no-op on non-GPU builds.
func (p *PreviewEncoder) Close() {}
