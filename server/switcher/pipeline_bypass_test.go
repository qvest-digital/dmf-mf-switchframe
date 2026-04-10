package switcher

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestBypassSurvivesRebuild(t *testing.T) {
	n1 := &countingNode{name: "upstream_key", active: true}
	n2 := &countingNode{name: "compositor", active: true}

	p := &Pipeline{}
	require.NoError(t, p.Build(DefaultFormat, nil, []PipelineNode{n1, n2}))

	// Bypass a node
	require.NoError(t, p.SetBypass("upstream_key", true))

	// Rebuild with same nodes (simulating pipeline reconfiguration)
	n1b := &countingNode{name: "upstream_key", active: true}
	n2b := &countingNode{name: "compositor", active: true}

	p2 := &Pipeline{}
	p2.RestoreBypass(p.BypassState())
	require.NoError(t, p2.Build(DefaultFormat, nil, []PipelineNode{n1b, n2b}))

	// Bypass should persist
	state := p2.BypassState()
	require.True(t, state["upstream_key"], "bypass should survive rebuild")
	require.False(t, state["compositor"])

	// Verify it actually skips the node
	pf := &ProcessingFrame{
		Data:  make([]byte, ColorYUV420_8bit.FrameSize(4, 4)),
		Width: 4, Height: 4,
	}
	p2.Run(pf)
	require.Equal(t, 0, n1b.calls, "bypassed node should be skipped after rebuild")
	require.Equal(t, 1, n2b.calls, "non-bypassed node should run after rebuild")
}

func TestBypassInSnapshot(t *testing.T) {
	n1 := &countingNode{name: "upstream_key", active: true}
	n2 := &countingNode{name: "compositor", active: true}

	p := &Pipeline{}
	require.NoError(t, p.Build(DefaultFormat, nil, []PipelineNode{n1, n2}))
	require.NoError(t, p.SetBypass("upstream_key", true))

	// Run once so snapshot has data
	pf := &ProcessingFrame{
		Data:  make([]byte, ColorYUV420_8bit.FrameSize(4, 4)),
		Width: 4, Height: 4,
	}
	p.Run(pf)

	snap := p.Snapshot()
	nodes := snap["active_nodes"].([]map[string]any)
	require.Len(t, nodes, 2)

	require.Equal(t, "upstream_key", nodes[0]["name"])
	require.Equal(t, true, nodes[0]["bypassed"])

	require.Equal(t, "compositor", nodes[1]["name"])
	require.Equal(t, false, nodes[1]["bypassed"])
}

func TestBypassStateReturnsCurrentState(t *testing.T) {
	n1 := &countingNode{name: "upstream_key", active: true}
	n2 := &countingNode{name: "compositor", active: true}
	n3 := &countingNode{name: "gpu_encode", active: true}

	p := &Pipeline{}
	require.NoError(t, p.Build(DefaultFormat, nil, []PipelineNode{n1, n2, n3}))

	// Initially all false
	state := p.BypassState()
	require.Len(t, state, 3)
	require.False(t, state["upstream_key"])
	require.False(t, state["compositor"])
	require.False(t, state["gpu_encode"])

	// Bypass one node
	require.NoError(t, p.SetBypass("compositor", true))

	state = p.BypassState()
	require.False(t, state["upstream_key"])
	require.True(t, state["compositor"])
	require.False(t, state["gpu_encode"])
}

func TestBypassUnknownNodeReturnsError(t *testing.T) {
	n1 := &countingNode{name: "upstream_key", active: true}

	p := &Pipeline{}
	require.NoError(t, p.Build(DefaultFormat, nil, []PipelineNode{n1}))

	err := p.SetBypass("nonexistent", true)
	require.Error(t, err)
	require.Contains(t, err.Error(), "not found")
}

func TestBypassProtectedNodeReturnsError(t *testing.T) {
	enc := &countingNode{name: "gpu_encode", active: true}

	p := &Pipeline{}
	require.NoError(t, p.Build(DefaultFormat, nil, []PipelineNode{enc}))

	err := p.SetBypass("gpu_encode", true)
	require.Error(t, err)
	require.Contains(t, err.Error(), "protected")
}

func TestBypassReenableResumesProcessing(t *testing.T) {
	n1 := &countingNode{name: "upstream_key", active: true}

	p := &Pipeline{}
	require.NoError(t, p.Build(DefaultFormat, nil, []PipelineNode{n1}))

	// Bypass then re-enable
	require.NoError(t, p.SetBypass("upstream_key", true))
	require.NoError(t, p.SetBypass("upstream_key", false))

	pf := &ProcessingFrame{
		Data:  make([]byte, ColorYUV420_8bit.FrameSize(4, 4)),
		Width: 4, Height: 4,
	}
	p.Run(pf)

	require.Equal(t, 1, n1.calls, "re-enabled node should be called")
}

func TestBypassSkipsNode(t *testing.T) {
	n1 := &countingNode{name: "upstream_key", active: true}
	n2 := &countingNode{name: "gpu_encode", active: true}

	p := &Pipeline{}
	require.NoError(t, p.Build(DefaultFormat, nil, []PipelineNode{n1, n2}))

	// Bypass n1
	require.NoError(t, p.SetBypass("upstream_key", true))

	pf := &ProcessingFrame{
		Data:  make([]byte, ColorYUV420_8bit.FrameSize(4, 4)),
		Width: 4, Height: 4,
	}
	p.Run(pf)

	require.Equal(t, 0, n1.calls, "bypassed node should not be called")
	require.Equal(t, 1, n2.calls, "non-bypassed node should be called")
}
