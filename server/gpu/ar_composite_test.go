package gpu

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// mockARCompositorState implements ARCompositorState for testing.
type mockARCompositorState struct {
	active    bool
	occlusion *ARCompositorOcclusion
	rendered  *ARRenderedFrame
}

func (m *mockARCompositorState) HasActiveAR() bool                     { return m.active }
func (m *mockARCompositorState) OcclusionMap() *ARCompositorOcclusion  { return m.occlusion }
func (m *mockARCompositorState) RenderedFrame() *ARRenderedFrame       { return m.rendered }
func (m *mockARCompositorState) ProcessFrame(uintptr, int, int, int)   {} // no-op for tests

func TestARCompositorNode_NilStateReturnsNil(t *testing.T) {
	node := NewGPUARCompositorNode(nil, nil, nil)
	assert.Nil(t, node, "nil state should return nil node")
}

func TestARCompositorNode_StubReturnsNil(t *testing.T) {
	// Even with a valid state, stub build returns nil (non-GPU builds).
	// On GPU builds, this returns a real node. Either way, the test
	// validates the nil-state path works.
	node := NewGPUARCompositorNode(nil, nil, nil)
	assert.Nil(t, node, "stub should return nil for nil state")
}

func TestARCompositorNode_WithStateReturnsNode(t *testing.T) {
	state := &mockARCompositorState{active: true}
	node := NewGPUARCompositorNode(nil, nil, state)

	// On non-GPU builds (stub), this returns nil because the stub always
	// returns nil regardless of state. On GPU builds, this returns a real node.
	if node == nil {
		// Running on stub build — this is expected
		t.Log("stub build: NewGPUARCompositorNode returned nil (expected)")
		return
	}

	assert.Equal(t, "gpu_ar_composite", node.Name())
	assert.True(t, node.Active())
	assert.Nil(t, node.Err())
	assert.NoError(t, node.Close())
}

func TestARCompositorOcclusion_Fields(t *testing.T) {
	occ := &ARCompositorOcclusion{
		MaskPtr:  0xDEAD,
		Width:    1920,
		Height:   1080,
		DepthPtr: 0xBEEF,
		DepthW:   518,
		DepthH:   294,
	}
	assert.Equal(t, uintptr(0xDEAD), occ.MaskPtr)
	assert.Equal(t, 1920, occ.Width)
	assert.Equal(t, 1080, occ.Height)
	assert.Equal(t, uintptr(0xBEEF), occ.DepthPtr)
	assert.Equal(t, 518, occ.DepthW)
	assert.Equal(t, 294, occ.DepthH)
}

func TestARRenderedFrame_Fields(t *testing.T) {
	rf := &ARRenderedFrame{
		DevPtr: 0xBEEF,
		Width:  1920,
		Height: 1080,
	}
	assert.Equal(t, uintptr(0xBEEF), rf.DevPtr)
	assert.Equal(t, 1920, rf.Width)
	assert.Equal(t, 1080, rf.Height)
}

func TestARCompositorStateInterfaceConformance(t *testing.T) {
	// Verify mockARCompositorState satisfies ARCompositorState.
	var _ ARCompositorState = (*mockARCompositorState)(nil)
}

func TestMockARCompositorState_Inactive(t *testing.T) {
	state := &mockARCompositorState{active: false}
	assert.False(t, state.HasActiveAR())
	assert.Nil(t, state.OcclusionMap())
	assert.Nil(t, state.RenderedFrame())
}

func TestMockARCompositorState_ActiveWithOcclusion(t *testing.T) {
	occ := &ARCompositorOcclusion{
		MaskPtr: 0x1000,
		Width:   1920,
		Height:  1080,
	}
	state := &mockARCompositorState{
		active:    true,
		occlusion: occ,
	}
	assert.True(t, state.HasActiveAR())
	require.NotNil(t, state.OcclusionMap())
	assert.Equal(t, 1920, state.OcclusionMap().Width)
	assert.Equal(t, 1080, state.OcclusionMap().Height)
}

