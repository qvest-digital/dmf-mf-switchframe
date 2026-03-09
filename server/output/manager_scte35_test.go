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

func TestOutputManager_RebuildAdapters_SCTE35Filter(t *testing.T) {
	// Verify that rebuildAdaptersLocked wraps destination adapters with
	// scte35Filter when SCTE35Enabled is false.
	m := NewOutputManager(nil)

	// Create two destinations: one with SCTE-35 enabled, one without.
	destEnabled := &OutputDestination{
		id:     "enabled",
		config: DestinationConfig{SCTE35Enabled: true},
		adapter: &mockFilterWriter{},
		active: true,
	}
	destDisabled := &OutputDestination{
		id:     "disabled",
		config: DestinationConfig{SCTE35Enabled: false},
		adapter: &mockFilterWriter{},
		active: true,
	}

	m.mu.Lock()
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
