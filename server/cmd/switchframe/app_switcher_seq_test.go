package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadSwitcherSeq_MissingFileReturnsZero(t *testing.T) {
	path := filepath.Join(t.TempDir(), "switcher_seq.json")
	got, err := loadSwitcherSeq(path)
	if err != nil {
		t.Fatalf("loadSwitcherSeq: %v", err)
	}
	if got != 0 {
		t.Errorf("seq = %d, want 0 for missing file", got)
	}
}

func TestSaveLoadSwitcherSeq_Roundtrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "switcher_seq.json")
	for _, want := range []uint64{1, 50, 127, 1_000_000, 1<<63 - 1} {
		if err := saveSwitcherSeq(path, want); err != nil {
			t.Fatalf("save(%d): %v", want, err)
		}
		got, err := loadSwitcherSeq(path)
		if err != nil {
			t.Fatalf("load after save(%d): %v", want, err)
		}
		if got != want {
			t.Errorf("roundtrip: got %d, want %d", got, want)
		}
	}
}

func TestLoadSwitcherSeq_MalformedFileErrors(t *testing.T) {
	path := filepath.Join(t.TempDir(), "switcher_seq.json")
	if err := os.WriteFile(path, []byte("{not json"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := loadSwitcherSeq(path); err == nil {
		t.Errorf("loadSwitcherSeq on malformed file returned nil error")
	}
}

// TestLoadSwitcherSeq_CleansUpStaleTmp confirms the side-effect: a leftover
// .tmp from a crashed save is removed on load, same as loadPeerEpoch.
func TestLoadSwitcherSeq_CleansUpStaleTmp(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "switcher_seq.json")
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, []byte("stale"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := loadSwitcherSeq(path); err != nil {
		t.Fatalf("loadSwitcherSeq: %v", err)
	}
	if _, err := os.Stat(tmp); !os.IsNotExist(err) {
		t.Errorf("stale .tmp not cleaned up: err=%v", err)
	}
}
