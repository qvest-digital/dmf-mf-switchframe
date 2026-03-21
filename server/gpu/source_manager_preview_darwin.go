//go:build darwin

package gpu

import "log/slog"

// previewYUVFrame carries CPU YUV420p data to the preview goroutine.
// On Metal, CPU scale + Upload avoids Metal command queue interleaving.
type previewYUVFrame struct {
	yuv  []byte
	w, h int
	pts  int64
}

// queuePreviewFrame sends a CPU YUV copy to the preview goroutine.
// Metal path: CPU scale avoids command queue interleaving between
// preview encode and program pipeline on the shared Metal queue.
func (m *GPUSourceManager) queuePreviewFrame(entry *gpuSourceEntry, yuv []byte, w, h int, pts int64) {
	if entry.prevCh == nil || len(yuv) < w*h*3/2 {
		return
	}
	previewYUV := make([]byte, w*h*3/2)
	copy(previewYUV, yuv[:w*h*3/2])
	pf := &previewYUVFrame{yuv: previewYUV, w: w, h: h, pts: pts}
	select {
	case entry.prevCh <- any(pf):
	default:
		select {
		case <-entry.prevCh:
		default:
		}
		select {
		case entry.prevCh <- any(pf):
		default:
		}
	}
}

// previewLoop reads CPU YUV frames, scales on CPU, uploads to GPU, encodes.
// Metal path avoids GPU ScaleBilinear due to command queue interleaving.
func (m *GPUSourceManager) previewLoop(sourceKey string, entry *gpuSourceEntry) {
	defer close(entry.prevDone)

	pe := entry.prevEnc
	dstW, dstH := pe.dstW, pe.dstH
	scaledBuf := make([]byte, dstW*dstH*3/2)

	forceIDR := true
	for item := range entry.prevCh {
		pf := item.(*previewYUVFrame)
		if pf.w != dstW || pf.h != dstH {
			scaleYUV420pCPU(scaledBuf, dstW, dstH, pf.yuv, pf.w, pf.h)
		} else {
			copy(scaledBuf, pf.yuv)
		}

		pe.mu.Lock()
		Upload(m.ctx, pe.scaleDst, scaledBuf, dstW, dstH)
		pe.scaleDst.PTS = pf.pts
		data, isIDR, err := pe.encoder.EncodeGPU(pe.scaleDst, forceIDR)
		pe.mu.Unlock()

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
func scaleYUV420pCPU(dst []byte, dstW, dstH int, src []byte, srcW, srcH int) {
	srcYSize := srcW * srcH
	dstYSize := dstW * dstH
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
