//go:build cgo && cuda

package gpu

/*
#include <cuda.h>
#include <cuda_runtime.h>
*/
import "C"

import (
	"fmt"
	"log/slog"
	"unsafe"

	"github.com/zsiec/switchframe/server/codec"
	"github.com/zsiec/switchframe/server/transition"
)

// GPUDecoder wraps an FFmpeg decoder with NVDEC hardware acceleration.
// Decoded frames are transferred from GPU to CPU as YUV420p (Phase 2 Option B).
// Phase 12 will add the zero-copy GPUFrame output path (Option A).
type GPUDecoder struct {
	ffDec  *codec.FFmpegDecoder
	gpuCtx *Context
}

// NewGPUDecoder creates an NVDEC-accelerated decoder.
// Falls back to software decode if NVDEC initialization fails.
func NewGPUDecoder(gpuCtx *Context, threadCount int) (*GPUDecoder, error) {
	hwCtx := codec.CreateHWDeviceCtx("cuda")
	if hwCtx == nil {
		return nil, fmt.Errorf("gpu: failed to create CUDA hw device context for decoder")
	}

	dec, err := codec.NewFFmpegDecoderWithThreads(hwCtx, threadCount)
	if err != nil {
		return nil, fmt.Errorf("gpu: NVDEC decoder creation failed: %w", err)
	}

	slog.Info("gpu: NVDEC decoder created")
	return &GPUDecoder{
		ffDec:  dec,
		gpuCtx: gpuCtx,
	}, nil
}

// Decode decodes H.264 Annex B data to YUV420p.
// NVDEC decodes on GPU, then the frame is transferred to CPU (av_hwframe_transfer_data).
// Returns YUV buffer, width, height, error.
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

// NewGPUDecoderFactory returns a DecoderFactory that creates NVDEC decoders,
// falling back to software decode on failure.
func NewGPUDecoderFactory(gpuCtx *Context) transition.DecoderFactory {
	return func() (transition.VideoDecoder, error) {
		if gpuCtx == nil {
			// No GPU context — use software decode
			return codec.NewFFmpegDecoder(nil)
		}

		dec, err := NewGPUDecoder(gpuCtx, 0)
		if err != nil {
			slog.Warn("gpu: NVDEC init failed, falling back to software decode", "err", err)
			return codec.NewFFmpegDecoder(unsafe.Pointer(nil))
		}
		return dec, nil
	}
}
