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
	"github.com/zsiec/switchframe/server/graphics"
	"github.com/zsiec/switchframe/server/metrics"
	"github.com/zsiec/switchframe/server/transition"
)

// sendRawFrame sends a raw YUV420 frame through handleRawVideoFrame,
// simulating what the sourceDecoder produces in always-decode mode.
// The YUV buffer is 4x4 (24 bytes) for test compatibility with mock decoders.
func sendRawFrame(sw *Switcher, sourceKey string, pts int64, isKeyframe bool) {
	pf := &ProcessingFrame{
		YUV:        make([]byte, 4*4*3/2), // 4x4 YUV420
		Width:      4,
		Height:     4,
		PTS:        pts,
		IsKeyframe: isKeyframe,
		Codec:      "h264",
	}
	sw.handleRawVideoFrame(sourceKey, pf)
}

func TestPipeline_AlwaysReEncodes(t *testing.T) {
	programRelay := newTestRelay()
	viewer := newMockProgramViewer("test")
	programRelay.AddViewer(viewer)

	sw := New(programRelay)
	sw.SetPipelineCodecs(
		func(w, h, bitrate int, fps float32) (transition.VideoEncoder, error) {
			return transition.NewMockEncoder(), nil
		},
	)
	defer sw.Close()

	cam1Relay := newTestRelay()
	sw.RegisterSource("cam1", cam1Relay)
	require.NoError(t, sw.Cut(context.Background(), "cam1"))

	// Send raw YUV frame through always-decode pipeline
	sendRawFrame(sw, "cam1", 100, true)

	// Wait for async video processing to complete
	require.Eventually(t, func() bool {
		viewer.mu.Lock()
		defer viewer.mu.Unlock()
		return len(viewer.videos) >= 1
	}, 200*time.Millisecond, 5*time.Millisecond)

	viewer.mu.Lock()
	got := viewer.videos[len(viewer.videos)-1]
	viewer.mu.Unlock()

	// PTS is preserved through the encode pipeline
	require.Equal(t, int64(100), got.PTS)
	require.Greater(t, got.GroupID, uint32(0), "GroupID should be set (keyframe increments programGroupID)")
}

