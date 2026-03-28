package gpu

import (
	"testing"
)

func TestNewGPUColorGradeNode_NilState(t *testing.T) {
	node := NewGPUColorGradeNode(nil, nil)
	if node != nil {
		t.Error("expected nil node for nil state")
	}
}

type mockColorGradeState struct {
	enabled   bool
	lutData   []float32
	lutSize   int
	lutGen    uint64
	intensity float32
}

func (m *mockColorGradeState) HasGradingEnabled() bool   { return m.enabled }
func (m *mockColorGradeState) GradingLUTData() []float32 { return m.lutData }
func (m *mockColorGradeState) GradingLUTSize() int       { return m.lutSize }
func (m *mockColorGradeState) GradingLUTGen() uint64     { return m.lutGen }
func (m *mockColorGradeState) GradingIntensity() float32 { return m.intensity }

func TestGPUColorGradeNode_Interface(t *testing.T) {
	state := &mockColorGradeState{enabled: false}
	node := NewGPUColorGradeNode(&Context{}, state)
	if node == nil {
		// Stub build — constructor returns nil. Verify that's expected.
		t.Skip("skipping on non-GPU build (stub returns nil)")
	}

	if node.Name() != "gpu_colorgrade" {
		t.Errorf("name = %q, want %q", node.Name(), "gpu_colorgrade")
	}

	if node.Active() {
		t.Error("expected Active() = false when disabled")
	}

	state.enabled = true
	if !node.Active() {
		t.Error("expected Active() = true when enabled")
	}

	if err := node.Configure(1920, 1080, 2048); err != nil {
		t.Errorf("Configure error: %v", err)
	}

	if err := node.Err(); err != nil {
		t.Errorf("Err() = %v, want nil", err)
	}

	if lat := node.Latency(); lat <= 0 {
		t.Errorf("Latency() = %v, want > 0", lat)
	}

	if err := node.Close(); err != nil {
		t.Errorf("Close error: %v", err)
	}
}

func TestColorGradeLUT3DP210_Stub_NilGuards(t *testing.T) {
	// Verify the P210 stub returns an error for nil context.
	err := ColorGradeLUT3DP210(nil, &GPUFrame{}, 0, 17, 1.0)
	if err == nil {
		t.Error("expected error for nil context from stub/nil check")
	}
}
