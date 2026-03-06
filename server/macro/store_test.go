package macro

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

func tempStorePath(t *testing.T) string {
	t.Helper()
	return filepath.Join(t.TempDir(), "macros.json")
}

func TestStore_SaveAndGet(t *testing.T) {
	s, err := NewStore(tempStorePath(t))
	if err != nil {
		t.Fatal(err)
	}

	m := Macro{
		Name: "my-macro",
		Steps: []MacroStep{
			{Action: ActionCut, Params: map[string]interface{}{"source": "cam1"}},
		},
	}
	if err := s.Save(m); err != nil {
		t.Fatal(err)
	}

	got, err := s.Get("my-macro")
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != "my-macro" {
		t.Fatalf("got name %q, want %q", got.Name, "my-macro")
	}
	if len(got.Steps) != 1 {
		t.Fatalf("got %d steps, want 1", len(got.Steps))
	}
	if got.Steps[0].Action != ActionCut {
		t.Fatalf("got action %q, want %q", got.Steps[0].Action, ActionCut)
	}
}

func TestStore_List(t *testing.T) {
	s, err := NewStore(tempStorePath(t))
	if err != nil {
		t.Fatal(err)
	}

	// Empty store
	if list := s.List(); len(list) != 0 {
		t.Fatalf("expected empty list, got %d", len(list))
	}

	// Add two macros
	_ = s.Save(Macro{Name: "alpha", Steps: []MacroStep{{Action: ActionCut, Params: map[string]interface{}{"source": "cam1"}}}})
	_ = s.Save(Macro{Name: "beta", Steps: []MacroStep{{Action: ActionPreview, Params: map[string]interface{}{"source": "cam2"}}}})

	list := s.List()
	if len(list) != 2 {
		t.Fatalf("expected 2 macros, got %d", len(list))
	}
}

func TestStore_Delete(t *testing.T) {
	s, err := NewStore(tempStorePath(t))
	if err != nil {
		t.Fatal(err)
	}

	_ = s.Save(Macro{Name: "to-delete", Steps: []MacroStep{{Action: ActionCut, Params: map[string]interface{}{"source": "cam1"}}}})

	if err := s.Delete("to-delete"); err != nil {
		t.Fatal(err)
	}

	_, err = s.Get("to-delete")
	if err == nil {
		t.Fatal("expected error after delete, got nil")
	}
}

func TestStore_GetNonexistent(t *testing.T) {
	s, err := NewStore(tempStorePath(t))
	if err != nil {
		t.Fatal(err)
	}

	_, err = s.Get("does-not-exist")
	if err == nil {
		t.Fatal("expected error for nonexistent macro")
	}
}

func TestStore_SaveEmptyName(t *testing.T) {
	s, err := NewStore(tempStorePath(t))
	if err != nil {
		t.Fatal(err)
	}

	err = s.Save(Macro{Name: "", Steps: []MacroStep{{Action: ActionCut, Params: map[string]interface{}{"source": "cam1"}}}})
	if err == nil {
		t.Fatal("expected error for empty name")
	}
}

func TestStore_SaveNoSteps(t *testing.T) {
	s, err := NewStore(tempStorePath(t))
	if err != nil {
		t.Fatal(err)
	}

	err = s.Save(Macro{Name: "empty-steps", Steps: nil})
	if err == nil {
		t.Fatal("expected error for no steps")
	}

	err = s.Save(Macro{Name: "empty-steps", Steps: []MacroStep{}})
	if err == nil {
		t.Fatal("expected error for empty steps slice")
	}
}

func TestStore_ConcurrentAccess(t *testing.T) {
	s, err := NewStore(tempStorePath(t))
	if err != nil {
		t.Fatal(err)
	}

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
	s, err := NewStore(tempStorePath(t))
	if err != nil {
		t.Fatal(err)
	}

	err = s.Delete("nope")
	if err == nil {
		t.Fatal("expected error deleting nonexistent macro")
	}
}

func TestStore_Persistence(t *testing.T) {
	path := tempStorePath(t)

	s1, err := NewStore(path)
	if err != nil {
		t.Fatal(err)
	}
	_ = s1.Save(Macro{Name: "persist-test", Steps: []MacroStep{{Action: ActionCut, Params: map[string]interface{}{"source": "cam1"}}}})

	// Re-open store from same file
	s2, err := NewStore(path)
	if err != nil {
		t.Fatal(err)
	}

	got, err := s2.Get("persist-test")
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != "persist-test" {
		t.Fatalf("expected persist-test, got %s", got.Name)
	}
}

func TestStore_SaveOverwrite(t *testing.T) {
	s, err := NewStore(tempStorePath(t))
	if err != nil {
		t.Fatal(err)
	}

	_ = s.Save(Macro{Name: "overwrite", Steps: []MacroStep{{Action: ActionCut, Params: map[string]interface{}{"source": "cam1"}}}})
	_ = s.Save(Macro{Name: "overwrite", Steps: []MacroStep{{Action: ActionPreview, Params: map[string]interface{}{"source": "cam2"}}}})

	got, err := s.Get("overwrite")
	if err != nil {
		t.Fatal(err)
	}
	if got.Steps[0].Action != ActionPreview {
		t.Fatalf("expected overwritten action %q, got %q", ActionPreview, got.Steps[0].Action)
	}

	// Should still be 1 macro total
	if list := s.List(); len(list) != 1 {
		t.Fatalf("expected 1 macro after overwrite, got %d", len(list))
	}
}

func TestStore_NewStoreCreatesDir(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sub", "dir", "macros.json")

	s, err := NewStore(path)
	if err != nil {
		t.Fatal(err)
	}

	_ = s.Save(Macro{Name: "dir-test", Steps: []MacroStep{{Action: ActionCut, Params: map[string]interface{}{"source": "cam1"}}}})

	if _, err := os.Stat(path); os.IsNotExist(err) {
		t.Fatal("expected file to exist after save")
	}
}
