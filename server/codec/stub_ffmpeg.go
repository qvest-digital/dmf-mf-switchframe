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

// FFmpegDecoder is a stub for builds without FFmpeg support.
type FFmpegDecoder struct{}

// NewFFmpegDecoder returns an error when FFmpeg is not available.
func NewFFmpegDecoder(hwDeviceCtx unsafe.Pointer) (*FFmpegDecoder, error) {
	return nil, errFFmpegDisabled
}

// Decode is a stub that always returns an error.
func (d *FFmpegDecoder) Decode(data []byte) ([]byte, int, int, error) {
	return nil, 0, 0, errFFmpegDisabled
}

// Close is a no-op stub.
func (d *FFmpegDecoder) Close() {}
