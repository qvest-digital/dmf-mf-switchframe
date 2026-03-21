//go:build cgo && cuda

package gpu

import "log/slog"

// previewGPUFrame carries a private GPU frame copy to the preview goroutine.
// On CUDA, GPU ScaleBilinear + NVENC encode runs entirely in VRAM.
// CUDA's ctx.mu serialization prevents the command queue interleaving
// that caused corruption on Metal.
type previewGPUFrame struct {
	frame *GPUFrame
	pts   int64
}

// queuePreviewFrame sends a GPU frame copy to the preview goroutine.
// CUDA path: copies the NV12 frame to a private GPU buffer (device-to-device
// cudaMemcpy), then the preview goroutine does GPU scale + NVENC encode
// entirely in VRAM — no CPU pixel processing.
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
// CUDA path: GPU ScaleBilinear + NVENC encode — zero CPU pixel processing.
// Each preview encoder has its own CUDA stream, so scale + encode operations
// run concurrently with the program pipeline without mutex serialization.
func (m *GPUSourceManager) previewLoop(sourceKey string, entry *gpuSourceEntry) {
	defer close(entry.prevDone)

	forceIDR := true
	for item := range entry.prevCh {
		pf := item.(*previewGPUFrame)
		// PreviewEncoder.Encode() runs scale + encode on its own CUDA stream.
		// No mutex needed — stream serialization handles concurrent GPU access.
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
