package dve

import (
	"os"
	"path/filepath"
	"testing"
)

func TestPresetStoreSaveLoadRoundTrip(t *testing.T) {
	dir := t.TempDir()
	store, err := NewPresetStore(dir)
	if err != nil {
		t.Fatalf("NewPresetStore: %v", err)
	}

	preset := &Preset{
		ID:   "test-1",
		Name: "Test Preset",
		Slots: []Slot{
			{
				SourceKey: "cam1",
				Transform: DefaultTransform(0, 0, 960, 540),
				ZOrder:    0,
				Enabled:   true,
			},
			{
				SourceKey: "cam2",
				Transform: DefaultTransform(960, 0, 960, 540),
				ZOrder:    1,
				Enabled:   true,
			},
		},
		TransitionMs: 500,
		Easing:       "ease-in-out",
	}

	if err := store.Save(preset); err != nil {
		t.Fatalf("Save: %v", err)
	}

	loaded, err := store.Load("test-1")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if loaded.ID != preset.ID {
		t.Errorf("ID = %q, want %q", loaded.ID, preset.ID)
	}
	if loaded.Name != preset.Name {
		t.Errorf("Name = %q, want %q", loaded.Name, preset.Name)
	}
	if len(loaded.Slots) != 2 {
		t.Fatalf("Slots len = %d, want 2", len(loaded.Slots))
	}
	if loaded.Slots[0].SourceKey != "cam1" {
		t.Errorf("Slot[0].SourceKey = %q, want %q", loaded.Slots[0].SourceKey, "cam1")
	}
	if loaded.Slots[1].Transform.PositionX != 960 {
		t.Errorf("Slot[1].PositionX = %v, want 960", loaded.Slots[1].Transform.PositionX)
	}
	if loaded.TransitionMs != 500 {
		t.Errorf("TransitionMs = %d, want 500", loaded.TransitionMs)
	}
	if loaded.Easing != "ease-in-out" {
		t.Errorf("Easing = %q, want %q", loaded.Easing, "ease-in-out")
	}
}

func TestPresetStoreDelete(t *testing.T) {
	dir := t.TempDir()
	store, err := NewPresetStore(dir)
	if err != nil {
		t.Fatalf("NewPresetStore: %v", err)
	}

	preset := &Preset{
		ID:   "to-delete",
		Name: "Delete Me",
		Slots: []Slot{
			{Transform: DefaultTransform(0, 0, 100, 100), Enabled: true},
		},
		TransitionMs: 300,
	}

	if err := store.Save(preset); err != nil {
		t.Fatalf("Save: %v", err)
	}

	if err := store.Delete("to-delete"); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	_, err = store.Load("to-delete")
	if err == nil {
		t.Fatal("expected error loading deleted preset, got nil")
	}

	// File should be gone.
	path := filepath.Join(dir, "to-delete.json")
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("expected file removed, got stat err: %v", err)
	}
}

func TestPresetStoreList(t *testing.T) {
	dir := t.TempDir()
	store, err := NewPresetStore(dir)
	if err != nil {
		t.Fatalf("NewPresetStore: %v", err)
	}

	presets := []*Preset{
		{ID: "b-second", Name: "Bravo", Slots: []Slot{{Transform: DefaultTransform(0, 0, 100, 100), Enabled: true}}, TransitionMs: 300},
		{ID: "a-first", Name: "Alpha", Slots: []Slot{{Transform: DefaultTransform(0, 0, 100, 100), Enabled: true}}, TransitionMs: 300},
		{ID: "c-third", Name: "Charlie", Slots: []Slot{{Transform: DefaultTransform(0, 0, 100, 100), Enabled: true}}, TransitionMs: 300},
	}

	for _, p := range presets {
		if err := store.Save(p); err != nil {
			t.Fatalf("Save %q: %v", p.ID, err)
		}
	}

	list, err := store.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}

	if len(list) != 3 {
		t.Fatalf("List len = %d, want 3", len(list))
	}

	// Should be sorted by name.
	if list[0].Name != "Alpha" {
		t.Errorf("list[0].Name = %q, want Alpha", list[0].Name)
	}
	if list[1].Name != "Bravo" {
		t.Errorf("list[1].Name = %q, want Bravo", list[1].Name)
	}
	if list[2].Name != "Charlie" {
		t.Errorf("list[2].Name = %q, want Charlie", list[2].Name)
	}

	// IDs should match.
	if list[0].ID != "a-first" {
		t.Errorf("list[0].ID = %q, want a-first", list[0].ID)
	}
}

