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

	encoders := make(map[string]*preview.Encoder, switcher.BuiltinSourceCount)
	for _, key := range []string{
		switcher.BuiltinSourceKeyBlack,
		switcher.BuiltinSourceKeyColorbars,
	} {
		relay := a.server.RegisterStream(key)

		cfg := a.builtinPreviewConfig(key, relay, pf.FPSNum, pf.FPSDen)
		pe, err := preview.NewEncoder(cfg)
		if err != nil {
			return fmt.Errorf("built-in preview encoder %q: %w", key, err)
		}
		encoders[key] = pe
		a.debugCollector.Register("preview:"+key, pe)
		slog.Info("built-in source preview encoder ready",
			"key", key, "preview_resolution", []int{cfg.Width, cfg.Height}, "scale_down", cfg.ScaleDown, "bitrate", cfg.Bitrate)
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

// builtinPreviewConfig configures a built-in source's relay encoder. Its
// relay exists whether the preview proxy is on or not; only under the
// proxy is it scaled down to the preview size like every other source's.
func (a *App) builtinPreviewConfig(key string, relay preview.Relay, fpsNum, fpsDen int) preview.Config {
	cfg := a.sourcePreviewConfig(key, relay, fpsNum, fpsDen)
	if cfg.Bitrate <= 0 {
		cfg.Bitrate = 500_000
	}
	cfg.ScaleDown = a.cfg.PreviewProxy
	return cfg
}
