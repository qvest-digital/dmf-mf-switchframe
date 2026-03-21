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
)

// PreviewEncoder provides GPU-accelerated preview encoding.
// Combines GPU bilinear scale (e.g., 1080p→480p) + NVENC encode
// in a single GPU pipeline. Each preview encoder owns a dedicated CUDA
// stream for workflow isolation — scale + encode operations run on the
// preview stream without blocking or racing with the program pipeline's
// ctx.stream.
type PreviewEncoder struct {
	gpuCtx   *Context
	encoder  *GPUEncoder
	pool     *FramePool    // pool for scaled preview frames
	scaleDst *GPUFrame     // pre-allocated scaled frame buffer
	stream   C.cudaStream_t // dedicated CUDA stream for this preview encoder
	srcW     int           // expected source width
	srcH     int           // expected source height
	dstW     int           // preview output width
	dstH     int           // preview output height
	mu       sync.Mutex    // protects Encode (single-writer but defensive)
}

// NewPreviewEncoder creates a GPU preview encoder that scales and encodes
// GPU frames to a lower resolution for browser multiview.
//
// srcW/srcH is the expected source frame size (for validation).
// dstW/dstH is the preview output size (e.g., 854x480).
// bitrate is the target in bps (e.g., 500_000).
// fpsNum/fpsDen is the frame rate (e.g., 30/1).
func NewPreviewEncoder(ctx *Context, srcW, srcH, dstW, dstH, bitrate, fpsNum, fpsDen int) (*PreviewEncoder, error) {
	if ctx == nil {
		return nil, ErrGPUNotAvailable
	}

	// Create a dedicated CUDA stream for this preview encoder.
	// This isolates scale + encode operations from the main pipeline stream,
	// enabling true GPU concurrency without mutex serialization.
	stream, err := ctx.NewStream()
	if err != nil {
		return nil, fmt.Errorf("gpu: preview stream create failed: %w", err)
	}

	// Create frame pool for the preview resolution
	pool, poolErr := NewFramePool(ctx, dstW, dstH, 2)
	if poolErr != nil {
		ctx.DestroyStream(stream)
		return nil, fmt.Errorf("gpu: preview pool failed: %w", poolErr)
	}

	// Pre-allocate the scaled frame buffer
	scaleDst, scaleErr := pool.Acquire()
	if scaleErr != nil {
		pool.Close()
		ctx.DestroyStream(stream)
		return nil, fmt.Errorf("gpu: preview frame alloc failed: %w", scaleErr)
	}

	// Create NVENC encoder at preview resolution
	encoder, encErr := NewGPUEncoder(ctx, dstW, dstH, fpsNum, fpsDen, bitrate)
	if encErr != nil {
		scaleDst.Release()
		pool.Close()
		ctx.DestroyStream(stream)
		return nil, fmt.Errorf("gpu: preview encoder failed: %w", encErr)
	}

	slog.Info("gpu: preview encoder created",
		"src", fmt.Sprintf("%dx%d", srcW, srcH),
		"dst", fmt.Sprintf("%dx%d", dstW, dstH),
		"bitrate", bitrate)

	return &PreviewEncoder{
		gpuCtx:   ctx,
		encoder:  encoder,
		pool:     pool,
		scaleDst: scaleDst,
		stream:   stream,
		srcW:     srcW,
		srcH:     srcH,
		dstW:     dstW,
		dstH:     dstH,
	}, nil
}

// Encode scales a GPU frame to preview resolution and encodes to H.264.
// Returns the encoded bitstream, whether it's an IDR, and any error.
// The source frame is not modified.
//
// Scale and encode run on the preview encoder's dedicated CUDA stream,
// enabling concurrent GPU execution with the program pipeline.
func (p *PreviewEncoder) Encode(src *GPUFrame, forceIDR bool) ([]byte, bool, error) {
	if p == nil || src == nil {
		return nil, false, ErrGPUNotAvailable
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	// Scale source → preview resolution on the preview stream.
	if err := ScaleBilinearOnStream(p.gpuCtx, p.scaleDst, src, p.stream); err != nil {
		return nil, false, fmt.Errorf("gpu: preview scale failed: %w", err)
	}

	// Set PTS from source
	p.scaleDst.PTS = src.PTS

	// Encode the scaled frame via NVENC using the preview stream
	// for the device-to-device copy into FFmpeg's hw_frame.
	return p.encoder.EncodeGPUOnStream(p.scaleDst, forceIDR, p.stream)
}

// Close releases all preview encoder resources.
func (p *PreviewEncoder) Close() {
	if p == nil {
		return
	}
	if p.encoder != nil {
		p.encoder.Close()
		p.encoder = nil
	}
	if p.scaleDst != nil {
		p.scaleDst.Release()
		p.scaleDst = nil
	}
	if p.pool != nil {
		p.pool.Close()
		p.pool = nil
	}
	if p.stream != nil {
		p.gpuCtx.DestroyStream(p.stream)
		p.stream = nil
	}
}

// Stream returns the dedicated CUDA stream for this preview encoder.
func (p *PreviewEncoder) Stream() unsafe.Pointer {
	if p == nil {
		return nil
	}
	return unsafe.Pointer(p.stream)
}
