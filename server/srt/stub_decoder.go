//go:build !cgo || noffmpeg

package srt

import (
	"errors"
	"io"
)

// ErrFFmpegNotAvailable is returned when the binary was built without FFmpeg support.
var ErrFFmpegNotAvailable = errors.New("FFmpeg not available: build with cgo")

// StreamDecoderConfig configures a StreamDecoder for demuxing and decoding
// an MPEG-TS stream from an io.Reader.
type StreamDecoderConfig struct {
	Reader     io.Reader
	MaxThreads int // default 4
	OnVideo    func(yuv []byte, width, height int, pts int64)
	OnAudio    func(pcm []float32, pts int64, sampleRate, channels int)
	OnCaptions func(data []byte, pts int64) // optional
	OnSCTE35   func(data []byte, pts int64) // optional
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
