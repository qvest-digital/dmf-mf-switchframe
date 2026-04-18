package main

import (
	"fmt"
	"log/slog"

	"github.com/zsiec/switchframe/server/internal/video"
	"github.com/zsiec/switchframe/server/preview"
	"github.com/zsiec/switchframe/server/switcher"
)

// initBuiltinSourcePreview wires each built-in virtual source
// (source:black, source:colorbars) to a MoQ-published preview encoder so
// the browser multiview tile renders a live thumbnail before the operator
// selects it on program or preview. Mirrors the per-source preview wiring
// used by SRT and MXL sources, but driven from the switcher's built-in
// producer via `SetBuiltinFrameTap` rather than an ingest decoder.
//
// Called after initCoreEngine — depends on a.server (Prism) and a.sw
// (switcher) being initialized. No-op if either is missing.
func (a *App) initBuiltinSourcePreview() error {
	if a.server == nil || a.sw == nil {
		return nil
	}

	pf := a.sw.PipelineFormat()
	pw, ph := parsePreviewResolution(a.cfg.PreviewResolution)
	if pw <= 0 || ph <= 0 {
		pw, ph = 854, 480
	}
	bitrate := a.cfg.PreviewBitrate
	if bitrate <= 0 {
		bitrate = 500_000
	}

	encoders := make(map[string]*preview.Encoder, switcher.BuiltinSourceCount)
	for _, key := range []string{
		switcher.BuiltinSourceKeyBlack,
		switcher.BuiltinSourceKeyColorbars,
	} {
		relay := a.server.RegisterStream(key)

		pe, err := preview.NewEncoder(preview.Config{
			SourceKey:     key,
			Width:         pw,
			Height:        ph,
			Bitrate:       bitrate,
			FPSNum:        pf.FPSNum,
			FPSDen:        pf.FPSDen,
			Relay:         relay,
			FrameInterval: a.cfg.PreviewFrameInterval,
		})
		if err != nil {
			return fmt.Errorf("built-in preview encoder %q: %w", key, err)
		}
		encoders[key] = pe
		a.debugCollector.Register("preview:"+key, pe)
		slog.Info("built-in source preview encoder ready",
			"key", key, "preview_resolution", []int{pw, ph}, "bitrate", bitrate)
	}

	// A scratch buffer per source for 10-bit → 8-bit downconversion, reused
	// across frames. The preview encoder always expects YUV420 8-bit input;
	// when the pipeline runs in professional mode we downconvert here.
	downconvertBufs := make(map[string][]byte, len(encoders))

	a.sw.SetBuiltinFrameTap(func(key string, yuv []byte, w, h int, pts int64, fmt video.Format) {
		pe, ok := encoders[key]
		if !ok {
			return
		}
		if fmt == video.YUV422_10bit {
			dstSize := video.YUV420_8bit.FrameSize(w, h)
			buf := downconvertBufs[key]
			if len(buf) < dstSize {
				buf = make([]byte, dstSize)
				downconvertBufs[key] = buf
			}
			if err := switcher.Downconvert422_10to420(yuv, buf[:dstSize], w, h); err != nil {
				return
			}
			pe.Send(buf[:dstSize], w, h, pts)
			return
		}
		pe.Send(yuv, w, h, pts)
	})

	a.builtinPreviewEncoders = encoders
	return nil
}

// closeBuiltinSourcePreview stops and releases the built-in preview
// encoders. Called during app shutdown after the switcher has stopped its
// producer goroutine so no further tap calls land on a closed encoder.
func (a *App) closeBuiltinSourcePreview() {
	if a.sw != nil {
		a.sw.SetBuiltinFrameTap(nil)
	}
	for _, pe := range a.builtinPreviewEncoders {
		if pe != nil {
			pe.Stop()
		}
	}
	a.builtinPreviewEncoders = nil
}
