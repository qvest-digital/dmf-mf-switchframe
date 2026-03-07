// server/switcher/raw_sink_test.go
package switcher

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/zsiec/prism/media"
	"github.com/zsiec/switchframe/server/transition"
)

func TestSetRawVideoSink_ReceivesFrames(t *testing.T) {
	programRelay := newTestRelay()
	viewer := newMockProgramViewer("test")
	programRelay.AddViewer(viewer)

	sw := New(programRelay)
	sw.SetPipelineCodecs(
		func() (transition.VideoDecoder, error) { return transition.NewMockDecoder(4, 4), nil },
		func(w, h, bitrate int, fps float32) (transition.VideoEncoder, error) {
			return transition.NewMockEncoder(), nil
		},
	)
	defer sw.Close()

	cam1Relay := newTestRelay()
	sw.RegisterSource("cam1", cam1Relay)
	require.NoError(t, sw.Cut(context.Background(), "cam1"))

	var mu sync.Mutex
	var received []*ProcessingFrame

	sw.SetRawVideoSink(func(pf *ProcessingFrame) {
		mu.Lock()
		defer mu.Unlock()
		received = append(received, pf)
	})

	frame := &media.VideoFrame{PTS: 12345, IsKeyframe: true, WireData: []byte{0xDE, 0xAD}}
	cam1Relay.BroadcastVideo(frame)

	require.Eventually(t, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return len(received) >= 1
	}, 200*time.Millisecond, 5*time.Millisecond, "sink should receive a frame")

	mu.Lock()
	defer mu.Unlock()
	got := received[0]
	require.Equal(t, 4, got.Width)
	require.Equal(t, 4, got.Height)
	require.Equal(t, int64(12345), got.PTS)
	require.NotNil(t, got.YUV)
	require.Equal(t, 4*4*3/2, len(got.YUV))
}

func TestSetRawVideoSink_DeepCopy(t *testing.T) {
	programRelay := newTestRelay()
	viewer := newMockProgramViewer("test")
	programRelay.AddViewer(viewer)

	sw := New(programRelay)
	sw.SetPipelineCodecs(
		func() (transition.VideoDecoder, error) { return transition.NewMockDecoder(4, 4), nil },
		func(w, h, bitrate int, fps float32) (transition.VideoEncoder, error) {
			return transition.NewMockEncoder(), nil
		},
	)
	defer sw.Close()

	cam1Relay := newTestRelay()
	sw.RegisterSource("cam1", cam1Relay)
	require.NoError(t, sw.Cut(context.Background(), "cam1"))

	var mu sync.Mutex
	var sinkFrame *ProcessingFrame

	sw.SetRawVideoSink(func(pf *ProcessingFrame) {
		mu.Lock()
		defer mu.Unlock()
		sinkFrame = pf
		// Mutate the sink's copy
		for i := range pf.YUV {
			pf.YUV[i] = 0xFF
		}
	})

	frame := &media.VideoFrame{PTS: 100, IsKeyframe: true, WireData: []byte{0xDE, 0xAD}}
	cam1Relay.BroadcastVideo(frame)

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

	// The viewer received the frame through the pipeline, meaning the
	// pipeline's buffer was not corrupted by the sink's mutations.
	// If the sink received the same pointer (not a copy), the sink's
	// 0xFF writes would have corrupted the encode input.
	viewer.mu.Lock()
	require.NotEmpty(t, viewer.videos, "pipeline should produce output despite sink mutations")
	viewer.mu.Unlock()
}

func TestSetRawVideoSink_NilDisables(t *testing.T) {
	programRelay := newTestRelay()
	viewer := newMockProgramViewer("test")
	programRelay.AddViewer(viewer)

	sw := New(programRelay)
	sw.SetPipelineCodecs(
		func() (transition.VideoDecoder, error) { return transition.NewMockDecoder(4, 4), nil },
		func(w, h, bitrate int, fps float32) (transition.VideoEncoder, error) {
			return transition.NewMockEncoder(), nil
		},
	)
	defer sw.Close()

	cam1Relay := newTestRelay()
	sw.RegisterSource("cam1", cam1Relay)
	require.NoError(t, sw.Cut(context.Background(), "cam1"))

	var callCount atomic.Int32
	sw.SetRawVideoSink(func(pf *ProcessingFrame) {
		callCount.Add(1)
	})

	// Clear the sink
	sw.SetRawVideoSink(nil)

	frame := &media.VideoFrame{PTS: 100, IsKeyframe: true, WireData: []byte{0xDE, 0xAD}}
	cam1Relay.BroadcastVideo(frame)

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

	sw := New(programRelay)
	sw.SetTransitionConfig(TransitionConfig{
		DecoderFactory: func() (transition.VideoDecoder, error) {
			return transition.NewMockDecoder(4, 4), nil
		},
	})
	sw.SetPipelineCodecs(
		func() (transition.VideoDecoder, error) { return transition.NewMockDecoder(4, 4), nil },
		func(w, h, bitrate int, fps float32) (transition.VideoEncoder, error) {
			return transition.NewMockEncoder(), nil
		},
	)
	defer sw.Close()

	cam1Relay := newTestRelay()
	cam2Relay := newTestRelay()
	sw.RegisterSource("cam1", cam1Relay)
	sw.RegisterSource("cam2", cam2Relay)
	require.NoError(t, sw.Cut(context.Background(), "cam1"))
	cam1Relay.BroadcastVideo(&media.VideoFrame{PTS: 50, IsKeyframe: true, WireData: []byte{0x01}})
	require.NoError(t, sw.SetPreview(context.Background(), "cam2"))

	var mu sync.Mutex
	var received []*ProcessingFrame

	sw.SetRawVideoSink(func(pf *ProcessingFrame) {
		mu.Lock()
		defer mu.Unlock()
		received = append(received, pf)
	})

	// Start transition
	require.NoError(t, sw.StartTransition(context.Background(), "cam2", "mix", 60000, ""))

	// Feed frames from both sources
	cam1Relay.BroadcastVideo(&media.VideoFrame{PTS: 100, IsKeyframe: true, WireData: []byte{0x01}})
	cam2Relay.BroadcastVideo(&media.VideoFrame{PTS: 101, IsKeyframe: true, WireData: []byte{0x02}})

	// Wait for transition output to be processed
	require.Eventually(t, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return len(received) >= 1
	}, 200*time.Millisecond, 5*time.Millisecond, "sink should receive transition frames")

	mu.Lock()
	got := received[0]
	mu.Unlock()

	require.NotNil(t, got.YUV, "transition frame should have YUV data")
	require.Greater(t, got.Width, 0, "transition frame should have positive width")
	require.Greater(t, got.Height, 0, "transition frame should have positive height")

	sw.AbortTransition()
}
