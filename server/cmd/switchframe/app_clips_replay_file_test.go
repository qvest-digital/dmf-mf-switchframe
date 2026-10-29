package main

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"testing"

	"github.com/zsiec/switchframe/server/clip"
)

const replayTestClipID = "00112233445566778899aabbccddeeff"

// newReplayStore returns a clip store holding a replay under
// replayTestClipID, stored as <id>.ts with the given content.
func newReplayStore(t *testing.T, content string) (*clip.Store, string) {
	t.Helper()
	store, err := clip.NewStore(t.TempDir(), 1<<30)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(store.Dir(), replayTestClipID+".ts")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	c := &clip.Clip{Name: "cam1 replay", Filename: replayTestClipID + ".ts", Ephemeral: true, ByteSize: int64(len(content))}
	if err := store.AddWithID(replayTestClipID, c); err != nil {
		t.Fatal(err)
	}
	return store, path
}

func tempExport(t *testing.T, content []byte) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "replay-clip.ts")
	if err := os.WriteFile(p, content, 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

// The UI retries a replay request after a partial failure with the same
// body, so an engine that handled the first one exports the replay again
// under the clip ID it already stored. The second export must leave the
// stored clip's file alone.
func TestReplayExport_SecondExportUnderStoredIDKeepsFile(t *testing.T) {
	fixture, err := os.ReadFile("../../output/testdata/fixture.ts")
	if err != nil {
		t.Fatal(err)
	}
	store, path := newReplayStore(t, "stored replay")
	tmp := tempExport(t, fixture)

	(&App{}).handleReplayClipExported("cam1", tmp, replayTestClipID, store)

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("stored clip's file gone: %v", err)
	}
	if string(got) != "stored replay" {
		t.Fatalf("stored clip's file replaced (%d bytes)", len(got))
	}
	if _, err := store.Get(replayTestClipID); err != nil {
		t.Fatalf("stored clip gone: %v", err)
	}
	if _, err := os.Stat(tmp); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("export left behind: %v", err)
	}
}

func TestPlaceReplayFile(t *testing.T) {
	t.Run("free path", func(t *testing.T) {
		store, err := clip.NewStore(t.TempDir(), 1<<30)
		if err != nil {
			t.Fatal(err)
		}
		dest := filepath.Join(store.Dir(), replayTestClipID+".ts")
		tmp := tempExport(t, []byte("new"))
		placed, err := placeReplayFile(store, tmp, dest, replayTestClipID)
		if err != nil || !placed {
			t.Fatalf("placed=%v err=%v", placed, err)
		}
		if got, _ := os.ReadFile(dest); string(got) != "new" {
			t.Fatalf("dest holds %q", got)
		}
		if _, err := os.Stat(tmp); !errors.Is(err, fs.ErrNotExist) {
			t.Fatalf("temp file left: %v", err)
		}
	})
	t.Run("clip already stored", func(t *testing.T) {
		store, dest := newReplayStore(t, "stored")
		tmp := tempExport(t, []byte("new"))
		placed, err := placeReplayFile(store, tmp, dest, replayTestClipID)
		if err != nil || placed {
			t.Fatalf("placed=%v err=%v", placed, err)
		}
		if got, _ := os.ReadFile(dest); string(got) != "stored" {
			t.Fatalf("stored file now holds %q", got)
		}
		if _, err := os.Stat(tmp); !errors.Is(err, fs.ErrNotExist) {
			t.Fatalf("temp file left: %v", err)
		}
	})
	// A file no clip owns was left by an export that did not finish.
	t.Run("file without a clip", func(t *testing.T) {
		store, err := clip.NewStore(t.TempDir(), 1<<30)
		if err != nil {
			t.Fatal(err)
		}
		dest := filepath.Join(store.Dir(), replayTestClipID+".ts")
		if err := os.WriteFile(dest, []byte("left over"), 0o644); err != nil {
			t.Fatal(err)
		}
		placed, err := placeReplayFile(store, tempExport(t, []byte("new")), dest, replayTestClipID)
		if err != nil || !placed {
			t.Fatalf("placed=%v err=%v", placed, err)
		}
		if got, _ := os.ReadFile(dest); string(got) != "new" {
			t.Fatalf("dest holds %q", got)
		}
	})
}

// The copy that stands in for a link across filesystems must not replace
// an existing file either.
func TestCopyFile_RefusesExistingDestination(t *testing.T) {
	dir := t.TempDir()
	dst := filepath.Join(dir, "dst.ts")
	if err := os.WriteFile(dst, []byte("stored"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := copyFile(tempExport(t, []byte("new")), dst); !errors.Is(err, fs.ErrExist) {
		t.Fatalf("copyFile onto an existing file = %v, want fs.ErrExist", err)
	}
	if got, _ := os.ReadFile(dst); string(got) != "stored" {
		t.Fatalf("dst holds %q", got)
	}
}
