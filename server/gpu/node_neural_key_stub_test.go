//go:build !cgo || !cuda || !tensorrt

package gpu

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNeuralKeyNodeStubReturnsNil(t *testing.T) {
	node := NewGPUNeuralKeyNode(nil, nil, nil, "")
	assert.Nil(t, node, "stub should return nil")
}

// mockNeuralKeyState implements NeuralKeyState for testing.
type mockNeuralKeyState struct {
	hasKey     bool
	programKey string
	snapshot   *NeuralKeySnapshot
}

func (m *mockNeuralKeyState) HasNeuralKey() bool                { return m.hasKey }
func (m *mockNeuralKeyState) ProgramSourceKey() string          { return m.programKey }
func (m *mockNeuralKeyState) NeuralKeySnapshot() *NeuralKeySnapshot { return m.snapshot }

func TestNeuralKeyNodeStubReturnsNilEvenWithState(t *testing.T) {
	state := &mockNeuralKeyState{hasKey: true, programKey: "cam1"}
	node := NewGPUNeuralKeyNode(nil, nil, state, "/model.onnx")
	assert.Nil(t, node, "stub should return nil even with state")
}

func TestNeuralKeyStateInterfaceConformance(t *testing.T) {
	// Verify mockNeuralKeyState satisfies NeuralKeyState.
	var _ NeuralKeyState = (*mockNeuralKeyState)(nil)
}

func TestNeuralKeySnapshotFields(t *testing.T) {
	snap := &NeuralKeySnapshot{
		SourceKey:  "cam1",
		KeyColorY:  16,
		KeyColorCb: 128,
		KeyColorCr: 128,
		Similarity: 0.4,
		Smoothness: 0.08,
	}
	assert.Equal(t, "cam1", snap.SourceKey)
	assert.Equal(t, uint8(16), snap.KeyColorY)
	assert.Equal(t, uint8(128), snap.KeyColorCb)
	assert.Equal(t, uint8(128), snap.KeyColorCr)
	assert.InDelta(t, 0.4, float64(snap.Similarity), 0.001)
	assert.InDelta(t, 0.08, float64(snap.Smoothness), 0.001)
}

func TestMockNeuralKeyStateNoKey(t *testing.T) {
	state := &mockNeuralKeyState{hasKey: false}
	assert.False(t, state.HasNeuralKey())
	assert.Empty(t, state.ProgramSourceKey())
	assert.Nil(t, state.NeuralKeySnapshot())
}

func TestMockNeuralKeyStateWithSnapshot(t *testing.T) {
	snap := &NeuralKeySnapshot{
		SourceKey:  "cam2",
		KeyColorY:  82,
		KeyColorCb: 90,
		KeyColorCr: 240,
		Similarity: 0.35,
		Smoothness: 0.1,
	}
	state := &mockNeuralKeyState{
		hasKey:     true,
		programKey: "cam2",
		snapshot:   snap,
	}
	assert.True(t, state.HasNeuralKey())
	assert.Equal(t, "cam2", state.ProgramSourceKey())

	got := state.NeuralKeySnapshot()
	require.NotNil(t, got)
	assert.Equal(t, "cam2", got.SourceKey)
	assert.Equal(t, uint8(82), got.KeyColorY)
	assert.InDelta(t, 0.35, float64(got.Similarity), 0.001)
}
