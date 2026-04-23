package switcher

import (
	"sync/atomic"
	"testing"
)

func TestHotState_AtomicPointer_LoadStoreRoundTrip(t *testing.T) {
	var hs atomic.Pointer[hotState]
	initial := &hotState{
		programSource:        "cam1",
		previewSource:        "cam2",
		state:                StateIdle,
		transitionMaxForward: 0,
	}
	hs.Store(initial)

	loaded := hs.Load()
	if loaded == nil {
		t.Fatal("expected non-nil hotState")
	}
	if loaded.programSource != "cam1" {
		t.Errorf("programSource: got %q, want %q", loaded.programSource, "cam1")
	}
	if loaded.previewSource != "cam2" {
		t.Errorf("previewSource: got %q, want %q", loaded.previewSource, "cam2")
	}
	if loaded.state != StateIdle {
		t.Errorf("state: got %v, want %v", loaded.state, StateIdle)
	}
}

func TestHotState_CopyOnWrite_PreservesUnrelatedFields(t *testing.T) {
	old := &hotState{
		programSource: "cam1",
		previewSource: "cam2",
		state:         StateIdle,
	}
	next := *old
	next.programSource = "cam3"

	if old.programSource != "cam1" {
		t.Errorf("old hotState mutated; CoW invariant broken")
	}
	if next.previewSource != "cam2" {
		t.Errorf("CoW did not preserve previewSource")
	}
	if next.programSource != "cam3" {
		t.Errorf("CoW did not apply mutation")
	}
}

func TestHotState_Switcher_InitializedNonNil(t *testing.T) {
	sw := New(nil)
	defer sw.Close()

	hs := sw.hotState.Load()
	if hs == nil {
		t.Fatal("Switcher.hotState.Load() returned nil after New()")
	}
	if hs.state != StateIdle {
		t.Errorf("initial state: got %v, want %v", hs.state, StateIdle)
	}
}
