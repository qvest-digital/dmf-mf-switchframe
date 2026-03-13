// server/switcher/pipeline_test.go
package switcher

import (
	"context"
	"fmt"
	"math"
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
		func(w, h, bitrate, fpsNum, fpsDen int) (transition.VideoEncoder, error) {
			return transition.NewMockEncoder(), nil
		},
	)
	require.NoError(t, sw.BuildPipeline())
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
		func(w, h, bitrate, fpsNum, fpsDen int) (transition.VideoEncoder, error) {
			encodeCount.Add(1)
			return transition.NewMockEncoder(), nil
		},
	)

	comp := graphics.NewCompositor()
	rgba := make([]byte, 4*4*4)
	for i := 3; i < len(rgba); i += 4 {
		rgba[i] = 255
	}
	layerID, err := comp.AddLayer()
	require.NoError(t, err)
	require.NoError(t, comp.SetOverlay(layerID, rgba, 4, 4, "test"))
	require.NoError(t, comp.On(layerID))
	sw.SetCompositor(comp)
	require.NoError(t, sw.BuildPipeline())

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
		func(w, h, bitrate, fpsNum, fpsDen int) (transition.VideoEncoder, error) {
			encodeCount.Add(1)
			return transition.NewMockEncoder(), nil
		},
	)

	comp := graphics.NewCompositor()
	rgba := make([]byte, 4*4*4)
	for i := 3; i < len(rgba); i += 4 {
		rgba[i] = 255
	}
	layerID, err := comp.AddLayer()
	require.NoError(t, err)
	require.NoError(t, comp.SetOverlay(layerID, rgba, 4, 4, "test"))
	require.NoError(t, comp.On(layerID))
	sw.SetCompositor(comp)
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
		func(w, h, bitrate, fpsNum, fpsDen int) (transition.VideoEncoder, error) {
			encoderCreateCount.Add(1)
			return transition.NewMockEncoder(), nil
		},
	)
	require.NoError(t, sw.BuildPipeline())
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
		func(w, h, bitrate, fpsNum, fpsDen int) (transition.VideoEncoder, error) {
			return &failingEncoder{}, nil
		},
	)
	require.NoError(t, sw.BuildPipeline())
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
		func(w, h, bitrate, fpsNum, fpsDen int) (transition.VideoEncoder, error) {
			return transition.NewMockEncoder(), nil
		},
	)
	require.NoError(t, sw.BuildPipeline())
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
		func(w, h, bitrate, fpsNum, fpsDen int) (transition.VideoEncoder, error) {
			return &failingEncoder{}, nil
		},
	)
	require.NoError(t, sw.BuildPipeline())
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
	var lastFPSNum int
	sw.SetPipelineCodecs(
		func(w, h, bitrate, fpsNum, fpsDen int) (transition.VideoEncoder, error) {
			lastBitrate = bitrate
			lastFPSNum = fpsNum
			return transition.NewMockEncoder(), nil
		},
	)
	require.NoError(t, sw.BuildPipeline())
	defer sw.Close()

	cam1Relay := newTestRelay()
	sw.RegisterSource("cam1", cam1Relay)
	require.NoError(t, sw.Cut(context.Background(), "cam1"))

	// Create a mock sourceDecoder to provide stats.
	// The sourceDecoder's Stats() returns avgFrameSize and avgFPS.
	mockDec := &sourceDecoder{
		sourceKey: "cam1",
	}
	mockDec.avgFrameSizeBits.Store(math.Float64bits(5000)) // ~5KB per frame
	mockDec.avgFPSBits.Store(math.Float64bits(30))         // 30fps
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

	// Verify the encoder factory received the pipeline format's FPS (default 1080p29.97).
	require.Equal(t, 30000, lastFPSNum, "encoder factory should receive pipeline format fpsNum")
	require.Greater(t, lastBitrate, 0, "encoder factory should receive a positive bitrate")
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
		func(w, h, bitrate, fpsNum, fpsDen int) (transition.VideoEncoder, error) {
			return &slowEncoder{
				inner: transition.NewMockEncoder(),
				delay: 30 * time.Millisecond,
			}, nil
		},
	)
	require.NoError(t, sw.BuildPipeline())
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
		func(w, h, bitrate, fpsNum, fpsDen int) (transition.VideoEncoder, error) {
			return &slowEncoder{
				inner: transition.NewMockEncoder(),
				delay: 30 * time.Millisecond,
			}, nil
		},
	)
	require.NoError(t, sw.BuildPipeline())
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
		func(w, h, bitrate, fpsNum, fpsDen int) (transition.VideoEncoder, error) {
			return newBufferingEncoder(transition.NewMockEncoder(), 3), nil
		},
	)
	require.NoError(t, sw.BuildPipeline())
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

