package macro

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
)

func tempStorePath(t *testing.T) string {
	t.Helper()
	return filepath.Join(t.TempDir(), "macros.json")
}

func TestStore_SaveAndGet(t *testing.T) {
	t.Parallel()
	s, err := NewStore(tempStorePath(t))
	require.NoError(t, err)

	m := Macro{
		Name: "my-macro",
		Steps: []Step{
			{Action: ActionCut, Params: map[string]any{"source": "cam1"}},
		},
	}
	require.NoError(t, s.Save(m))

	got, err := s.Get("my-macro")
	require.NoError(t, err)
	require.Equal(t, "my-macro", got.Name)
	require.Len(t, got.Steps, 1)
	require.Equal(t, ActionCut, got.Steps[0].Action)
}

func TestStore_List(t *testing.T) {
	t.Parallel()
	s, err := NewStore(tempStorePath(t))
	require.NoError(t, err)

	// Empty store
	require.Empty(t, s.List())

	// Add two macros
	_ = s.Save(Macro{Name: "alpha", Steps: []Step{{Action: ActionCut, Params: map[string]any{"source": "cam1"}}}})
	_ = s.Save(Macro{Name: "beta", Steps: []Step{{Action: ActionPreview, Params: map[string]any{"source": "cam2"}}}})

	list := s.List()
	require.Len(t, list, 2)
}

func TestStore_Delete(t *testing.T) {
	t.Parallel()
	s, err := NewStore(tempStorePath(t))
	require.NoError(t, err)

	_ = s.Save(Macro{Name: "to-delete", Steps: []Step{{Action: ActionCut, Params: map[string]any{"source": "cam1"}}}})

	require.NoError(t, s.Delete("to-delete"))

	_, err = s.Get("to-delete")
	require.Error(t, err, "expected error after delete")
}

func TestStore_GetNonexistent(t *testing.T) {
	t.Parallel()
	s, err := NewStore(tempStorePath(t))
	require.NoError(t, err)

	_, err = s.Get("does-not-exist")
	require.Error(t, err, "expected error for nonexistent macro")
}

func TestStore_SaveEmptyName(t *testing.T) {
	t.Parallel()
	s, err := NewStore(tempStorePath(t))
	require.NoError(t, err)

	err = s.Save(Macro{Name: "", Steps: []Step{{Action: ActionCut, Params: map[string]any{"source": "cam1"}}}})
	require.Error(t, err, "expected error for empty name")
}

func TestStore_SaveNoSteps(t *testing.T) {
	t.Parallel()
	s, err := NewStore(tempStorePath(t))
	require.NoError(t, err)

	err = s.Save(Macro{Name: "empty-steps", Steps: nil})
	require.Error(t, err, "expected error for no steps")

	err = s.Save(Macro{Name: "empty-steps", Steps: []Step{}})
	require.Error(t, err, "expected error for empty steps slice")
}

func TestStore_ConcurrentAccess(t *testing.T) {
	t.Parallel()
	s, err := NewStore(tempStorePath(t))
	require.NoError(t, err)

	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			name := fmt.Sprintf("macro-%d", n)
			_ = s.Save(Macro{Name: name, Steps: []Step{{Action: ActionWait, Params: map[string]any{"ms": float64(100)}}}})
			_ = s.List()
			_, _ = s.Get(name)
		}(i)
	}
	wg.Wait()
}

func TestStore_DeleteNonexistent(t *testing.T) {
	t.Parallel()
	s, err := NewStore(tempStorePath(t))
	require.NoError(t, err)

	err = s.Delete("nope")
	require.Error(t, err, "expected error deleting nonexistent macro")
}

func TestStore_Persistence(t *testing.T) {
	t.Parallel()
	path := tempStorePath(t)

	s1, err := NewStore(path)
	require.NoError(t, err)
	_ = s1.Save(Macro{Name: "persist-test", Steps: []Step{{Action: ActionCut, Params: map[string]any{"source": "cam1"}}}})

	// Re-open store from same file
	s2, err := NewStore(path)
	require.NoError(t, err)

	got, err := s2.Get("persist-test")
	require.NoError(t, err)
	require.Equal(t, "persist-test", got.Name)
}

