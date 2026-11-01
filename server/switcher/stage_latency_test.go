package switcher

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/zsiec/switchframe/server/clock"
)

// The stage latency gauges describe the program frame of the last tick
// for every source, raw ones (MXL, SRT, playout) included, and on the GPU
// path as on the CPU path: sync_wait from the frame's arrival to the tick
// that shows it, tick from the start of the tick's grain to the end of the
// pipeline run (the raw sinks, and so the MXL writer, have the frame), and
// total from arrival to the end of the pipeline run.
func TestStageLatencyGaugesDescribeARawSourceOnTheGPUPath(t *testing.T) {
	s, _ := newSyncTestSwitcher(t)
	defer s.Close()
	s.RegisterMXLSource("mxl:a")
	require.NoError(t, s.Cut(context.Background(), "mxl:a"))
	s.SetGPUPipeline(&syncLoopGPUMock{
		onRunFromCache:  func(string, int64) error { return nil },
		onRunWithUpload: func([]byte, int, int, int64) error { return nil },
	})

	pts := clock.NowPTS()
	s.IngestRawVideo("mxl:a", &ProcessingFrame{
		Data: make([]byte, 320*240*3/2), Width: 320, Height: 240, PTS: pts, DTS: pts,
	})
	time.Sleep(5 * time.Millisecond)
	// The tick's grain started 7 ms ago.
	s.processVideoTick(clock.Tick{
		VideoPTS:      clock.NowPTS() - 630,
		ReadPTS:       pts,
		AudioSamples:  1600,
		FrameDuration: 33333 * time.Microsecond,
	})

	got := values(gather(t, NewMetricsCollector(s))["switchframe_pipeline_stage_latency_seconds"])
	require.GreaterOrEqual(t, got["sync_wait"], 0.005, "sync_wait %v", got)
	require.GreaterOrEqual(t, got["tick"], 0.007, "tick %v", got)
	require.GreaterOrEqual(t, got["total"], 0.005, "total %v", got)
}
