// server/switcher/pipeline_test.go
package switcher

import (
	"context"
	"fmt"
	"math"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/stretchr/testify/require"
	"github.com/zsiec/prism/media"
	"github.com/zsiec/switchframe/server/clock"
	"github.com/zsiec/switchframe/server/graphics"
	"github.com/zsiec/switchframe/server/metrics"
	"github.com/zsiec/switchframe/server/transition"
	"github.com/zsiec/switchframe/server/transition/wipemap"
)

// sendRawFrame sends a raw YUV420 frame through IngestRawVideo, as the
// sourceDecoder does in always-decode mode. pts is replaced by the arrival
// time for a non-MXL source.
// The YUV buffer is 4x4 (24 bytes) for test compatibility with mock decoders.
func sendRawFrame(sw *Switcher, sourceKey string, pts int64, isKeyframe bool) {
	pf := &ProcessingFrame{
		Data:       make([]byte, ColorYUV420_8bit.FrameSize(4, 4)), // 4x4 YUV420
		Width:      4,
		Height:     4,
		PTS:        pts,
		IsKeyframe: isKeyframe,
		Codec:      "h264",
	}
	sw.IngestRawVideo(sourceKey, pf)
}

func TestPipeline_AlwaysReEncodes(t *testing.T) {
	programRelay := newTestRelay()
	viewer := newMockProgramViewer("test")
	programRelay.AddViewer(viewer)

	sw := newTestSwitcher(t, programRelay)
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

	// The encoded PTS is the tick's, on the program frame grid.
	pf := sw.PipelineFormat()
	r := clock.Rate{Num: int64(pf.FPSNum), Den: int64(pf.FPSDen)}
	require.Equal(t, clock.IndexPTS(r, clock.PTSIndex(r, got.PTS)), got.PTS, "PTS off the program grid")
	require.Greater(t, got.GroupID, uint32(0), "GroupID should be set (keyframe increments programGroupID)")
}

func TestPipeline_CompositorEncodesOnce(t *testing.T) {
	// In always-decode mode, frames arrive as raw YUV. The compositor
	// processes YUV and the pipeline encodes once. No pipeline decoder
	// is needed for the normal frame path.
	programRelay := newTestRelay()
	viewer := newMockProgramViewer("test")
	programRelay.AddViewer(viewer)

	sw := newTestSwitcher(t, programRelay)

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
	// The sync loop blends YUV with the transition engine.
	// The pipeline encoder should be created exactly once.
	programRelay := newTestRelay()
	viewer := newMockProgramViewer("test")
	programRelay.AddViewer(viewer)

	sw := newTestSwitcher(t, programRelay)
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

	sw := newTestSwitcher(t, programRelay)
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

	sw := newTestSwitcher(t, programRelay)

	reg := prometheus.NewRegistry()
	m := metrics.NewMetrics(reg)
	sw.SetMetrics(m)

	enc := &failingEncoder{called: make(chan struct{})}
	sw.SetPipelineCodecs(
		func(w, h, bitrate, fpsNum, fpsDen int) (transition.VideoEncoder, error) {
			return enc, nil
		},
	)
	require.NoError(t, sw.BuildPipeline())
	defer sw.Close()

	cam1Relay := newTestRelay()
	sw.RegisterSource("cam1", cam1Relay)
	require.NoError(t, sw.Cut(context.Background(), "cam1"))

	// A raw YUV frame is dropped on the encode error. It is encoded at a
	// program tick, on the encoder's goroutine.
	sendRawFrame(sw, "cam1", 100, true)
	select {
	case <-enc.called:
	case <-time.After(5 * time.Second):
		t.Fatal("the encoder never got the frame")
	}
	// The error is counted once the failed Encode has returned.
	require.Eventually(t, func() bool {
		return testutil.ToFloat64(m.PipelineEncodeErrorsTotal) >= 1
	}, 5*time.Second, time.Millisecond, "encode error metric should be incremented")

	viewer.mu.Lock()
	count := len(viewer.videos)
	viewer.mu.Unlock()
	require.Equal(t, 0, count, "frame should be dropped on encode error, not passed through")
}

