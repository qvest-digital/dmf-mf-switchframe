//go:build !cgo || !cuda || !tensorrt

package gpu

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAISegmentNodeStubReturnsNil(t *testing.T) {
	node := NewGPUAISegmentNode(nil, nil, nil)
	assert.Nil(t, node, "stub should return nil")
}

// mockSegState implements SegmentationState for testing.
type mockSegState struct {
	enabled    bool
	programKey string
	mask       *GPUFrame
	configs    map[string]*AISegmentConfig
}

func (m *mockSegState) HasEnabledSources() bool { return m.enabled }
func (m *mockSegState) ProgramSourceKey() string { return m.programKey }
func (m *mockSegState) MaskForSource(key string) *GPUFrame {
	return m.mask
}
func (m *mockSegState) ConfigForSource(key string) *AISegmentConfig {
	if m.configs == nil {
		return nil
	}
	return m.configs[key]
}

func TestAISegmentNodeStubReturnsNilEvenWithState(t *testing.T) {
	state := &mockSegState{enabled: true, programKey: "cam1"}
	node := NewGPUAISegmentNode(nil, nil, state)
	assert.Nil(t, node, "stub should return nil even with state")
}

func TestSegmentationStateInterfaceConformance(t *testing.T) {
	// Verify mockSegState satisfies SegmentationState.
	var _ SegmentationState = (*mockSegState)(nil)
}

func TestAISegmentConfigFields(t *testing.T) {
	cfg := &AISegmentConfig{
		Background:  "blur:15",
		Sensitivity: 0.7,
	}
	assert.Equal(t, "blur:15", cfg.Background)
	assert.InDelta(t, 0.7, float64(cfg.Sensitivity), 0.001)
}

func TestAISegmentConfigTransparentMode(t *testing.T) {
	cfg := &AISegmentConfig{
		Background:  "transparent",
		Sensitivity: 0.5,
	}
	assert.Equal(t, "transparent", cfg.Background)
}

func TestAISegmentConfigColorMode(t *testing.T) {
	cfg := &AISegmentConfig{
		Background:  "color:00FF00",
		Sensitivity: 0.5,
	}
	assert.Equal(t, "color:00FF00", cfg.Background)
}

func TestMockSegStateNoEnabledSources(t *testing.T) {
	state := &mockSegState{enabled: false}
	assert.False(t, state.HasEnabledSources())
	assert.Empty(t, state.ProgramSourceKey())
	assert.Nil(t, state.MaskForSource("cam1"))
	assert.Nil(t, state.ConfigForSource("cam1"))
}

func TestMockSegStateWithConfig(t *testing.T) {
	state := &mockSegState{
		enabled:    true,
		programKey: "cam1",
		configs: map[string]*AISegmentConfig{
			"cam1": {Background: "blur:20", Sensitivity: 0.6},
		},
	}
	assert.True(t, state.HasEnabledSources())
	assert.Equal(t, "cam1", state.ProgramSourceKey())

	cfg := state.ConfigForSource("cam1")
	require.NotNil(t, cfg)
	assert.Equal(t, "blur:20", cfg.Background)
	assert.InDelta(t, 0.6, float64(cfg.Sensitivity), 0.001)

	// Non-existent source returns nil.
	assert.Nil(t, state.ConfigForSource("cam2"))
}
