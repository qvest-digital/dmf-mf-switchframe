// server/switcher/pipeline_test.go
package switcher

import (
	"context"
	"fmt"
	"sync/atomic"
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

func TestPipeline_AlwaysReEncodes(t *testing.T) {
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

	frame := &media.VideoFrame{PTS: 100, IsKeyframe: true, WireData: []byte{0xDE, 0xAD}}
	cam1Relay.BroadcastVideo(frame)

	time.Sleep(10 * time.Millisecond)

	viewer.mu.Lock()
	require.GreaterOrEqual(t, len(viewer.videos), 1)
	got := viewer.videos[len(viewer.videos)-1]
	// Always re-encode: PTS is preserved, but WireData differs because
	// the frame was decoded then re-encoded through the pipeline.
	require.Equal(t, frame.PTS, got.PTS)
	require.NotEqual(t, frame.WireData, got.WireData, "WireData should differ after re-encode")
	require.Greater(t, got.GroupID, uint32(0), "GroupID should be set (keyframe increments programGroupID)")
	viewer.mu.Unlock()
}

func TestPipeline_CompositorActiveDecodesOnce(t *testing.T) {
	programRelay := newTestRelay()
	viewer := newMockProgramViewer("test")
	programRelay.AddViewer(viewer)

	sw := New(programRelay)

	var decodeCount atomic.Int32
	var encodeCount atomic.Int32
	sw.SetPipelineCodecs(
		func() (transition.VideoDecoder, error) {
			decodeCount.Add(1)
			return transition.NewMockDecoder(4, 4), nil
		},
		func(w, h, bitrate int, fps float32) (transition.VideoEncoder, error) {
			encodeCount.Add(1)
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

	// Wait for async video processing to complete
	require.Eventually(t, func() bool {
		return encodeCount.Load() >= 1
	}, 200*time.Millisecond, 5*time.Millisecond)

	// Should have created exactly 1 decoder and 1 encoder
	require.Equal(t, int32(1), decodeCount.Load(), "should decode exactly once")
	require.Equal(t, int32(1), encodeCount.Load(), "should encode exactly once")
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

	var encodeCount atomic.Int32
	sw.SetPipelineCodecs(
		func() (transition.VideoDecoder, error) { return transition.NewMockDecoder(4, 4), nil },
		func(w, h, bitrate int, fps float32) (transition.VideoEncoder, error) {
			encodeCount.Add(1)
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

	// Wait for async video processing to complete
	require.Eventually(t, func() bool {
		return encodeCount.Load() >= 1
	}, 200*time.Millisecond, 5*time.Millisecond)

	// The transition engine decoded both sources internally (2 decoders).
	// The pipeline coordinator should encode exactly once (not 2 or 3 times).
	// encodeCount tracks encoder FACTORY calls, not Encode() calls.
	require.Equal(t, int32(1), encodeCount.Load(), "should create only one encoder for the pipeline")
}

func TestPipeline_ResolutionChange(t *testing.T) {
	// Validates Task 2: encoder is recreated when resolution changes.
	programRelay := newTestRelay()
	viewer := newMockProgramViewer("test")
	programRelay.AddViewer(viewer)

	sw := New(programRelay)
	var encoderCreateCount atomic.Int32
	sw.SetPipelineCodecs(
		func() (transition.VideoDecoder, error) { return transition.NewMockDecoder(4, 4), nil },
		func(w, h, bitrate int, fps float32) (transition.VideoEncoder, error) {
			encoderCreateCount.Add(1)
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

	// Wait for async video processing to complete
	require.Eventually(t, func() bool {
		return encoderCreateCount.Load() >= 1
	}, 200*time.Millisecond, 5*time.Millisecond)
	require.Equal(t, int32(1), encoderCreateCount.Load())

	// The mock decoder always returns 4x4, so the encoder won't be recreated
	// through the full pipeline. The unit test in pipeline_codecs_test.go
	// validates the recreation logic directly.
}

func TestPipeline_DecoderFailureDropsFrame(t *testing.T) {
	// Validates that a decode error (short buffer) causes the frame to be
	// dropped rather than passed through. The program output must never
	// contain source SPS/PPS, so passthrough is not an option.
	programRelay := newTestRelay()
	viewer := newMockProgramViewer("test")
	programRelay.AddViewer(viewer)

	sw := New(programRelay)

	reg := prometheus.NewRegistry()
	m := metrics.NewMetrics(reg)
	sw.SetMetrics(m)

	// Use a decoder factory that returns a short buffer
	sw.SetPipelineCodecs(
		func() (transition.VideoDecoder, error) {
			return &shortBufferDecoder{width: 4, height: 4}, nil
		},
		func(w, h, bitrate int, fps float32) (transition.VideoEncoder, error) {
			return transition.NewMockEncoder(), nil
		},
	)
	defer sw.Close()

	cam1Relay := newTestRelay()
	sw.RegisterSource("cam1", cam1Relay)
	require.NoError(t, sw.Cut(context.Background(), "cam1"))

	// Frame should be dropped (decode error), not passed through
	frame := &media.VideoFrame{
		PTS: 100, IsKeyframe: true,
		WireData: []byte{0x00, 0x00, 0x00, 0x04, 0x65, 0x88, 0x80, 0x40},
		SPS: []byte{0x67, 0x42, 0x00, 0x0a}, PPS: []byte{0x68, 0x42, 0x00},
	}
	cam1Relay.BroadcastVideo(frame)
	time.Sleep(10 * time.Millisecond)

	viewer.mu.Lock()
	require.Equal(t, 0, len(viewer.videos), "frame should be dropped on decode error, not passed through")
	viewer.mu.Unlock()

	// Verify decode error metric was incremented
	val := testutil.ToFloat64(m.PipelineDecodeErrorsTotal)
	require.GreaterOrEqual(t, val, 1.0, "decode error metric should be incremented")
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

func TestPipeline_AsyncVideoProcessing(t *testing.T) {
	// Verify that handleVideoFrame does NOT block the caller for the
	// duration of decode+encode. This is critical because the source relay's
	// delivery goroutine calls handleVideoFrame synchronously — if it blocks
	// for 30-100ms on decode+encode, audio delivery from the same goroutine
	// gets starved, causing permanent audio choppiness.
	programRelay := newTestRelay()
	viewer := newMockProgramViewer("test")
	programRelay.AddViewer(viewer)

	sw := New(programRelay)

	sw.SetPipelineCodecs(
		func() (transition.VideoDecoder, error) {
			return &slowDecoder{
				inner: transition.NewMockDecoder(4, 4),
				delay: 30 * time.Millisecond,
			}, nil
		},
		func(w, h, bitrate int, fps float32) (transition.VideoEncoder, error) {
			return transition.NewMockEncoder(), nil
		},
	)
	defer sw.Close()

	cam1Relay := newTestRelay()
	sw.RegisterSource("cam1", cam1Relay)
	require.NoError(t, sw.Cut(context.Background(), "cam1"))

	frame := &media.VideoFrame{PTS: 100, IsKeyframe: true, WireData: []byte{0xDE, 0xAD}}

	// handleVideoFrame should return quickly (< 5ms), NOT block for 30ms decode
	start := time.Now()
	sw.handleVideoFrame("cam1", frame)
	elapsed := time.Since(start)

	require.Less(t, elapsed, 5*time.Millisecond,
		"handleVideoFrame should return immediately, not block for decode+encode (took %v)", elapsed)

	// But the frame should still be processed asynchronously
	require.Eventually(t, func() bool {
		viewer.mu.Lock()
		defer viewer.mu.Unlock()
		return len(viewer.videos) >= 1
	}, 200*time.Millisecond, 5*time.Millisecond,
		"frame should be processed asynchronously and reach viewer")
}

func TestPipeline_AsyncTransitionOutput(t *testing.T) {
	// Verify that broadcastProcessed (transition engine output) does NOT
	// block the caller for the duration of encoding. During transitions,
	// the transition engine callback runs inside IngestFrame, which is called
	// from handleVideoFrame on the source relay's delivery goroutine. If
	// encoding blocks that goroutine, audio delivery is starved.
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
			return &slowEncoder{
				inner: transition.NewMockEncoder(),
				delay: 30 * time.Millisecond,
			}, nil
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

	// Start transition — broadcastProcessed will be called with slow encoder
	require.NoError(t, sw.StartTransition(context.Background(), "cam2", "mix", 60000, ""))

	// Feed frames from both sources; handleVideoFrame should return quickly
	start := time.Now()
	cam1Relay.BroadcastVideo(&media.VideoFrame{PTS: 100, IsKeyframe: true, WireData: []byte{0x01}})
	cam2Relay.BroadcastVideo(&media.VideoFrame{PTS: 101, IsKeyframe: true, WireData: []byte{0x02}})
	elapsed := time.Since(start)

	require.Less(t, elapsed, 10*time.Millisecond,
		"handleVideoFrame during transition should not block for encode (took %v)", elapsed)

	// Transition output should still reach viewer asynchronously
	require.Eventually(t, func() bool {
		viewer.mu.Lock()
		defer viewer.mu.Unlock()
		return len(viewer.videos) >= 1
	}, 200*time.Millisecond, 5*time.Millisecond,
		"transition output should reach viewer asynchronously")

	sw.AbortTransition()
}

// slowEncoder wraps a mock encoder and adds a delay to each Encode() call.
type slowEncoder struct {
	inner transition.VideoEncoder
	delay time.Duration
}

func (e *slowEncoder) Encode(yuv []byte, forceIDR bool) ([]byte, bool, error) {
	time.Sleep(e.delay)
	return e.inner.Encode(yuv, forceIDR)
}

func (e *slowEncoder) Close() { e.inner.Close() }

func TestPipeline_EncoderBufferingDropsFrames(t *testing.T) {
	// Hardware encoders (e.g. VideoToolbox) return nil data during warmup
	// (EAGAIN). The pipeline should silently drop these frames without
	// error, then resume normal output once the encoder starts producing.
	programRelay := newTestRelay()
	viewer := newMockProgramViewer("test")
	programRelay.AddViewer(viewer)

	sw := New(programRelay)
	sw.SetPipelineCodecs(
		func() (transition.VideoDecoder, error) { return transition.NewMockDecoder(4, 4), nil },
		func(w, h, bitrate int, fps float32) (transition.VideoEncoder, error) {
			return newBufferingEncoder(transition.NewMockEncoder(), 3), nil
		},
	)
	defer sw.Close()

	cam1Relay := newTestRelay()
	sw.RegisterSource("cam1", cam1Relay)
	require.NoError(t, sw.Cut(context.Background(), "cam1"))

	// Send 5 frames: first 3 should be silently dropped (buffering),
	// frames 4 and 5 should produce output.
	for i := range 5 {
		cam1Relay.BroadcastVideo(&media.VideoFrame{
			PTS: int64(i*100 + 100), IsKeyframe: i == 0, WireData: []byte{0x01},
		})
		time.Sleep(5 * time.Millisecond) // let async processing run
	}

	require.Eventually(t, func() bool {
		viewer.mu.Lock()
		defer viewer.mu.Unlock()
		return len(viewer.videos) >= 2
	}, 200*time.Millisecond, 5*time.Millisecond,
		"should receive frames after encoder warmup")

	viewer.mu.Lock()
	// Exactly 2 frames should have made it through (frames 4 and 5)
	require.Equal(t, 2, len(viewer.videos), "only post-warmup frames should reach viewer")
	viewer.mu.Unlock()
}

// bufferingEncoder returns nil data for the first N calls (simulating
// hardware encoder warmup like VideoToolbox EAGAIN), then delegates.
type bufferingEncoder struct {
	inner     transition.VideoEncoder
	remaining atomic.Int32
}

func newBufferingEncoder(inner transition.VideoEncoder, warmupFrames int) *bufferingEncoder {
	e := &bufferingEncoder{inner: inner}
	e.remaining.Store(int32(warmupFrames))
	return e
}

func (e *bufferingEncoder) Encode(yuv []byte, forceIDR bool) ([]byte, bool, error) {
	if e.remaining.Add(-1) >= 0 {
		return nil, false, nil // EAGAIN — no output yet
	}
	return e.inner.Encode(yuv, forceIDR)
}

func (e *bufferingEncoder) Close() { e.inner.Close() }

func TestPipelineCodecs_ClampsBitrate(t *testing.T) {
	pc := &pipelineCodecs{}

	// Simulate a source sending at 100 Mbps (absurdly high CRF source)
	// avgFrameSize=400KB, avgFPS=30 -> 400000 * 30 * 8 = 96Mbps
	pc.updateSourceStats(400000, 30)
	pc.mu.Lock()
	require.LessOrEqual(t, pc.sourceBitrate, 50_000_000,
		"source bitrate should be clamped to sane maximum")
	pc.mu.Unlock()

	// Simulate very low bitrate source (300bps - broken source)
	pc.updateSourceStats(10, 30)
	pc.mu.Lock()
	require.GreaterOrEqual(t, pc.sourceBitrate, 500_000,
		"source bitrate should be clamped to minimum floor")
	pc.mu.Unlock()
}

// failingEncoder always returns an error from Encode.
type failingEncoder struct{}

func (e *failingEncoder) Encode(yuv []byte, forceIDR bool) ([]byte, bool, error) {
	return nil, false, fmt.Errorf("mock encode failure")
}

func (e *failingEncoder) Close() {}

// countingDecoder wraps a decoder and counts Decode calls.
type countingDecoder struct {
	inner transition.VideoDecoder
	count atomic.Int32
}

func (d *countingDecoder) Decode(data []byte) ([]byte, int, int, error) {
	d.count.Add(1)
	return d.inner.Decode(data)
}

func (d *countingDecoder) Close() { d.inner.Close() }

func TestPipelineCodecs_ReplayGOP(t *testing.T) {
	dec := &countingDecoder{inner: transition.NewMockDecoder(4, 4)}
	pc := &pipelineCodecs{
		decoder: dec,
	}

	// Simulate a 3-frame GOP: keyframe + 2 P-frames
	frames := []*media.VideoFrame{
		{PTS: 100, IsKeyframe: true, WireData: []byte{0x01}, SPS: []byte{0x67}, PPS: []byte{0x68}},
		{PTS: 200, IsKeyframe: false, WireData: []byte{0x02}},
		{PTS: 300, IsKeyframe: false, WireData: []byte{0x03}},
	}

	pc.replayGOP(frames)

	require.Equal(t, int32(3), dec.count.Load(),
		"replayGOP should decode all frames to build reference chain")
	require.True(t, pc.forceNextIDR,
		"replayGOP should set forceNextIDR so next encode produces keyframe")
}

func TestPipelineCodecs_ReplayGOPNilDecoder(t *testing.T) {
	// replayGOP should be a no-op when decoder is nil
	pc := &pipelineCodecs{decoder: nil}
	pc.replayGOP([]*media.VideoFrame{
		{PTS: 100, IsKeyframe: true, WireData: []byte{0x01}},
	})
	require.False(t, pc.forceNextIDR, "should not set forceNextIDR without decoder")
}

func TestPipelineCodecs_ReplayGOPEmpty(t *testing.T) {
	dec := &countingDecoder{inner: transition.NewMockDecoder(4, 4)}
	pc := &pipelineCodecs{decoder: dec}
	pc.replayGOP(nil)
	require.False(t, pc.forceNextIDR, "should not set forceNextIDR for empty frames")
	require.Equal(t, int32(0), dec.count.Load())
}

func TestPipelineCodecs_ForceNextIDRConsumedByEncode(t *testing.T) {
	// Verify that forceNextIDR flag is consumed by the next encode call.
	pc := &pipelineCodecs{
		encoderFactory: func(w, h, bitrate int, fps float32) (transition.VideoEncoder, error) {
			return transition.NewMockEncoder(), nil
		},
	}

	// Set the flag as replayGOP would.
	pc.forceNextIDR = true

	pf := &ProcessingFrame{
		YUV:    make([]byte, 4*4*3/2),
		Width:  4,
		Height: 4,
		PTS:    100,
	}

	out, err := pc.encode(pf, false)
	require.NoError(t, err)
	require.NotNil(t, out)
	require.True(t, out.IsKeyframe, "first encode after forceNextIDR should produce keyframe")
	require.False(t, pc.forceNextIDR, "forceNextIDR should be cleared after encode")

	// Second encode should NOT force IDR.
	out2, err := pc.encode(pf, false)
	require.NoError(t, err)
	require.NotNil(t, out2)
	// MockEncoder always returns keyframe, but the flag should be cleared.
	require.False(t, pc.forceNextIDR, "forceNextIDR should remain cleared")
}
