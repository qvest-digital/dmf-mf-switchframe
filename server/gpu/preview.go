//go:build cgo && cuda

package gpu

import (
	"fmt"
	"log/slog"
	"sync"
)

// PreviewEncoder provides GPU-accelerated preview encoding.
// Combines GPU bilinear scale (e.g., 1080p→480p) + NVENC encode
// in a single GPU pipeline. Uses the L4's second NVENC engine,
// running concurrently with the program encoder via separate CUDA streams.
type PreviewEncoder struct {
	gpuCtx   *Context
	encoder  *GPUEncoder
	pool     *FramePool // pool for scaled preview frames
	scaleDst *GPUFrame  // pre-allocated scaled frame buffer
	srcW     int        // expected source width
	srcH     int        // expected source height
	dstW     int        // preview output width
	dstH     int        // preview output height
	mu       sync.Mutex // protects Encode (single-writer but defensive)
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

	// Create frame pool for the preview resolution
	pool, err := NewFramePool(ctx, dstW, dstH, 2)
	if err != nil {
		return nil, fmt.Errorf("gpu: preview pool failed: %w", err)
	}

	// Pre-allocate the scaled frame buffer
	scaleDst, err := pool.Acquire()
	if err != nil {
		pool.Close()
		return nil, fmt.Errorf("gpu: preview frame alloc failed: %w", err)
	}

	// Create NVENC encoder at preview resolution
	encoder, err := NewGPUEncoder(ctx, dstW, dstH, fpsNum, fpsDen, bitrate)
	if err != nil {
		scaleDst.Release()
		pool.Close()
		return nil, fmt.Errorf("gpu: preview encoder failed: %w", err)
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
		srcW:     srcW,
		srcH:     srcH,
		dstW:     dstW,
		dstH:     dstH,
	}, nil
}

// Encode scales a GPU frame to preview resolution and encodes to H.264.
// Returns the encoded bitstream, whether it's an IDR, and any error.
// The source frame is not modified.
func (p *PreviewEncoder) Encode(src *GPUFrame, forceIDR bool) ([]byte, bool, error) {
	if p == nil || src == nil {
		return nil, false, ErrGPUNotAvailable
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	// Scale source → preview resolution on GPU
	if err := ScaleBilinear(p.gpuCtx, p.scaleDst, src); err != nil {
		return nil, false, fmt.Errorf("gpu: preview scale failed: %w", err)
	}

	// Set PTS from source
	p.scaleDst.PTS = src.PTS

	// Encode the scaled frame via NVENC
	return p.encoder.EncodeGPU(p.scaleDst, forceIDR)
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
}
