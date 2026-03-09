package output

import (
	"testing"
)

func TestOutputManager_InjectSCTE35_NoMuxer(t *testing.T) {
	// InjectSCTE35 with no muxer should be a no-op (no panic)
	m := &OutputManager{}
	if err := m.InjectSCTE35([]byte{0x00}); err != nil {
		t.Fatalf("expected nil error with no muxer, got: %v", err)
	}
}

func TestOutputManager_SetSCTE35Injector(t *testing.T) {
	m := &OutputManager{}
	mock := &mockSCTE35Injector{}
	m.SetSCTE35Injector(mock)
	if m.scte35Injector == nil {
		t.Fatal("expected non-nil scte35Injector")
	}
}

type mockSCTE35Injector struct {
	synthBytes []byte
}

func (m *mockSCTE35Injector) SyntheticBreakState() []byte {
	return m.synthBytes
}
