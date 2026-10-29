package main

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/zsiec/switchframe/server/internal/metricstest"
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

// Each MXL input reports its readers' losses by essence, and the video
// reader's lag, under the input's source label.
func TestMXLReaderCollector(t *testing.T) {
	src := mxl.NewSource(mxl.SourceConfig{FlowName: "mxl:cam1", Width: 16, Height: 16})
	ctx, cancel := context.WithCancel(context.Background())
	src.Start(ctx, waitingFlow{head: new(atomic.Uint64)}, nil)
	defer func() { cancel(); src.Stop() }()

	c := &mxlReaderCollector{sources: func() []*mxl.Source { return []*mxl.Source{src} }}
	require.Eventually(t, func() bool {
		return metricstest.Series(t, c)["switchframe_mxl_reader_lag_grains"]["mxl:cam1"] > 0
	}, time.Second, 5*time.Millisecond, "the head runs ahead of a reader that reads nothing")
	f := metricstest.Series(t, c)
	require.Equal(t, map[string]float64{"video/mxl:cam1": 0}, f["switchframe_mxl_reader_skipped_grains_total"])
	require.Equal(t, map[string]float64{"video/mxl:cam1": 0}, f["switchframe_mxl_reader_resyncs_total"])
}
