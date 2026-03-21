//go:build darwin

package gpu

/*
#include "metal_bridge.h"
#include <stdlib.h>
#cgo LDFLAGS: -framework VideoToolbox -framework CoreMedia -framework CoreVideo -framework CoreFoundation
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

// GPUEncoder uses native VideoToolbox to encode NV12 frames directly from
// Metal unified memory — zero download, zero NV12→YUV420p conversion.
// On Apple Silicon, the VTCompressionSession reads from the same physical
// memory that the GPU compute shaders write to (unified memory architecture).
//
// Falls back to FFmpeg VideoToolbox or libx264 if native VT creation fails,
// and to CPU download+encode if needed.
type GPUEncoder struct {
	vtEnc  C.VTEncoderRef        // native VideoToolbox encoder (preferred)
	ffEnc  *codec.FFmpegEncoder  // FFmpeg fallback encoder (CPU path)
	gpuCtx *Context
	width  int
	height int
	mu     sync.Mutex // serializes encode calls (VT callback is async)

	// CPU fallback state — used only when vtEnc is nil
	cpuBuf []byte
}

// NewGPUEncoder creates a native VideoToolbox encoder that accepts GPU frames
// directly from unified memory. Falls back to FFmpeg if VT session creation fails.
func NewGPUEncoder(gpuCtx *Context, width, height, fpsNum, fpsDen, bitrate int) (*GPUEncoder, error) {
	if gpuCtx == nil || gpuCtx.mtl == nil {
		return nil, ErrGPUNotAvailable
	}

	gopSecs := transition.DefaultGOPSecs
	gopFrames := fpsNum * gopSecs / fpsDen
	if gopFrames < 1 {
		gopFrames = 60
	}

	// Try native VT encoder first — zero-copy from unified memory
	vtEnc := C.metal_vt_encoder_create(
		C.int(width), C.int(height),
		C.int(fpsNum), C.int(fpsDen),
		C.int(bitrate), C.int(gopFrames),
	)

	if vtEnc != nil {
		slog.Info("gpu: native VideoToolbox encoder created (zero-copy unified memory)",
			"size", fmt.Sprintf("%dx%d", width, height),
			"bitrate", bitrate,
			"gop", gopFrames,
		)
		return &GPUEncoder{
			vtEnc:  vtEnc,
			gpuCtx: gpuCtx,
			width:  width,
			height: height,
		}, nil
	}

	// Fallback: FFmpeg VideoToolbox or libx264 (requires download to CPU)
	slog.Warn("gpu: native VT encoder failed, falling back to FFmpeg path")
	enc, err := codec.NewFFmpegEncoder("h264_videotoolbox", width, height, bitrate, fpsNum, fpsDen, gopSecs, nil)
	if err != nil {
		slog.Warn("gpu: VideoToolbox FFmpeg encoder unavailable, falling back to libx264", "err", err)
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

// EncodeGPU encodes a GPU-resident NV12 frame to H.264 Annex B.
//
// When the native VT encoder is active, this reads NV12 data directly from
// unified memory via CVPixelBufferCreateWithPlanarBytes — no GPU download,
// no NV12→YUV420p conversion. The CVPixelBuffer wraps the existing Metal
// buffer contents pointer, so there is zero data copy.
//
// Returns the encoded Annex B H.264 bitstream, whether it's an IDR, and any error.
func (e *GPUEncoder) EncodeGPU(frame *GPUFrame, forceIDR bool) ([]byte, bool, error) {
	if frame == nil {
		return nil, false, fmt.Errorf("gpu: encode: nil frame")
	}

	// Native VT path: zero-copy encode from unified memory
	if e.vtEnc != nil {
		e.mu.Lock()
		defer e.mu.Unlock()

		forceIDRInt := C.int(0)
		if forceIDR {
			forceIDRInt = 1
		}

		var outBuf *C.uint8_t
		var outLen, outIsIDR C.int

		rc := C.metal_vt_encode(
			e.vtEnc,
			unsafe.Pointer(frame.DevPtr),
			C.int(frame.Pitch),
			C.int(frame.Width),
			C.int(frame.Height),
			C.int64_t(frame.PTS),
			forceIDRInt,
			&outBuf, &outLen, &outIsIDR,
		)
		if rc != 0 {
			return nil, false, fmt.Errorf("gpu: VT encode failed: %d", rc)
		}
		if outLen == 0 {
			// Encoder buffering (warmup) — no output yet
			return nil, false, nil
		}

		// Copy encoded data from C heap to Go memory
		data := C.GoBytes(unsafe.Pointer(outBuf), outLen)
		C.free(unsafe.Pointer(outBuf))
		return data, outIsIDR != 0, nil
	}

	// FFmpeg fallback path: download NV12 → CPU YUV420p, then encode
	e.mu.Lock()
	err := Download(e.gpuCtx, e.cpuBuf, frame, frame.Width, frame.Height)
	if err != nil {
		e.mu.Unlock()
		return nil, false, fmt.Errorf("gpu: encode: download failed: %w", err)
	}

	data, isIDR, err := e.ffEnc.Encode(e.cpuBuf, frame.PTS, forceIDR)
	e.mu.Unlock()

	if err != nil {
		return nil, false, fmt.Errorf("gpu: encode failed: %w", err)
	}
	return data, isIDR, nil
}

// EncodeCPU encodes a CPU-side YUV420p frame (passthrough to underlying encoder).
// Only available when using the FFmpeg fallback path.
func (e *GPUEncoder) EncodeCPU(yuv []byte, pts int64, forceIDR bool) ([]byte, bool, error) {
	if e.ffEnc != nil {
		return e.ffEnc.Encode(yuv, pts, forceIDR)
	}
	// Native VT encoder doesn't support CPU input — would need upload first
	return nil, false, fmt.Errorf("gpu: EncodeCPU not supported with native VT encoder")
}

// IsNativeVT returns true if the encoder is using the native VideoToolbox path
// (zero-copy from unified memory) rather than the FFmpeg fallback.
func (e *GPUEncoder) IsNativeVT() bool {
	return e.vtEnc != nil
}

// IsHWFrames returns false on Metal builds (uses native VideoToolbox instead).
func (e *GPUEncoder) IsHWFrames() bool {
	return false
}

// Close releases encoder resources.
func (e *GPUEncoder) Close() {
	if e.vtEnc != nil {
		C.metal_vt_encoder_destroy(e.vtEnc)
		e.vtEnc = nil
	}
	if e.ffEnc != nil {
		e.ffEnc.Close()
		e.ffEnc = nil
	}
}
