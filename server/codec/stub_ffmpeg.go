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
func NewFFmpegEncoder(codecName string, width, height, bitrate, fpsNum, fpsDen, gopSecs int, hwDeviceCtx unsafe.Pointer, opts ...*EncoderOptions) (*FFmpegEncoder, error) {
	return nil, errFFmpegDisabled
}

// Extradata is a stub that always returns nil.
func (e *FFmpegEncoder) Extradata() []byte { return nil }

// Encode is a stub that always returns an error.
func (e *FFmpegEncoder) Encode(yuv []byte, pts int64, forceIDR bool) ([]byte, bool, error) {
	return nil, false, errFFmpegDisabled
}

// Close is a no-op stub.
func (e *FFmpegEncoder) Close() {}

// FFmpegHWFramesEncoder is a stub for builds without FFmpeg support.
type FFmpegHWFramesEncoder struct{}

// NewFFmpegHWFramesEncoder returns an error when FFmpeg is not available.
func NewFFmpegHWFramesEncoder(cudaCtx unsafe.Pointer, width, height, bitrate, fpsNum, fpsDen, gopSecs int, preview ...bool) (*FFmpegHWFramesEncoder, error) {
	return nil, errFFmpegDisabled
}

// NewFFmpegHWFramesEncoderHEVC returns an error when FFmpeg is not available.
func NewFFmpegHWFramesEncoderHEVC(cudaCtx unsafe.Pointer, width, height, bitrate, fpsNum, fpsDen, gopSecs int,
	is10bit bool) (*FFmpegHWFramesEncoder, error) {
	return nil, errFFmpegDisabled
}

// EncodeNV12CUDA is a stub that always returns an error.
func (e *FFmpegHWFramesEncoder) EncodeNV12CUDA(yDevPtr, uvDevPtr unsafe.Pointer, pitch int, pts int64, forceIDR bool, cudaStream unsafe.Pointer) ([]byte, bool, error) {
	return nil, false, errFFmpegDisabled
}

// Close is a no-op stub.
func (e *FFmpegHWFramesEncoder) Close() {}

// FFmpegDecoder is a stub for builds without FFmpeg support.
type FFmpegDecoder struct{}

// NewFFmpegDecoder returns an error when FFmpeg is not available.
func NewFFmpegDecoder(hwDeviceCtx unsafe.Pointer) (*FFmpegDecoder, error) {
	return nil, errFFmpegDisabled
}

// NewFFmpegDecoderWithThreads returns an error when FFmpeg is not available.
func NewFFmpegDecoderWithThreads(hwDeviceCtx unsafe.Pointer, threadCount int) (*FFmpegDecoder, error) {
	return nil, errFFmpegDisabled
}

// NewFFmpegHEVCDecoder returns an error when FFmpeg is not available.
func NewFFmpegHEVCDecoder(hwDeviceCtx unsafe.Pointer) (*FFmpegDecoder, error) {
	return nil, errFFmpegDisabled
}

// NewFFmpegHEVCDecoderWithThreads returns an error when FFmpeg is not available.
func NewFFmpegHEVCDecoderWithThreads(hwDeviceCtx unsafe.Pointer, threadCount int) (*FFmpegDecoder, error) {
	return nil, errFFmpegDisabled
}

// NewFFmpegHEVCDecoderNative10bit returns an error when FFmpeg is not available.
func NewFFmpegHEVCDecoderNative10bit(hwDeviceCtx unsafe.Pointer) (*FFmpegDecoder, error) {
	return nil, errFFmpegDisabled
}

// NewFFmpegHEVCDecoderNative10bitWithThreads returns an error when FFmpeg is not available.
func NewFFmpegHEVCDecoderNative10bitWithThreads(hwDeviceCtx unsafe.Pointer, threadCount int) (*FFmpegDecoder, error) {
	return nil, errFFmpegDisabled
}

// Decode is a stub that always returns an error.
func (d *FFmpegDecoder) Decode(data []byte) ([]byte, int, int, error) {
	return nil, 0, 0, errFFmpegDisabled
}

// DecodeInto is a stub that always returns an error.
func (d *FFmpegDecoder) DecodeInto(data []byte, dst []byte) ([]byte, int, int, error) {
	return nil, 0, 0, errFFmpegDisabled
}

// Flush is a no-op stub.
func (d *FFmpegDecoder) Flush() {}

// SendEOS is a stub that always returns an error.
func (d *FFmpegDecoder) SendEOS() error { return errFFmpegDisabled }

// ReceiveFrame is a stub that always returns an error.
func (d *FFmpegDecoder) ReceiveFrame() ([]byte, int, int, error) {
	return nil, 0, 0, errFFmpegDisabled
}

// Close is a no-op stub.
func (d *FFmpegDecoder) Close() {}

// ProbeEncoders is a stub that returns "none" when FFmpeg is not available.
// When FFmpeg is available, the real implementation probes hardware and software
// encoders to find the best backend.
func ProbeEncoders() (string, string) { return "none", "none" }

// ProbeHEVCEncoders is a stub that returns "none" when FFmpeg is not available.
func ProbeHEVCEncoders() (string, string) { return "none", "none" }

// ListAvailableEncoders is a stub that returns nil when FFmpeg is not available.
func ListAvailableEncoders() []EncoderInfo { return nil }

// ListAvailableHEVCEncoders is a stub that returns nil when FFmpeg is not available.
func ListAvailableHEVCEncoders() []EncoderInfo { return nil }

