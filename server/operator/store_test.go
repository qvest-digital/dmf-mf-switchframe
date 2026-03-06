package operator

import (
	"os"
	"path/filepath"
	"testing"
)

func tempStorePath(t *testing.T) string {
	t.Helper()
	return filepath.Join(t.TempDir(), "operators.json")
}

func TestNewStore_EmptyFile(t *testing.T) {
	s, err := NewStore(tempStorePath(t))
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	ops := s.List()
	if len(ops) != 0 {
		t.Errorf("expected 0 operators, got %d", len(ops))
	}
}

func TestStore_Register(t *testing.T) {
	s, _ := NewStore(tempStorePath(t))

	op, err := s.Register("Alice", RoleDirector)
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	if op.Name != "Alice" {
		t.Errorf("expected name 'Alice', got %q", op.Name)
	}
	if op.Role != RoleDirector {
		t.Errorf("expected role 'director', got %q", op.Role)
	}
	if op.ID == "" {
		t.Error("expected non-empty ID")
	}
	if op.Token == "" {
		t.Error("expected non-empty token")
	}
	if len(op.Token) != 64 {
		t.Errorf("expected 64-char hex token, got %d chars", len(op.Token))
	}
}

func TestStore_Register_EmptyName(t *testing.T) {
	s, _ := NewStore(tempStorePath(t))
	_, err := s.Register("", RoleDirector)
	if err != ErrEmptyName {
		t.Errorf("expected ErrEmptyName, got %v", err)
	}
}

func TestStore_Register_InvalidRole(t *testing.T) {
	s, _ := NewStore(tempStorePath(t))
	_, err := s.Register("Alice", Role("admin"))
	if err != ErrInvalidRole {
		t.Errorf("expected ErrInvalidRole, got %v", err)
	}
}

func TestStore_Register_DuplicateName(t *testing.T) {
	s, _ := NewStore(tempStorePath(t))
	_, _ = s.Register("Alice", RoleDirector)
	_, err := s.Register("Alice", RoleAudio)
	if err != ErrDuplicateName {
		t.Errorf("expected ErrDuplicateName, got %v", err)
	}
}

func TestStore_Get(t *testing.T) {
	s, _ := NewStore(tempStorePath(t))
	op, _ := s.Register("Alice", RoleDirector)

	got, err := s.Get(op.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Name != "Alice" {
		t.Errorf("expected name 'Alice', got %q", got.Name)
	}
}

func TestStore_Get_NotFound(t *testing.T) {
	s, _ := NewStore(tempStorePath(t))
	_, err := s.Get("nonexistent")
	if err != ErrNotFound {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
}

func TestStore_GetByToken(t *testing.T) {
	s, _ := NewStore(tempStorePath(t))
	op, _ := s.Register("Alice", RoleDirector)

	got, err := s.GetByToken(op.Token)
	if err != nil {
		t.Fatalf("GetByToken: %v", err)
	}
	if got.ID != op.ID {
		t.Errorf("expected ID %q, got %q", op.ID, got.ID)
	}
}

func TestStore_GetByToken_NotFound(t *testing.T) {
	s, _ := NewStore(tempStorePath(t))
	_, err := s.GetByToken("deadbeef")
	if err != ErrNotFound {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
}

func TestStore_Delete(t *testing.T) {
	s, _ := NewStore(tempStorePath(t))
	op, _ := s.Register("Alice", RoleDirector)

	err := s.Delete(op.ID)
	if err != nil {
		t.Fatalf("Delete: %v", err)
	}

	_, err = s.Get(op.ID)
	if err != ErrNotFound {
		t.Errorf("expected ErrNotFound after delete, got %v", err)
	}
}

func TestStore_Delete_NotFound(t *testing.T) {
	s, _ := NewStore(tempStorePath(t))
	err := s.Delete("nonexistent")
	if err != ErrNotFound {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
}

func TestStore_List(t *testing.T) {
	s, _ := NewStore(tempStorePath(t))
	_, _ = s.Register("Alice", RoleDirector)
	_, _ = s.Register("Bob", RoleAudio)
	_, _ = s.Register("Carol", RoleGraphics)

	ops := s.List()
	if len(ops) != 3 {
		t.Errorf("expected 3 operators, got %d", len(ops))
	}
}

func TestStore_Persistence(t *testing.T) {
	path := tempStorePath(t)

	// Create store and register operators.
	s1, _ := NewStore(path)
	_, _ = s1.Register("Alice", RoleDirector)
	_, _ = s1.Register("Bob", RoleAudio)

	// Create new store from same file — should reload operators.
	s2, err := NewStore(path)
	if err != nil {
		t.Fatalf("NewStore (reload): %v", err)
	}

	ops := s2.List()
	if len(ops) != 2 {
		t.Errorf("expected 2 operators after reload, got %d", len(ops))
	}
}

func TestStore_Persistence_DeleteAndReload(t *testing.T) {
	path := tempStorePath(t)

	s1, _ := NewStore(path)
	op, _ := s1.Register("Alice", RoleDirector)
	_ = s1.Delete(op.ID)

	s2, _ := NewStore(path)
	ops := s2.List()
	if len(ops) != 0 {
		t.Errorf("expected 0 operators after delete+reload, got %d", len(ops))
	}
}

func TestStore_FileCreatesDirectory(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "subdir", "operators.json")

	s, err := NewStore(path)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}

	_, _ = s.Register("Alice", RoleDirector)

	// Verify the file was created.
	if _, err := os.Stat(path); os.IsNotExist(err) {
		t.Error("expected operators.json to be created")
	}
}
