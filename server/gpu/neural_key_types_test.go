package gpu

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestNeuralKeySnapshot_HasFillFrame verifies that NeuralKeySnapshot carries
// a GPU fill frame reference for compositing the keyed source onto program.
func TestNeuralKeySnapshot_HasFillFrame(t *testing.T) {
	snap := NeuralKeySnapshot{
		SourceKey:  "clip:1",
		KeyColorCb: 42,
		KeyColorCr: 26,
		Similarity: 0.4,
		Smoothness: 0.1,
		FillFrame:  &GPUFrame{Width: 1280, Height: 720},
	}
	require.NotNil(t, snap.FillFrame, "snapshot should carry fill frame")
	assert.Equal(t, 1280, snap.FillFrame.Width)
	assert.Equal(t, 720, snap.FillFrame.Height)
}

// TestNeuralKeyState_SnapshotNeuralKeys verifies the interface returns
// snapshots for non-program sources with neural keys.
func TestNeuralKeyState_SnapshotNeuralKeys(t *testing.T) {
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

	require.True(t, state.HasNeuralKeys())
	snaps := state.SnapshotNeuralKeys()
	require.Len(t, snaps, 1)
	assert.Equal(t, "clip:1", snaps[0].SourceKey)
	assert.NotNil(t, snaps[0].FillFrame)
}

// TestNeuralKeyState_NoKeys verifies empty state returns false/nil.
func TestNeuralKeyState_NoKeys(t *testing.T) {
	state := &mockNeuralKeyStateV2{}
	assert.False(t, state.HasNeuralKeys())
	assert.Empty(t, state.SnapshotNeuralKeys())
}

// mockNeuralKeyStateV2 implements the new NeuralKeyState interface.
type mockNeuralKeyStateV2 struct {
	keys []NeuralKeySnapshot
}

func (m *mockNeuralKeyStateV2) HasNeuralKeys() bool {
	return len(m.keys) > 0
}

func (m *mockNeuralKeyStateV2) SnapshotNeuralKeys() []NeuralKeySnapshot {
	return m.keys
}
