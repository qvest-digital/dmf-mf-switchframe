package preset

import (
	"os"
	"path/filepath"
	"sync"
	"testing"
)

func testSnapshot() ControlRoomSnapshot {
	return ControlRoomSnapshot{
		ProgramSource:        "cam1",
		PreviewSource:        "cam2",
		TransitionType:       "mix",
		TransitionDurationMs: 500,
		AudioChannels: map[string]AudioChannelSnapshot{
			"cam1": {Level: 0, Muted: false, AFV: true},
			"cam2": {Level: -6, Muted: true, AFV: false},
		},
		MasterLevel: -3.0,
	}
}

func newTestStore(t *testing.T) *PresetStore {
	t.Helper()
	dir := t.TempDir()
	fp := filepath.Join(dir, "presets.json")
	ps, err := NewPresetStore(fp)
	if err != nil {
		t.Fatalf("NewPresetStore: %v", err)
	}
	return ps
}

func TestCreatePreset(t *testing.T) {
	ps := newTestStore(t)

	p, err := ps.Create("Morning Service", testSnapshot())
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if p.ID == "" {
		t.Error("expected non-empty ID")
	}
	if p.Name != "Morning Service" {
		t.Errorf("Name = %q, want %q", p.Name, "Morning Service")
	}
	if p.ProgramSource != "cam1" {
		t.Errorf("ProgramSource = %q, want %q", p.ProgramSource, "cam1")
	}
	if p.PreviewSource != "cam2" {
		t.Errorf("PreviewSource = %q, want %q", p.PreviewSource, "cam2")
	}
	if p.TransitionType != "mix" {
		t.Errorf("TransitionType = %q, want %q", p.TransitionType, "mix")
	}
	if p.TransitionDurMs != 500 {
		t.Errorf("TransitionDurMs = %d, want %d", p.TransitionDurMs, 500)
	}
	if p.MasterLevel != -3.0 {
		t.Errorf("MasterLevel = %f, want %f", p.MasterLevel, -3.0)
	}
	if len(p.AudioChannels) != 2 {
		t.Errorf("AudioChannels count = %d, want %d", len(p.AudioChannels), 2)
	}
	if p.CreatedAt.IsZero() {
		t.Error("expected non-zero CreatedAt")
	}
}

func TestCreatePresetEmptyName(t *testing.T) {
	ps := newTestStore(t)

	_, err := ps.Create("", testSnapshot())
	if err != ErrEmptyName {
		t.Errorf("expected ErrEmptyName, got %v", err)
	}
}

func TestListPresets(t *testing.T) {
	ps := newTestStore(t)

	if presets := ps.List(); len(presets) != 0 {
		t.Fatalf("expected empty list, got %d", len(presets))
	}

	ps.Create("Preset A", testSnapshot())
	ps.Create("Preset B", testSnapshot())

	presets := ps.List()
	if len(presets) != 2 {
		t.Fatalf("expected 2 presets, got %d", len(presets))
	}
	if presets[0].Name != "Preset A" {
		t.Errorf("presets[0].Name = %q, want %q", presets[0].Name, "Preset A")
	}
	if presets[1].Name != "Preset B" {
		t.Errorf("presets[1].Name = %q, want %q", presets[1].Name, "Preset B")
	}
}

func TestGetPreset(t *testing.T) {
	ps := newTestStore(t)

	created, _ := ps.Create("Test", testSnapshot())

	got, ok := ps.Get(created.ID)
	if !ok {
		t.Fatal("expected to find preset")
	}
	if got.Name != "Test" {
		t.Errorf("Name = %q, want %q", got.Name, "Test")
	}

	_, ok = ps.Get("nonexistent-id")
	if ok {
		t.Error("expected not to find nonexistent preset")
	}
}

