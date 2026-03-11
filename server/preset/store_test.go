package preset

import (
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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

func newTestStore(t *testing.T) *Store {
	t.Helper()
	dir := t.TempDir()
	fp := filepath.Join(dir, "presets.json")
	ps, err := NewStore(fp)
	require.NoError(t, err)
	return ps
}

func TestCreatePreset(t *testing.T) {
	t.Parallel()
	ps := newTestStore(t)

	p, err := ps.Create("Morning Service", testSnapshot())
	require.NoError(t, err)
	require.NotEmpty(t, p.ID)
	require.Equal(t, "Morning Service", p.Name)
	require.Equal(t, "cam1", p.ProgramSource)
	require.Equal(t, "cam2", p.PreviewSource)
	require.Equal(t, "mix", p.TransitionType)
	require.Equal(t, 500, p.TransitionDurMs)
	require.Equal(t, -3.0, p.MasterLevel)
	require.Len(t, p.AudioChannels, 2)
	require.False(t, p.CreatedAt.IsZero(), "expected non-zero CreatedAt")
}

func TestCreatePresetEmptyName(t *testing.T) {
	t.Parallel()
	ps := newTestStore(t)

	_, err := ps.Create("", testSnapshot())
	require.ErrorIs(t, err, ErrEmptyName)
}

func TestListPresets(t *testing.T) {
	t.Parallel()
	ps := newTestStore(t)

	require.Empty(t, ps.List())

	_, _ = ps.Create("Preset A", testSnapshot())
	_, _ = ps.Create("Preset B", testSnapshot())

	presets := ps.List()
	require.Len(t, presets, 2)
	require.Equal(t, "Preset A", presets[0].Name)
	require.Equal(t, "Preset B", presets[1].Name)
}

func TestGetPreset(t *testing.T) {
	t.Parallel()
	ps := newTestStore(t)

	created, _ := ps.Create("Test", testSnapshot())

	got, ok := ps.Get(created.ID)
	require.True(t, ok, "expected to find preset")
	require.Equal(t, "Test", got.Name)

	_, ok = ps.Get("nonexistent-id")
	require.False(t, ok, "expected not to find nonexistent preset")
}

func TestUpdatePreset(t *testing.T) {
	t.Parallel()
	ps := newTestStore(t)

	created, _ := ps.Create("Original", testSnapshot())

	newName := "Updated"
	updated, err := ps.Update(created.ID, Update{Name: &newName})
	require.NoError(t, err)
	require.Equal(t, "Updated", updated.Name)

	// Verify persisted
	got, ok := ps.Get(created.ID)
	require.True(t, ok, "preset not found after update")
	require.Equal(t, "Updated", got.Name)

	// Update nonexistent
	_, err = ps.Update("nonexistent-id", Update{Name: &newName})
	require.ErrorIs(t, err, ErrNotFound)

	// Update with empty name
	emptyName := ""
	_, err = ps.Update(created.ID, Update{Name: &emptyName})
	require.ErrorIs(t, err, ErrEmptyName)
}

func TestDeletePreset(t *testing.T) {
	t.Parallel()
	ps := newTestStore(t)

	created, _ := ps.Create("ToDelete", testSnapshot())

	require.NoError(t, ps.Delete(created.ID))

	_, ok := ps.Get(created.ID)
	require.False(t, ok, "preset should not exist after delete")

	require.Empty(t, ps.List())

	// Delete nonexistent
	require.ErrorIs(t, ps.Delete("nonexistent-id"), ErrNotFound)
}

func TestPersistenceRoundTrip(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	fp := filepath.Join(dir, "presets.json")

	// Create store and add presets
	ps1, err := NewStore(fp)
	require.NoError(t, err)
	_, _ = ps1.Create("Preset 1", testSnapshot())
	_, _ = ps1.Create("Preset 2", testSnapshot())

	// Load from same file in a new store
	ps2, err := NewStore(fp)
	require.NoError(t, err)

	presets := ps2.List()
	require.Len(t, presets, 2)
	require.Equal(t, "Preset 1", presets[0].Name)
	require.Equal(t, "Preset 2", presets[1].Name)

	// Verify audio channels survived round-trip
	ch, ok := presets[0].AudioChannels["cam1"]
	require.True(t, ok, "cam1 audio channel not found after reload")
	require.Equal(t, float64(0), ch.Level)
	require.False(t, ch.Muted)
	require.True(t, ch.AFV)
}

func TestUniqueIDs(t *testing.T) {
	t.Parallel()
	ps := newTestStore(t)

	ids := make(map[string]bool)
	for i := 0; i < 100; i++ {
		p, err := ps.Create("test", testSnapshot())
		require.NoError(t, err, "Create %d", i)
		require.False(t, ids[p.ID], "duplicate ID %q at iteration %d", p.ID, i)
		ids[p.ID] = true
	}
}

func TestConcurrency(t *testing.T) {
	t.Parallel()
	ps := newTestStore(t)

	const goroutines = 20
	var wg sync.WaitGroup
	wg.Add(goroutines)

	for i := 0; i < goroutines; i++ {
		go func() {
			defer wg.Done()
			p, err := ps.Create("concurrent", testSnapshot())
			assert.NoError(t, err, "Create")
			if err != nil {
				return
			}
			ps.Get(p.ID)
			ps.List()
		}()
	}
	wg.Wait()

	presets := ps.List()
	require.Len(t, presets, goroutines)
}

func TestNewStoreNonexistentFile(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	fp := filepath.Join(dir, "subdir", "presets.json")

	ps, err := NewStore(fp)
	require.NoError(t, err)
	require.Empty(t, ps.List())

	// First mutation should create the directory and file
	_, err = ps.Create("test", testSnapshot())
	require.NoError(t, err)

	// Verify file was created
	_, err = os.Stat(fp)
	require.False(t, os.IsNotExist(err), "presets file was not created")
}
