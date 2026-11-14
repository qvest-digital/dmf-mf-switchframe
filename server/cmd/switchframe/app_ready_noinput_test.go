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
	// Every frame rate, 120 and 240 included, at 1080 and 2160 lines, 8
	// and 10 bits. The 720p formats are 1280 wide, which the MXL writer
	// cannot pack into v210 here: its rows would end in a partial 6-pixel
	// group.
	for _, tc := range []struct{ format, color string }{
		{"1080p23.976", "standard"}, {"1080p24", "standard"}, {"1080p25", "standard"},
		{"1080p29.97", "standard"}, {"1080p30", "standard"}, {"1080p50", "standard"},
		{"1080p59.94", "standard"}, {"1080p60", "standard"},
		{"1080p120", "standard"}, {"1080p240", "standard"},
		{"1080p25", "professional"}, {"2160p50", "standard"},
	} {
		t.Run(tc.format+"/"+tc.color, func(t *testing.T) {
			a := &App{programRelay: distribution.NewRelay()}
			// Above 60 no preset exists: the engine starts at 60 and runs
			// the rate set after.
			preset := tc.format
			if _, ok := switcher.FormatPresets[preset]; !ok {
				preset = "1080p60"
			}
			a.cfg.Format, a.cfg.ColorFormat = preset, tc.color
			if err := a.initCoreEngine(); err != nil {
				t.Fatal(err)
			}
			if preset != tc.format {
				f := readyFormat(t, tc.format)
				f.Color = a.sw.PipelineFormat().Color
				if err := a.sw.SetPipelineFormat(f); err != nil {
					t.Fatal(err)
				}
			}
			// Released as closeSubsystems releases them: the encoders hold
			// memory outside the Go heap.
			t.Cleanup(func() {
				a.sw.Close()
				if a.refClockEncoder != nil {
					_ = a.refClockEncoder.Close()
				}
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
		})
	}
}
