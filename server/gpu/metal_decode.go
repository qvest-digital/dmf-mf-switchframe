//go:build darwin

package gpu

import (
	"fmt"
	"log/slog"
	"unsafe"

	"github.com/zsiec/switchframe/server/codec"
	"github.com/zsiec/switchframe/server/transition"
)

// GPUDecoder wraps an FFmpeg decoder with VideoToolbox hardware acceleration.
type GPUDecoder struct {
	ffDec  *codec.FFmpegDecoder
	gpuCtx *Context
}

// NewGPUDecoder creates a VideoToolbox-accelerated decoder.
func NewGPUDecoder(gpuCtx *Context, threadCount int) (*GPUDecoder, error) {
	hwCtx := codec.CreateHWDeviceCtx("videotoolbox")
	if hwCtx == nil {
		// Fall back to software decode
		dec, err := codec.NewFFmpegDecoderWithThreads(nil, threadCount)
		if err != nil {
			return nil, fmt.Errorf("gpu: software decoder creation failed: %w", err)
		}
		slog.Warn("gpu: VideoToolbox hw context unavailable, using software decode")
		return &GPUDecoder{ffDec: dec, gpuCtx: gpuCtx}, nil
	}

	dec, err := codec.NewFFmpegDecoderWithThreads(hwCtx, threadCount)
	if err != nil {
		return nil, fmt.Errorf("gpu: VideoToolbox decoder creation failed: %w", err)
	}

	slog.Info("gpu: VideoToolbox decoder created")
	return &GPUDecoder{ffDec: dec, gpuCtx: gpuCtx}, nil
}

// Decode decodes H.264 Annex B data to YUV420p.
func (d *GPUDecoder) Decode(data []byte) ([]byte, int, int, error) {
	return d.ffDec.Decode(data)
}

// DecodeInto decodes H.264 data, writing into dst if it fits.
func (d *GPUDecoder) DecodeInto(data []byte, dst []byte) ([]byte, int, int, error) {
	return d.ffDec.DecodeInto(data, dst)
}

// Flush resets the decoder state.
func (d *GPUDecoder) Flush() {
	d.ffDec.Flush()
}

// Close releases decoder resources.
func (d *GPUDecoder) Close() {
	d.ffDec.Close()
}

// Compile-time check that GPUDecoder implements transition.VideoDecoder.
var _ transition.VideoDecoder = (*GPUDecoder)(nil)

// NewGPUDecoderFactory returns a DecoderFactory that creates VideoToolbox decoders.
func NewGPUDecoderFactory(gpuCtx *Context) transition.DecoderFactory {
	return func() (transition.VideoDecoder, error) {
		if gpuCtx == nil || gpuCtx.mtl == nil {
			return codec.NewFFmpegDecoder(nil)
		}

		dec, err := NewGPUDecoder(gpuCtx, 0)
		if err != nil {
			slog.Warn("gpu: VideoToolbox init failed, falling back to software decode", "err", err)
			return codec.NewFFmpegDecoder(unsafe.Pointer(nil))
		}
		return dec, nil
	}
}
