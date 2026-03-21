// server/switcher/raw_sink_test.go
package switcher

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/zsiec/switchframe/server/transition"
)

func TestSetRawVideoSink_ReceivesFrames(t *testing.T) {
	// In always-decode mode, frames arrive as raw YUV and flow through
	// broadcastProcessedFromPF → pipeline (raw-sink node taps, then encode).
	programRelay := newTestRelay()
	viewer := newMockProgramViewer("test")
	programRelay.AddViewer(viewer)

	sw := newTestSwitcher(programRelay)
	sw.SetPipelineCodecs(
		func(w, h, bitrate, fpsNum, fpsDen int) (transition.VideoEncoder, error) {
			return transition.NewMockEncoder(), nil
		},
	)

	type sinkSnapshot struct {
		Width, Height int
		PTS           int64
		YUVLen        int
	}
	var mu sync.Mutex
	var received []sinkSnapshot

	// Set sink BEFORE BuildPipeline so rawSinkNode.Active() == true at build time.
	// Capture data inside the callback — the frame's YUV is released after callback returns.
	sw.SetRawVideoSink(func(pf *ProcessingFrame) {
		mu.Lock()
		defer mu.Unlock()
		received = append(received, sinkSnapshot{
			Width: pf.Width, Height: pf.Height,
			PTS: pf.PTS, YUVLen: len(pf.YUV),
		})
	})
	require.NoError(t, sw.BuildPipeline())
	defer sw.Close()

	cam1Relay := newTestRelay()
	sw.RegisterSource("cam1", cam1Relay)
	require.NoError(t, sw.Cut(context.Background(), "cam1"))

	sendRawFrame(sw, "cam1", 12345, true)

	require.Eventually(t, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return len(received) >= 1
	}, 200*time.Millisecond, 5*time.Millisecond, "sink should receive a frame")

	mu.Lock()
	defer mu.Unlock()
	got := received[0]
	// The raw sink receives the frame as-is from handleRawVideoFrame.
	// The frame dimensions depend on the pipeline format (320x240 in tests)
	// because the frame pool allocates at pipeline resolution.
	require.Equal(t, int64(12345), got.PTS)
	require.Greater(t, got.YUVLen, 0, "sink should receive non-empty YUV data")
}

func TestSetRawVideoSink_DeepCopy(t *testing.T) {
	programRelay := newTestRelay()
	viewer := newMockProgramViewer("test")
	programRelay.AddViewer(viewer)

	sw := newTestSwitcher(programRelay)
	sw.SetPipelineCodecs(
		func(w, h, bitrate, fpsNum, fpsDen int) (transition.VideoEncoder, error) {
			return transition.NewMockEncoder(), nil
		},
	)

	var mu sync.Mutex
	var sinkFrame *ProcessingFrame

	// Set sink BEFORE BuildPipeline so rawSinkNode.Active() == true at build time.
	sw.SetRawVideoSink(func(pf *ProcessingFrame) {
		mu.Lock()
		defer mu.Unlock()
		sinkFrame = pf
		// Mutate the sink's copy
		for i := range pf.YUV {
			pf.YUV[i] = 0xFF
		}
	})
	require.NoError(t, sw.BuildPipeline())
	defer sw.Close()

	cam1Relay := newTestRelay()
	sw.RegisterSource("cam1", cam1Relay)
	require.NoError(t, sw.Cut(context.Background(), "cam1"))

	sendRawFrame(sw, "cam1", 100, true)

	// Wait for the frame to be processed and the sink to be called
	require.Eventually(t, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return sinkFrame != nil
	}, 200*time.Millisecond, 5*time.Millisecond, "sink should receive a frame")

	// Wait for the pipeline to finish (frame should reach viewer)
	require.Eventually(t, func() bool {
		viewer.mu.Lock()
		defer viewer.mu.Unlock()
		return len(viewer.videos) >= 1
	}, 200*time.Millisecond, 5*time.Millisecond, "frame should reach program viewer")

	viewer.mu.Lock()
	require.NotEmpty(t, viewer.videos, "pipeline should produce output despite sink mutations")
	viewer.mu.Unlock()
}

