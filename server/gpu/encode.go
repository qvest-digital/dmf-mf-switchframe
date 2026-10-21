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
	"github.com/zsiec/switchframe/server/internal/video"
	"github.com/zsiec/switchframe/server/switcher"
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
	is10bit  bool       // true when encoder uses P010LE (HEVC 10-bit)
	p010Tmp  *GPUFrame  // temp P010 frame for P210→P010 conversion (lazy, 10-bit only)
	nv12Tmp  *GPUFrame  // temp NV12 frame for P210→NV12 conversion (lazy, 8-bit encoder + P210 input)
	cpuBuf   []byte     // reusable CPU-side YUV420p buffer for fallback
	cpuBufMu sync.Mutex // protects cpuBuf

	// GOP safety: force IDR if NVENC's internal GOP counter fails.
	// NVENC can stop producing periodic IDRs after PTS discontinuities.
	framesSinceIDR int   // frames since last IDR output
	maxGOPFrames   int   // max frames before forcing IDR (0 = disabled)
	lastPTS        int64 // previous frame PTS for discontinuity detection
}

// NewGPUEncoder creates an NVENC encoder that accepts GPU frames.
// Tries hw_frames_ctx (zero-copy) first, falls back to download+NVENC,
// then libx264 if NVENC is unavailable. Uses DefaultGOPSecs (2s), program quality.
func NewGPUEncoder(gpuCtx *Context, width, height, fpsNum, fpsDen, bitrate int) (*GPUEncoder, error) {
	return newGPUEncoderWithGOP(gpuCtx, width, height, fpsNum, fpsDen, bitrate, transition.DefaultGOPSecs, false)
}

