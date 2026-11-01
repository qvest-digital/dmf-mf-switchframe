//go:build (cgo && darwin) || (cgo && cuda)

package gpu

import "log/slog"

// previewGPUFrame carries a private GPU frame copy to the preview goroutine.
// The GPU frame copy ensures the preview goroutine can scale+encode without
// racing with the main pipeline's access to the source's current frame.
type previewGPUFrame struct {
	frame *GPUFrame
	pts   int64
}

// queuePreviewFrame sends a GPU frame copy to the preview goroutine.
// Copies the NV12 frame to a private GPU buffer (device-to-device on CUDA,
// unified memory memcpy on Metal), then the preview goroutine does GPU
// scale + encode entirely in VRAM.
func (m *GPUSourceManager) queuePreviewFrame(entry *gpuSourceEntry, _ []byte, _ int, _ int, pts int64) {
	if entry.prev == nil || !entry.prev.accepting() {
		return
	}

	// Get the current cached frame with a Ref (taken under the entry's
	// curMu) to prevent it from being recycled while we copy. Without this,
	// IngestYUV can swap+release the cached frame and the pool can recycle
	// it, causing us to read from a buffer containing another source's data.
	cached := refCurrent(entry)
	if cached == nil {
		return
	}

	previewFrame, err := m.pool.Acquire()
	if err != nil {
		cached.Release()
		return
	}
	if err := CopyGPUFrame(previewFrame, cached); err != nil {
		previewFrame.Release()
		cached.Release()
		return
	}
	cached.Release() // Done reading — safe to release our ref.
	previewFrame.PTS = pts

	// Newest wins; released instead once the preview is stopped.
	entry.prev.queue(&previewGPUFrame{frame: previewFrame, pts: pts})
}

// startPreview starts the goroutine that opens a preview encoder for a
// source of w x h and encodes the GPU frame copies queued to it. Scale +
// encode run on the preview encoder's dedicated work queue, enabling
// concurrent GPU execution with the program pipeline.
func (m *GPUSourceManager) startPreview(sourceKey string, w, h int, cfg PreviewConfig) *previewWorker[*previewGPUFrame] {
	open := func() (previewEncodeFunc[*previewGPUFrame], func(), error) {
		enc, err := NewPreviewEncoder(m.ctx, w, h, cfg.Width, cfg.Height, cfg.Bitrate, cfg.FPSNum, cfg.FPSDen)
		if err != nil {
			return nil, nil, err
		}
		encode := func(pf *previewGPUFrame, forceIDR bool) bool {
			data, isIDR, err := enc.Encode(pf.frame, forceIDR)
			pf.frame.Release()
			if err != nil {
				slog.Warn("gpu: source manager: preview encode failed",
					"source", sourceKey, "error", err)
				return false
			}
			if len(data) > 0 {
				cfg.OnPreview(data, isIDR, pf.pts)
			}
			return true
		}
		return encode, enc.Close, nil
	}
	return startPreviewWorker(sourceKey, open, func(pf *previewGPUFrame) { pf.frame.Release() })
}

// scaleYUV420pCPU scales YUV420p on CPU using nearest-neighbor.
// Used by IngestYUV (source_manager.go) to normalize source resolution to
// the pool/pipeline format before GPU upload.
func scaleYUV420pCPU(dst []byte, dstW, dstH int, src []byte, srcW, srcH int) {
	srcYSize := srcW * srcH
	dstYSize := dstW * dstH
	// Y plane
	for dy := 0; dy < dstH; dy++ {
		sy := dy * (srcH - 1) / max(dstH-1, 1)
		if sy >= srcH {
			sy = srcH - 1
		}
		for dx := 0; dx < dstW; dx++ {
			sx := dx * (srcW - 1) / max(dstW-1, 1)
			if sx >= srcW {
				sx = srcW - 1
			}
			dst[dy*dstW+dx] = src[sy*srcW+sx]
		}
	}
	// Cb plane
	srcCW, srcCH := srcW/2, srcH/2
	dstCW, dstCH := dstW/2, dstH/2
	srcCbOff := srcYSize
	dstCbOff := dstYSize
	for dy := 0; dy < dstCH; dy++ {
		sy := dy * (srcCH - 1) / max(dstCH-1, 1)
		if sy >= srcCH {
			sy = srcCH - 1
		}
		for dx := 0; dx < dstCW; dx++ {
			sx := dx * (srcCW - 1) / max(dstCW-1, 1)
			if sx >= srcCW {
				sx = srcCW - 1
			}
			dst[dstCbOff+dy*dstCW+dx] = src[srcCbOff+sy*srcCW+sx]
		}
	}
	// Cr plane
	srcCrOff := srcCbOff + srcCW*srcCH
	dstCrOff := dstCbOff + dstCW*dstCH
	for dy := 0; dy < dstCH; dy++ {
		sy := dy * (srcCH - 1) / max(dstCH-1, 1)
		if sy >= srcCH {
			sy = srcCH - 1
		}
		for dx := 0; dx < dstCW; dx++ {
			sx := dx * (srcCW - 1) / max(dstCW-1, 1)
			if sx >= srcCW {
				sx = srcCW - 1
			}
			dst[dstCrOff+dy*dstCW+dx] = src[srcCrOff+sy*srcCW+sx]
		}
	}
}
