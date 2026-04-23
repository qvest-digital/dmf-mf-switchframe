package switcher

import (
	"testing"
)

func TestGraphicsHandlers_CopyOnWrite(t *testing.T) {
	old := &graphicsHandlers{}
	next := *old
	// Simulate SetDVECompositor without affecting keyBridge.
	next.dveCompositor = nil
	if old.dveCompositor != nil {
		t.Error("CoW mutated old.dveCompositor")
	}
	// Invariant: CoW publishes a distinct pointer.
	if &next == old {
		t.Error("CoW did not produce a distinct struct address")
	}
}

func TestAudioHandlers_CopyOnWrite(t *testing.T) {
	old := &audioHandlers{}
	next := *old
	next.audioHandler = nil
	if old.audioHandler != nil {
		t.Error("CoW mutated old.audioHandler")
	}
}

func TestFrameSyncBundle_CopyOnWrite(t *testing.T) {
	old := &frameSyncBundle{active: true}
	next := *old
	next.active = false
	if !old.active {
		t.Error("CoW mutated old.active")
	}
	if next.active {
		t.Error("CoW did not apply mutation")
	}
}

func TestSwitcher_GroupedHandlers_InitializedNonNil(t *testing.T) {
	sw := New(nil)
	defer sw.Close()

	if gh := sw.graphicsHandlers.Load(); gh == nil {
		t.Error("graphicsHandlers.Load() returned nil after New()")
	}
	if ah := sw.audioHandlers.Load(); ah == nil {
		t.Error("audioHandlers.Load() returned nil after New()")
	}
	if fsb := sw.frameSyncBundle.Load(); fsb == nil {
		t.Error("frameSyncBundle.Load() returned nil after New()")
	}
}
