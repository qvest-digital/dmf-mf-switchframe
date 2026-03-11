package output

import (
	"testing"
)

func TestManager_InjectSCTE35_NoMuxer(t *testing.T) {
	// InjectSCTE35 with no muxer should be a no-op (no panic)
	m := &Manager{}
	if err := m.InjectSCTE35([]byte{0x00}); err != nil {
		t.Fatalf("expected nil error with no muxer, got: %v", err)
	}
}

func TestManager_SetSCTE35Injector(t *testing.T) {
	m := &Manager{}
	mock := &mockSCTE35Injector{}
	m.SetSCTE35Injector(mock, 0x102)
	if m.scte35Injector == nil {
		t.Fatal("expected non-nil scte35Injector")
	}
	if m.scte35PID != 0x102 {
		t.Fatalf("expected scte35PID=0x102, got 0x%X", m.scte35PID)
	}
}

type mockSCTE35Injector struct {
	synthBytes []byte
}

func (m *mockSCTE35Injector) SyntheticBreakState() []byte {
	return m.synthBytes
}

func TestManager_RebuildAdapters_SCTE35Filter(t *testing.T) {
	// Verify that rebuildAdaptersLocked wraps destination adapters with
	// scte35Filter when SCTE35Enabled is false.
	m := NewManager(nil)

	// Create two destinations: one with SCTE-35 enabled, one without.
	destEnabled := &Destination{
		id:     "enabled",
		config: DestinationConfig{SCTE35Enabled: true},
		adapter: &mockFilterWriter{},
		active: true,
	}
	destDisabled := &Destination{
		id:     "disabled",
		config: DestinationConfig{SCTE35Enabled: false},
		adapter: &mockFilterWriter{},
		active: true,
	}

	m.mu.Lock()
	m.scte35PID = defaultSCTE35PID // enable SCTE-35 filtering
	m.destinations["enabled"] = destEnabled
	m.destinations["disabled"] = destDisabled
	stale := m.rebuildAdaptersLocked()
	adapters := *m.adapters.Load()
	m.mu.Unlock()

	// Stop any stale wrappers.
	for _, w := range stale {
		w.Stop()
	}

	if len(adapters) != 2 {
		t.Fatalf("expected 2 adapters, got %d", len(adapters))
	}

	// Check that the wrapper for the disabled destination has a scte35Filter.
	// The asyncWrappers map stores wrappers keyed by "dest:"+id.
	m.mu.Lock()
	disabledWrapper := m.asyncWrappers["dest:disabled"]
	enabledWrapper := m.asyncWrappers["dest:enabled"]
	m.mu.Unlock()

	if disabledWrapper == nil {
		t.Fatal("expected async wrapper for disabled destination")
	}
	if enabledWrapper == nil {
		t.Fatal("expected async wrapper for enabled destination")
	}

	// The disabled destination's inner adapter should be a scte35Filter.
	if _, ok := disabledWrapper.inner.(*scte35Filter); !ok {
		t.Errorf("expected scte35Filter for disabled destination, got %T", disabledWrapper.inner)
	}

	// The enabled destination's inner adapter should be the raw mockFilterWriter.
	if _, ok := enabledWrapper.inner.(*mockFilterWriter); !ok {
		t.Errorf("expected mockFilterWriter for enabled destination, got %T", enabledWrapper.inner)
	}

	// Stop wrappers for cleanup.
	for _, w := range m.asyncWrappers {
		w.Stop()
	}
}
