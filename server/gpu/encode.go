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
	"unsafe"

	"github.com/zsiec/switchframe/server/codec"
	"github.com/zsiec/switchframe/server/transition"
)

// GPUEncoder wraps an NVENC encoder and accepts GPU-resident frames.
//
// Preferred path: hw_frames_ctx — CUDA device pointers are passed directly
// to NVENC via FFmpeg's AV_PIX_FMT_CUDA, eliminating all GPU→CPU copies.
// This is the zero-copy path equivalent to Metal's native VideoToolbox.
//
// Fallback path: download NV12→YUV420p to CPU, then encode via NVENC or
// libx264 through FFmpeg. The download adds ~0.5ms latency at 1080p.
type GPUEncoder struct {
	hwEnc    *codec.FFmpegHWFramesEncoder // zero-copy NVENC (preferred)
	ffEnc    *codec.FFmpegEncoder         // CPU-input fallback
	gpuCtx   *Context
	width    int
	height   int
	cpuBuf   []byte     // reusable CPU-side YUV420p buffer for fallback
	cpuBufMu sync.Mutex // protects cpuBuf
}

// NewGPUEncoder creates an NVENC encoder that accepts GPU frames.
// Tries hw_frames_ctx (zero-copy) first, falls back to download+NVENC,
// then libx264 if NVENC is unavailable.
func NewGPUEncoder(gpuCtx *Context, width, height, fpsNum, fpsDen, bitrate int) (*GPUEncoder, error) {
	if gpuCtx == nil {
		return nil, ErrGPUNotAvailable
	}

	gopSecs := transition.DefaultGOPSecs

	// Try zero-copy hw_frames_ctx path first
	cudaCtx := gpuCtx.CUDAContext()
	if cudaCtx != nil {
		hwEnc, err := codec.NewFFmpegHWFramesEncoder(cudaCtx, width, height, bitrate, fpsNum, fpsDen, gopSecs)
		if err == nil {
			slog.Info("gpu: NVENC hw_frames_ctx encoder created (zero-copy GPU encode)",
				"size", fmt.Sprintf("%dx%d", width, height),
				"bitrate", bitrate,
			)
			return &GPUEncoder{
				hwEnc:  hwEnc,
				gpuCtx: gpuCtx,
				width:  width,
				height: height,
			}, nil
		}
		slog.Warn("gpu: NVENC hw_frames_ctx failed, falling back to download path", "err", err)
	}

	// Fallback: download to CPU then encode via NVENC or libx264
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
//
// When hw_frames_ctx is active, passes CUDA device pointers directly to NVENC
// — no GPU→CPU download, no NV12→YUV420p conversion. The CUDA device memory
// is read by NVENC's hardware encoder engine directly.
//
// When falling back, downloads NV12→YUV420p to CPU then encodes via FFmpeg.
//
// Returns the H.264 bitstream, whether it's an IDR, and any error.
func (e *GPUEncoder) EncodeGPU(frame *GPUFrame, forceIDR bool) ([]byte, bool, error) {
	if frame == nil {
		return nil, false, fmt.Errorf("gpu: encode: nil frame")
	}

	// Zero-copy path: pass CUDA device pointers directly to NVENC
	if e.hwEnc != nil {
		yDevPtr := unsafe.Pointer(uintptr(frame.DevPtr))
		uvDevPtr := unsafe.Pointer(uintptr(frame.DevPtr) + uintptr(frame.Pitch*frame.Height))

		data, isIDR, err := e.hwEnc.EncodeNV12CUDA(yDevPtr, uvDevPtr, frame.Pitch, frame.PTS, forceIDR)
		if err != nil {
			return nil, false, fmt.Errorf("gpu: hw_frames encode failed: %w", err)
		}
		return data, isIDR, nil
	}

	// Fallback: download GPU NV12 → CPU YUV420p, then encode
	e.cpuBufMu.Lock()
	err := Download(e.gpuCtx, e.cpuBuf, frame, frame.Width, frame.Height)
	if err != nil {
		e.cpuBufMu.Unlock()
		return nil, false, fmt.Errorf("gpu: encode: download failed: %w", err)
	}

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
	if e.ffEnc != nil {
		return e.ffEnc.Encode(yuv, pts, forceIDR)
	}
	// hw_frames encoder doesn't support CPU input — would need upload first
	return nil, false, fmt.Errorf("gpu: EncodeCPU not supported with hw_frames encoder")
}

// IsNativeVT returns false on CUDA builds (no VideoToolbox).
func (e *GPUEncoder) IsNativeVT() bool {
	return false
}

// IsHWFrames returns true if using the zero-copy hw_frames_ctx path.
func (e *GPUEncoder) IsHWFrames() bool {
	return e.hwEnc != nil
}

// Close releases encoder resources.
func (e *GPUEncoder) Close() {
	if e.hwEnc != nil {
		e.hwEnc.Close()
		e.hwEnc = nil
	}
	if e.ffEnc != nil {
		e.ffEnc.Close()
		e.ffEnc = nil
	}
}
