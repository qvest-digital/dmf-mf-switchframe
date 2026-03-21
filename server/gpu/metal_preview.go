//go:build darwin

package gpu

/*
#include "metal_bridge.h"
*/
import "C"

import (
	"fmt"
	"log/slog"
	"sync"
)

// PreviewEncoder provides GPU-accelerated preview encoding on Metal.
// Each encoder has its own dedicated Metal command queue to prevent
// command buffer interleaving with other GPU work (pipeline, other previews).
type PreviewEncoder struct {
	gpuCtx   *Context
	encoder  *GPUEncoder
	pool     *FramePool
	scaleDst *GPUFrame
	queue    C.MetalQueueRef // dedicated command queue for this encoder
	srcW     int
	srcH     int
	dstW     int
	dstH     int
	mu       sync.Mutex
}

// NewPreviewEncoder creates a GPU preview encoder that scales and encodes.
func NewPreviewEncoder(ctx *Context, srcW, srcH, dstW, dstH, bitrate, fpsNum, fpsDen int) (*PreviewEncoder, error) {
	if ctx == nil || ctx.mtl == nil {
		return nil, ErrGPUNotAvailable
	}

	// Create a dedicated command queue so this encoder's ScaleBilinear
	// operations don't interleave with the main pipeline or other encoders.
	queue := ctx.mtl.createQueue()
	if queue == nil {
		return nil, fmt.Errorf("gpu: preview: failed to create command queue")
	}

	pool, err := NewFramePool(ctx, dstW, dstH, 2)
	if err != nil {
		return nil, fmt.Errorf("gpu: preview pool failed: %w", err)
	}

	scaleDst, err := pool.Acquire()
	if err != nil {
		pool.Close()
		return nil, fmt.Errorf("gpu: preview frame alloc failed: %w", err)
	}

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
		queue:    queue,
		srcW:     srcW,
		srcH:     srcH,
		dstW:     dstW,
		dstH:     dstH,
	}, nil
}

// Encode scales a GPU frame to preview resolution and encodes to H.264.
func (p *PreviewEncoder) Encode(src *GPUFrame, forceIDR bool) ([]byte, bool, error) {
	if p == nil || src == nil {
		return nil, false, ErrGPUNotAvailable
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	if err := ScaleBilinearWithQueue(p.gpuCtx, p.scaleDst, src, p.queue); err != nil {
		return nil, false, fmt.Errorf("gpu: preview scale failed: %w", err)
	}

	p.scaleDst.PTS = src.PTS
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
	p.queue = nil
}
