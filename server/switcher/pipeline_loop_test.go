package switcher

import (
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// countingNode counts Process calls and optionally modifies YUV.
type countingNode struct {
	name      string
	active    bool
	calls     int
	latency   time.Duration
	marker    byte // if non-zero, sets YUV[0] to this value
	lastErr   error
	configureCalled bool
}

func (n *countingNode) Name() string                          { return n.name }
func (n *countingNode) Configure(format PipelineFormat) error { n.configureCalled = true; return nil }
func (n *countingNode) Active() bool                          { return n.active }
func (n *countingNode) Err() error                            { return n.lastErr }
func (n *countingNode) Latency() time.Duration                { return n.latency }
func (n *countingNode) Close() error                          { return nil }

func (n *countingNode) Process(dst, src *ProcessingFrame) *ProcessingFrame {
	n.calls++
	if n.marker != 0 {
		src.YUV[0] = n.marker
	}
	return src
}

// failConfigNode fails on Configure.
type failConfigNode struct{ countingNode }

func (n *failConfigNode) Configure(format PipelineFormat) error {
	return fmt.Errorf("config failed")
}

func TestPipelineLoop_Build(t *testing.T) {
	n1 := &countingNode{name: "node1", active: true, latency: time.Microsecond}
	n2 := &countingNode{name: "node2", active: false, latency: 2 * time.Microsecond}
	n3 := &countingNode{name: "node3", active: true, latency: 3 * time.Microsecond}

	p := &Pipeline{}
	err := p.Build(DefaultFormat, nil, []PipelineNode{n1, n2, n3})
	require.NoError(t, err)

	require.True(t, n1.configureCalled)
	require.True(t, n2.configureCalled, "Configure called on all nodes, even inactive")
	require.True(t, n3.configureCalled)

	// Only active nodes in activeNodes
	require.Len(t, p.activeNodes, 2)
	require.Equal(t, "node1", p.activeNodes[0].Name())
	require.Equal(t, "node3", p.activeNodes[1].Name())

	// Total latency = sum of active nodes only
	require.Equal(t, 4*time.Microsecond, p.TotalLatency())
}

func TestPipelineLoop_BuildConfigureError(t *testing.T) {
	n := &failConfigNode{countingNode: countingNode{name: "bad", active: true}}
	p := &Pipeline{}
	err := p.Build(DefaultFormat, nil, []PipelineNode{n})
	require.Error(t, err)
	require.Contains(t, err.Error(), "bad")
	require.Contains(t, err.Error(), "config failed")
}

func TestPipelineLoop_Run(t *testing.T) {
	n1 := &countingNode{name: "first", active: true, marker: 0xAA}
	n2 := &countingNode{name: "second", active: true, marker: 0xBB}

	p := &Pipeline{}
	require.NoError(t, p.Build(DefaultFormat, nil, []PipelineNode{n1, n2}))

	pf := &ProcessingFrame{
		YUV:   make([]byte, 4*4*3/2),
		Width: 4, Height: 4,
	}

	out := p.Run(pf)
	require.Same(t, pf, out)
	require.Equal(t, 1, n1.calls)
	require.Equal(t, 1, n2.calls)
	// Last node's marker wins
	require.Equal(t, byte(0xBB), out.YUV[0])
}

func TestPipelineLoop_RunSkipsInactive(t *testing.T) {
	active := &countingNode{name: "active", active: true}
	inactive := &countingNode{name: "inactive", active: false}

	p := &Pipeline{}
	require.NoError(t, p.Build(DefaultFormat, nil, []PipelineNode{active, inactive}))

	pf := &ProcessingFrame{
		YUV:   make([]byte, 4*4*3/2),
		Width: 4, Height: 4,
	}

	p.Run(pf)
	require.Equal(t, 1, active.calls)
	require.Equal(t, 0, inactive.calls, "inactive node should not be called")
}

func TestPipelineLoop_Snapshot(t *testing.T) {
	n1 := &countingNode{name: "fast", active: true, latency: time.Microsecond}
	n2 := &countingNode{name: "slow", active: true, latency: 10 * time.Millisecond}

	p := &Pipeline{}
	require.NoError(t, p.Build(DefaultFormat, nil, []PipelineNode{n1, n2}))

	pf := &ProcessingFrame{
		YUV:   make([]byte, 4*4*3/2),
		Width: 4, Height: 4,
	}
	p.Run(pf)

	snap := p.Snapshot()
	require.Equal(t, 2, snap["total_nodes"])
	require.Equal(t, int64(1), snap["run_count"])
	require.Greater(t, snap["last_run_ns"], int64(0))

	nodes := snap["active_nodes"].([]map[string]any)
	require.Len(t, nodes, 2)
	require.Equal(t, "fast", nodes[0]["name"])
	require.Equal(t, "slow", nodes[1]["name"])
	require.GreaterOrEqual(t, nodes[0]["last_ns"], int64(0))
}

func TestPipelineLoop_WaitAndClose(t *testing.T) {
	n := &countingNode{name: "test", active: true}
	p := &Pipeline{}
	require.NoError(t, p.Build(DefaultFormat, nil, []PipelineNode{n}))

	// Run a frame, then wait
	pf := &ProcessingFrame{
		YUV:   make([]byte, 4*4*3/2),
		Width: 4, Height: 4,
	}
	p.Run(pf)
	p.Wait() // should return immediately — Run already completed

	err := p.Close()
	require.NoError(t, err)
}

// createTestSwitcher creates a minimal Switcher for pipeline tests.
// Uses nil relay — sufficient for testing pipeline swap mechanics.
func createTestSwitcher(t *testing.T) *Switcher {
	t.Helper()
	return New(nil)
}

func TestPipelineLoop_SnapshotIncludesEpoch(t *testing.T) {
	n := &countingNode{name: "a", active: true}
	p := &Pipeline{}
	require.NoError(t, p.Build(DefaultFormat, nil, []PipelineNode{n}))
	p.epoch = 42

	snap := p.Snapshot()
	require.Equal(t, uint64(42), snap["epoch"])
}

func TestSwapPipeline_NilOldPipeline(t *testing.T) {
	sw := createTestSwitcher(t)
	defer sw.Close()

	n := &countingNode{name: "a", active: true}
	p := &Pipeline{}
	require.NoError(t, p.Build(DefaultFormat, nil, []PipelineNode{n}))

	// Swap into empty — no old pipeline to drain.
	sw.swapPipeline(p)

	loaded := sw.pipeline.Load()
	require.NotNil(t, loaded)
	require.Equal(t, 1, len(loaded.activeNodes))
}

func TestSwapPipeline_OldPipelineDrained(t *testing.T) {
	sw := createTestSwitcher(t)
	defer sw.Close()

	// Build and install initial pipeline.
	n1 := &countingNode{name: "old", active: true}
	old := &Pipeline{}
	require.NoError(t, old.Build(DefaultFormat, nil, []PipelineNode{n1}))
	sw.pipeline.Store(old)

	// Build replacement.
	n2 := &countingNode{name: "new", active: true}
	newP := &Pipeline{}
	require.NoError(t, newP.Build(DefaultFormat, nil, []PipelineNode{n2}))

	sw.swapPipeline(newP)

	loaded := sw.pipeline.Load()
	require.Equal(t, "new", loaded.activeNodes[0].Name())
}

func TestRebuildPipeline_NoPipeCodecsNoop(t *testing.T) {
	sw := createTestSwitcher(t)
	defer sw.Close()

	// No pipeCodecs set — rebuildPipeline should be a no-op.
	sw.rebuildPipeline()
	require.Nil(t, sw.pipeline.Load())
}

func TestRebuildPipeline_IncrementsEpoch(t *testing.T) {
	sw := createTestSwitcher(t)
	defer sw.Close()

	// Set up minimal pipeCodecs so rebuild proceeds.
	sw.mu.Lock()
	sw.pipeCodecs = &pipelineCodecs{}
	sw.mu.Unlock()
	sw.framePool = NewFramePool(4, DefaultFormat.Width, DefaultFormat.Height)

	before := sw.pipelineEpoch.Load()
	sw.rebuildPipeline()
	after := sw.pipelineEpoch.Load()

	require.Equal(t, before+1, after)
	require.NotNil(t, sw.pipeline.Load())
}

func TestSetCompositor_TriggersPipelineRebuild(t *testing.T) {
	sw := createTestSwitcher(t)
	defer sw.Close()

	// Set up pipeCodecs + framePool so rebuild works.
	sw.mu.Lock()
	sw.pipeCodecs = &pipelineCodecs{}
	sw.mu.Unlock()
	sw.framePool = NewFramePool(4, DefaultFormat.Width, DefaultFormat.Height)

	epochBefore := sw.pipelineEpoch.Load()
	sw.SetCompositor(nil)
	epochAfter := sw.pipelineEpoch.Load()

	require.Equal(t, epochBefore+1, epochAfter, "SetCompositor should trigger rebuildPipeline")
}

func TestSetKeyBridge_TriggersPipelineRebuild(t *testing.T) {
	sw := createTestSwitcher(t)
	defer sw.Close()

	sw.mu.Lock()
	sw.pipeCodecs = &pipelineCodecs{}
	sw.mu.Unlock()
	sw.framePool = NewFramePool(4, DefaultFormat.Width, DefaultFormat.Height)

	epochBefore := sw.pipelineEpoch.Load()
	sw.SetKeyBridge(nil)
	epochAfter := sw.pipelineEpoch.Load()

	require.Equal(t, epochBefore+1, epochAfter, "SetKeyBridge should trigger rebuildPipeline")
}

func TestSetRawVideoSink_TriggersPipelineRebuild(t *testing.T) {
	sw := createTestSwitcher(t)
	defer sw.Close()

	sw.mu.Lock()
	sw.pipeCodecs = &pipelineCodecs{}
	sw.mu.Unlock()
	sw.framePool = NewFramePool(4, DefaultFormat.Width, DefaultFormat.Height)

	epochBefore := sw.pipelineEpoch.Load()
	sink := RawVideoSink(func(pf *ProcessingFrame) {})
	sw.SetRawVideoSink(sink)
	epochAfter := sw.pipelineEpoch.Load()

	require.Equal(t, epochBefore+1, epochAfter, "SetRawVideoSink should trigger rebuildPipeline")
}

func TestSetRawMonitorSink_TriggersPipelineRebuild(t *testing.T) {
	sw := createTestSwitcher(t)
	defer sw.Close()

	sw.mu.Lock()
	sw.pipeCodecs = &pipelineCodecs{}
	sw.mu.Unlock()
	sw.framePool = NewFramePool(4, DefaultFormat.Width, DefaultFormat.Height)

	epochBefore := sw.pipelineEpoch.Load()
	sink := RawVideoSink(func(pf *ProcessingFrame) {})
	sw.SetRawMonitorSink(sink)
	epochAfter := sw.pipelineEpoch.Load()

	require.Equal(t, epochBefore+1, epochAfter, "SetRawMonitorSink should trigger rebuildPipeline")
}

func TestSetPipelineFormat_SwapsPipeline(t *testing.T) {
	sw := createTestSwitcher(t)
	defer sw.Close()

	// Set up pipeCodecs + initial pipeline.
	sw.mu.Lock()
	sw.pipeCodecs = &pipelineCodecs{}
	sw.mu.Unlock()
	sw.framePool = NewFramePool(4, DefaultFormat.Width, DefaultFormat.Height)
	sw.rebuildPipeline()
	require.NotNil(t, sw.pipeline.Load())

	epochBefore := sw.pipelineEpoch.Load()

	// Change format — should swap pipeline and increment epoch.
	newFormat := PipelineFormat{Width: 1280, Height: 720, FPSNum: 30, FPSDen: 1, Name: "720p30"}
	err := sw.SetPipelineFormat(newFormat)
	require.NoError(t, err)

	epochAfter := sw.pipelineEpoch.Load()
	require.Greater(t, epochAfter, epochBefore, "SetPipelineFormat should increment epoch")

	p := sw.pipeline.Load()
	require.NotNil(t, p)
}

func TestClose_SwapsNilAndWaits(t *testing.T) {
	sw := createTestSwitcher(t)

	// Install a pipeline.
	sw.mu.Lock()
	sw.pipeCodecs = &pipelineCodecs{}
	sw.mu.Unlock()
	sw.framePool = NewFramePool(4, DefaultFormat.Width, DefaultFormat.Height)
	sw.rebuildPipeline()
	require.NotNil(t, sw.pipeline.Load())

	sw.Close()

	// After Close, pipeline should be nil.
	require.Nil(t, sw.pipeline.Load(), "Close should swap pipeline to nil")
}

func TestBuildPipeline_SetsEpoch(t *testing.T) {
	sw := createTestSwitcher(t)
	defer sw.Close()

	sw.mu.Lock()
	sw.pipeCodecs = &pipelineCodecs{}
	sw.mu.Unlock()
	sw.framePool = NewFramePool(4, DefaultFormat.Width, DefaultFormat.Height)

	err := sw.BuildPipeline()
	require.NoError(t, err)

	p := sw.pipeline.Load()
	require.NotNil(t, p)
	require.Equal(t, uint64(1), p.epoch, "initial BuildPipeline should set epoch 1")
	require.Equal(t, uint64(1), sw.pipelineEpoch.Load())
}

func TestPipelineLoop_EmptyPipeline(t *testing.T) {
	p := &Pipeline{}
	require.NoError(t, p.Build(DefaultFormat, nil, nil))

	pf := &ProcessingFrame{
		YUV:   make([]byte, 4*4*3/2),
		Width: 4, Height: 4,
	}
	out := p.Run(pf)
	require.Same(t, pf, out)

	snap := p.Snapshot()
	require.Len(t, snap["active_nodes"].([]map[string]any), 0)
}
