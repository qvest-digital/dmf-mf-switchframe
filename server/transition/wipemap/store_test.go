package wipemap

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func mustNewStore(t *testing.T) *WipeMapStore {
	t.Helper()
	dir := t.TempDir()
	s, err := NewWipeMapStore(filepath.Join(dir, "custom"))
	require.NoError(t, err)
	s.RegisterBuiltins()
	return s
}

func TestStore_RegisterBuiltins(t *testing.T) {
	s := mustNewStore(t)
	patterns := s.List()
	if len(patterns) < 96 {
		t.Errorf("expected ≥96 patterns, got %d", len(patterns))
	}
	// All patterns should be marked as builtin.
	for _, p := range patterns {
		assert.True(t, p.Builtin, "pattern %q should be builtin", p.ID)
	}
}

func TestStore_GetMap(t *testing.T) {
	s := mustNewStore(t)
	m, err := s.GetMap("h-left", 100, 100)
	require.NoError(t, err)
	require.Len(t, m, 10000)
}

func TestStore_GetMap_Caching(t *testing.T) {
	s := mustNewStore(t)
	m1, err := s.GetMap("h-left", 100, 100)
	require.NoError(t, err)
	m2, err := s.GetMap("h-left", 100, 100)
	require.NoError(t, err)
	// Same pointer: cached, not regenerated.
	require.True(t, &m1[0] == &m2[0], "second GetMap should return cached slice")
}

func TestStore_GetMap_DifferentResolutions(t *testing.T) {
	s := mustNewStore(t)
	m1, err := s.GetMap("h-left", 100, 100)
	require.NoError(t, err)
	m2, err := s.GetMap("h-left", 200, 200)
	require.NoError(t, err)
	require.Len(t, m1, 10000)
	require.Len(t, m2, 40000)
}

func TestStore_InvalidateCache(t *testing.T) {
	s := mustNewStore(t)
	m1, err := s.GetMap("h-left", 100, 100)
	require.NoError(t, err)
	s.InvalidateCache()
	m2, err := s.GetMap("h-left", 100, 100)
	require.NoError(t, err)
	// After invalidation, must be a different allocation (pointers differ).
	require.False(t, &m1[0] == &m2[0], "post-invalidate GetMap should regenerate (different allocation)")
}

func TestStore_ListByCategory(t *testing.T) {
	s := mustNewStore(t)
	basics := s.ListByCategory(CatBasic)
	if len(basics) < 8 {
		t.Errorf("expected ≥8 basic patterns, got %d", len(basics))
	}
	for _, p := range basics {
		assert.Equal(t, CatBasic, p.Category)
	}
}

func TestStore_GetPattern_NotFound(t *testing.T) {
	s := mustNewStore(t)
	_, err := s.GetPattern("nonexistent-pattern")
	require.Error(t, err)
}

func TestStore_GetPattern_Found(t *testing.T) {
	s := mustNewStore(t)
	p, err := s.GetPattern("h-left")
	require.NoError(t, err)
	assert.Equal(t, "h-left", p.ID)
	assert.Equal(t, CatBasic, p.Category)
	assert.True(t, p.Builtin)
}

func TestStore_UploadCustom(t *testing.T) {
	dir := t.TempDir()
	s, err := NewWipeMapStore(filepath.Join(dir, "custom"))
	require.NoError(t, err)
	s.RegisterBuiltins()

	mapData := make([]byte, 100*100)
	for i := range mapData {
		mapData[i] = byte(i % 256)
	}
	err = s.UploadCustom("my-custom", "My Custom Wipe", mapData, 100, 100)
	require.NoError(t, err)

	// Should be retrievable.
	got, err := s.GetMap("my-custom", 100, 100)
	require.NoError(t, err)
	require.Equal(t, mapData, got)

	// Should appear in list.
	p, err := s.GetPattern("my-custom")
	require.NoError(t, err)
	assert.Equal(t, "my-custom", p.ID)
	assert.Equal(t, CatCustom, p.Category)
	assert.False(t, p.Builtin)

	// Should be persisted on disk.
	binPath := filepath.Join(dir, "custom", "my-custom.bin")
	_, err = os.Stat(binPath)
	require.NoError(t, err, "custom map binary should exist on disk")
	jsonPath := filepath.Join(dir, "custom", "my-custom.json")
	_, err = os.Stat(jsonPath)
	require.NoError(t, err, "custom map metadata should exist on disk")
}

func TestStore_DeleteCustom(t *testing.T) {
	dir := t.TempDir()
	s, err := NewWipeMapStore(filepath.Join(dir, "custom"))
	require.NoError(t, err)
	s.RegisterBuiltins()

	mapData := make([]byte, 100*100)
	err = s.UploadCustom("del-me", "Delete Me", mapData, 100, 100)
	require.NoError(t, err)

	err = s.DeleteCustom("del-me")
	require.NoError(t, err)

	_, err = s.GetPattern("del-me")
	require.Error(t, err)

	// Disk files should be gone.
	binPath := filepath.Join(dir, "custom", "del-me.bin")
	_, err = os.Stat(binPath)
	require.True(t, os.IsNotExist(err))
}

func TestStore_DeleteBuiltinFails(t *testing.T) {
	s := mustNewStore(t)
	err := s.DeleteCustom("h-left")
	require.Error(t, err)
}

func TestStore_PatternCategories(t *testing.T) {
	s := mustNewStore(t)
	patterns := s.List()

	cats := make(map[string]int)
	for _, p := range patterns {
		cats[p.Category]++
	}

	expectedCats := []string{CatBasic, CatIris, CatClock, CatMatrix, CatShape, CatSpecialty, CatMask, CatDissolve}
	for _, cat := range expectedCats {
		count, ok := cats[cat]
		if !ok || count == 0 {
			t.Errorf("category %q has no patterns", cat)
		}
	}
}