func TestPipeline_CompositorDoesNotCorruptSharedBuffer(t *testing.T) {
	// Regression test: broadcastProcessedFromPF must deep-copy the YUV buffer
	// BEFORE applying in-place processors (compositor, key bridge). The frame
	// sync and FRC retain references to source buffers for repeated/interpolated
	// frames. If the compositor modifies the shared buffer in-place, repeated
	// frames get the overlay baked in progressively (visible as blinking).
	programRelay := newTestRelay()
	viewer := newMockProgramViewer("test")
	programRelay.AddViewer(viewer)

	sw := New(programRelay)
	sw.SetPipelineCodecs(
		func(w, h, bitrate, fpsNum, fpsDen int) (transition.VideoEncoder, error) {
			return transition.NewMockEncoder(), nil
		},
	)

	// Create a white overlay with full alpha (will visibly modify Y values).
	comp := graphics.NewCompositor()
	rgba := make([]byte, 4*4*4)
	for i := 0; i < len(rgba); i += 4 {
		rgba[i] = 255   // R
		rgba[i+1] = 255 // G
		rgba[i+2] = 255 // B
		rgba[i+3] = 128 // 50% alpha — partial overlay
	}
	layerID, err := comp.AddLayer()
	require.NoError(t, err)
	require.NoError(t, comp.SetOverlay(layerID, rgba, 4, 4, "test"))
	require.NoError(t, comp.On(layerID))
	sw.SetCompositor(comp)
	require.NoError(t, sw.BuildPipeline())

	defer sw.Close()

	cam1Relay := newTestRelay()
	sw.RegisterSource("cam1", cam1Relay)
	require.NoError(t, sw.Cut(context.Background(), "cam1"))

	// Create a shared buffer simulating what the frame sync retains.
	// Fill Y plane with a known value (128 = mid-gray).
	sharedYUV := make([]byte, 4*4*3/2) // 4x4 YUV420
	for i := 0; i < 4*4; i++ {
		sharedYUV[i] = 128 // Y = 128
	}

	// Save a copy of the original for comparison.
	original := make([]byte, len(sharedYUV))
	copy(original, sharedYUV)

	// Send the shared buffer through the pipeline twice (simulating
	// repeated frames from frame sync).
	for i := 0; i < 2; i++ {
		pf := &ProcessingFrame{
			YUV:        sharedYUV, // same underlying buffer both times
			Width:      4,
			Height:     4,
			PTS:        int64(i * 100),
			IsKeyframe: i == 0,
			Codec:      "h264",
		}
		sw.broadcastProcessedFromPF(pf)
	}

	// Wait for async processing.
	require.Eventually(t, func() bool {
		viewer.mu.Lock()
		defer viewer.mu.Unlock()
		return len(viewer.videos) >= 2
	}, 200*time.Millisecond, 5*time.Millisecond)

	// The shared buffer must NOT have been modified by the compositor.
	// If the deep copy happens after compositing, Y values would have changed
	// from the alpha blend (128 blended with white at 50% != 128).
	require.Equal(t, original, sharedYUV,
		"compositor must not modify the shared source buffer; deep copy should happen before processing")
}

// failingEncoder always returns an error from Encode.
type failingEncoder struct{}

func (e *failingEncoder) Encode(yuv []byte, pts int64, forceIDR bool) ([]byte, bool, error) {
	return nil, false, fmt.Errorf("mock encode failure")
}

func (e *failingEncoder) Close() {}