func TestStore_SaveOverwrite(t *testing.T) {
	t.Parallel()
	s, err := NewStore(tempStorePath(t))
	require.NoError(t, err)

	_ = s.Save(Macro{Name: "overwrite", Steps: []Step{{Action: ActionCut, Params: map[string]any{"source": "cam1"}}}})
	_ = s.Save(Macro{Name: "overwrite", Steps: []Step{{Action: ActionPreview, Params: map[string]any{"source": "cam2"}}}})

	got, err := s.Get("overwrite")
	require.NoError(t, err)
	require.Equal(t, ActionPreview, got.Steps[0].Action)

	// Should still be 1 macro total
	require.Len(t, s.List(), 1)
}

func TestStore_Save_RollbackOnSaveFailure_Create(t *testing.T) {
	t.Parallel()
	path := tempStorePath(t)
	s, err := NewStore(path)
	require.NoError(t, err)

	// Successfully save one macro first.
	m1 := Macro{Name: "alpha", Steps: []Step{{Action: ActionCut, Params: map[string]any{"source": "cam1"}}}}
	require.NoError(t, s.Save(m1))

	// Make the directory read-only so save() will fail.
	dir := filepath.Dir(path)
	require.NoError(t, os.Chmod(dir, 0o500))
	t.Cleanup(func() { _ = os.Chmod(dir, 0o755) })
	_ = os.Remove(path)

	// Try to create a new macro — should fail.
	m2 := Macro{Name: "beta", Steps: []Step{{Action: ActionPreview, Params: map[string]any{"source": "cam2"}}}}
	err = s.Save(m2)
	require.Error(t, err, "expected save failure")

	// In-memory state should be rolled back: only alpha should remain.
	require.Len(t, s.List(), 1)
	require.Equal(t, "alpha", s.List()[0].Name)

	// The failed macro should not be findable.
	_, err = s.Get("beta")
	require.ErrorIs(t, err, ErrNotFound)
}

func TestStore_Save_RollbackOnSaveFailure_Update(t *testing.T) {
	t.Parallel()
	path := tempStorePath(t)
	s, err := NewStore(path)
	require.NoError(t, err)

	// Successfully save a macro.
	original := Macro{Name: "alpha", Steps: []Step{{Action: ActionCut, Params: map[string]any{"source": "cam1"}}}}
	require.NoError(t, s.Save(original))

	// Make the directory read-only so save() will fail.
	dir := filepath.Dir(path)
	require.NoError(t, os.Chmod(dir, 0o500))
	t.Cleanup(func() { _ = os.Chmod(dir, 0o755) })
	_ = os.Remove(path)

	// Try to update the macro — should fail.
	updated := Macro{Name: "alpha", Steps: []Step{{Action: ActionPreview, Params: map[string]any{"source": "cam2"}}}}
	err = s.Save(updated)
	require.Error(t, err, "expected save failure")

	// In-memory state should still have the original action.
	got, err := s.Get("alpha")
	require.NoError(t, err)
	require.Equal(t, ActionCut, got.Steps[0].Action)
	require.Equal(t, "cam1", got.Steps[0].Params["source"])
}

func TestStore_Delete_RollbackOnSaveFailure(t *testing.T) {
	t.Parallel()
	path := tempStorePath(t)
	s, err := NewStore(path)
	require.NoError(t, err)

	// Save three macros.
	for _, name := range []string{"alpha", "beta", "gamma"} {
		require.NoError(t, s.Save(Macro{
			Name:  name,
			Steps: []Step{{Action: ActionCut, Params: map[string]any{"source": "cam1"}}},
		}))
	}

	// Make the directory read-only so save() will fail.
	dir := filepath.Dir(path)
	require.NoError(t, os.Chmod(dir, 0o500))
	t.Cleanup(func() { _ = os.Chmod(dir, 0o755) })
	_ = os.Remove(path)

	// Try to delete beta — should fail.
	err = s.Delete("beta")
	require.Error(t, err, "expected save failure")

	// In-memory state should be rolled back: all three macros remain.
	require.Len(t, s.List(), 3)

	// Order should be preserved.
	list := s.List()
	require.Equal(t, "alpha", list[0].Name)
	require.Equal(t, "beta", list[1].Name)
	require.Equal(t, "gamma", list[2].Name)

	// Each should be retrievable.
	for _, name := range []string{"alpha", "beta", "gamma"} {
		_, err := s.Get(name)
		require.NoError(t, err)
	}
}

func TestStore_NewStoreCreatesDir(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "sub", "dir", "macros.json")

	s, err := NewStore(path)
	require.NoError(t, err)

	_ = s.Save(Macro{Name: "dir-test", Steps: []Step{{Action: ActionCut, Params: map[string]any{"source": "cam1"}}}})

	_, err = os.Stat(path)
	require.False(t, os.IsNotExist(err), "expected file to exist after save")
}