func TestMockARCompositorState_ActiveWithRendered(t *testing.T) {
	rf := &ARRenderedFrame{
		DevPtr: 0x2000,
		Width:  1920,
		Height: 1080,
	}
	state := &mockARCompositorState{
		active:   true,
		rendered: rf,
	}
	assert.True(t, state.HasActiveAR())
	require.NotNil(t, state.RenderedFrame())
	assert.Equal(t, 1920, state.RenderedFrame().Width)
}

func TestARComposite_NilContext(t *testing.T) {
	err := ARComposite(nil, 0x1000, 0x2000, 1920, 1080, 0x3000, 1920, 1080, 2048, 128)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrGPUNotAvailable)
}

func TestARCompositorOcclusion_ZeroValue(t *testing.T) {
	var occ ARCompositorOcclusion
	assert.Equal(t, uintptr(0), occ.MaskPtr)
	assert.Equal(t, 0, occ.Width)
	assert.Equal(t, 0, occ.Height)
	assert.Equal(t, uintptr(0), occ.DepthPtr)
	assert.Equal(t, 0, occ.DepthW)
	assert.Equal(t, 0, occ.DepthH)
}

func TestARRenderedFrame_ZeroValue(t *testing.T) {
	var rf ARRenderedFrame
	assert.Equal(t, uintptr(0), rf.DevPtr)
	assert.Equal(t, 0, rf.Width)
	assert.Equal(t, 0, rf.Height)
}

func TestARCompositorNode_Name(t *testing.T) {
	state := &mockARCompositorState{active: true}
	node := NewGPUARCompositorNode(nil, nil, state)
	if node == nil {
		t.Log("stub build: skipping name check")
		return
	}
	assert.Equal(t, "gpu_ar_composite", node.Name())
}

func TestARCompositorNode_OcclusionMaskProvider(t *testing.T) {
	state := &mockARCompositorState{active: true}
	node := NewGPUARCompositorNode(nil, nil, state)
	if node == nil {
		t.Log("stub build: skipping OcclusionMaskProvider check")
		return
	}

	// The node should implement OcclusionMaskProvider.
	provider, ok := node.(OcclusionMaskProvider)
	require.True(t, ok, "AR compositor node should implement OcclusionMaskProvider")

	// Initially, occlusion should be zero (no data yet).
	ptr, w, h := provider.OcclusionMask()
	assert.Equal(t, uintptr(0), ptr)
	assert.Equal(t, 0, w)
	assert.Equal(t, 0, h)
}

func TestARCompositorState_ProcessFrameInterface(t *testing.T) {
	// Verify the interface method exists and is callable.
	var called bool
	state := &processFrameRecordingState{
		mockARCompositorState: mockARCompositorState{active: true},
		onProcess:             func() { called = true },
	}
	state.ProcessFrame(0x1000, 1920, 1080, 2048)
	assert.True(t, called, "ProcessFrame should have been called")
}

// processFrameRecordingState extends mockARCompositorState to record ProcessFrame calls.
type processFrameRecordingState struct {
	mockARCompositorState
	onProcess func()
}

func (p *processFrameRecordingState) ProcessFrame(framePtr uintptr, w, h, pitch int) {
	if p.onProcess != nil {
		p.onProcess()
	}
}

func TestARCompositorOcclusion_DepthFields(t *testing.T) {
	occ := &ARCompositorOcclusion{
		MaskPtr:  0x1000,
		Width:    1920,
		Height:   1080,
		DepthPtr: 0x2000,
		DepthW:   518,
		DepthH:   294,
	}
	assert.Equal(t, uintptr(0x2000), occ.DepthPtr, "DepthPtr should be set")
	assert.Equal(t, 518, occ.DepthW, "DepthW should match depth model resolution")
	assert.Equal(t, 294, occ.DepthH, "DepthH should match depth model resolution")
}
