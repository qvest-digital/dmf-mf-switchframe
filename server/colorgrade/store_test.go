package colorgrade

import (
	"strings"
	"testing"
)

func setupTestStore(t *testing.T) *Store {
	t.Helper()
	uploadDir := t.TempDir()

	store, err := NewStore(uploadDir)
	if err != nil {
		t.Fatal(err)
	}
	return store
}

func TestStore_List(t *testing.T) {
	store := setupTestStore(t)

	looks := store.List()
	if len(looks) < 6 {
		t.Fatalf("expected at least 6 built-in looks, got %d", len(looks))
	}
	// Verify all are builtin.
	for _, l := range looks {
		if !l.Builtin {
			t.Errorf("expected look %q to be builtin", l.Name)
		}
	}
	// Check one known look exists.
	found := false
	for _, l := range looks {
		if l.Name == "broadcast-news" {
			found = true
		}
	}
	if !found {
		t.Error("broadcast-news not found in built-in looks")
	}
}

func TestStore_Load(t *testing.T) {
	store := setupTestStore(t)

	lut, err := store.Load("broadcast-news")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if lut.Size != 33 {
		t.Errorf("size = %d, want 33", lut.Size)
	}
	if len(lut.Data) != 33*33*33*3 {
		t.Errorf("data length = %d, want %d", len(lut.Data), 33*33*33*3)
	}
}

func TestStore_Load_NotFound(t *testing.T) {
	store := setupTestStore(t)

	_, err := store.Load("nonexistent")
	if err == nil {
		t.Fatal("expected error for nonexistent look")
	}
}

func TestStore_Upload(t *testing.T) {
	store := setupTestStore(t)

	// Create a valid .cube string.
	lut := GenerateLook(BuiltinLooks()[1]) // cinematic
	var sb strings.Builder
	if err := WriteCube(&sb, lut); err != nil {
		t.Fatal(err)
	}

	if err := store.Upload("my-custom-look", strings.NewReader(sb.String())); err != nil {
		t.Fatalf("upload error: %v", err)
	}

	// Should appear in list.
	looks := store.List()
	found := false
	for _, l := range looks {
		if l.Name == "my-custom-look" {
			found = true
			if l.Builtin {
				t.Error("uploaded look should not be builtin")
			}
		}
	}
	if !found {
		t.Error("uploaded look not found in list")
	}

	// Should be loadable.
	loaded, err := store.Load("my-custom-look")
	if err != nil {
		t.Fatalf("load uploaded look: %v", err)
	}
	if loaded.Size != 33 {
		t.Errorf("loaded size = %d, want 33", loaded.Size)
	}
}

func TestStore_Upload_InvalidCube(t *testing.T) {
	store := setupTestStore(t)

	err := store.Upload("bad-look", strings.NewReader("not a valid cube file"))
	if err == nil {
		t.Fatal("expected error for invalid .cube upload")
	}
}

func TestStore_Delete(t *testing.T) {
	store := setupTestStore(t)

	// Upload a look first.
	lut := GenerateIdentityLUT()
	var sb strings.Builder
	_ = WriteCube(&sb, lut)
	_ = store.Upload("to-delete", strings.NewReader(sb.String()))

	// Delete it.
	if err := store.Delete("to-delete"); err != nil {
		t.Fatalf("delete error: %v", err)
	}

	// Should no longer be in list.
	for _, l := range store.List() {
		if l.Name == "to-delete" {
			t.Error("deleted look still appears in list")
		}
	}
}

func TestStore_Delete_Builtin(t *testing.T) {
	store := setupTestStore(t)

	err := store.Delete("broadcast-news")
	if err == nil {
		t.Fatal("expected error when deleting built-in look")
	}
}

func TestStore_Delete_NotFound(t *testing.T) {
	store := setupTestStore(t)

	err := store.Delete("nonexistent")
	if err == nil {
		t.Fatal("expected error when deleting nonexistent look")
	}
}

func TestStore_Upload_InvalidName(t *testing.T) {
	store := setupTestStore(t)

	lut := GenerateIdentityLUT()
	var sb strings.Builder
	_ = WriteCube(&sb, lut)

	err := store.Upload("../etc/passwd", strings.NewReader(sb.String()))
	if err == nil {
		t.Fatal("expected error for path traversal name")
	}

	err = store.Upload("has spaces", strings.NewReader(sb.String()))
	if err == nil {
		t.Fatal("expected error for name with spaces")
	}
}
