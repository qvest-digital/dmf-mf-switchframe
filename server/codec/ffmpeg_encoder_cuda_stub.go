//go:build cgo && !cuda && !noffmpeg

package codec

import (
	"fmt"
	"unsafe"
)

// FFmpegHWFramesEncoder is a stub for non-CUDA builds with FFmpeg.
// CUDA hw_frames_ctx is only available on NVIDIA GPUs.
type FFmpegHWFramesEncoder struct{}

// NewFFmpegHWFramesEncoder returns an error on non-CUDA builds.
func NewFFmpegHWFramesEncoder(cudaCtx unsafe.Pointer, width, height, bitrate, fpsNum, fpsDen, gopSecs int) (*FFmpegHWFramesEncoder, error) {
	return nil, fmt.Errorf("CUDA hw_frames encoder not available: built without CUDA support")
}

// EncodeNV12CUDA is a stub that always returns an error.
func (e *FFmpegHWFramesEncoder) EncodeNV12CUDA(yDevPtr, uvDevPtr unsafe.Pointer, pitch int, pts int64, forceIDR bool, cudaStream unsafe.Pointer) ([]byte, bool, error) {
	return nil, false, fmt.Errorf("CUDA hw_frames encoder not available")
}

// Close is a no-op stub.
func (e *FFmpegHWFramesEncoder) Close() {}