func TestEnqueueVideoWork_DroppedFrameReleasesPool(t *testing.T) {
	// When the videoProcCh is full and a new frame arrives, the oldest
	// frame is dropped. Its pool buffer must be released to prevent
	// exhausting the FramePool under sustained back-pressure.
	programRelay := newTestRelay()
	sw := New(programRelay)
	sw.SetPipelineCodecs(
		func(w, h, bitrate, fpsNum, fpsDen int) (transition.VideoEncoder, error) {
			return &slowEncoder{
				inner: transition.NewMockEncoder(),
				delay: 100 * time.Millisecond, // slow enough to fill channel
			}, nil
		},
	)
	require.NoError(t, sw.BuildPipeline())
	defer sw.Close()

	pool := NewFramePool(4, 4, 4) // small pool to detect leaks

	// Fill the channel completely, then enqueue one more to trigger drop.
	for i := 0; i < cap(sw.videoProcCh)+1; i++ {
		pf := &ProcessingFrame{
			YUV:        pool.Acquire(),
			Width:      4,
			Height:     4,
			PTS:        int64(i * 100),
			IsKeyframe: i == 0,
			pool:       pool,
		}
		sw.enqueueVideoWork(videoProcWork{yuvFrame: pf})
	}

	// Let pipeline drain.
	time.Sleep(50 * time.Millisecond)

	// At least one frame was dropped. Check pool stats — hits > 0 means
	// the dropped frame's buffer was returned to the pool via ReleaseYUV.
	hits, _ := pool.Stats()
	require.Greater(t, hits, uint64(0),
		"dropped frame's buffer should be returned to pool (hits > 0)")
}

func TestBuildNodeList_Ordering(t *testing.T) {
	// The node ordering is architecturally critical:
	// upstream-key → compositor → raw-sink-mxl → raw-sink-monitor → h264-encode
	programRelay := newTestRelay()
	sw := New(programRelay)

	comp := graphics.NewCompositor()
	sw.SetCompositor(comp)

	kp := graphics.NewKeyProcessor()
	bridge := graphics.NewKeyProcessorBridge(kp)
	sw.SetKeyBridge(bridge)

	sw.SetPipelineCodecs(
		func(w, h, bitrate, fpsNum, fpsDen int) (transition.VideoEncoder, error) {
			return transition.NewMockEncoder(), nil
		},
	)
	defer sw.Close()

	nodes := sw.buildNodeList()
	require.Len(t, nodes, 6)
	require.Equal(t, "upstream-key", nodes[0].Name())
	require.Equal(t, "layout-compositor", nodes[1].Name())
	require.Equal(t, "compositor", nodes[2].Name())
	require.Equal(t, "raw-sink-mxl", nodes[3].Name())
	require.Equal(t, "raw-sink-monitor", nodes[4].Name())
	require.Equal(t, "h264-encode", nodes[5].Name())
}

func TestBuildPipeline_NilPipeCodecs(t *testing.T) {
	// BuildPipeline with no encoder configured should be a no-op.
	programRelay := newTestRelay()
	sw := New(programRelay)
	defer sw.Close()

	err := sw.BuildPipeline()
	require.NoError(t, err)
	require.Nil(t, sw.pipeline.Load(), "pipeline should not be created without pipeCodecs")
}

func TestPipelineSnapshot_LastError(t *testing.T) {
	// Verify that Snapshot reports last_error from nodes.
	codecs := &pipelineCodecs{
		encoderFactory: func(w, h, bitrate, fpsNum, fpsDen int) (transition.VideoEncoder, error) {
			return &failingMockEncoder{}, nil
		},
	}

	var forceIDR atomic.Bool
	n := &encodeNode{
		codecs:   codecs,
		forceIDR: &forceIDR,
	}

	p := &Pipeline{}
	require.NoError(t, p.Build(DefaultFormat, nil, []PipelineNode{n}))

	pf := &ProcessingFrame{
		YUV:    make([]byte, 4*4*3/2),
		Width:  4,
		Height: 4,
		PTS:    100,
	}
	p.Run(pf)

	snap := p.Snapshot()
	activeNodes := snap["active_nodes"].([]map[string]any)
	require.Len(t, activeNodes, 1)
	require.Equal(t, "h264-encode", activeNodes[0]["name"])
	require.Contains(t, activeNodes[0], "last_error")
	require.Contains(t, activeNodes[0]["last_error"], "mock encode error")
}

