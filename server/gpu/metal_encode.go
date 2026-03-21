//go:build darwin

package gpu

import (
	"fmt"
	"log/slog"
	"sync"

	"github.com/zsiec/switchframe/server/codec"
	"github.com/zsiec/switchframe/server/transition"
)

// GPUEncoder wraps an FFmpeg encoder and accepts GPU-resident frames.
// On Metal with unified memory, there's no PCIe transfer — the encoder
// reads directly from the same physical memory the GPU writes to.
type GPUEncoder struct {
	ffEnc    *codec.FFmpegEncoder
	gpuCtx   *Context
	width    int
	height   int
	cpuBuf   []byte
	cpuBufMu sync.Mutex
}

// NewGPUEncoder creates a VideoToolbox/libx264 encoder that accepts GPU frames.
func NewGPUEncoder(gpuCtx *Context, width, height, fpsNum, fpsDen, bitrate int) (*GPUEncoder, error) {
	if gpuCtx == nil || gpuCtx.mtl == nil {
		return nil, ErrGPUNotAvailable
	}

	gopSecs := transition.DefaultGOPSecs

	// Try VideoToolbox first, fall back to libx264
	enc, err := codec.NewFFmpegEncoder("h264_videotoolbox", width, height, bitrate, fpsNum, fpsDen, gopSecs, nil)
	if err != nil {
		slog.Warn("gpu: VideoToolbox encoder unavailable, falling back to libx264", "err", err)
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
func (e *GPUEncoder) EncodeGPU(frame *GPUFrame, forceIDR bool) ([]byte, bool, error) {
	if frame == nil {
		return nil, false, fmt.Errorf("gpu: encode: nil frame")
	}

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

// EncodeCPU encodes a CPU-side YUV420p frame.
func (e *GPUEncoder) EncodeCPU(yuv []byte, pts int64, forceIDR bool) ([]byte, bool, error) {
	return e.ffEnc.Encode(yuv, pts, forceIDR)
}

// Close releases encoder resources.
func (e *GPUEncoder) Close() {
	if e.ffEnc != nil {
		e.ffEnc.Close()
	}
}