func TestPresetStorePathTraversal(t *testing.T) {
	dir := t.TempDir()
	store, err := NewPresetStore(dir)
	if err != nil {
		t.Fatalf("NewPresetStore: %v", err)
	}

	badIDs := []string{
		"../etc/passwd",
		"foo/bar",
		"foo\\bar",
		"..\\windows",
		"..",
		".",
	}

	for _, id := range badIDs {
		p := &Preset{
			ID:    id,
			Name:  "bad",
			Slots: []Slot{{Transform: DefaultTransform(0, 0, 100, 100), Enabled: true}},
		}
		if err := store.Save(p); err == nil {
			t.Errorf("Save(%q) should fail for path traversal", id)
		}
		if _, err := store.Load(id); err == nil {
			t.Errorf("Load(%q) should fail for path traversal", id)
		}
		if err := store.Delete(id); err == nil {
			t.Errorf("Delete(%q) should fail for path traversal", id)
		}
	}
}

func TestPresetStoreLoadNonExistent(t *testing.T) {
	dir := t.TempDir()
	store, err := NewPresetStore(dir)
	if err != nil {
		t.Fatalf("NewPresetStore: %v", err)
	}

	_, err = store.Load("does-not-exist")
	if err == nil {
		t.Fatal("expected error loading non-existent preset, got nil")
	}
}

func TestPresetStoreCreatesDirIfNeeded(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "nested", "dir")
	_, err := NewPresetStore(dir)
	if err != nil {
		t.Fatalf("NewPresetStore with nested dir: %v", err)
	}

	info, err := os.Stat(dir)
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	if !info.IsDir() {
		t.Error("expected directory")
	}
}

func TestRecallPresetSameState(t *testing.T) {
	layout := &Layout{
		Name: "test",
		Slots: []Slot{
			{
				SourceKey: "cam1",
				Transform: DefaultTransform(100, 200, 400, 300),
				ZOrder:    0,
				Enabled:   true,
			},
		},
	}

	anim := RecallPreset(layout, layout, 500)

	if len(anim.Tracks) != 0 {
		t.Errorf("same state should produce 0 tracks, got %d", len(anim.Tracks))
	}
	if anim.DurationMs != 500 {
		t.Errorf("DurationMs = %d, want 500", anim.DurationMs)
	}
}

func TestRecallPresetDifferentPosition(t *testing.T) {
	current := &Layout{
		Name: "current",
		Slots: []Slot{
			{
				SourceKey: "cam1",
				Transform: DefaultTransform(0, 0, 400, 300),
				ZOrder:    0,
				Enabled:   true,
			},
		},
	}
	target := &Layout{
		Name: "target",
		Slots: []Slot{
			{
				SourceKey: "cam1",
				Transform: DefaultTransform(100, 200, 400, 300),
				ZOrder:    0,
				Enabled:   true,
			},
		},
	}

	anim := RecallPreset(current, target, 300)

	if anim.DurationMs != 300 {
		t.Errorf("DurationMs = %d, want 300", anim.DurationMs)
	}

	// Should have positionX and positionY tracks.
	foundX := false
	foundY := false
	for _, tr := range anim.Tracks {
		if tr.SlotIndex != 0 {
			t.Errorf("unexpected slot index %d", tr.SlotIndex)
		}
		switch tr.Param {
		case "positionX":
			foundX = true
			if len(tr.Keyframes) != 2 {
				t.Fatalf("positionX: %d keyframes, want 2", len(tr.Keyframes))
			}
			if tr.Keyframes[0].Value != 0 {
				t.Errorf("positionX start = %v, want 0", tr.Keyframes[0].Value)
			}
			if tr.Keyframes[1].Value != 100 {
				t.Errorf("positionX end = %v, want 100", tr.Keyframes[1].Value)
			}
			// Linear easing.
			if tr.Keyframes[0].EaseOutX != 1 || tr.Keyframes[0].EaseOutY != 1 {
				t.Errorf("positionX easing = (%v,%v), want (1,1)", tr.Keyframes[0].EaseOutX, tr.Keyframes[0].EaseOutY)
			}
		case "positionY":
			foundY = true
			if len(tr.Keyframes) != 2 {
				t.Fatalf("positionY: %d keyframes, want 2", len(tr.Keyframes))
			}
			if tr.Keyframes[0].Value != 0 {
				t.Errorf("positionY start = %v, want 0", tr.Keyframes[0].Value)
			}
			if tr.Keyframes[1].Value != 200 {
				t.Errorf("positionY end = %v, want 200", tr.Keyframes[1].Value)
			}
		}
	}

	if !foundX {
		t.Error("missing positionX track")
	}
	if !foundY {
		t.Error("missing positionY track")
	}
}

