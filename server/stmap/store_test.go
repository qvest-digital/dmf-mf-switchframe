package stmap

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestStore_SaveAndLoadStatic(t *testing.T) {
	dir := t.TempDir()
	store, err := NewStore(dir)
	require.NoError(t, err)

	// Create a small ST map with known values.
	m, err := NewSTMap("test-map", 4, 2)
	require.NoError(t, err)

	// Fill with known values.
	for i := range m.S {
		m.S[i] = float32(i) * 0.1
		m.T[i] = float32(i) * 0.2
	}

	// Save.
	err = store.SaveStatic(m)
	require.NoError(t, err)

	// Verify file exists on disk.
	_, err = os.Stat(filepath.Join(dir, "test-map.stmap"))
	require.NoError(t, err)

	// Load and verify exact values.
	loaded, err := store.LoadStatic("test-map")
	require.NoError(t, err)
	require.Equal(t, "test-map", loaded.Name)
	require.Equal(t, 4, loaded.Width)
	require.Equal(t, 2, loaded.Height)
	require.Len(t, loaded.S, 8)
	require.Len(t, loaded.T, 8)

	for i := range m.S {
		require.Equal(t, m.S[i], loaded.S[i], "S[%d] mismatch", i)
		require.Equal(t, m.T[i], loaded.T[i], "T[%d] mismatch", i)
	}
}

func TestStore_SaveStatic_InvalidName(t *testing.T) {
	dir := t.TempDir()
	store, err := NewStore(dir)
	require.NoError(t, err)

	m, err := NewSTMap("valid", 4, 2)
	require.NoError(t, err)

	// Override name with invalid values.
	m.Name = ""
	err = store.SaveStatic(m)
	require.ErrorIs(t, err, ErrInvalidName)

	m.Name = "../escape"
	err = store.SaveStatic(m)
	require.ErrorIs(t, err, ErrInvalidName)

	m.Name = "a/b"
	err = store.SaveStatic(m)
	require.ErrorIs(t, err, ErrInvalidName)
}

func TestStore_LoadStatic_NotFound(t *testing.T) {
	dir := t.TempDir()
	store, err := NewStore(dir)
	require.NoError(t, err)

	_, err = store.LoadStatic("nonexistent")
	require.Error(t, err)
	require.ErrorIs(t, err, ErrNotFound)
}

func TestStore_SaveAndLoadAnimatedMeta(t *testing.T) {
	dir := t.TempDir()
	store, err := NewStore(dir)
	require.NoError(t, err)

	// Create an animated map to extract metadata from.
	frames := make([]*STMap, 10)
	for i := range frames {
		frames[i] = Identity(1920, 1080)
	}
	anim := NewAnimatedSTMap("barrel-spin", frames, 30)

	params := map[string]float64{
		"strength": 0.5,
		"centerX":  0.5,
		"centerY":  0.5,
	}

	// Save metadata.
	err = store.SaveAnimatedMeta(anim, "barrel_distortion", params)
	require.NoError(t, err)

	// Verify file exists on disk.
	_, err = os.Stat(filepath.Join(dir, "barrel-spin.stmap-anim.json"))
	require.NoError(t, err)

	// Load and verify.
	meta, err := store.LoadAnimatedMeta("barrel-spin")
	require.NoError(t, err)
	require.Equal(t, "barrel_distortion", meta.Generator)
	require.Equal(t, 1920, meta.Width)
	require.Equal(t, 1080, meta.Height)
	require.Equal(t, 10, meta.FrameCount)
	require.Equal(t, 30, meta.FPS)
	require.InDelta(t, 0.5, meta.Params["strength"], 1e-9)
	require.InDelta(t, 0.5, meta.Params["centerX"], 1e-9)
	require.InDelta(t, 0.5, meta.Params["centerY"], 1e-9)
}

func TestStore_SaveAnimatedMeta_InvalidName(t *testing.T) {
	dir := t.TempDir()
	store, err := NewStore(dir)
	require.NoError(t, err)

	frames := []*STMap{Identity(4, 2)}
	anim := NewAnimatedSTMap("", frames, 30)

	err = store.SaveAnimatedMeta(anim, "gen", nil)
	require.ErrorIs(t, err, ErrInvalidName)
}

func TestStore_LoadAnimatedMeta_NotFound(t *testing.T) {
	dir := t.TempDir()
	store, err := NewStore(dir)
	require.NoError(t, err)

	_, err = store.LoadAnimatedMeta("nonexistent")
	require.Error(t, err)
	require.ErrorIs(t, err, ErrNotFound)
}

func TestStore_Delete(t *testing.T) {
	dir := t.TempDir()
	store, err := NewStore(dir)
	require.NoError(t, err)

	// Save a static map, then delete it.
	m, err := NewSTMap("to-delete", 4, 2)
	require.NoError(t, err)
	err = store.SaveStatic(m)
	require.NoError(t, err)

	// Verify it exists.
	_, err = store.LoadStatic("to-delete")
	require.NoError(t, err)

	// Delete.
	err = store.Delete("to-delete")
	require.NoError(t, err)

	// Verify it's gone.
	_, err = store.LoadStatic("to-delete")
	require.ErrorIs(t, err, ErrNotFound)
}

