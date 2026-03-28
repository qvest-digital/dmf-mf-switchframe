//go:build (cgo && darwin) || (cgo && cuda)

package gpu

import (
	"fmt"
	"log/slog"
	"sync"

	"github.com/zsiec/switchframe/server/transition"
)

// PreviewEncoder provides GPU-accelerated preview encoding on both Metal
// and CUDA. Each encoder owns a dedicated GPUWorkQueue (Metal command queue
// or CUDA stream) to prevent interleaving with other GPU work.
type PreviewEncoder struct {
	gpuCtx   *Context
	encoder  *GPUEncoder
	pool     *FramePool
	scaleDst *GPUFrame
	queue    *GPUWorkQueue // dedicated work queue for this encoder
	srcW     int
	srcH     int
	dstW     int
	dstH     int
	mu       sync.Mutex
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

	// Create a dedicated work queue so this encoder's operations don't
	// interleave with the main pipeline or other encoders.
	queue, err := NewWorkQueue(ctx)
	if err != nil {
		return nil, fmt.Errorf("gpu: preview: failed to create work queue: %w", err)
	}

	pool, err := NewFramePool(ctx, dstW, dstH, 2)
	if err != nil {
		CloseWorkQueue(queue)
		return nil, fmt.Errorf("gpu: preview pool failed: %w", err)
	}

	scaleDst, err := pool.Acquire()
	if err != nil {
		pool.Close()
		CloseWorkQueue(queue)
		return nil, fmt.Errorf("gpu: preview frame alloc failed: %w", err)
	}

	encoder, err := newGPUEncoderWithGOP(ctx, dstW, dstH, fpsNum, fpsDen, bitrate, transition.PreviewGOPSecs, true)
	if err != nil {
		scaleDst.Release()
		pool.Close()
		CloseWorkQueue(queue)
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
// Returns the encoded bitstream, whether it's an IDR, and any error.
// The source frame is not modified.
//
// Scale and encode run on the preview encoder's dedicated work queue,
// enabling concurrent GPU execution with the program pipeline.
//
// When the source frame is P210 (10-bit 4:2:2), the cross-format GPU kernel
// ScaleP210ToNV12On reads P210 uint16 samples, bilinear-interpolates, and
// writes NV12 uint8 output in a single pass — no CPU round-trip needed.
func (p *PreviewEncoder) Encode(src *GPUFrame, forceIDR bool) ([]byte, bool, error) {
	if p == nil || src == nil {
		return nil, false, ErrGPUNotAvailable
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	if src.Format == GPUFormatP210 {
		// P210→NV12 cross-format scale: single GPU pass, no CPU round-trip.
		if err := ScaleP210ToNV12On(p.gpuCtx, p.scaleDst, src, p.queue); err != nil {
			return nil, false, fmt.Errorf("gpu: preview P210→NV12 scale failed: %w", err)
		}
	} else {
		if err := ScaleBilinearOn(p.gpuCtx, p.scaleDst, src, p.queue); err != nil {
			return nil, false, fmt.Errorf("gpu: preview scale failed: %w", err)
		}
	}

	p.scaleDst.PTS = src.PTS
	return p.encoder.EncodeGPUOn(p.scaleDst, forceIDR, p.queue)
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
	if p.queue != nil {
		CloseWorkQueue(p.queue)
		p.queue = nil
	}
}