func TestUpdatePreset(t *testing.T) {
	ps := newTestStore(t)

	created, _ := ps.Create("Original", testSnapshot())

	newName := "Updated"
	updated, err := ps.Update(created.ID, PresetUpdate{Name: &newName})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if updated.Name != "Updated" {
		t.Errorf("Name = %q, want %q", updated.Name, "Updated")
	}

	// Verify persisted
	got, ok := ps.Get(created.ID)
	if !ok {
		t.Fatal("preset not found after update")
	}
	if got.Name != "Updated" {
		t.Errorf("persisted Name = %q, want %q", got.Name, "Updated")
	}

	// Update nonexistent
	_, err = ps.Update("nonexistent-id", PresetUpdate{Name: &newName})
	if err != ErrNotFound {
		t.Errorf("expected ErrNotFound, got %v", err)
	}

	// Update with empty name
	emptyName := ""
	_, err = ps.Update(created.ID, PresetUpdate{Name: &emptyName})
	if err != ErrEmptyName {
		t.Errorf("expected ErrEmptyName, got %v", err)
	}
}

func TestDeletePreset(t *testing.T) {
	ps := newTestStore(t)

	created, _ := ps.Create("ToDelete", testSnapshot())

	if err := ps.Delete(created.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	_, ok := ps.Get(created.ID)
	if ok {
		t.Error("preset should not exist after delete")
	}

	if presets := ps.List(); len(presets) != 0 {
		t.Errorf("expected 0 presets after delete, got %d", len(presets))
	}

	// Delete nonexistent
	if err := ps.Delete("nonexistent-id"); err != ErrNotFound {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
}

func TestPersistenceRoundTrip(t *testing.T) {
	dir := t.TempDir()
	fp := filepath.Join(dir, "presets.json")

	// Create store and add presets
	ps1, err := NewPresetStore(fp)
	if err != nil {
		t.Fatalf("NewPresetStore: %v", err)
	}
	ps1.Create("Preset 1", testSnapshot())
	ps1.Create("Preset 2", testSnapshot())

	// Load from same file in a new store
	ps2, err := NewPresetStore(fp)
	if err != nil {
		t.Fatalf("NewPresetStore (reload): %v", err)
	}

	presets := ps2.List()
	if len(presets) != 2 {
		t.Fatalf("expected 2 presets after reload, got %d", len(presets))
	}
	if presets[0].Name != "Preset 1" {
		t.Errorf("presets[0].Name = %q, want %q", presets[0].Name, "Preset 1")
	}
	if presets[1].Name != "Preset 2" {
		t.Errorf("presets[1].Name = %q, want %q", presets[1].Name, "Preset 2")
	}

	// Verify audio channels survived round-trip
	ch, ok := presets[0].AudioChannels["cam1"]
	if !ok {
		t.Fatal("cam1 audio channel not found after reload")
	}
	if ch.Level != 0 || ch.Muted != false || ch.AFV != true {
		t.Errorf("cam1 channel = %+v, want Level=0 Muted=false AFV=true", ch)
	}
}

func TestUniqueIDs(t *testing.T) {
	ps := newTestStore(t)

	ids := make(map[string]bool)
	for i := 0; i < 100; i++ {
		p, err := ps.Create("test", testSnapshot())
		if err != nil {
			t.Fatalf("Create %d: %v", i, err)
		}
		if ids[p.ID] {
			t.Fatalf("duplicate ID %q at iteration %d", p.ID, i)
		}
		ids[p.ID] = true
	}
}

func TestConcurrency(t *testing.T) {
	ps := newTestStore(t)

	const goroutines = 20
	var wg sync.WaitGroup
	wg.Add(goroutines)

	for i := 0; i < goroutines; i++ {
		go func() {
			defer wg.Done()
			p, err := ps.Create("concurrent", testSnapshot())
			if err != nil {
				t.Errorf("Create: %v", err)
				return
			}
			ps.Get(p.ID)
			ps.List()
		}()
	}
	wg.Wait()

	presets := ps.List()
	if len(presets) != goroutines {
		t.Errorf("expected %d presets, got %d", goroutines, len(presets))
	}
}

func TestNewPresetStoreNonexistentFile(t *testing.T) {
	dir := t.TempDir()
	fp := filepath.Join(dir, "subdir", "presets.json")

	ps, err := NewPresetStore(fp)
	if err != nil {
		t.Fatalf("NewPresetStore: %v", err)
	}
	if presets := ps.List(); len(presets) != 0 {
		t.Errorf("expected empty list, got %d", len(presets))
	}

	// First mutation should create the directory and file
	_, err = ps.Create("test", testSnapshot())
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	// Verify file was created
	if _, err := os.Stat(fp); os.IsNotExist(err) {
		t.Fatal("presets file was not created")
	}
}
