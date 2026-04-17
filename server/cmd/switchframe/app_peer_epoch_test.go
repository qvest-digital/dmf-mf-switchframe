package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadPeerEpoch_MissingFileReturnsZero(t *testing.T) {
	path := filepath.Join(t.TempDir(), "peer_epoch.json")
	got, err := loadPeerEpoch(path)
	if err != nil {
		t.Fatalf("loadPeerEpoch: %v", err)
	}
	if got != 0 {
		t.Errorf("epoch = %d, want 0 for missing file", got)
	}
}

func TestSaveLoadPeerEpoch_Roundtrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "peer_epoch.json")
	for _, want := range []uint64{1, 7, 42, 1_000_000} {
		if err := savePeerEpoch(path, want); err != nil {
			t.Fatalf("save(%d): %v", want, err)
		}
		got, err := loadPeerEpoch(path)
		if err != nil {
			t.Fatalf("load after save(%d): %v", want, err)
		}
		if got != want {
			t.Errorf("roundtrip: got %d, want %d", got, want)
		}
	}
}

func TestLoadPeerEpoch_MalformedFileErrors(t *testing.T) {
	path := filepath.Join(t.TempDir(), "peer_epoch.json")
	if err := os.WriteFile(path, []byte("{not json"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := loadPeerEpoch(path); err == nil {
		t.Errorf("loadPeerEpoch on malformed file returned nil error")
	}
}
