//go:build cgo

package main

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/zsiec/prism/distribution"
	"github.com/zsiec/switchframe/server/audio"
	"github.com/zsiec/switchframe/server/mxl"
	"github.com/zsiec/switchframe/server/switcher"
)

// countingSampleWriter takes every audio batch.
type countingSampleWriter struct{}

func (countingSampleWriter) WriteSamples(uint64, [][]float32) error { return nil }
func (countingSampleWriter) Close() error                           { return nil }

// A booking whose inputs are not there yet, or are all down, has nothing
// on air: its program is black and silent, and that is still a program the
// engine writes at the clock. The engine becomes ready, so it stays in its
// Service for an operator to set its inputs up, and its peer may be taken
// down. The engine runs as initCoreEngine and initMXL wire it, with no
// source at all.
func TestReady_NoInputsIsReady(t *testing.T) {
	a := &App{programRelay: distribution.NewRelay()}
	// 1080: v210 packs six pixels per group, which 1280 is not a multiple of.
	a.cfg.Format, a.cfg.ColorFormat = "1080p25", "standard"
	if err := a.initCoreEngine(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		a.sw.Close()
		_ = a.mixer.Close()
	})
	color := a.sw.PipelineFormat().Color
	a.sw.SetPipelineCodecs(encoderFactoryForCodecWithOpts(color.Codec, nil, color.ColorFormat), color.Codec)
	if err := a.sw.BuildPipeline(); err != nil {
		t.Fatal(err)
	}

	pf := a.sw.PipelineFormat()
	out := mxl.NewOutput(mxl.OutputConfig{
		Width: pf.Width, Height: pf.Height,
		VideoRate: mxl.Rational{Numerator: int64(pf.FPSNum), Denominator: int64(pf.FPSDen)},
	})
	a.mxlOutput = out
	a.sw.SetRawVideoSink(switcher.RawVideoSink(func(f *switcher.ProcessingFrame) {
		if f.Format == switcher.ColorYUV422_10bit {
			out.Writer().WriteVideo10bit(f.Data, f.Width, f.Height, f.PTS)
		} else {
			out.Writer().WriteVideo(f.Data, f.Width, f.Height, f.PTS)
		}
	}))
	a.mixer.SetRawAudioSink(audio.RawAudioSink(out.Writer().WriteAudio))
	out.StartLifecycle(context.Background(), &gatedGrainWriter{}, countingSampleWriter{})
	t.Cleanup(out.Stop)

	a.sw.Start()
	a.startupDone.Store(true)
	readyFlag.Store(true)
	t.Cleanup(func() { readyFlag.Store(false) })

	if src := a.sw.ProgramSource(); src != "" {
		t.Fatalf("program source %q, want none", src)
	}
	waitReady(t, startReady(t, a), http.StatusOK, 5*time.Second)
}
