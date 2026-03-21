//go:build !cgo || noffmpeg

package srt

import (
	"errors"
	"io"
	"unsafe"
)

// ErrFFmpegNotAvailable is returned when the binary was built without FFmpeg support.
var ErrFFmpegNotAvailable = errors.New("FFmpeg not available: build with cgo")

// StreamDecoderConfig configures a StreamDecoder for demuxing and decoding
// an MPEG-TS stream from an io.Reader.
type StreamDecoderConfig struct {
	Reader     io.Reader
	MaxThreads int // default 4
	OnVideo    func(yuv []byte, width, height int, pts int64)
	OnVideoGPU func(devPtr uintptr, pitch, width, height int, pts int64) // NVDEC zero-copy CUDA frames
	OnAudio    func(pcm []float32, pts int64, sampleRate, channels int)
	// OnCaptions is called when CEA-608/708 closed caption data is extracted
	// from H.264 SEI NALUs (A53 CC side data) during video decode.
	// The real (cgo) implementation invokes this callback.
	OnCaptions func(data []byte, pts int64) // optional

	// OnSCTE35 is called when SCTE-35 splice_info_section data is found on
	// a data PID in the MPEG-TS stream.
	// The real (cgo) implementation invokes this callback.
	OnSCTE35 func(data []byte, pts int64) // optional

	// HWDeviceCtx is an FFmpeg AVBufferRef* for NVDEC hardware decode.
	// Ignored in stub builds.
	HWDeviceCtx unsafe.Pointer
}

// StreamDecoder bridges an io.Reader to FFmpeg's avformat/avcodec for live
// MPEG-TS demuxing and decoding.
type StreamDecoder struct{}

// NewStreamDecoder returns ErrFFmpegNotAvailable when built without FFmpeg.
func NewStreamDecoder(cfg StreamDecoderConfig) (*StreamDecoder, error) {
	return nil, ErrFFmpegNotAvailable
}

// Run is a no-op stub.
func (d *StreamDecoder) Run() {}

// Stop is a no-op stub.
func (d *StreamDecoder) Stop() {}

// IsHWDecode always returns false in stub builds.
func (d *StreamDecoder) IsHWDecode() bool { return false }
