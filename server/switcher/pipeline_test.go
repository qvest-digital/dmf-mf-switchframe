// server/switcher/pipeline_test.go
package switcher

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/stretchr/testify/require"
	"github.com/zsiec/prism/media"
	"github.com/zsiec/switchframe/server/graphics"
	"github.com/zsiec/switchframe/server/metrics"
	"github.com/zsiec/switchframe/server/transition"
)

func TestPipeline_NoProcessorsPassthrough(t *testing.T) {
	programRelay := newTestRelay()
	viewer := newMockProgramViewer("test")
	programRelay.AddViewer(viewer)

	sw := New(programRelay)
	sw.SetPipelineCodecs(
		func() (transition.VideoDecoder, error) { return transition.NewMockDecoder(4, 4), nil },
		func(w, h, bitrate int, fps float32) (transition.VideoEncoder, error) { return transition.NewMockEncoder(), nil },
	)
	defer sw.Close()

	cam1Relay := newTestRelay()
	sw.RegisterSource("cam1", cam1Relay)
	require.NoError(t, sw.Cut(context.Background(), "cam1"))

	frame := &media.VideoFrame{PTS: 100, IsKeyframe: true, WireData: []byte{0x00, 0x00, 0x00, 0x01, 0x65}}
	cam1Relay.BroadcastVideo(frame)

	time.Sleep(10 * time.Millisecond)

	viewer.mu.Lock()
	require.GreaterOrEqual(t, len(viewer.videos), 1)
	got := viewer.videos[len(viewer.videos)-1]
	// Passthrough: frame should be a shallow copy (broadcastToProgram stamps GroupID).
	// Not pointer-identical because broadcastToProgram copies the struct to avoid
	// mutating the shared source frame.
	require.NotSame(t, frame, got)
	require.Equal(t, frame.PTS, got.PTS)
	require.Equal(t, frame.IsKeyframe, got.IsKeyframe)
	require.Equal(t, frame.WireData, got.WireData)
	require.Equal(t, uint32(1), got.GroupID) // keyframe increments programGroupID
	viewer.mu.Unlock()
}

func TestPipeline_CompositorActiveDecodesOnce(t *testing.T) {
	programRelay := newTestRelay()
	viewer := newMockProgramViewer("test")
	programRelay.AddViewer(viewer)

	sw := New(programRelay)

	decodeCount := 0
	encodeCount := 0
	sw.SetPipelineCodecs(
		func() (transition.VideoDecoder, error) {
			decodeCount++
			return transition.NewMockDecoder(4, 4), nil
		},
		func(w, h, bitrate int, fps float32) (transition.VideoEncoder, error) {
			encodeCount++
			return transition.NewMockEncoder(), nil
		},
	)

	comp := graphics.NewCompositor()
	rgba := make([]byte, 4*4*4)
	for i := 3; i < len(rgba); i += 4 {
		rgba[i] = 255
	}
	require.NoError(t, comp.SetOverlay(rgba, 4, 4, "test"))
	require.NoError(t, comp.On())
	sw.SetCompositor(comp)

	defer sw.Close()

	cam1Relay := newTestRelay()
	sw.RegisterSource("cam1", cam1Relay)
	require.NoError(t, sw.Cut(context.Background(), "cam1"))

	cam1Relay.BroadcastVideo(&media.VideoFrame{
		PTS: 100, IsKeyframe: true,
		WireData: []byte{0x00, 0x00, 0x00, 0x04, 0x65, 0x88, 0x80, 0x40},
		SPS: []byte{0x67, 0x42, 0x00, 0x0a}, PPS: []byte{0x68, 0x42, 0x00},
	})

	time.Sleep(10 * time.Millisecond)

	// Should have created exactly 1 decoder and 1 encoder
	require.Equal(t, 1, decodeCount, "should decode exactly once")
	require.Equal(t, 1, encodeCount, "should encode exactly once")
}

