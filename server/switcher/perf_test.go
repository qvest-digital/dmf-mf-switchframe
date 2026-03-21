package switcher

import (
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/zsiec/prism/media"
)

func TestSwitcher_PerfSubStages(t *testing.T) {
	programRelay := newTestRelay()
	sw := newTestSwitcher(programRelay)
	defer sw.Close()

	sample := sw.PerfSample()

	// All sub-stage fields should be zero initially (no frames processed yet)
	if sample.DecodeQueueNs != 0 {
		t.Errorf("initial DecodeQueueNs should be 0, got %d", sample.DecodeQueueNs)
	}
	if sample.DecodeNs != 0 {
		t.Errorf("initial DecodeNs should be 0, got %d", sample.DecodeNs)
	}
	if sample.SyncWaitNs != 0 {
		t.Errorf("initial SyncWaitNs should be 0, got %d", sample.SyncWaitNs)
	}
	if sample.ProcQueueNs != 0 {
		t.Errorf("initial ProcQueueNs should be 0, got %d", sample.ProcQueueNs)
	}

	// Verify the fields are populated from the atomic stores
	sw.lastDecodeQueueNs.Store(100)
	sw.lastDecodeNs.Store(200)
	sw.lastSyncWaitNs.Store(300)
	sw.lastProcQueueNs.Store(400)

	sample = sw.PerfSample()
	if sample.DecodeQueueNs != 100 {
		t.Errorf("DecodeQueueNs should be 100, got %d", sample.DecodeQueueNs)
	}
	if sample.DecodeNs != 200 {
		t.Errorf("DecodeNs should be 200, got %d", sample.DecodeNs)
	}
	if sample.SyncWaitNs != 300 {
		t.Errorf("SyncWaitNs should be 300, got %d", sample.SyncWaitNs)
	}
	if sample.ProcQueueNs != 400 {
		t.Errorf("ProcQueueNs should be 400, got %d", sample.ProcQueueNs)
	}
}

func TestPerfSample_FRCFrameZeroesSyncWait(t *testing.T) {
	// FRC-synthesized frames have DecodeEndNano=0 (no actual decode).
	// The sync wait measurement should store 0 for these frames,
	// not retain a stale value from a previous fresh frame.
	programRelay := newTestRelay()
	sw := newTestSwitcher(programRelay)
	defer sw.Close()

	// Simulate a prior fresh frame leaving a stale sync wait value.
	sw.lastSyncWaitNs.Store(25_000_000) // 25ms stale value

	// Simulate processing an FRC frame: DecodeEndNano=0, SyncReleaseNano set.
	// This is what videoProcessingLoop does when it dequeues a work item.
	frame := &ProcessingFrame{
		YUV:             make([]byte, 64),
		Width:           8,
		Height:          4,
		PTS:             1000,
		DecodeEndNano:   0,                 // FRC frame — no decode
		SyncReleaseNano: 1_000_000_000_000, // non-zero: was released by sync
	}

	// Replicate the videoProcessingLoop sync wait measurement logic.
	if frame.SyncReleaseNano > 0 {
		if frame.DecodeEndNano > 0 {
			sw.lastSyncWaitNs.Store(frame.SyncReleaseNano - frame.DecodeEndNano)
		} else {
			sw.lastSyncWaitNs.Store(0)
		}
	}

	sample := sw.PerfSample()
	require.Equal(t, int64(0), sample.SyncWaitNs,
		"FRC frame (DecodeEndNano=0) should zero out sync wait, not retain stale value")
}

func TestPerfSample_FreshFramePreservesSyncWait(t *testing.T) {
	// Fresh (decoded) frames have both DecodeEndNano and SyncReleaseNano set.
	// The sync wait should be the difference between them.
	programRelay := newTestRelay()
	sw := newTestSwitcher(programRelay)
	defer sw.Close()

	decodeEnd := int64(1_000_000_000)
	syncRelease := int64(1_005_000_000) // 5ms later

	frame := &ProcessingFrame{
		YUV:             make([]byte, 64),
		Width:           8,
		Height:          4,
		PTS:             1000,
		DecodeEndNano:   decodeEnd,
		SyncReleaseNano: syncRelease,
	}

	// Replicate the updated measurement logic.
	if frame.SyncReleaseNano > 0 {
		if frame.DecodeEndNano > 0 {
			sw.lastSyncWaitNs.Store(frame.SyncReleaseNano - frame.DecodeEndNano)
		} else {
			sw.lastSyncWaitNs.Store(0)
		}
	}

	sample := sw.PerfSample()
	require.Equal(t, int64(5_000_000), sample.SyncWaitNs,
		"fresh frame sync wait should be SyncReleaseNano - DecodeEndNano")
}