func TestPipelineCodecs_SetEncoderFactory(t *testing.T) {
	callCount := atomic.Int32{}
	factory1 := transition.EncoderFactory(func(w, h, bitrate, fpsNum, fpsDen int) (transition.VideoEncoder, error) {
		callCount.Add(1)
		return transition.NewMockEncoder(), nil
	})
	factory2 := transition.EncoderFactory(func(w, h, bitrate, fpsNum, fpsDen int) (transition.VideoEncoder, error) {
		callCount.Add(10) // distinguishable increment
		return transition.NewMockEncoder(), nil
	})

	format := DefaultFormat
	formatPtr := &atomic.Pointer[PipelineFormat]{}
	formatPtr.Store(&format)

	pc := &pipelineCodecs{
		encoderFactory: factory1,
		formatRef:      formatPtr,
	}
	defer pc.close()

	// First encode triggers factory1.
	pf := &ProcessingFrame{
		YUV:    make([]byte, 1920*1080*3/2),
		Width:  1920,
		Height: 1080,
		PTS:    90000,
		Codec:  "H264",
	}
	_, err := pc.encode(pf, true)
	require.NoError(t, err)
	require.Equal(t, int32(1), callCount.Load(), "factory1 should have been called once")

	// Swap factory.
	pc.SetEncoderFactory(factory2)

	// Next encode triggers factory2.
	pf.PTS = 93003
	_, err = pc.encode(pf, true)
	require.NoError(t, err)
	require.Equal(t, int32(11), callCount.Load(), "factory2 should have been called (1 + 10)")
}

func TestPipelineSnapshot_AsyncMetrics(t *testing.T) {
	// Verify that Snapshot merges AsyncMetrics from nodes that implement
	// AsyncMetricsProvider (e.g., encodeNode with real encode timing).
	mockEnc := transition.NewMockEncoder()
	codecs := &pipelineCodecs{
		encoderFactory: func(w, h, bitrate, fpsNum, fpsDen int) (transition.VideoEncoder, error) {
			return mockEnc, nil
		},
	}

	var forceIDR atomic.Bool
	var encoded atomic.Pointer[media.VideoFrame]
	n := &encodeNode{
		codecs:   codecs,
		forceIDR: &forceIDR,
		onEncoded: func(frame *media.VideoFrame) {
			encoded.Store(frame)
		},
	}
	n.start()
	defer func() { _ = n.Close() }()

	p := &Pipeline{}
	require.NoError(t, p.Build(DefaultFormat, nil, []PipelineNode{n}))

	pf := &ProcessingFrame{
		YUV:    make([]byte, 4*4*3/2),
		Width:  4,
		Height: 4,
		PTS:    100,
		Codec:  "h264",
	}
	pf.SetRefs(1)
	p.Run(pf)

	// Wait for async encode to complete.
	require.Eventually(t, func() bool {
		return encoded.Load() != nil
	}, time.Second, time.Millisecond)

	snap := p.Snapshot()
	activeNodes := snap["active_nodes"].([]map[string]any)
	require.Len(t, activeNodes, 1)

	node := activeNodes[0]
	require.Equal(t, "h264-encode", node["name"])

	// Async metrics should be merged into the snapshot.
	require.Contains(t, node, "encode_last_ns", "snapshot should contain async encode_last_ns")
	require.Contains(t, node, "encode_max_ns", "snapshot should contain async encode_max_ns")
	require.Contains(t, node, "encode_total", "snapshot should contain async encode_total")
	require.Contains(t, node, "encode_queue_len", "snapshot should contain encode_queue_len")

	// Real encode timing should be non-zero.
	require.Greater(t, node["encode_last_ns"].(int64), int64(0), "encode_last_ns should be > 0")
	require.Greater(t, node["encode_max_ns"].(int64), int64(0), "encode_max_ns should be > 0")
	require.Equal(t, int64(1), node["encode_total"].(int64))
}