func TestRecallPresetEnableDisableOpacity(t *testing.T) {
	t.Run("disabled to enabled", func(t *testing.T) {
		current := &Layout{
			Slots: []Slot{
				{Transform: DefaultTransform(0, 0, 400, 300), Enabled: false},
			},
		}
		target := &Layout{
			Slots: []Slot{
				{Transform: DefaultTransform(0, 0, 400, 300), Enabled: true},
			},
		}

		anim := RecallPreset(current, target, 300)

		var opTrack *Track
		for i := range anim.Tracks {
			if anim.Tracks[i].Param == "opacity" {
				opTrack = &anim.Tracks[i]
				break
			}
		}
		if opTrack == nil {
			t.Fatal("expected opacity track for disabled->enabled transition")
		}
		if len(opTrack.Keyframes) != 2 {
			t.Fatalf("opacity: %d keyframes, want 2", len(opTrack.Keyframes))
		}
		if opTrack.Keyframes[0].Value != 0 {
			t.Errorf("opacity start = %v, want 0", opTrack.Keyframes[0].Value)
		}
		if opTrack.Keyframes[1].Value != 1.0 {
			t.Errorf("opacity end = %v, want 1.0", opTrack.Keyframes[1].Value)
		}
	})

	t.Run("enabled to disabled", func(t *testing.T) {
		current := &Layout{
			Slots: []Slot{
				{Transform: DefaultTransform(0, 0, 400, 300), Enabled: true},
			},
		}
		target := &Layout{
			Slots: []Slot{
				{Transform: DefaultTransform(0, 0, 400, 300), Enabled: false},
			},
		}

		anim := RecallPreset(current, target, 300)

		var opTrack *Track
		for i := range anim.Tracks {
			if anim.Tracks[i].Param == "opacity" {
				opTrack = &anim.Tracks[i]
				break
			}
		}
		if opTrack == nil {
			t.Fatal("expected opacity track for enabled->disabled transition")
		}
		if opTrack.Keyframes[0].Value != 1.0 {
			t.Errorf("opacity start = %v, want 1.0", opTrack.Keyframes[0].Value)
		}
		if opTrack.Keyframes[1].Value != 0 {
			t.Errorf("opacity end = %v, want 0", opTrack.Keyframes[1].Value)
		}
	})
}

func TestBuiltinPresetsCount(t *testing.T) {
	presets := BuiltinPresets(1920, 1080)
	if len(presets) != 8 {
		t.Errorf("BuiltinPresets count = %d, want 8", len(presets))
	}
}

func TestBuiltinPresetsValidate(t *testing.T) {
	presets := BuiltinPresets(1920, 1080)
	for _, p := range presets {
		for i, slot := range p.Slots {
			if err := ValidateSlot(slot); err != nil {
				t.Errorf("preset %q slot %d: %v", p.ID, i, err)
			}
		}
	}
}

func TestBuiltinPresetsNames(t *testing.T) {
	presets := BuiltinPresets(1920, 1080)
	expected := map[string]bool{
		"pip-top-right":    true,
		"pip-top-left":     true,
		"pip-bottom-right": true,
		"pip-bottom-left":  true,
		"side-by-side":     true,
		"quad":             true,
		"three-box":        true,
		"over-shoulder":    true,
	}
	for _, p := range presets {
		if !expected[p.ID] {
			t.Errorf("unexpected preset ID %q", p.ID)
		}
		delete(expected, p.ID)
	}
	for id := range expected {
		t.Errorf("missing preset %q", id)
	}
}

func TestResolveBuiltinPreset(t *testing.T) {
	p := ResolveBuiltinPreset("quad", 1920, 1080)
	if p == nil {
		t.Fatal("ResolveBuiltinPreset(quad) returned nil")
	}
	if p.ID != "quad" {
		t.Errorf("ID = %q, want quad", p.ID)
	}
	if len(p.Slots) != 4 {
		t.Errorf("quad slots = %d, want 4", len(p.Slots))
	}
}

func TestResolveBuiltinPresetUnknown(t *testing.T) {
	p := ResolveBuiltinPreset("nonexistent", 1920, 1080)
	if p != nil {
		t.Errorf("expected nil for unknown preset, got %+v", p)
	}
}

func TestOverShoulderPreset(t *testing.T) {
	p := ResolveBuiltinPreset("over-shoulder", 1920, 1080)
	if p == nil {
		t.Fatal("over-shoulder preset not found")
	}

	if len(p.Slots) < 1 {
		t.Fatal("over-shoulder has no slots")
	}

	// Find the PIP slot (not the full-frame one).
	var pip *Slot
	for i := range p.Slots {
		if p.Slots[i].Transform.Width < float64(1920) {
			pip = &p.Slots[i]
			break
		}
	}
	if pip == nil {
		t.Fatal("no PIP slot found in over-shoulder preset")
	}

	if pip.Transform.RotationDeg == 0 {
		t.Error("over-shoulder PIP should have rotation")
	}
	if !pip.Transform.Shadow.Enabled {
		t.Error("over-shoulder PIP should have shadow enabled")
	}
	if pip.Transform.Shadow.Blur == 0 {
		t.Error("over-shoulder PIP should have shadow blur > 0")
	}
	if pip.Transform.Border.Width == 0 {
		t.Error("over-shoulder PIP should have border")
	}
}