func TestPerfSample_RawFrameCount(t *testing.T) {
	// Raw frame count should be reported per-source in PerfSourceSample.
	programRelay := newTestRelay()
	sw := newTestSwitcher(programRelay)
	defer sw.Close()

	// Register a raw source (MXL-style, no viewer).
	sw.RegisterMXLSource("mxl:cam1")

	// Initially zero
	sample := sw.PerfSample()
	src, ok := sample.Sources["mxl:cam1"]
	require.True(t, ok, "mxl:cam1 should be in sources")
	require.Equal(t, int64(0), src.RawFrameCount, "initial raw frame count should be 0")

	// Ingest some raw video frames
	for i := 0; i < 30; i++ {
		pf := &ProcessingFrame{
			YUV:    make([]byte, 64),
			Width:  8,
			Height: 4,
			PTS:    int64(i * 3000),
		}
		sw.IngestRawVideo("mxl:cam1", pf)
	}

	sample = sw.PerfSample()
	src = sample.Sources["mxl:cam1"]
	require.Equal(t, int64(30), src.RawFrameCount, "raw frame count should be 30 after 30 ingests")
}

func TestPerfSample_FrameSyncFields(t *testing.T) {
	// FrameSync fields should be zero when no frame sync is active.
	programRelay := newTestRelay()
	sw := newTestSwitcher(programRelay)
	defer sw.Close()

	sample := sw.PerfSample()
	require.Equal(t, float64(0), sample.FrameSyncReleaseFPS, "frame sync release FPS should be 0 without frame sync")
	require.Equal(t, 0, sample.FrameSyncSourceCount, "frame sync source count should be 0 without frame sync")

	// Enable frame sync
	sw.SetFrameSync(true, 33*time.Millisecond)

	// Register a relay-based source (has viewer → gets added to frame sync)
	sourceRelay := newTestRelay()
	sw.RegisterSource("cam1", sourceRelay)

	sample = sw.PerfSample()
	require.Equal(t, 1, sample.FrameSyncSourceCount, "frame sync source count should be 1 after adding relay source")
}

func TestFrameSynchronizer_ReleaseFPS(t *testing.T) {
	fs := NewFrameSynchronizer(
		33*time.Millisecond,
		func(key string, frame media.VideoFrame) {},
		func(key string, frame media.AudioFrame) {},
	)

	// First call initializes state, returns 0.
	fps := fs.ReleaseFPS()
	require.Equal(t, float64(0), fps, "first call should return 0 (no baseline)")

	// Simulate some releases via the atomic counters.
	fs.programDrivenReleases.Store(30)
	fs.timerDrivenReleases.Store(0)

	// Hack: set the last time slightly in the past so elapsed > 0.
	fs.mu.Lock()
	fs.releaseFPSLastTime = time.Now().Add(-1 * time.Second)
	fs.releaseFPSLastTotal = 0
	fs.mu.Unlock()

	fps = fs.ReleaseFPS()
	require.InDelta(t, 30.0, fps, 2.0,
		"should compute ~30 FPS from 30 releases in ~1 second")
}

func TestFrameSynchronizer_SourceCount(t *testing.T) {
	fs := NewFrameSynchronizer(
		33*time.Millisecond,
		func(key string, frame media.VideoFrame) {},
		func(key string, frame media.AudioFrame) {},
	)

	require.Equal(t, 0, fs.SourceCount(), "initial source count should be 0")

	fs.AddSource("cam1")
	require.Equal(t, 1, fs.SourceCount())

	fs.AddSource("cam2")
	require.Equal(t, 2, fs.SourceCount())

	fs.RemoveSource("cam1")
	require.Equal(t, 1, fs.SourceCount())
}

