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

func TestPipelineLoop_SnapshotIncludesEpoch(t *testing.T) {
	n := &countingNode{name: "a", active: true}
	p := &Pipeline{}
	require.NoError(t, p.Build(DefaultFormat, nil, []PipelineNode{n}))
	p.epoch = 42

	snap := p.Snapshot()
	require.Equal(t, uint64(42), snap["epoch"])
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
