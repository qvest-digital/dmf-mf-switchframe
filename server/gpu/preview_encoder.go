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

	// P210 fallback buffers (allocated lazily on first P210 frame)
	p210Buf []byte // CPU-side YUV422P10LE download buffer
	yuvBuf  []byte // CPU-side YUV420P buffer at preview resolution
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
// When the source frame is P210 (10-bit 4:2:2), the GPU scale kernel cannot
// handle the format mismatch (P210 src → NV12 dst), so we use a CPU fallback:
// download P210 → convert to 8-bit YUV420P at preview resolution → upload to
// the NV12 scaleDst frame → encode via normal GPU path. This is acceptable
// for 480p preview running in an async background goroutine.
func (p *PreviewEncoder) Encode(src *GPUFrame, forceIDR bool) ([]byte, bool, error) {
	if p == nil || src == nil {
		return nil, false, ErrGPUNotAvailable
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	// P210 source: preview always outputs H.264 NV12, so use CPU fallback path.
	if src.Format == GPUFormatP210 {
		return p.encodeP210Fallback(src, forceIDR)
	}

	if err := ScaleBilinearOn(p.gpuCtx, p.scaleDst, src, p.queue); err != nil {
		return nil, false, fmt.Errorf("gpu: preview scale failed: %w", err)
	}

	p.scaleDst.PTS = src.PTS
	return p.encoder.EncodeGPUOn(p.scaleDst, forceIDR, p.queue)
}

// encodeP210Fallback handles preview encoding when the source is P210.
// Downloads the P210 frame to CPU, converts to 8-bit YUV420P at preview
// resolution, uploads to the NV12 scaleDst frame, and encodes via the
// normal GPU path. Acceptable for 480p preview running in an async
// background goroutine.
func (p *PreviewEncoder) encodeP210Fallback(src *GPUFrame, forceIDR bool) ([]byte, bool, error) {
	// Lazy-allocate CPU buffers.
	// P210 CPU frame: YUV422P10LE = w*h*4 bytes (Y: w*h*2, U: w/2*h*2, V: w/2*h*2)
	p210Size := yuv10FrameSize(src.Width, src.Height)
	if len(p.p210Buf) < p210Size {
		p.p210Buf = make([]byte, p210Size)
	}

	// Download P210 frame to CPU as YUV422P10LE.
	if err := Download10(p.gpuCtx, src, p.p210Buf[:p210Size], src.Width, src.Height); err != nil {
		return nil, false, fmt.Errorf("gpu: preview P210 download: %w", err)
	}

	// Convert P210 → YUV420P at preview resolution.
	yuvSize := yuvFrameSize(p.dstW, p.dstH)
	if len(p.yuvBuf) < yuvSize {
		p.yuvBuf = make([]byte, yuvSize)
	}
	convertP210ToYUV420PScaled(p.p210Buf[:p210Size], src.Width, src.Height,
		p.yuvBuf[:yuvSize], p.dstW, p.dstH)

	// Upload YUV420P to the NV12 scaleDst frame.
	if err := Upload(p.gpuCtx, p.scaleDst, p.yuvBuf[:yuvSize], p.dstW, p.dstH); err != nil {
		return nil, false, fmt.Errorf("gpu: preview P210 upload: %w", err)
	}

	p.scaleDst.PTS = src.PTS
	return p.encoder.EncodeGPUOn(p.scaleDst, forceIDR, p.queue)
}

// convertP210ToYUV420PScaled converts YUV422P10LE to YUV420P with nearest-neighbor scaling.
// src is planar: Y (srcW*srcH*2 bytes of uint16LE), U (srcW/2*srcH*2 bytes), V (srcW/2*srcH*2 bytes).
// dst is planar YUV420P: Y (dstW*dstH), U (dstW/2*dstH/2), V (dstW/2*dstH/2).
//
// Nearest-neighbor is sufficient for 480p preview — the visual difference
// from bilinear is imperceptible at this resolution and frame rate.
func convertP210ToYUV420PScaled(src []byte, srcW, srcH int, dst []byte, dstW, dstH int) {
	// Source plane offsets (YUV422P10LE: Y + U + V, each sample is 2 bytes)
	srcYStride := srcW * 2
	srcUOffset := srcW * srcH * 2
	srcUStride := (srcW / 2) * 2
	srcVOffset := srcUOffset + (srcW/2)*srcH*2

	// Destination plane offsets (YUV420P: Y + U + V, each sample is 1 byte)
	dstYStride := dstW
	dstUOffset := dstW * dstH
	dstUStride := dstW / 2
	dstVOffset := dstUOffset + (dstW/2)*(dstH/2)

	// Scale Y plane: nearest-neighbor + 10-bit→8-bit (right-shift 2)
	for dy := 0; dy < dstH; dy++ {
		sy := dy * srcH / dstH
		if sy >= srcH {
			sy = srcH - 1
		}
		srcRowOff := sy * srcYStride
		dstRowOff := dy * dstYStride
		for dx := 0; dx < dstW; dx++ {
			sx := dx * srcW / dstW
			if sx >= srcW {
				sx = srcW - 1
			}
			// Read uint16LE, shift to 8-bit
			off := srcRowOff + sx*2
			val := uint16(src[off]) | uint16(src[off+1])<<8
			dst[dstRowOff+dx] = byte(val >> 2)
		}
	}

	// Scale U and V planes: nearest-neighbor + 10-bit→8-bit
	// Source is 4:2:2 (full height chroma), dest is 4:2:0 (half height chroma)
	srcChromaW := srcW / 2
	srcChromaH := srcH // 4:2:2: full height
	dstChromaW := dstW / 2
	dstChromaH := dstH / 2

	for _, planeInfo := range [2]struct {
		srcOffset int
		srcStride int
		dstOffset int
		dstStride int
	}{
		{srcUOffset, srcUStride, dstUOffset, dstUStride},
		{srcVOffset, srcUStride, dstVOffset, dstUStride},
	} {
		for dy := 0; dy < dstChromaH; dy++ {
			sy := dy * srcChromaH / dstChromaH
			if sy >= srcChromaH {
				sy = srcChromaH - 1
			}
			srcRowOff := planeInfo.srcOffset + sy*planeInfo.srcStride
			dstRowOff := planeInfo.dstOffset + dy*planeInfo.dstStride
			for dx := 0; dx < dstChromaW; dx++ {
				sx := dx * srcChromaW / dstChromaW
				if sx >= srcChromaW {
					sx = srcChromaW - 1
				}
				off := srcRowOff + sx*2
				val := uint16(src[off]) | uint16(src[off+1])<<8
				dst[dstRowOff+dx] = byte(val >> 2)
			}
		}
	}
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