func TestPerfSample_GPUFields(t *testing.T) {
	programRelay := newTestRelay()
	sw := newTestSwitcher(programRelay)
	defer sw.Close()

	// Without GPU: GPUActive should be false.
	sample := sw.PerfSample()
	require.False(t, sample.GPUActive, "GPU should not be active without GPU pipeline")
	require.Empty(t, sample.GPUNodeTimings, "no GPU node timings without GPU pipeline")
	require.Equal(t, int64(0), sample.GPUPipelineLastNs, "no GPU pipeline timing without GPU pipeline")

	// Set a mock GPU pipeline.
	mockGPU := &mockGPUPipelineRunnerWithTiming{
		lastRunNs: 500_000,
		nodes: []map[string]any{
			{"name": "gpu_key", "last_ns": int64(100_000)},
			{"name": "gpu_encode", "last_ns": int64(300_000)},
		},
	}
	sw.SetGPUPipeline(mockGPU)

	sample = sw.PerfSample()
	require.True(t, sample.GPUActive, "GPU should be active with GPU pipeline set")
	require.Equal(t, int64(500_000), sample.GPUPipelineLastNs)
	require.Equal(t, "test-metal", sample.GPUBackend)
	require.Equal(t, "Test GPU", sample.GPUDevice)
	require.Len(t, sample.GPUNodeTimings, 2)
	require.Equal(t, int64(100_000), sample.GPUNodeTimings["gpu_key"])
	require.Equal(t, int64(300_000), sample.GPUNodeTimings["gpu_encode"])

	// Clear GPU pipeline.
	sw.SetGPUPipeline(nil)
	sample = sw.PerfSample()
	require.False(t, sample.GPUActive)
}

func TestDebugSnapshot_GPUPipeline(t *testing.T) {
	programRelay := newTestRelay()
	sw := newTestSwitcher(programRelay)
	defer sw.Close()

	// Without GPU.
	snap := sw.DebugSnapshot()
	_, hasGPU := snap["gpu_pipeline"]
	require.False(t, hasGPU, "no gpu_pipeline key without GPU")

	// With GPU.
	mockGPU := &mockGPUPipelineRunnerWithTiming{
		lastRunNs: 1_000_000,
		nodes:     []map[string]any{},
	}
	sw.SetGPUPipeline(mockGPU)

	snap = sw.DebugSnapshot()
	gpuPipeline, hasGPU := snap["gpu_pipeline"]
	require.True(t, hasGPU, "gpu_pipeline key should exist with GPU active")
	gpuMap := gpuPipeline.(map[string]any)
	require.True(t, gpuMap["gpu"].(bool))
	require.Equal(t, "test-metal", gpuMap["backend"])
	require.Equal(t, "Test GPU", gpuMap["device"])

	// Verify GPU counters appear in video_pipeline.
	vpMap := snap["video_pipeline"].(map[string]any)
	require.Contains(t, vpMap, "gpu_frames_processed")
	require.Contains(t, vpMap, "gpu_cache_misses")
	require.Contains(t, vpMap, "gpu_transition_frames")
	require.Contains(t, vpMap, "gpu_fallback_frames")
}

func TestDebugSnapshot_GPUCounters(t *testing.T) {
	programRelay := newTestRelay()
	sw := newTestSwitcher(programRelay)
	defer sw.Close()

	// Manually set GPU counters.
	sw.gpuFramesProcessed.Store(100)
	sw.gpuCacheMisses.Store(5)
	sw.gpuTransitionFrames.Store(30)
	sw.gpuFallbackFrames.Store(2)

	snap := sw.DebugSnapshot()
	vpMap := snap["video_pipeline"].(map[string]any)
	require.Equal(t, int64(100), vpMap["gpu_frames_processed"])
	require.Equal(t, int64(5), vpMap["gpu_cache_misses"])
	require.Equal(t, int64(30), vpMap["gpu_transition_frames"])
	require.Equal(t, int64(2), vpMap["gpu_fallback_frames"])
}

// mockGPUPipelineRunnerWithTiming returns realistic GPU snapshot data.
type mockGPUPipelineRunnerWithTiming struct {
	lastRunNs int64
	nodes     []map[string]any
}

func (m *mockGPUPipelineRunnerWithTiming) RunWithUpload(yuv []byte, width, height int, pts int64) error {
	return nil
}

func (m *mockGPUPipelineRunnerWithTiming) RunFromCache(sourceKey string, pts int64) error {
	return fmt.Errorf("mock: no cached GPU frame")
}

func (m *mockGPUPipelineRunnerWithTiming) RunTransition(fromKey, toKey string, transType string, wipeDir int, position float64, pts int64, stinger *GPUStingerFrame) error {
	return nil
}

func (m *mockGPUPipelineRunnerWithTiming) Snapshot() map[string]any {
	return map[string]any{
		"gpu":              true,
		"backend":          "test-metal",
		"device":           "Test GPU",
		"run_count":        int64(42),
		"last_run_ns":      m.lastRunNs,
		"max_run_ns":       m.lastRunNs * 2,
		"total_latency_us": int64(10),
		"active_nodes":     m.nodes,
		"total_nodes":      len(m.nodes),
	}
}