// newGPUEncoderWithGOP is the internal constructor with explicit GOP interval.
// When isPreview is true, uses fast NVENC preset (p1/baseline/no-aq) for tight
// bitrate control at low targets. Otherwise uses quality preset (p4/high/spatial-aq).
func newGPUEncoderWithGOP(gpuCtx *Context, width, height, fpsNum, fpsDen, bitrate, gopSecs int, isPreview bool) (*GPUEncoder, error) {
	if gpuCtx == nil {
		return nil, ErrGPUNotAvailable
	}

	// Try zero-copy hw_frames_ctx path first
	cudaCtx := gpuCtx.CUDAContext()
	if cudaCtx != nil {
		hwEnc, err := codec.NewFFmpegHWFramesEncoder(cudaCtx, width, height, bitrate, fpsNum, fpsDen, gopSecs, isPreview)
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

	// Fallback: download to CPU then encode via NVENC or libx264. Those
	// encoders take the GOP in whole seconds only, so a GOP given as a frame
	// count (negative) is rounded up to the seconds that hold it.
	fallbackGOP := gopSecs
	if fallbackGOP < 0 {
		fallbackGOP = max(1, (-fallbackGOP*fpsDen+fpsNum-1)/fpsNum)
	}
	enc, err := codec.NewFFmpegEncoder("h264_nvenc", width, height, bitrate, fpsNum, fpsDen, fallbackGOP, nil)
	if err != nil {
		slog.Warn("gpu: NVENC encoder unavailable, falling back to libx264", "err", err)
		enc, err = codec.NewFFmpegEncoder("libx264", width, height, bitrate, fpsNum, fpsDen, fallbackGOP, nil)
		if err != nil {
			return nil, fmt.Errorf("gpu: no encoder available: %w", err)
		}
	}

	yuvSize := video.YUV420FrameSize(width, height)
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

	// GOP safety: force IDR if NVENC's internal GOP counter may have failed.
	// NVENC can stop producing periodic IDRs after PTS discontinuities.
	if e.maxGOPFrames > 0 && !forceIDR {
		e.framesSinceIDR++
		// Force IDR if we've exceeded the GOP limit without one.
		if e.framesSinceIDR >= e.maxGOPFrames {
			forceIDR = true
		}
		// Also force IDR on PTS discontinuity (backward jump or large forward jump).
		if e.lastPTS != 0 && frame.PTS != 0 {
			ptsDelta := frame.PTS - e.lastPTS
			if ptsDelta < 0 || ptsDelta > 90000*5 { // backward or >5s forward jump
				forceIDR = true
			}
		}
	}
	e.lastPTS = frame.PTS

	// Zero-copy path: pass CUDA device pointers directly to NVENC.
	// Use encStream for device-to-device copies into FFmpeg's hw_frame,
	// preventing races with kernel launches on ctx.stream.
	if e.hwEnc != nil {
		encFrame := frame
		// P210 source + NV12 encoder (H.264): convert P210→NV12 (10-bit→8-bit
		// + chroma downsample 4:2:2→4:2:0) into a temp NV12 frame.
		// This handles the case where the pipeline runs P210 but the encoder is AVC.
		if !e.is10bit && frame.Format == GPUFormatP210 {
			if e.nv12Tmp == nil {
				pool, err := NewFramePool(e.gpuCtx, e.width, e.height, 1)
				if err != nil {
					return nil, false, fmt.Errorf("gpu: NV12 tmp pool alloc: %w", err)
				}
				tmp, err := pool.Acquire()
				if err != nil {
					pool.Close()
					return nil, false, fmt.Errorf("gpu: NV12 tmp alloc: %w", err)
				}
				e.nv12Tmp = tmp
			}
			if err := ScaleP210ToNV12On(e.gpuCtx, e.nv12Tmp, frame, nil); err != nil {
				return nil, false, fmt.Errorf("gpu: P210→NV12 convert: %w", err)
			}
			e.nv12Tmp.PTS = frame.PTS
			encFrame = e.nv12Tmp
		}
		// P210 source + P010LE encoder: convert P210→P010 (chroma downsample
		// 4:2:2→4:2:0 + LSB→MSB shift <<6) into a temp NV12-sized frame.
		// NVENC doesn't support 4:2:2 — this is the only lossy step in the pipeline.
		if e.is10bit && frame.Format == GPUFormatP210 {
			if e.p010Tmp == nil {
				tmp, err := e.gpuCtx.Pool().Acquire()
				if err != nil {
					return nil, false, fmt.Errorf("gpu: P010 tmp alloc: %w", err)
				}
				e.p010Tmp = tmp
				slog.Info("gpu: P010 tmp frame allocated",
					"width", tmp.Width, "height", tmp.Height, "pitch", tmp.Pitch,
					"format", tmp.Format, "frameSize", tmp.FrameSize(),
					"srcPitch", frame.Pitch, "srcFormat", frame.Format)
			}
			// Convert P210 → P010 (chroma downsample + MSB shift) on GPU.
			if err := ConvertP210ToP010MSB(e.gpuCtx, e.p010Tmp, frame); err != nil {
				return nil, false, fmt.Errorf("gpu: P210→P010 convert: %w", err)
			}
			e.p010Tmp.PTS = frame.PTS
			encFrame = e.p010Tmp
		}

		yDevPtr := unsafe.Pointer(uintptr(encFrame.DevPtr))
		uvDevPtr := unsafe.Pointer(uintptr(encFrame.DevPtr) + uintptr(encFrame.Pitch*encFrame.Height))

		encStream := unsafe.Pointer(e.gpuCtx.EncStream())
		data, isIDR, err := e.hwEnc.EncodeNV12CUDA(yDevPtr, uvDevPtr, encFrame.Pitch, frame.PTS, forceIDR, encStream)
		if err != nil {
			return nil, false, fmt.Errorf("gpu: hw_frames encode failed: %w", err)
		}
		if isIDR {
			e.framesSinceIDR = 0
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

	if isIDR {
		e.framesSinceIDR = 0
	}
	if err != nil {
		return nil, false, fmt.Errorf("gpu: encode failed: %w", err)
	}
	return data, isIDR, nil
}

// EncodeGPUOn encodes a GPU-resident NV12 frame to H.264 using the specified
// work queue for the device-to-device copy into FFmpeg's hw_frame.
// If q is nil, the context's encode stream is used.
func (e *GPUEncoder) EncodeGPUOn(frame *GPUFrame, forceIDR bool, q *GPUWorkQueue) ([]byte, bool, error) {
	if frame == nil {
		return nil, false, fmt.Errorf("gpu: encode: nil frame")
	}

	// Zero-copy path: pass CUDA device pointers directly to NVENC.
	// Use the provided work queue's stream, or fall back to encStream.
	if e.hwEnc != nil {
		yDevPtr := unsafe.Pointer(uintptr(frame.DevPtr))
		uvDevPtr := unsafe.Pointer(uintptr(frame.DevPtr) + uintptr(frame.Pitch*frame.Height))

		stream := cudaStream(q)
		if stream == nil {
			stream = e.gpuCtx.EncStream()
		}

		data, isIDR, err := e.hwEnc.EncodeNV12CUDA(yDevPtr, uvDevPtr, frame.Pitch, frame.PTS, forceIDR, unsafe.Pointer(stream))
		if err != nil {
			return nil, false, fmt.Errorf("gpu: hw_frames encode failed: %w", err)
		}
		return data, isIDR, nil
	}

	// Fallback: download GPU NV12 -> CPU YUV420p, then encode
	e.cpuBufMu.Lock()
	err := Download(e.gpuCtx, e.cpuBuf, frame, frame.Width, frame.Height)
	if err != nil {
		e.cpuBufMu.Unlock()
		return nil, false, fmt.Errorf("gpu: encode: download failed: %w", err)
	}

	data, isIDR, encErr := e.ffEnc.Encode(e.cpuBuf, frame.PTS, forceIDR)
	e.cpuBufMu.Unlock()

	if encErr != nil {
		return nil, false, fmt.Errorf("gpu: encode failed: %w", encErr)
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
	if e.nv12Tmp != nil {
		e.nv12Tmp.Release()
		e.nv12Tmp = nil
	}
	if e.p010Tmp != nil {
		e.p010Tmp.Release()
		e.p010Tmp = nil
	}
	if e.hwEnc != nil {
		e.hwEnc.Close()
		e.hwEnc = nil
	}
	if e.ffEnc != nil {
		e.ffEnc.Close()
		e.ffEnc = nil
	}
}

// NewGPUEncoderForCodec creates a GPU encoder for the specified pipeline codec and color format.
// For CodecAVC, delegates to NewGPUEncoder. For CodecHEVC, tries zero-copy hw_frames_ctx
// with hevc_nvenc first, then falls back to download+hevc_nvenc, then libx265.
func NewGPUEncoderForCodec(gpuCtx *Context, width, height, fpsNum, fpsDen, bitrate int,
	pipelineCodec switcher.PipelineCodec, colorFormat switcher.PipelineColorFormat) (*GPUEncoder, error) {
	if gpuCtx == nil {
		return nil, ErrGPUNotAvailable
	}

	if pipelineCodec == switcher.CodecAVC {
		return NewGPUEncoder(gpuCtx, width, height, fpsNum, fpsDen, bitrate)
	}

	// HEVC path: try zero-copy hw_frames_ctx with hevc_nvenc
	is10bit := colorFormat == switcher.ColorYUV422_10bit
	cudaCtx := gpuCtx.CUDAContext()
	if cudaCtx != nil {
		hwEnc, err := codec.NewFFmpegHWFramesEncoderHEVC(cudaCtx, width, height, bitrate,
			fpsNum, fpsDen, transition.DefaultGOPSecs, is10bit)
		if err == nil {
			slog.Info("gpu: NVENC HEVC hw_frames_ctx encoder created",
				"size", fmt.Sprintf("%dx%d", width, height),
				"bitrate", bitrate,
				"10bit", is10bit,
			)
			gopFrames := fpsNum * transition.DefaultGOPSecs / fpsDen
			if gopFrames < 1 {
				gopFrames = 120
			}
			return &GPUEncoder{
				hwEnc:        hwEnc,
				gpuCtx:       gpuCtx,
				width:        width,
				height:       height,
				is10bit:      is10bit,
				maxGOPFrames: gopFrames,
			}, nil
		}
		slog.Warn("gpu: NVENC HEVC hw_frames_ctx failed, falling back", "err", err)
	}

	// Fallback: download to CPU then encode via hevc_nvenc or libx265
	enc, err := codec.NewFFmpegEncoder("hevc_nvenc", width, height, bitrate, fpsNum, fpsDen,
		transition.DefaultGOPSecs, nil)
	if err != nil {
		slog.Warn("gpu: hevc_nvenc unavailable, trying libx265", "err", err)
		enc, err = codec.NewFFmpegEncoder("libx265", width, height, bitrate, fpsNum, fpsDen,
			transition.DefaultGOPSecs, nil)
		if err != nil {
			return nil, fmt.Errorf("gpu: no HEVC encoder available: %w", err)
		}
	}

	yuvSize := video.YUV420FrameSize(width, height)
	return &GPUEncoder{
		ffEnc:  enc,
		gpuCtx: gpuCtx,
		width:  width,
		height: height,
		cpuBuf: make([]byte, yuvSize),
	}, nil
}

// IsHEVC returns true if this encoder produces HEVC output.
func (e *GPUEncoder) IsHEVC() bool {
	return false // HEVC detection tracked via pipeline codec, not encoder struct
}
