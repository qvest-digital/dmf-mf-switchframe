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
	"sync"

	"github.com/zsiec/switchframe/server/codec"
	"github.com/zsiec/switchframe/server/transition"
)

// GPUEncoder wraps an FFmpeg NVENC encoder and accepts GPU-resident frames.
//
// Phase 3 implementation (Option B): downloads NV12→YUV420p to CPU, then
// encodes via NVENC. This still benefits from hardware encode acceleration —
// the only CPU work is the NV12→YUV420p conversion + memcpy. The true
// zero-copy path (Phase 12, Option A) will pass CUDA device pointers directly
// to NVENC via hw_frames_ctx, eliminating all CPU-side frame copies.
type GPUEncoder struct {
	ffEnc    *codec.FFmpegEncoder
	gpuCtx   *Context
	width    int
	height   int
	cpuBuf   []byte     // reusable CPU-side YUV420p buffer for download
	cpuBufMu sync.Mutex // protects cpuBuf (Encode is single-writer but defensive)
}

// NewGPUEncoder creates an NVENC encoder that accepts GPU frames.
// Falls back to libx264 if NVENC is unavailable.
func NewGPUEncoder(gpuCtx *Context, width, height, fpsNum, fpsDen, bitrate int) (*GPUEncoder, error) {
	if gpuCtx == nil {
		return nil, ErrGPUNotAvailable
	}

	gopSecs := transition.DefaultGOPSecs

	// Try NVENC first, fall back to libx264
	enc, err := codec.NewFFmpegEncoder("h264_nvenc", width, height, bitrate, fpsNum, fpsDen, gopSecs, nil)
	if err != nil {
		slog.Warn("gpu: NVENC encoder unavailable, falling back to libx264", "err", err)
		enc, err = codec.NewFFmpegEncoder("libx264", width, height, bitrate, fpsNum, fpsDen, gopSecs, nil)
		if err != nil {
			return nil, fmt.Errorf("gpu: no encoder available: %w", err)
		}
	}

	yuvSize := width * height * 3 / 2
	return &GPUEncoder{
		ffEnc:  enc,
		gpuCtx: gpuCtx,
		width:  width,
		height: height,
		cpuBuf: make([]byte, yuvSize),
	}, nil
}

// EncodeGPU encodes a GPU-resident NV12 frame to H.264.
// Downloads the frame to CPU (NV12→YUV420p conversion), then encodes via NVENC.
// Returns the H.264 bitstream, whether it's an IDR, and any error.
func (e *GPUEncoder) EncodeGPU(frame *GPUFrame, forceIDR bool) ([]byte, bool, error) {
	if frame == nil {
		return nil, false, fmt.Errorf("gpu: encode: nil frame")
	}

	// Download GPU NV12 → CPU YUV420p
	e.cpuBufMu.Lock()
	err := Download(e.gpuCtx, e.cpuBuf, frame, frame.Width, frame.Height)
	if err != nil {
		e.cpuBufMu.Unlock()
		return nil, false, fmt.Errorf("gpu: encode: download failed: %w", err)
	}

	// Encode via NVENC (still hardware-accelerated, just with CPU-side input)
	data, isIDR, err := e.ffEnc.Encode(e.cpuBuf, frame.PTS, forceIDR)
	e.cpuBufMu.Unlock()

	if err != nil {
		return nil, false, fmt.Errorf("gpu: encode failed: %w", err)
	}
	return data, isIDR, nil
}

// EncodeCPU encodes a CPU-side YUV420p frame (passthrough to underlying encoder).
// Used when frames are already on CPU (e.g., MXL raw input, CPU pipeline fallback).
func (e *GPUEncoder) EncodeCPU(yuv []byte, pts int64, forceIDR bool) ([]byte, bool, error) {
	return e.ffEnc.Encode(yuv, pts, forceIDR)
}

// Close releases encoder resources.
func (e *GPUEncoder) Close() {
	if e.ffEnc != nil {
		e.ffEnc.Close()
	}
}
