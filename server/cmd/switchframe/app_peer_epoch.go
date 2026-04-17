package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

// peerEpochFile is the on-disk JSON representation of the local force-leader
// epoch. Kept intentionally trivial — one integer — so it can be re-parsed
// by operators with jq without tooling.
type peerEpochFile struct {
	Epoch uint64 `json:"epoch"`
}

// loadPeerEpoch reads the persisted force-leader epoch from path. Returns
// (0, nil) if the file does not exist — a fresh install is epoch 0. Any
// other error (unreadable, malformed JSON) is surfaced.
//
// Side effect: removes any stale `<path>.tmp` left over from a crashed
// savePeerEpoch — a leftover tmp is cosmetic not corrupting (the real
// file is atomically renamed), but cleaning up on load prevents it
// accumulating across restarts.
func loadPeerEpoch(path string) (uint64, error) {
	_ = os.Remove(path + ".tmp")
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	var f peerEpochFile
	if err := json.Unmarshal(data, &f); err != nil {
		return 0, fmt.Errorf("peer epoch file %s: %w", path, err)
	}
	return f.Epoch, nil
}

// savePeerEpoch atomically writes the epoch to path. Uses write-to-temp +
// fsync + rename + parent-dir fsync so a crash mid-write cannot leave a
// truncated file that parses as epoch 0 on next load (which would silently
// undo the force-leader). The tmp-file fsync is required before rename:
// on xfs/btrfs/ext4-writeback the dirent can survive a power loss pointing
// to unwritten data blocks, yielding an empty file on reboot.
func savePeerEpoch(path string, epoch uint64) error {
	data, err := json.Marshal(peerEpochFile{Epoch: epoch})
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
	// Best-effort fsync of the parent directory so the rename survives
	// a kernel panic. Ignore errors on platforms that don't support it.
	if dir, err := os.Open(filepath.Dir(path)); err == nil {
		_ = dir.Sync()
		_ = dir.Close()
	}
	return nil
}
