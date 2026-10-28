package renamewatch

import (
	"os"
	"path/filepath"
	"testing"
)

func TestWatchSeesRenames(t *testing.T) {
	dir, out := t.TempDir(), t.TempDir()
	renames := Watch(t, dir)
	if err := os.WriteFile(filepath.Join(out, "a"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(filepath.Join(out, "a"), filepath.Join(dir, "a")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "b"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	got := renames()
	if len(got) != 1 || got[0] != filepath.Join(dir, "a") {
		t.Fatalf("renames = %v, want only the one into %s", got, dir)
	}
}
