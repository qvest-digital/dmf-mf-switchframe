//go:build linux

package clip

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/zsiec/switchframe/server/internal/createonly/renamewatch"
)

// Nothing the store does renames a file in the library: on an object-store
// mount a rename loses the file or leaves it empty.
func TestLibraryIsNeverRenamedInto(t *testing.T) {
	lib := t.TempDir()
	writeFile(t, filepath.Join(lib, "Opener.mp4"), "clip-opener")
	writeFile(t, filepath.Join(lib, "Cut.ts"), "clip-cut")
	writeFile(t, filepath.Join(lib, "Cut.ts.pending"), "x")
	renames := renamewatch.Watch(t, lib)

	s, _ := newLibraryStore(t, lib)
	if _, err := s.ScanLibrary(); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(s.Dir(), "upload.ts"), "clip-upload")
	c := &Clip{Name: "Upload", Filename: "upload.ts", Source: SourceReplay, Ephemeral: true,
		CreatedAt: time.Now().Add(-48 * time.Hour), ByteSize: 11}
	if err := s.Add(c); err != nil {
		t.Fatal(err)
	}
	if err := s.Publish(c.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.Update(c.ID, func(c *Clip) { c.Name = "Renamed" }); err != nil {
		t.Fatal(err)
	}
	if err := s.Pin(c.ID); err != nil {
		t.Fatal(err)
	}
	s.CleanEphemeral(time.Hour, nil)
	for _, cl := range s.List() {
		if err := s.Delete(cl.ID); err != nil {
			t.Fatal(err)
		}
	}
	if got := renames(); len(got) != 0 {
		t.Fatalf("renamed in the library: %v", got)
	}
	if _, err := os.Stat(filepath.Join(lib, "Cut.ts")); err != nil {
		t.Fatalf("pending file touched: %v", err)
	}
}
