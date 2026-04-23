package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

// switcherSeqFile is the on-disk JSON representation of the switcher's
// last-enqueued command seq — the high-water mark that must survive
// a process restart so the peer does not mis-read a fresh engine as
// "regressed to seq=0" and trigger spurious snapshot-push divergence
// recovery.
//
// Kept trivially small so operators can parse it with jq.
type switcherSeqFile struct {
	Seq uint64 `json:"seq"`
}

// loadSwitcherSeq reads the persisted lastCommandSeq from path. Returns
// (0, nil) if the file does not exist — a fresh install starts at 0. Any
// other error (unreadable, malformed JSON) is surfaced so the caller can
// log loudly rather than silently reset.
//
// Side effect (same as loadPeerEpoch): removes any stale <path>.tmp left
// by a crashed saveSwitcherSeq so they don't accumulate across restarts.
func loadSwitcherSeq(path string) (uint64, error) {
	_ = os.Remove(path + ".tmp")
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	var f switcherSeqFile
	if err := json.Unmarshal(data, &f); err != nil {
		return 0, fmt.Errorf("switcher seq file %s: %w", path, err)
	}
	return f.Seq, nil
}

// saveSwitcherSeq atomically writes the seq to path using the same
// temp+fsync+rename+parent-dir-fsync sequence as savePeerEpoch. A crash
// mid-write cannot leave a truncated file that parses as seq=0 and
// silently re-opens the restart-divergence hazard.
func saveSwitcherSeq(path string, seq uint64) error {
	data, err := json.Marshal(switcherSeqFile{Seq: seq})
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0600)
	if err != nil {
		return fmt.Errorf("open %s: %w", tmp, err)
	}
	if _, err := f.Write(data); err != nil {
		_ = f.Close()
		_ = os.Remove(tmp)
		return fmt.Errorf("write %s: %w", tmp, err)
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		_ = os.Remove(tmp)
		return fmt.Errorf("fsync %s: %w", tmp, err)
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("close %s: %w", tmp, err)
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("rename %s → %s: %w", tmp, path, err)
	}
	if dir, err := os.Open(filepath.Dir(path)); err == nil {
		_ = dir.Sync()
		_ = dir.Close()
	}
	return nil
}
