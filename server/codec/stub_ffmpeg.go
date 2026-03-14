//go:build !cgo || noffmpeg

package codec

import (
	"errors"
	"log/slog"
	"unsafe"

	"github.com/zsiec/switchframe/server/transition"
)

var errFFmpegDisabled = errors.New("FFmpeg codec unavailable: built without FFmpeg support (use cgo without noffmpeg tag)")

func init() {
	slog.Debug("codec: FFmpeg not available (built without FFmpeg support)")
}

// FFmpegEncoder is a stub for builds without FFmpeg support.
type FFmpegEncoder struct{}

// NewFFmpegEncoder returns an error when FFmpeg is not available.
func NewFFmpegEncoder(codecName string, width, height, bitrate, fpsNum, fpsDen, gopSecs int, hwDeviceCtx unsafe.Pointer) (*FFmpegEncoder, error) {
	return nil, errFFmpegDisabled
}

// Encode is a stub that always returns an error.
func (e *FFmpegEncoder) Encode(yuv []byte, pts int64, forceIDR bool) ([]byte, bool, error) {
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

// ProbeEncoders is a stub that returns "none" when FFmpeg is not available.
// When FFmpeg is available, the real implementation probes hardware and software
// encoders to find the best backend.
func ProbeEncoders() (string, string) { return "none", "none" }

// ListAvailableEncoders is a stub that returns nil when FFmpeg is not available.
func ListAvailableEncoders() []EncoderInfo { return nil }

// HWDeviceCtx is a stub that returns nil when FFmpeg is not available.
func HWDeviceCtx() unsafe.Pointer { return nil }

// NewVideoEncoder is a stub that returns an error when FFmpeg is not available.
// When FFmpeg is available, the real implementation auto-selects the best encoder.
func NewVideoEncoder(width, height, bitrate, fpsNum, fpsDen int) (transition.VideoEncoder, error) {
	return nil, errFFmpegDisabled
}

// NewVideoDecoder is a stub that returns an error when FFmpeg is not available.
// When FFmpeg is available, the real implementation auto-selects the best decoder.
func NewVideoDecoder() (transition.VideoDecoder, error) {
	return nil, errFFmpegDisabled
}
