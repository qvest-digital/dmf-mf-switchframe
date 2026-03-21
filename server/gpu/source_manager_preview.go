//go:build darwin || (cgo && cuda)

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
	if entry.prevCh == nil {
		return
	}

	// Get the current cached frame and make a private copy for preview.
	cached := entry.current.Load()
	if cached == nil {
		return
	}

	previewFrame, err := m.pool.Acquire()
	if err != nil {
		return
	}
	CopyGPUFrame(previewFrame, cached)
	previewFrame.PTS = pts

	pf := &previewGPUFrame{frame: previewFrame, pts: pts}
	select {
	case entry.prevCh <- any(pf):
	default:
		// Channel full — drop oldest, send newest.
		select {
		case dropped := <-entry.prevCh:
			if gpf, ok := dropped.(*previewGPUFrame); ok {
				gpf.frame.Release()
			}
		default:
		}
		select {
		case entry.prevCh <- any(pf):
		default:
			previewFrame.Release()
		}
	}
}

// previewLoop reads GPU frame copies and encodes via PreviewEncoder.Encode().
// Scale + encode run on the preview encoder's dedicated work queue, enabling
// concurrent GPU execution with the program pipeline.
func (m *GPUSourceManager) previewLoop(sourceKey string, entry *gpuSourceEntry) {
	defer close(entry.prevDone)

	forceIDR := true
	for item := range entry.prevCh {
		pf := item.(*previewGPUFrame)
		data, isIDR, err := entry.prevEnc.Encode(pf.frame, forceIDR)
		pf.frame.Release()

		if err != nil {
			slog.Warn("gpu: source manager: preview encode failed",
				"source", sourceKey, "error", err)
			continue
		}
		forceIDR = false
		if len(data) > 0 && entry.onPreview != nil {
			entry.onPreview(data, isIDR, pf.pts)
		}
	}
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