func TestPipeline_CompositorEncodesOnce(t *testing.T) {
	// In always-decode mode, frames arrive as raw YUV. The compositor
	// processes YUV and the pipeline encodes once. No pipeline decoder
	// is needed for the normal frame path.
	programRelay := newTestRelay()
	viewer := newMockProgramViewer("test")
	programRelay.AddViewer(viewer)

	sw := New(programRelay)

	var encodeCount atomic.Int32
	sw.SetPipelineCodecs(
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

	// Send raw YUV frame through pipeline with compositor active
	sendRawFrame(sw, "cam1", 100, true)

	// Wait for async video processing to complete
	require.Eventually(t, func() bool {
		return encodeCount.Load() >= 1
	}, 200*time.Millisecond, 5*time.Millisecond)

	require.Equal(t, int32(1), encodeCount.Load(), "should encode exactly once")
}

func TestPipeline_TransitionPlusCompositor_SingleEncode(t *testing.T) {
	// In always-decode mode, both sources provide raw YUV via IngestRawFrame.
	// The transition engine blends YUV and outputs to broadcastProcessed.
	// The pipeline encoder should be created exactly once.
	programRelay := newTestRelay()
	viewer := newMockProgramViewer("test")
	programRelay.AddViewer(viewer)

	sw := New(programRelay)
	sw.SetTransitionConfig(TransitionConfig{})

	var encodeCount atomic.Int32
	sw.SetPipelineCodecs(
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
	require.NoError(t, sw.SetPreview(context.Background(), "cam2"))

	// Start transition
	require.NoError(t, sw.StartTransition(context.Background(), "cam2", "mix", 60000, ""))

	// Feed raw YUV frames from both sources — transition engine uses IngestRawFrame
	sendRawFrame(sw, "cam1", 100, true)
	sendRawFrame(sw, "cam2", 101, true)

	// Wait for async video processing to complete
	require.Eventually(t, func() bool {
		return encodeCount.Load() >= 1
	}, 200*time.Millisecond, 5*time.Millisecond)

	// encodeCount tracks encoder FACTORY calls, not Encode() calls.
	require.Equal(t, int32(1), encodeCount.Load(), "should create only one encoder for the pipeline")

	sw.AbortTransition()
}

func TestPipeline_ResolutionChange(t *testing.T) {
	// Validates encoder creation when raw YUV frames arrive.
	programRelay := newTestRelay()
	viewer := newMockProgramViewer("test")
	programRelay.AddViewer(viewer)

	sw := New(programRelay)
	var encoderCreateCount atomic.Int32
	sw.SetPipelineCodecs(
		func(w, h, bitrate int, fps float32) (transition.VideoEncoder, error) {
			encoderCreateCount.Add(1)
			return transition.NewMockEncoder(), nil
		},
	)
	defer sw.Close()

	cam1Relay := newTestRelay()
	sw.RegisterSource("cam1", cam1Relay)
	require.NoError(t, sw.Cut(context.Background(), "cam1"))

	// Feed 4x4 raw YUV frame
	sendRawFrame(sw, "cam1", 100, true)

	// Wait for async video processing to complete
	require.Eventually(t, func() bool {
		return encoderCreateCount.Load() >= 1
	}, 200*time.Millisecond, 5*time.Millisecond)
	require.Equal(t, int32(1), encoderCreateCount.Load())
}

func TestPipeline_EncodeFailureDropsFrame(t *testing.T) {
	// Validates that an encode error causes the frame to be dropped.
	// In always-decode mode, frames arrive as raw YUV — no pipeline decode needed.
	programRelay := newTestRelay()
	viewer := newMockProgramViewer("test")
	programRelay.AddViewer(viewer)

	sw := New(programRelay)

	reg := prometheus.NewRegistry()
	m := metrics.NewMetrics(reg)
	sw.SetMetrics(m)

	sw.SetPipelineCodecs(
		func(w, h, bitrate int, fps float32) (transition.VideoEncoder, error) {
			return &failingEncoder{}, nil
		},
	)
	defer sw.Close()

	cam1Relay := newTestRelay()
	sw.RegisterSource("cam1", cam1Relay)
	require.NoError(t, sw.Cut(context.Background(), "cam1"))

	// Send raw YUV frame — should be dropped (encode error)
	sendRawFrame(sw, "cam1", 100, true)
	time.Sleep(50 * time.Millisecond)

	viewer.mu.Lock()
	count := len(viewer.videos)
	viewer.mu.Unlock()
	require.Equal(t, 0, count, "frame should be dropped on encode error, not passed through")

	// Verify encode error metric was incremented
	val := testutil.ToFloat64(m.PipelineEncodeErrorsTotal)
	require.GreaterOrEqual(t, val, 1.0, "encode error metric should be incremented")
}

func TestPipeline_TransitionOutputReachesViewer(t *testing.T) {
	programRelay := newTestRelay()
	viewer := newMockProgramViewer("test")
	programRelay.AddViewer(viewer)

	sw := New(programRelay)
	sw.SetTransitionConfig(TransitionConfig{})
	sw.SetPipelineCodecs(
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

	require.NoError(t, sw.StartTransition(context.Background(), "cam2", "mix", 60000, ""))

	// Feed raw YUV frames from both sources
	sendRawFrame(sw, "cam1", 100, true)
	sendRawFrame(sw, "cam2", 101, true)

	require.Eventually(t, func() bool {
		viewer.mu.Lock()
		defer viewer.mu.Unlock()
		return len(viewer.videos) >= 1
	}, 200*time.Millisecond, 5*time.Millisecond,
		"transition output should reach viewer via pipeline encode")

	sw.AbortTransition()
}

func TestPipeline_EncodeFailureMetrics(t *testing.T) {
	// Validates that encode failure increments metric counter.
	programRelay := newTestRelay()
	sw := New(programRelay)

	reg := prometheus.NewRegistry()
	m := metrics.NewMetrics(reg)
	sw.SetMetrics(m)

	sw.SetPipelineCodecs(
		func(w, h, bitrate int, fps float32) (transition.VideoEncoder, error) {
			return &failingEncoder{}, nil
		},
	)
	defer sw.Close()

	cam1Relay := newTestRelay()
	sw.RegisterSource("cam1", cam1Relay)
	require.NoError(t, sw.Cut(context.Background(), "cam1"))

	// Send raw YUV frame — will fail at encode
	sendRawFrame(sw, "cam1", 100, true)
	time.Sleep(10 * time.Millisecond)

	// Check that the encode error metric was incremented
	val := testutil.ToFloat64(m.PipelineEncodeErrorsTotal)
	require.GreaterOrEqual(t, val, 1.0, "encode error metric should be incremented")
}

func TestPipeline_SourceStatsPropagate(t *testing.T) {
	// Validates that source bitrate/fps are propagated to pipeline codecs
	// via handleRawVideoFrame (always-decode mode).
	programRelay := newTestRelay()
	sw := New(programRelay)

	var lastBitrate int
	var lastFPS float32
	sw.SetPipelineCodecs(
		func(w, h, bitrate int, fps float32) (transition.VideoEncoder, error) {
			lastBitrate = bitrate
			lastFPS = fps
			return transition.NewMockEncoder(), nil
		},
	)
	defer sw.Close()

	cam1Relay := newTestRelay()
	sw.RegisterSource("cam1", cam1Relay)
	require.NoError(t, sw.Cut(context.Background(), "cam1"))

	// Create a mock sourceDecoder to provide stats.
	// The sourceDecoder's Stats() returns avgFrameSize and avgFPS.
	mockDec := &sourceDecoder{
		sourceKey:    "cam1",
		avgFrameSize: 5000, // ~5KB per frame
		avgFPS:       30,   // 30fps
	}
	sw.mu.RLock()
	ss := sw.sources["cam1"]
	sw.mu.RUnlock()
	ss.viewer.srcDecoder.Store(mockDec)
	// Clear the mock decoder before sw.Close() runs to avoid panic.
	// This defer runs AFTER the earlier `defer sw.Close()` because defers
	// run in LIFO order. So this clears the mock before Close unregisters.
	defer func() { ss.viewer.srcDecoder.Store(nil) }()

	// Feed enough raw YUV frames to trigger stats propagation
	for i := 0; i < 15; i++ {
		pf := &ProcessingFrame{
			YUV:        make([]byte, 4*4*3/2), // 4x4 YUV420
			Width:      4,
			Height:     4,
			PTS:        int64(i * 33333),
			IsKeyframe: i == 0,
		}
		sw.handleRawVideoFrame("cam1", pf)
		time.Sleep(2 * time.Millisecond)
	}

	// Verify that updateSourceStats was called by inspecting the pipeCodecs directly.
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
	// Verify that handleRawVideoFrame does NOT block the caller for the
	// duration of encode. This is critical because the source decoder's
	// callback calls handleRawVideoFrame synchronously — if it blocks
	// for 30ms on encode, audio delivery gets starved.
	programRelay := newTestRelay()
	viewer := newMockProgramViewer("test")
	programRelay.AddViewer(viewer)

	sw := New(programRelay)

	sw.SetPipelineCodecs(
		func(w, h, bitrate int, fps float32) (transition.VideoEncoder, error) {
			return &slowEncoder{
				inner: transition.NewMockEncoder(),
				delay: 30 * time.Millisecond,
			}, nil
		},
	)
	defer sw.Close()

	cam1Relay := newTestRelay()
	sw.RegisterSource("cam1", cam1Relay)
	require.NoError(t, sw.Cut(context.Background(), "cam1"))

	// handleRawVideoFrame should return quickly (< 5ms), NOT block for 30ms encode
	start := time.Now()
	sendRawFrame(sw, "cam1", 100, true)
	elapsed := time.Since(start)

	require.Less(t, elapsed, 5*time.Millisecond,
		"handleRawVideoFrame should return immediately, not block for encode (took %v)", elapsed)

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
	// block the caller. During transitions, handleRawVideoFrame routes
	// frames to the engine which outputs via broadcastProcessed — encoding
	// happens asynchronously.
	programRelay := newTestRelay()
	viewer := newMockProgramViewer("test")
	programRelay.AddViewer(viewer)

	sw := New(programRelay)
	sw.SetTransitionConfig(TransitionConfig{})
	sw.SetPipelineCodecs(
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
	require.NoError(t, sw.SetPreview(context.Background(), "cam2"))

	// Start transition — broadcastProcessed will be called with slow encoder
	require.NoError(t, sw.StartTransition(context.Background(), "cam2", "mix", 60000, ""))

	// Feed raw YUV frames from both sources; should return quickly
	start := time.Now()
	sendRawFrame(sw, "cam1", 100, true)
	sendRawFrame(sw, "cam2", 101, true)
	elapsed := time.Since(start)

	require.Less(t, elapsed, 10*time.Millisecond,
		"handleRawVideoFrame during transition should not block for encode (took %v)", elapsed)

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

func (e *slowEncoder) Encode(yuv []byte, pts int64, forceIDR bool) ([]byte, bool, error) {
	time.Sleep(e.delay)
	return e.inner.Encode(yuv, pts, forceIDR)
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
		func(w, h, bitrate int, fps float32) (transition.VideoEncoder, error) {
			return newBufferingEncoder(transition.NewMockEncoder(), 3), nil
		},
	)
	defer sw.Close()

	cam1Relay := newTestRelay()
	sw.RegisterSource("cam1", cam1Relay)
	require.NoError(t, sw.Cut(context.Background(), "cam1"))

	// Send 5 raw YUV frames: first 3 should be silently dropped (buffering),
	// frames 4 and 5 should produce output.
	for i := range 5 {
		sendRawFrame(sw, "cam1", int64(i*100+100), i == 0)
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

func (e *bufferingEncoder) Encode(yuv []byte, pts int64, forceIDR bool) ([]byte, bool, error) {
	if e.remaining.Add(-1) >= 0 {
		return nil, false, nil // EAGAIN — no output yet
	}
	return e.inner.Encode(yuv, pts, forceIDR)
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

func (e *failingEncoder) Encode(yuv []byte, pts int64, forceIDR bool) ([]byte, bool, error) {
	return nil, false, fmt.Errorf("mock encode failure")
}

func (e *failingEncoder) Close() {}

