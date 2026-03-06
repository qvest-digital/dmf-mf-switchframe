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
		Steps: []MacroStep{
			{Action: ActionCut, Params: map[string]interface{}{"source": "cam1"}},
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
	_ = s.Save(Macro{Name: "alpha", Steps: []MacroStep{{Action: ActionCut, Params: map[string]interface{}{"source": "cam1"}}}})
	_ = s.Save(Macro{Name: "beta", Steps: []MacroStep{{Action: ActionPreview, Params: map[string]interface{}{"source": "cam2"}}}})

	list := s.List()
	require.Len(t, list, 2)
}

func TestStore_Delete(t *testing.T) {
	t.Parallel()
	s, err := NewStore(tempStorePath(t))
	require.NoError(t, err)

	_ = s.Save(Macro{Name: "to-delete", Steps: []MacroStep{{Action: ActionCut, Params: map[string]interface{}{"source": "cam1"}}}})

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

	err = s.Save(Macro{Name: "", Steps: []MacroStep{{Action: ActionCut, Params: map[string]interface{}{"source": "cam1"}}}})
	require.Error(t, err, "expected error for empty name")
}

func TestStore_SaveNoSteps(t *testing.T) {
	t.Parallel()
	s, err := NewStore(tempStorePath(t))
	require.NoError(t, err)

	err = s.Save(Macro{Name: "empty-steps", Steps: nil})
	require.Error(t, err, "expected error for no steps")

	err = s.Save(Macro{Name: "empty-steps", Steps: []MacroStep{}})
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
			_ = s.Save(Macro{Name: name, Steps: []MacroStep{{Action: ActionWait, Params: map[string]interface{}{"ms": float64(100)}}}})
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
	_ = s1.Save(Macro{Name: "persist-test", Steps: []MacroStep{{Action: ActionCut, Params: map[string]interface{}{"source": "cam1"}}}})

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

	_ = s.Save(Macro{Name: "overwrite", Steps: []MacroStep{{Action: ActionCut, Params: map[string]interface{}{"source": "cam1"}}}})
	_ = s.Save(Macro{Name: "overwrite", Steps: []MacroStep{{Action: ActionPreview, Params: map[string]interface{}{"source": "cam2"}}}})

	got, err := s.Get("overwrite")
	require.NoError(t, err)
	require.Equal(t, ActionPreview, got.Steps[0].Action)

	// Should still be 1 macro total
	require.Len(t, s.List(), 1)
}

func TestStore_NewStoreCreatesDir(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "sub", "dir", "macros.json")

	s, err := NewStore(path)
	require.NoError(t, err)

	_ = s.Save(Macro{Name: "dir-test", Steps: []MacroStep{{Action: ActionCut, Params: map[string]interface{}{"source": "cam1"}}}})

	_, err = os.Stat(path)
	require.False(t, os.IsNotExist(err), "expected file to exist after save")
}
