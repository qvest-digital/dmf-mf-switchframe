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

func TestNeuralKeyNodeStubReturnsNilEvenWithState(t *testing.T) {
	state := &mockNeuralKeyStateV2{
		keys: []NeuralKeySnapshot{{SourceKey: "cam1"}},
	}
	node := NewGPUNeuralKeyNode(nil, nil, state, "/model.onnx")
	assert.Nil(t, node, "stub should return nil even with state")
}

func TestNeuralKeyStateInterfaceConformance(t *testing.T) {
	var _ NeuralKeyState = (*mockNeuralKeyStateV2)(nil)
}

func TestNeuralKeySnapshotFields(t *testing.T) {
	snap := &NeuralKeySnapshot{
		SourceKey:  "cam1",
		KeyColorY:  16,
		KeyColorCb: 128,
		KeyColorCr: 128,
		Similarity: 0.4,
		Smoothness: 0.08,
		FillFrame:  &GPUFrame{Width: 1920, Height: 1080},
	}
	assert.Equal(t, "cam1", snap.SourceKey)
	assert.Equal(t, uint8(16), snap.KeyColorY)
	assert.Equal(t, uint8(128), snap.KeyColorCb)
	assert.Equal(t, uint8(128), snap.KeyColorCr)
	assert.InDelta(t, 0.4, float64(snap.Similarity), 0.001)
	assert.InDelta(t, 0.08, float64(snap.Smoothness), 0.001)
	require.NotNil(t, snap.FillFrame)
}

func TestMockNeuralKeyStateNoKeys(t *testing.T) {
	state := &mockNeuralKeyStateV2{}
	assert.False(t, state.HasNeuralKeys())
	assert.Empty(t, state.SnapshotNeuralKeys())
}

func TestMockNeuralKeyStateWithKeys(t *testing.T) {
	state := &mockNeuralKeyStateV2{
		keys: []NeuralKeySnapshot{
			{
				SourceKey:  "clip:1",
				KeyColorCb: 42,
				KeyColorCr: 26,
				Similarity: 0.4,
				Smoothness: 0.1,
				FillFrame:  &GPUFrame{Width: 1280, Height: 720},
			},
		},
	}
	assert.True(t, state.HasNeuralKeys())
	snaps := state.SnapshotNeuralKeys()
	require.Len(t, snaps, 1)
	assert.Equal(t, "clip:1", snaps[0].SourceKey)
	assert.NotNil(t, snaps[0].FillFrame)
}