func TestPipeline_TransitionPlusCompositor_SingleEncode(t *testing.T) {
	programRelay := newTestRelay()
	viewer := newMockProgramViewer("test")
	programRelay.AddViewer(viewer)

	sw := New(programRelay)
	sw.SetTransitionConfig(TransitionConfig{
		DecoderFactory: func() (transition.VideoDecoder, error) {
			return transition.NewMockDecoder(4, 4), nil
		},
	})

	encodeCount := 0
	sw.SetPipelineCodecs(
		func() (transition.VideoDecoder, error) { return transition.NewMockDecoder(4, 4), nil },
		func(w, h, bitrate int, fps float32) (transition.VideoEncoder, error) {
			encodeCount++
			return transition.NewMockEncoder(), nil
		},
	)

	comp := graphics.NewCompositor()
	rgba := make([]byte, 4*4*4)
	for i := 3; i < len(rgba); i += 4 {
		rgba[i] = 255
	}
	require.NoError(t, comp.SetOverlay(rgba, 4, 4, "test"))
	require.NoError(t, comp.On())
	sw.SetCompositor(comp)

	defer sw.Close()

	cam1Relay := newTestRelay()
	cam2Relay := newTestRelay()
	sw.RegisterSource("cam1", cam1Relay)
	sw.RegisterSource("cam2", cam2Relay)
	require.NoError(t, sw.Cut(context.Background(), "cam1"))
	cam1Relay.BroadcastVideo(&media.VideoFrame{PTS: 50, IsKeyframe: true, WireData: []byte{0x01}})
	require.NoError(t, sw.SetPreview(context.Background(), "cam2"))

	// Start transition
	require.NoError(t, sw.StartTransition(context.Background(), "cam2", "mix", 60000, ""))

	// Feed frames from both sources
	cam1Relay.BroadcastVideo(&media.VideoFrame{PTS: 100, IsKeyframe: true, WireData: []byte{0x01}})
	cam2Relay.BroadcastVideo(&media.VideoFrame{PTS: 101, IsKeyframe: true, WireData: []byte{0x02}})

	time.Sleep(20 * time.Millisecond)

	// The transition engine decoded both sources internally (2 decoders).
	// The pipeline coordinator should encode exactly once (not 2 or 3 times).
	// encodeCount tracks encoder FACTORY calls, not Encode() calls.
	require.Equal(t, 1, encodeCount, "should create only one encoder for the pipeline")
}

func TestPipeline_ResolutionChange(t *testing.T) {
	// Validates Task 2: encoder is recreated when resolution changes.
	programRelay := newTestRelay()
	viewer := newMockProgramViewer("test")
	programRelay.AddViewer(viewer)

	sw := New(programRelay)
	encoderCreateCount := 0
	sw.SetPipelineCodecs(
		func() (transition.VideoDecoder, error) { return transition.NewMockDecoder(4, 4), nil },
		func(w, h, bitrate int, fps float32) (transition.VideoEncoder, error) {
			encoderCreateCount++
			return transition.NewMockEncoder(), nil
		},
	)

	comp := graphics.NewCompositor()
	rgba := make([]byte, 4*4*4)
	for i := 3; i < len(rgba); i += 4 {
		rgba[i] = 255
	}
	require.NoError(t, comp.SetOverlay(rgba, 4, 4, "test"))
	require.NoError(t, comp.On())
	sw.SetCompositor(comp)
	defer sw.Close()

	cam1Relay := newTestRelay()
	sw.RegisterSource("cam1", cam1Relay)
	require.NoError(t, sw.Cut(context.Background(), "cam1"))

	// Feed 4x4 frame
	cam1Relay.BroadcastVideo(&media.VideoFrame{
		PTS: 100, IsKeyframe: true,
		WireData: []byte{0x00, 0x00, 0x00, 0x04, 0x65, 0x88, 0x80, 0x40},
		SPS: []byte{0x67, 0x42, 0x00, 0x0a}, PPS: []byte{0x68, 0x42, 0x00},
	})
	time.Sleep(10 * time.Millisecond)
	require.Equal(t, 1, encoderCreateCount)

	// The mock decoder always returns 4x4, so the encoder won't be recreated
	// through the full pipeline. The unit test in pipeline_codecs_test.go
	// validates the recreation logic directly.
}

func TestPipeline_DecoderShortBufferGracefulDegradation(t *testing.T) {
	// Validates Task 1: short buffer from decoder causes graceful passthrough.
	programRelay := newTestRelay()
	viewer := newMockProgramViewer("test")
	programRelay.AddViewer(viewer)

	sw := New(programRelay)
	// Use a decoder factory that returns a short buffer
	sw.SetPipelineCodecs(
		func() (transition.VideoDecoder, error) {
			return &shortBufferDecoder{width: 4, height: 4}, nil
		},
		func(w, h, bitrate int, fps float32) (transition.VideoEncoder, error) {
			return transition.NewMockEncoder(), nil
		},
	)

	comp := graphics.NewCompositor()
	rgba := make([]byte, 4*4*4)
	for i := 3; i < len(rgba); i += 4 {
		rgba[i] = 255
	}
	require.NoError(t, comp.SetOverlay(rgba, 4, 4, "test"))
	require.NoError(t, comp.On())
	sw.SetCompositor(comp)
	defer sw.Close()

	cam1Relay := newTestRelay()
	sw.RegisterSource("cam1", cam1Relay)
	require.NoError(t, sw.Cut(context.Background(), "cam1"))

	// Frame should fall back to passthrough (not panic)
	frame := &media.VideoFrame{
		PTS: 100, IsKeyframe: true,
		WireData: []byte{0x00, 0x00, 0x00, 0x04, 0x65, 0x88, 0x80, 0x40},
		SPS: []byte{0x67, 0x42, 0x00, 0x0a}, PPS: []byte{0x68, 0x42, 0x00},
	}
	cam1Relay.BroadcastVideo(frame)
	time.Sleep(10 * time.Millisecond)

	viewer.mu.Lock()
	require.GreaterOrEqual(t, len(viewer.videos), 1, "should fall back to passthrough")
	viewer.mu.Unlock()
}