func TestStore_Delete_AnimatedMeta(t *testing.T) {
	dir := t.TempDir()
	store, err := NewStore(dir)
	require.NoError(t, err)

	// Save animated meta, then delete.
	frames := []*STMap{Identity(4, 2)}
	anim := NewAnimatedSTMap("anim-del", frames, 30)
	err = store.SaveAnimatedMeta(anim, "gen", nil)
	require.NoError(t, err)

	err = store.Delete("anim-del")
	require.NoError(t, err)

	_, err = store.LoadAnimatedMeta("anim-del")
	require.ErrorIs(t, err, ErrNotFound)
}

func TestStore_Delete_NotFound(t *testing.T) {
	dir := t.TempDir()
	store, err := NewStore(dir)
	require.NoError(t, err)

	err = store.Delete("nonexistent")
	require.Error(t, err)
	require.ErrorIs(t, err, ErrNotFound)
}

func TestStore_Delete_InvalidName(t *testing.T) {
	dir := t.TempDir()
	store, err := NewStore(dir)
	require.NoError(t, err)

	err = store.Delete("../escape")
	require.ErrorIs(t, err, ErrInvalidName)
}

func TestStore_ListStatic(t *testing.T) {
	dir := t.TempDir()
	store, err := NewStore(dir)
	require.NoError(t, err)

	// Empty directory returns empty list.
	names, err := store.ListStatic()
	require.NoError(t, err)
	require.Empty(t, names)

	// Save a few static maps.
	for _, name := range []string{"barrel", "fisheye", "zoom"} {
		m, err := NewSTMap(name, 4, 2)
		require.NoError(t, err)
		err = store.SaveStatic(m)
		require.NoError(t, err)
	}

	// Also save an animated meta to ensure it's not included.
	frames := []*STMap{Identity(4, 2)}
	anim := NewAnimatedSTMap("anim-map", frames, 30)
	err = store.SaveAnimatedMeta(anim, "gen", nil)
	require.NoError(t, err)

	names, err = store.ListStatic()
	require.NoError(t, err)
	require.Len(t, names, 3)
	require.Contains(t, names, "barrel")
	require.Contains(t, names, "fisheye")
	require.Contains(t, names, "zoom")
}

func TestStore_ListAnimated(t *testing.T) {
	dir := t.TempDir()
	store, err := NewStore(dir)
	require.NoError(t, err)

	// Empty directory returns empty list.
	names, err := store.ListAnimated()
	require.NoError(t, err)
	require.Empty(t, names)

	// Save animated metas.
	for _, name := range []string{"spin", "wobble"} {
		frames := []*STMap{Identity(4, 2)}
		anim := NewAnimatedSTMap(name, frames, 30)
		err = store.SaveAnimatedMeta(anim, "gen", nil)
		require.NoError(t, err)
	}

	// Also save a static map to ensure it's not included.
	m, err := NewSTMap("static-one", 4, 2)
	require.NoError(t, err)
	err = store.SaveStatic(m)
	require.NoError(t, err)

	names, err = store.ListAnimated()
	require.NoError(t, err)
	require.Len(t, names, 2)
	require.Contains(t, names, "spin")
	require.Contains(t, names, "wobble")
}

func TestStore_NewStore_CreatesDir(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "nested", "stmaps")
	_, err := NewStore(dir)
	require.NoError(t, err)

	info, err := os.Stat(dir)
	require.NoError(t, err)
	require.True(t, info.IsDir())
}

func TestStore_SaveStatic_LargeMap(t *testing.T) {
	dir := t.TempDir()
	store, err := NewStore(dir)
	require.NoError(t, err)

	// Use a realistic resolution.
	m, err := NewSTMap("hd", 1920, 1080)
	require.NoError(t, err)

	// Fill with identity-like values.
	for y := 0; y < m.Height; y++ {
		for x := 0; x < m.Width; x++ {
			idx := y*m.Width + x
			m.S[idx] = float32(x) / float32(m.Width)
			m.T[idx] = float32(y) / float32(m.Height)
		}
	}

	err = store.SaveStatic(m)
	require.NoError(t, err)

	loaded, err := store.LoadStatic("hd")
	require.NoError(t, err)
	require.Equal(t, m.Width, loaded.Width)
	require.Equal(t, m.Height, loaded.Height)

	// Spot-check a few values.
	require.Equal(t, m.S[0], loaded.S[0])
	require.Equal(t, m.T[0], loaded.T[0])
	last := m.Width*m.Height - 1
	require.Equal(t, m.S[last], loaded.S[last])
	require.Equal(t, m.T[last], loaded.T[last])
}
