//go:build !cgo || noffmpeg

package codec

import (
	"errors"
	"log/slog"
	"unsafe"
)

var errFFmpegDisabled = errors.New("FFmpeg codec unavailable: built without FFmpeg support (use cgo without noffmpeg tag)")

func init() {
	slog.Debug("codec: FFmpeg not available (built without FFmpeg support)")
}

// FFmpegEncoder is a stub for builds without FFmpeg support.
type FFmpegEncoder struct{}

// NewFFmpegEncoder returns an error when FFmpeg is not available.
func NewFFmpegEncoder(codecName string, width, height, bitrate int, fps float32, hwDeviceCtx unsafe.Pointer) (*FFmpegEncoder, error) {
	return nil, errFFmpegDisabled
}

// Encode is a stub that always returns an error.
func (e *FFmpegEncoder) Encode(yuv []byte, forceIDR bool) ([]byte, bool, error) {
	return nil, false, errFFmpegDisabled
}

// Close is a no-op stub.
func (e *FFmpegEncoder) Close() {}