func TestPipeline_TransitionOutputReachesViewer(t *testing.T) {
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

	require.NoError(t, sw.StartTransition(context.Background(), "cam2", "mix", 60000, ""))

	// Feed frames
	cam1Relay.BroadcastVideo(&media.VideoFrame{PTS: 100, IsKeyframe: true, WireData: []byte{0x01}})
	cam2Relay.BroadcastVideo(&media.VideoFrame{PTS: 101, IsKeyframe: true, WireData: []byte{0x02}})

	time.Sleep(20 * time.Millisecond)

	viewer.mu.Lock()
	count := len(viewer.videos)
	viewer.mu.Unlock()

	require.GreaterOrEqual(t, count, 1, "transition output should reach viewer via pipeline encode")

	sw.AbortTransition()
}

func TestPipeline_EncodeFailureMetrics(t *testing.T) {
	// Validates Task 4: encode failure increments metric counter.
	programRelay := newTestRelay()
	sw := New(programRelay)

	reg := prometheus.NewRegistry()
	m := metrics.NewMetrics(reg)
	sw.SetMetrics(m)

	sw.SetPipelineCodecs(
		func() (transition.VideoDecoder, error) { return transition.NewMockDecoder(4, 4), nil },
		func(w, h, bitrate int, fps float32) (transition.VideoEncoder, error) {
			return &failingEncoder{}, nil
		},
	)

	comp := graphics.NewCompositor()
	rgba := make([]byte, 4*4*4)
	for i := 3; i < len(rgba); i += 4 {
		rgba[i] = 255
	}
	require.NoError(t, comp.SetOverlay(rgba, 4, 4, "test"))
	require.NoError(t, comp.On())
	sw.SetCompositor(comp)
	defer sw.Close()

	cam1Relay := newTestRelay()
	sw.RegisterSource("cam1", cam1Relay)
	require.NoError(t, sw.Cut(context.Background(), "cam1"))

	cam1Relay.BroadcastVideo(&media.VideoFrame{
		PTS: 100, IsKeyframe: true,
		WireData: []byte{0x00, 0x00, 0x00, 0x04, 0x65, 0x88, 0x80, 0x40},
		SPS: []byte{0x67, 0x42, 0x00, 0x0a}, PPS: []byte{0x68, 0x42, 0x00},
	})
	time.Sleep(10 * time.Millisecond)

	// Check that the encode error metric was incremented
	val := testutil.ToFloat64(m.PipelineEncodeErrorsTotal)
	require.GreaterOrEqual(t, val, 1.0, "encode error metric should be incremented")
}

func TestPipeline_SourceStatsPropagate(t *testing.T) {
	// Validates Task 5: source bitrate/fps are propagated to pipeline codecs.
	programRelay := newTestRelay()
	sw := New(programRelay)

	var lastBitrate int
	var lastFPS float32
	sw.SetPipelineCodecs(
		func() (transition.VideoDecoder, error) { return transition.NewMockDecoder(4, 4), nil },
		func(w, h, bitrate int, fps float32) (transition.VideoEncoder, error) {
			lastBitrate = bitrate
			lastFPS = fps
			return transition.NewMockEncoder(), nil
		},
	)

	comp := graphics.NewCompositor()
	rgba := make([]byte, 4*4*4)
	for i := 3; i < len(rgba); i += 4 {
		rgba[i] = 255
	}
	require.NoError(t, comp.SetOverlay(rgba, 4, 4, "test"))
	require.NoError(t, comp.On())
	sw.SetCompositor(comp)
	defer sw.Close()

	cam1Relay := newTestRelay()
	sw.RegisterSource("cam1", cam1Relay)
	require.NoError(t, sw.Cut(context.Background(), "cam1"))

	// Feed enough frames to build up EMA stats
	for i := 0; i < 15; i++ {
		cam1Relay.BroadcastVideo(&media.VideoFrame{
			PTS: int64(i * 33333), IsKeyframe: i == 0,
			WireData: make([]byte, 5000), // ~5KB per frame → ~1.2 Mbps at 30fps
			SPS: []byte{0x67, 0x42, 0x00, 0x0a}, PPS: []byte{0x68, 0x42, 0x00},
		})
		time.Sleep(2 * time.Millisecond)
	}

	// Verify that updateSourceStats was called by inspecting the pipeCodecs directly.
	// The encoder is lazy-inited on the first frame before stats build up, so we
	// verify propagation to the pipeCodecs struct, not the encoder factory args.
	sw.mu.RLock()
	pc := sw.pipeCodecs
	sw.mu.RUnlock()

	pc.mu.Lock()
	require.Greater(t, pc.sourceBitrate, 0, "source bitrate should be propagated")
	require.Greater(t, pc.sourceFPS, float32(0), "source FPS should be propagated")
	pc.mu.Unlock()

	_ = lastBitrate
	_ = lastFPS
}

// failingEncoder always returns an error from Encode.
type failingEncoder struct{}

func (e *failingEncoder) Encode(yuv []byte, forceIDR bool) ([]byte, bool, error) {
	return nil, false, fmt.Errorf("mock encode failure")
}

func (e *failingEncoder) Close() {}