// HWDeviceCtx is a stub that returns nil when FFmpeg is not available.
func HWDeviceCtx() unsafe.Pointer { return nil }

// NewFFmpegPreviewEncoder returns an error when FFmpeg is not available.
func NewFFmpegPreviewEncoder(width, height, bitrate, fpsNum, fpsDen, gopSecs int) (*FFmpegEncoder, error) {
	return nil, errFFmpegDisabled
}

// NewVideoEncoder is a stub that returns an error when FFmpeg is not available.
// When FFmpeg is available, the real implementation auto-selects the best encoder.
func NewVideoEncoder(width, height, bitrate, fpsNum, fpsDen int, opts ...*EncoderOptions) (transition.VideoEncoder, error) {
	return nil, errFFmpegDisabled
}

// NewPreviewEncoder is a stub that returns an error when FFmpeg is not available.
func NewPreviewEncoder(width, height, bitrate, fpsNum, fpsDen int, preset ...string) (transition.VideoEncoder, error) {
	return nil, errFFmpegDisabled
}

// NewVideoDecoder is a stub that returns an error when FFmpeg is not available.
// When FFmpeg is available, the real implementation auto-selects the best decoder.
func NewVideoDecoder() (transition.VideoDecoder, error) {
	return nil, errFFmpegDisabled
}

// NewVideoDecoderSingleThread is a stub that returns an error when FFmpeg is not available.
func NewVideoDecoderSingleThread() (transition.VideoDecoder, error) {
	return nil, errFFmpegDisabled
}

// NewHEVCVideoDecoder is a stub that returns an error when FFmpeg is not available.
func NewHEVCVideoDecoder() (transition.VideoDecoder, error) {
	return nil, errFFmpegDisabled
}

// NewHEVCVideoDecoderSingleThread is a stub that returns an error when FFmpeg is not available.
func NewHEVCVideoDecoderSingleThread() (transition.VideoDecoder, error) {
	return nil, errFFmpegDisabled
}

// NewVideoDecoderNative10bit is a stub that returns an error when FFmpeg is not available.
func NewVideoDecoderNative10bit() (transition.VideoDecoder, error) {
	return nil, errFFmpegDisabled
}

// NewVideoDecoderNative10bitSingleThread is a stub that returns an error when FFmpeg is not available.
func NewVideoDecoderNative10bitSingleThread() (transition.VideoDecoder, error) {
	return nil, errFFmpegDisabled
}

// NewVideoDecoderForCodecNative10bit is a stub that returns an error when FFmpeg is not available.
func NewVideoDecoderForCodecNative10bit(codec string) (transition.VideoDecoder, error) {
	return nil, errFFmpegDisabled
}

// NewVideoDecoderForCodecNative10bitSingleThread is a stub that returns an error when FFmpeg is not available.
func NewVideoDecoderForCodecNative10bitSingleThread(codec string) (transition.VideoDecoder, error) {
	return nil, errFFmpegDisabled
}

// NewVideoDecoderForCodec is a stub that returns an error when FFmpeg is not available.
func NewVideoDecoderForCodec(codec string) (transition.VideoDecoder, error) {
	return nil, errFFmpegDisabled
}

// NewVideoDecoderForCodecSingleThread is a stub that returns an error when FFmpeg is not available.
func NewVideoDecoderForCodecSingleThread(codec string) (transition.VideoDecoder, error) {
	return nil, errFFmpegDisabled
}

// NewHEVCVideoEncoder is a stub that returns an error when FFmpeg is not available.
func NewHEVCVideoEncoder(width, height, bitrate, fpsNum, fpsDen int, opts ...*EncoderOptions) (transition.VideoEncoder, error) {
	return nil, errFFmpegDisabled
}

// NewHEVCVideoEncoder10bit is a stub that returns an error when FFmpeg is not available.
func NewHEVCVideoEncoder10bit(width, height, bitrate, fpsNum, fpsDen int, opts ...*EncoderOptions) (transition.VideoEncoder, error) {
	return nil, errFFmpegDisabled
}

// FileProbeResult holds the result of probing a media file.
type FileProbeResult struct {
	VideoCodecID int
	AudioCodecID int
	Width        int
	Height       int
	HasVideo     bool
	HasAudio     bool
}

// ProbeFile returns an error when FFmpeg is not available.
func ProbeFile(path string) (*FileProbeResult, error) {
	return nil, errFFmpegDisabled
}

// IsH264 always returns false when FFmpeg is not available.
func (r *FileProbeResult) IsH264() bool { return false }

// IsHEVC always returns false when FFmpeg is not available.
func (r *FileProbeResult) IsHEVC() bool { return false }

// TranscodeResult holds metadata from a transcode operation.
type TranscodeResult struct {
	Width       int
	Height      int
	DurationMs  int64
	FPS         float64
	SampleRate  int
	Channels    int
	VideoFrames int
}

// TranscodeFile returns an error when FFmpeg is not available.
func TranscodeFile(inputPath, outputPath, encoderName string, bitrate int) (*TranscodeResult, error) {
	return nil, errFFmpegDisabled
}

// TranscodeFileWithProgress returns an error when FFmpeg is not available.
func TranscodeFileWithProgress(inputPath, outputPath, encoderName string, bitrate int, progressPct *int32) (*TranscodeResult, error) {
	return nil, errFFmpegDisabled
}
