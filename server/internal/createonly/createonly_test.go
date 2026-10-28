package createonly

import (
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// An existing file is never replaced: an object-store mount without
// overwrite support refuses it, and one with it would lose the other
// writer's object.
func TestWriteRefusesExisting(t *testing.T) {
	p := filepath.Join(t.TempDir(), "clip.ts")
	if err := os.WriteFile(p, []byte("first"), 0o644); err != nil {
		t.Fatal(err)
	}
	err := Write(p, strings.NewReader("second"))
	if !errors.Is(err, fs.ErrExist) {
		t.Fatalf("err = %v, want fs.ErrExist", err)
	}
	if data, _ := os.ReadFile(p); string(data) != "first" {
		t.Fatalf("file replaced: %q", data)
	}
}

type failingReader struct{ n int }

func (r *failingReader) Read(p []byte) (int, error) {
	if r.n == 0 {
		return 0, io.ErrUnexpectedEOF
	}
	r.n--
	p[0] = 'x'
	return 1, nil
}

// A copy that fails part way leaves no file behind, so a later scan does
// not list a truncated clip.
func TestWriteRemovesPartialFile(t *testing.T) {
	p := filepath.Join(t.TempDir(), "clip.ts")
	if err := Write(p, &failingReader{n: 3}); !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("err = %v", err)
	}
	if _, err := os.Stat(p); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("partial file left: %v", err)
	}
}

func TestCopy(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "src")
	if err := os.WriteFile(src, []byte("media"), 0o644); err != nil {
		t.Fatal(err)
	}
	dst := filepath.Join(dir, "dst")
	if err := Copy(dst, src); err != nil {
		t.Fatal(err)
	}
	if data, _ := os.ReadFile(dst); string(data) != "media" {
		t.Fatalf("dst = %q", data)
	}
}
