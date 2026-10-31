package main

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/zsiec/switchframe/server/clock"
	"github.com/zsiec/switchframe/server/internal/metricstest"
	"github.com/zsiec/switchframe/server/metrics"
	"github.com/zsiec/switchframe/server/mxl"
)

// waitingFlow is a video flow whose head moves on while it delivers
// nothing, so a reader falls behind it.
type waitingFlow struct{ head *atomic.Uint64 }

func (waitingFlow) ReadGrain(uint64, uint64) ([]byte, mxl.GrainInfo, error) {
	return nil, mxl.GrainInfo{}, mxl.ErrTimeout
}
func (waitingFlow) ConfigInfo() mxl.FlowConfig {
	return mxl.FlowConfig{Format: mxl.DataFormatVideo, GrainRate: mxl.Rational{Numerator: 50, Denominator: 1}}
}
func (f waitingFlow) HeadIndex() (uint64, error) { return f.head.Add(1), nil }
func (waitingFlow) Close() error                 { return nil }

// silentAudio is an audio flow that delivers nothing.
type silentAudio struct{}

func (silentAudio) ReadSamples(uint64, int, uint64) ([][]float32, error) {
	return nil, mxl.ErrTimeout
}
func (silentAudio) ConfigInfo() mxl.FlowConfig {
	return mxl.FlowConfig{Format: mxl.DataFormatAudio, GrainRate: mxl.Rational{Numerator: 48000, Denominator: 1}, ChannelCount: 2}
}
func (silentAudio) HeadIndex() (uint64, error) { return 0, nil }
func (silentAudio) Close() error               { return nil }

// Each MXL input reports its readers' re-syncs by essence, and the video
// reader's skips and lag, the only reader that skips, under the input's
// source label.
func TestMXLReaderCollector(t *testing.T) {
	src := mxl.NewSource(mxl.SourceConfig{FlowName: "mxl:cam1", Width: 16, Height: 16})
	ctx, cancel := context.WithCancel(context.Background())
	src.Start(ctx, waitingFlow{head: new(atomic.Uint64)}, silentAudio{})
	defer func() { cancel(); src.Stop() }()

	c := &mxlReaderCollector{sources: func() []*mxl.Source { return []*mxl.Source{src} }}
	require.Eventually(t, func() bool {
		return metricstest.Series(t, c)["switchframe_mxl_reader_lag_grains"]["mxl:cam1"] > 0
	}, time.Second, 5*time.Millisecond, "the head runs ahead of a reader that reads nothing")
	f := metricstest.Series(t, c)
	require.Equal(t, map[string]float64{"mxl:cam1": 0}, f["switchframe_mxl_reader_skipped_grains_total"])
	require.Equal(t, map[string]float64{"audio/mxl:cam1": 0, "video/mxl:cam1": 0}, f["switchframe_mxl_reader_resyncs_total"])
}

// grainSink is a video flow writer that takes every grain.
type grainSink struct{ grains atomic.Int64 }

func (g *grainSink) WriteGrain(uint64, []byte) error { g.grains.Add(1); return nil }
func (g *grainSink) Close() error                    { return nil }

// sampleSink is an audio flow writer that takes every batch.
type sampleSink struct{}

func (sampleSink) WriteSamples(uint64, [][]float32) error { return nil }
func (sampleSink) Close() error                           { return nil }

// registrySeries gathers the registry /metrics serves and returns the
// unlabelled series of each family by name.
func registrySeries(t *testing.T) map[string]float64 {
	t.Helper()
	families, err := metrics.GetRegistry().Gather()
	require.NoError(t, err)
	out := map[string]float64{}
	for _, f := range families {
		for _, m := range f.GetMetric() {
			if len(m.GetLabel()) == 0 && m.Counter != nil {
				out[f.GetName()] = m.GetCounter().GetValue()
			}
		}
	}
	return out
}

// The program MXL writer's dropped audio batches and video frames, each
// dropped for landing on media time already written, are exported, so a
// program that loses media to its own timeline shows on /metrics.
func TestMXLWriterMetrics(t *testing.T) {
	out := mxl.NewOutput(mxl.OutputConfig{FlowName: "pgm", Width: 12, Height: 2, VideoRate: mxl.Rational{Numerator: 50, Denominator: 1}})
	video := &grainSink{}
	ctx, cancel := context.WithCancel(context.Background())
	out.Start(ctx, video, sampleSink{}, nil, nil)
	defer func() { cancel(); out.Stop() }()
	w := out.Writer()

	rate := clock.Rate{Num: 50, Den: 1}
	pts := clock.IndexPTS(rate, clock.TimestampToIndex(rate, clock.TAINow()))
	yuv := make([]byte, 12*2+2*6*1)
	w.WriteVideo(yuv, 12, 2, pts)
	require.Eventually(t, func() bool { return video.grains.Load() > 0 }, time.Second, time.Millisecond)
	w.WriteVideo(yuv, 12, 2, pts) // its grain is written already

	pcm := make([]float32, 2*1024)
	apts := clock.NowPTS()
	w.WriteAudio(pcm, apts, 48000, 2)
	w.WriteAudio(pcm, apts, 48000, 2) // overlaps the batch before

	a := &App{appMetrics: &metrics.Metrics{}, mxlOutput: out}
	a.registerSubsystemCollectors()
	require.Eventually(t, func() bool {
		return registrySeries(t)["switchframe_mxl_writer_video_index_drops_total"] == 1
	}, time.Second, 5*time.Millisecond, "series %v", registrySeries(t))
	require.Equal(t, float64(1), registrySeries(t)["switchframe_mxl_writer_audio_overlaps_total"])
}
