package layout

import (
	"image"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestStore_CRUD(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "layout_presets.json")
	s := NewStore(path)

	// Save
	l := &Layout{
		Name: "custom-pip",
		Slots: []LayoutSlot{
			{SourceKey: "cam2", Rect: image.Rect(100, 100, 500, 370), Enabled: true},
		},
	}
	require.NoError(t, s.Save(l))

	// List
	presets := s.List()
	require.Len(t, presets, 1)
	require.Equal(t, "custom-pip", presets[0])

	// Get
	got, err := s.Get("custom-pip")
	require.NoError(t, err)
	require.Equal(t, "cam2", got.Slots[0].SourceKey)

	// Delete
	require.NoError(t, s.Delete("custom-pip"))
	presets = s.List()
	require.Len(t, presets, 0)

	// Get missing
	_, err = s.Get("nonexistent")
	require.Error(t, err)

	// Delete missing
	err = s.Delete("nonexistent")
	require.Error(t, err)
}

func TestStore_Persistence(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "layout_presets.json")

	s1 := NewStore(path)
	l := &Layout{Name: "test", Slots: []LayoutSlot{
		{SourceKey: "cam1", Rect: image.Rect(0, 0, 480, 270)},
	}}
	require.NoError(t, s1.Save(l))

	// New store reads from file
	s2 := NewStore(path)
	presets := s2.List()
	require.Len(t, presets, 1)

	got, err := s2.Get("test")
	require.NoError(t, err)
	require.Equal(t, "cam1", got.Slots[0].SourceKey)
}

func TestStore_NilFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "nonexistent", "presets.json")
	s := NewStore(path)
	// Should not panic — file doesn't exist yet
	require.Len(t, s.List(), 0)

	// Create parent dir and save
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	l := &Layout{Name: "test", Slots: []LayoutSlot{}}
	require.NoError(t, s.Save(l))
}