func TestSetRawVideoSink_NilDisables(t *testing.T) {
	programRelay := newTestRelay()
	viewer := newMockProgramViewer("test")
	programRelay.AddViewer(viewer)

	sw := newTestSwitcher(programRelay)
	sw.SetPipelineCodecs(
		func(w, h, bitrate, fpsNum, fpsDen int) (transition.VideoEncoder, error) {
			return transition.NewMockEncoder(), nil
		},
	)

	var callCount atomic.Int32
	// Set sink BEFORE BuildPipeline so rawSinkNode is active in the pipeline.
	sw.SetRawVideoSink(func(pf *ProcessingFrame) {
		callCount.Add(1)
	})
	require.NoError(t, sw.BuildPipeline())
	defer sw.Close()

	cam1Relay := newTestRelay()
	sw.RegisterSource("cam1", cam1Relay)
	require.NoError(t, sw.Cut(context.Background(), "cam1"))

	// Clear the sink — node is still in pipeline but Process() skips atomically.
	sw.SetRawVideoSink(nil)

	sendRawFrame(sw, "cam1", 100, true)

	// Wait for pipeline to finish processing
	require.Eventually(t, func() bool {
		viewer.mu.Lock()
		defer viewer.mu.Unlock()
		return len(viewer.videos) >= 1
	}, 200*time.Millisecond, 5*time.Millisecond, "frame should reach viewer")

	require.Equal(t, int32(0), callCount.Load(), "sink should not be called after clearing")
}

func TestSetRawVideoSink_TransitionFrames(t *testing.T) {
	programRelay := newTestRelay()
	viewer := newMockProgramViewer("test")
	programRelay.AddViewer(viewer)

	sw := newTestSwitcher(programRelay)
	sw.SetTransitionConfig(TransitionConfig{})
	sw.SetPipelineCodecs(
		func(w, h, bitrate, fpsNum, fpsDen int) (transition.VideoEncoder, error) {
			return transition.NewMockEncoder(), nil
		},
	)

	type sinkSnapshot struct {
		Width, Height int
		HasYUV        bool
	}
	var mu sync.Mutex
	var received []sinkSnapshot

	// Set sink BEFORE BuildPipeline so rawSinkNode.Active() == true at build time.
	// Capture data inside the callback — the frame's YUV is released after callback returns.
	sw.SetRawVideoSink(func(pf *ProcessingFrame) {
		mu.Lock()
		defer mu.Unlock()
		received = append(received, sinkSnapshot{
			Width: pf.Width, Height: pf.Height,
			HasYUV: pf.YUV != nil,
		})
	})
	require.NoError(t, sw.BuildPipeline())
	defer sw.Close()

	cam1Relay := newTestRelay()
	cam2Relay := newTestRelay()
	sw.RegisterSource("cam1", cam1Relay)
	sw.RegisterSource("cam2", cam2Relay)
	require.NoError(t, sw.Cut(context.Background(), "cam1"))
	require.NoError(t, sw.SetPreview(context.Background(), "cam2"))

	// Start transition
	require.NoError(t, sw.StartTransition(context.Background(), "cam2", "mix", 60000, ""))

	// Feed raw YUV frames from both sources
	sendRawFrame(sw, "cam1", 100, true)
	sendRawFrame(sw, "cam2", 101, true)

	// Wait for transition output to be processed
	require.Eventually(t, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return len(received) >= 1
	}, 200*time.Millisecond, 5*time.Millisecond, "sink should receive transition frames")

	mu.Lock()
	got := received[0]
	mu.Unlock()

	require.True(t, got.HasYUV, "transition frame should have YUV data")
	require.Greater(t, got.Width, 0, "transition frame should have positive width")
	require.Greater(t, got.Height, 0, "transition frame should have positive height")

	sw.AbortTransition()
}
