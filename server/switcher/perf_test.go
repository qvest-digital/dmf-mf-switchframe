package switcher

import (
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/zsiec/switchframe/server/clock"
	"github.com/zsiec/switchframe/server/transition/wipemap"
)

func TestSwitcher_PerfSubStages(t *testing.T) {
	programRelay := newTestRelay()
	sw := newTestSwitcher(t, programRelay)
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

	// Verify the fields are populated from the atomic stores
	sw.lastDecodeQueueNs.Store(100)
	sw.lastDecodeNs.Store(200)
	sw.lastSyncWaitNs.Store(300)

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
}

func TestPerfSample_FreshFramePreservesSyncWait(t *testing.T) {
	// A fresh frame's sync wait is the time from its arrival to the tick
	// that shows it.
	s := tickSwitcher(t)
	onProgram(t, s, "mxl:cam1")
	s.lastSyncWaitNs.Store(1) // a stale value the fresh frame replaces

	g := grainNow(s)
	pf := mxlFrame(s, g, 50)
	pf.ArrivalNano = time.Now().Add(-5 * time.Millisecond).UnixNano()
	s.IngestRawVideo("mxl:cam1", pf)
	s.processVideoTick(tickAt(s, g+clock.ReaderDelayGrains))

	wait := s.PerfSample().SyncWaitNs
	require.GreaterOrEqual(t, wait, int64(5_000_000),
		"fresh frame sync wait should be the time since its arrival")
	require.Less(t, wait, int64(time.Second))
}

func TestPerfSample_RawFrameCount(t *testing.T) {
	// Raw frame count should be reported per-source in PerfSourceSample.
	programRelay := newTestRelay()
	sw := newTestSwitcher(t, programRelay)
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
			Data:   make([]byte, 64),
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
	programRelay := newTestRelay()
	sw := newTestSwitcher(t, programRelay)
	defer sw.Close()

	sample := sw.PerfSample()
	require.Equal(t, builtinSourceCount, sample.FrameSyncSourceCount,
		"frame sync source count: the 2 built-ins")

	// A relay-based source is added to the frame synchronizer.
	sourceRelay := newTestRelay()
	sw.RegisterSource("cam1", sourceRelay)

	sample = sw.PerfSample()
	require.Equal(t, 1+builtinSourceCount, sample.FrameSyncSourceCount,
		"frame sync source count: cam1 + 2 built-ins")
}

func TestFrameSynchronizer_SourceCount(t *testing.T) {
	fs := NewFrameSynchronizer()

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
	sw := newTestSwitcher(t, programRelay)
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
	sw := newTestSwitcher(t, programRelay)
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
	sw := newTestSwitcher(t, programRelay)
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

func (m *mockGPUPipelineRunnerWithTiming) RunFromCache(sourceKey string, srcPTS, pts int64) error {
	return fmt.Errorf("mock: no cached GPU frame")
}

func (m *mockGPUPipelineRunnerWithTiming) RunTransition(fromKey, toKey string, transType string, wipeDir int, position float64, pts int64, stinger *GPUStingerFrame) error {
	return nil
}

func (m *mockGPUPipelineRunnerWithTiming) SetWipeMap(gradient []byte, config *wipemap.WipeConfig, width, height int) error {
	return nil
}

func (m *mockGPUPipelineRunnerWithTiming) ClearWipeMap() {}

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