func TestPipeline_TransitionOutputReachesViewer(t *testing.T) {
	programRelay := newTestRelay()
	viewer := newMockProgramViewer("test")
	programRelay.AddViewer(viewer)

	sw := newTestSwitcher(t, programRelay)
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
	sw := newTestSwitcher(t, programRelay)

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

	// Check that the encode error metric was incremented
	require.Eventually(t, func() bool {
		return testutil.ToFloat64(m.PipelineEncodeErrorsTotal) >= 1
	}, time.Second, 5*time.Millisecond, "encode error metric should be incremented")
}

func TestPipeline_SourceStatsPropagate(t *testing.T) {
	// Validates that source bitrate/fps are propagated to pipeline codecs
	// via IngestRawVideo (always-decode mode).
	programRelay := newTestRelay()
	sw := newTestSwitcher(t, programRelay)

	var lastBitrate, lastFPSNum atomic.Int64 // set on the sync loop's first encode
	sw.SetPipelineCodecs(
		func(w, h, bitrate, fpsNum, fpsDen int) (transition.VideoEncoder, error) {
			lastBitrate.Store(int64(bitrate))
			lastFPSNum.Store(int64(fpsNum))
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
	sw.configMu.Lock()
	ss := (*sw.sourcesAtomic.Load())["cam1"]
	sw.configMu.Unlock()
	ss.viewer.srcDecoder.Store(mockDec)
	// Clear the mock decoder before sw.Close() runs to avoid panic.
	// This defer runs AFTER the earlier `defer sw.Close()` because defers
	// run in LIFO order. So this clears the mock before Close unregisters.
	defer func() { ss.viewer.srcDecoder.Store(nil) }()

	// Feed enough raw YUV frames to trigger stats propagation
	for i := 0; i < 15; i++ {
		pf := &ProcessingFrame{
			Data:       make([]byte, ColorYUV420_8bit.FrameSize(4, 4)), // 4x4 YUV420
			Width:      4,
			Height:     4,
			PTS:        int64(i * 33333),
			IsKeyframe: i == 0,
		}
		sw.IngestRawVideo("cam1", pf)
		time.Sleep(2 * time.Millisecond)
	}

	// Verify that updateSourceStats was called by inspecting the pipeCodecs directly.
	sw.configMu.Lock()
	pc := sw.pipeCodecs
	sw.configMu.Unlock()

	pc.mu.Lock()
	require.Greater(t, pc.sourceBitrate, 0, "source bitrate should be propagated")
	require.Greater(t, pc.sourceFPS, float32(0), "source FPS should be propagated")
	pc.mu.Unlock()

	// Verify the encoder factory received the pipeline format's FPS (default 1080p29.97).
	require.Eventually(t, func() bool { return lastFPSNum.Load() != 0 }, time.Second, 5*time.Millisecond,
		"encoder never created")
	require.Equal(t, int64(30000), lastFPSNum.Load(), "encoder factory should receive pipeline format fpsNum")
	require.Greater(t, lastBitrate.Load(), int64(0), "encoder factory should receive a positive bitrate")
}

func TestPipeline_AsyncVideoProcessing(t *testing.T) {
	// Verify that IngestRawVideo does NOT block the caller for the
	// duration of encode. This is critical because the source decoder's
	// callback calls IngestRawVideo synchronously: if it blocks
	// for 30ms on encode, audio delivery gets starved.
	programRelay := newTestRelay()
	viewer := newMockProgramViewer("test")
	programRelay.AddViewer(viewer)

	sw := newTestSwitcher(t, programRelay)

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

	// IngestRawVideo should return quickly (< 5ms), NOT block for 30ms encode
	start := time.Now()
	sendRawFrame(sw, "cam1", 100, true)
	elapsed := time.Since(start)

	require.Less(t, elapsed, 5*time.Millisecond,
		"IngestRawVideo should return immediately, not block for encode (took %v)", elapsed)

	// But the frame should still be processed asynchronously
	require.Eventually(t, func() bool {
		viewer.mu.Lock()
		defer viewer.mu.Unlock()
		return len(viewer.videos) >= 1
	}, 200*time.Millisecond, 5*time.Millisecond,
		"frame should be processed asynchronously and reach viewer")
}

func TestPipeline_AsyncTransitionOutput(t *testing.T) {
	// Verify that ingest during a transition does NOT block the caller:
	// the sync loop blends and encodes on its own goroutine.
	programRelay := newTestRelay()
	viewer := newMockProgramViewer("test")
	programRelay.AddViewer(viewer)

	sw := newTestSwitcher(t, programRelay)
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

	// Start transition; the blended output goes through the slow encoder.
	require.NoError(t, sw.StartTransition(context.Background(), "cam2", "mix", 60000, ""))

	// Feed raw YUV frames from both sources; should return quickly
	start := time.Now()
	sendRawFrame(sw, "cam1", 100, true)
	sendRawFrame(sw, "cam2", 101, true)
	elapsed := time.Since(start)

	require.Less(t, elapsed, 10*time.Millisecond,
		"IngestRawVideo during transition should not block for encode (took %v)", elapsed)

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

	sw := newTestSwitcher(t, programRelay)
	enc := newBufferingEncoder(transition.NewMockEncoder(), 3)
	sw.SetPipelineCodecs(
		func(w, h, bitrate, fpsNum, fpsDen int) (transition.VideoEncoder, error) {
			return enc, nil
		},
	)
	require.NoError(t, sw.BuildPipeline())
	defer sw.Close()

	cam1Relay := newTestRelay()
	sw.RegisterSource("cam1", cam1Relay)
	require.NoError(t, sw.Cut(context.Background(), "cam1"))

	// Every tick encodes a frame: the first 3 should be silently dropped
	// (buffering), every later one should produce output.
	sendRawFrame(sw, "cam1", 100, true)

	require.Eventually(t, func() bool {
		viewer.mu.Lock()
		defer viewer.mu.Unlock()
		return len(viewer.videos) >= 2
	}, 2*time.Second, 5*time.Millisecond,
		"should receive frames after encoder warmup")

	sw.Close() // drains the encoder
	viewer.mu.Lock()
	require.Equal(t, int(enc.calls.Load())-3, len(viewer.videos), "only post-warmup frames should reach viewer")
	viewer.mu.Unlock()
}

// bufferingEncoder returns nil data for the first N calls (simulating
// hardware encoder warmup like VideoToolbox EAGAIN), then delegates.
type bufferingEncoder struct {
	inner     transition.VideoEncoder
	remaining atomic.Int32
	calls     atomic.Int32
}

func newBufferingEncoder(inner transition.VideoEncoder, warmupFrames int) *bufferingEncoder {
	e := &bufferingEncoder{inner: inner}
	e.remaining.Store(int32(warmupFrames))
	return e
}

func (e *bufferingEncoder) Encode(yuv []byte, pts int64, forceIDR bool) ([]byte, bool, error) {
	e.calls.Add(1)
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
	// Regression test: the pipeline must not apply in-place processors
	// (compositor, key bridge) to a source buffer the frame sync retains for
	// repeated frames. If the compositor modifies the shared buffer in-place, repeated
	// frames get the overlay baked in progressively (visible as blinking).
	programRelay := newTestRelay()
	viewer := newMockProgramViewer("test")
	programRelay.AddViewer(viewer)

	sw := newTestSwitcher(t, programRelay)
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
	sharedYUV := make([]byte, ColorYUV420_8bit.FrameSize(4, 4)) // 4x4 YUV420
	for i := 0; i < 4*4; i++ {
		sharedYUV[i] = 128 // Y = 128
	}

	// Save a copy of the original for comparison.
	original := make([]byte, len(sharedYUV))
	copy(original, sharedYUV)

	// Ingest the shared buffer twice; the ticks show it, then repeat it.
	for i := 0; i < 2; i++ {
		pf := &ProcessingFrame{
			Data:       sharedYUV, // same underlying buffer both times
			Width:      4,
			Height:     4,
			IsKeyframe: i == 0,
			Codec:      "h264",
		}
		sw.IngestRawVideo("cam1", pf)
	}

	// Wait for the ticks.
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
// failingEncoder fails every Encode, and signals called, when set, after
// the first failure has been returned.
type failingEncoder struct {
	called chan struct{}
	once   sync.Once
}

func (e *failingEncoder) Encode(yuv []byte, pts int64, forceIDR bool) ([]byte, bool, error) {
	if e.called != nil {
		defer e.once.Do(func() { close(e.called) })
	}
	return nil, false, fmt.Errorf("mock encode failure")
}

func (e *failingEncoder) Close() {}

func TestBuildNodeList_Ordering(t *testing.T) {
	// The node ordering is architecturally critical:
	// upstream-key -> dve-compositor -> compositor -> colorgrade -> stmap-program -> raw-sink-mxl -> raw-sink-preview -> h264-encode
	programRelay := newTestRelay()
	sw := newTestSwitcher(t, programRelay)

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
	require.Len(t, nodes, 8)
	require.Equal(t, "upstream-key", nodes[0].Name())
	require.Equal(t, "dve-compositor", nodes[1].Name())
	require.Equal(t, "compositor", nodes[2].Name())
	// Where gpu_colorgrade sits: after the downstream keyers, before the
	// program ST map and every output tap.
	require.Equal(t, "colorgrade", nodes[3].Name())
	require.Equal(t, "stmap-program", nodes[4].Name())
	require.Equal(t, "raw-sink-mxl", nodes[5].Name())
	require.Equal(t, "raw-sink-preview", nodes[6].Name())
	require.Equal(t, "h264-encode", nodes[7].Name())
}

func TestSetGPUPipeline(t *testing.T) {
	// When a GPU pipeline is set, buildNodeList still returns CPU nodes
	// (GPU pipeline runs independently via RunWithUpload).
	programRelay := newTestRelay()
	sw := newTestSwitcher(t, programRelay)

	sw.SetPipelineCodecs(
		func(w, h, bitrate, fpsNum, fpsDen int) (transition.VideoEncoder, error) {
			return transition.NewMockEncoder(), nil
		},
	)
	defer sw.Close()

	// Set a mock GPU pipeline.
	var called atomic.Bool
	mockGPU := &mockGPUPipelineRunner{onRun: func() { called.Store(true) }}
	sw.SetGPUPipeline(mockGPU)

	sw.configMu.Lock()
	nodes := sw.buildNodeList()
	sw.configMu.Unlock()

	// CPU nodes are unchanged — GPU pipeline is a separate path.
	require.Len(t, nodes, 8)
	require.Equal(t, "upstream-key", nodes[0].Name())

	// Verify GPU pipeline runner is stored.
	h := sw.gpuRunner.Load()
	require.NotNil(t, h)
	require.NotNil(t, h.runner)

	// Clear GPU pipeline.
	sw.SetGPUPipeline(nil)
}

type mockGPUPipelineRunner struct {
	onRun func()
}

func (m *mockGPUPipelineRunner) RunWithUpload(yuv []byte, width, height int, pts int64) error {
	if m.onRun != nil {
		m.onRun()
	}
	return nil
}

func (m *mockGPUPipelineRunner) RunFromCache(sourceKey string, srcPTS, pts int64, nearest bool) error {
	// Mock always falls back to RunWithUpload.
	return fmt.Errorf("mock: no cached GPU frame")
}

func (m *mockGPUPipelineRunner) RunTransition(fromKey, toKey string, transType string, wipeDir int, position float64, pts int64, stinger *GPUStingerFrame) error {
	if m.onRun != nil {
		m.onRun()
	}
	return nil
}

func (m *mockGPUPipelineRunner) SetWipeMap(gradient []byte, config *wipemap.WipeConfig, width, height int) error {
	return nil
}

func (m *mockGPUPipelineRunner) ClearWipeMap() {}

func (m *mockGPUPipelineRunner) Snapshot() map[string]any {
	return map[string]any{
		"gpu":              true,
		"backend":          "mock",
		"device":           "mock-device",
		"run_count":        int64(0),
		"last_run_ns":      int64(0),
		"max_run_ns":       int64(0),
		"total_latency_us": int64(0),
		"active_nodes":     []map[string]any{},
		"total_nodes":      0,
	}
}

func TestBuildPipeline_NilPipeCodecs(t *testing.T) {
	// BuildPipeline with no encoder configured should be a no-op.
	programRelay := newTestRelay()
	sw := newTestSwitcher(t, programRelay)
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
		Data:   make([]byte, ColorYUV420_8bit.FrameSize(4, 4)),
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
		Data:   make([]byte, ColorYUV420_8bit.FrameSize(1920, 1080)),
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
		Data:   make([]byte, ColorYUV420_8bit.FrameSize(4, 4)),
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
