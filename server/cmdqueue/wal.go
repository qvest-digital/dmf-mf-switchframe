package cmdqueue

import (
	"bufio"
	"encoding/json"
	"io"
	"os"
	"sync"
	"time"
)

// walFile is the subset of *os.File operations the WAL uses.  Extracted
// as an interface so tests can inject a wrapper that counts Sync() calls
// to verify durability invariants (the production WAL passes a real
// *os.File).
type walFile interface {
	io.Writer
	io.Seeker
	io.Closer
	Truncate(size int64) error
	Sync() error
}

// WAL provides crash-recovery for enqueued commands via an append-only
// JSON-lines file. Commands are appended on Enqueue and marked executed
// after Drain. On startup, Replay returns un-executed commands that are
// not stale relative to the provided maxAge.
type WAL struct {
	path string
	mu   sync.Mutex
	file walFile
}

// walEntry is a single line in the WAL file.
type walEntry struct {
	Command  Command `json:"command"`
	Executed bool    `json:"executed"`
}

// NewWAL opens (or creates) a WAL file at path. The file is opened in
// append mode for writes. It must be closed via Close when no longer needed.
func NewWAL(path string) (*WAL, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR|os.O_APPEND, 0o644)
	if err != nil {
		return nil, err
	}
	return &WAL{path: path, file: f}, nil
}

// Append writes a new un-executed command entry to the WAL.
func (w *WAL) Append(cmd Command) error {
	w.mu.Lock()
	defer w.mu.Unlock()

	entry := walEntry{Command: cmd, Executed: false}
	data, err := json.Marshal(entry)
	if err != nil {
		return err
	}
	data = append(data, '\n')
	_, err = w.file.Write(data)
	if err != nil {
		return err
	}
	return w.file.Sync()
}

// MarkExecuted reads the entire WAL, marks the entry with the matching Seq
// as executed, and rewrites the file. This is acceptable because the WAL
// is small (typically <1000 entries between truncations).
func (w *WAL) MarkExecuted(seq uint64) error {
	w.mu.Lock()
	defer w.mu.Unlock()

	entries, err := w.readEntriesLocked()
	if err != nil {
		return err
	}

	for i := range entries {
		if entries[i].Command.Seq == seq {
			entries[i].Executed = true
		}
	}

	return w.rewriteLocked(entries)
}

// Replay returns all commands that are not executed and not stale (i.e.,
// whose ExecuteAt is within maxAge microseconds of the current wall-clock time,
// or ExecuteAt is 0). Wall clock is used because the PTP clock is not yet
// available at construction time; it is close enough for crash recovery
// (commands are typically <10s old).
func (w *WAL) Replay(maxAge int64) ([]Command, error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	entries, err := w.readEntriesLocked()
	if err != nil {
		return nil, err
	}

	now := time.Now().UnixMicro()

	var cmds []Command
	for _, e := range entries {
		if e.Executed {
			continue
		}
		// Skip stale commands.
		if e.Command.ExecuteAt != 0 && now-e.Command.ExecuteAt > maxAge {
			continue
		}
		cmds = append(cmds, e.Command)
	}
	return cmds, nil
}

// ReplayExecutedSeqs returns Seq values of all executed commands in the WAL.
// Used at startup to seed the dedup map so already-executed commands are rejected.
func (w *WAL) ReplayExecutedSeqs() ([]uint64, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	entries, err := w.readEntriesLocked()
	if err != nil {
		return nil, err
	}
	var seqs []uint64
	for _, e := range entries {
		if e.Executed && e.Command.Seq > 0 {
			seqs = append(seqs, e.Command.Seq)
		}
	}
	return seqs, nil
}

// Truncate clears the WAL file.  Dangerous: it also wipes the executed
// entries the dedup map is seeded from, so a restart immediately after
// Truncate cannot dedup retransmitted commands.  Use only for admin
// compaction, never from the Drain hot path.  Fsync'd before return so a
// crash cannot leave the on-disk file showing the pre-Truncate content.
func (w *WAL) Truncate() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if err := w.file.Truncate(0); err != nil {
		return err
	}
	return w.file.Sync()
}

// CompactExecuted rewrites the WAL keeping every un-executed entry (for
// crash-replay) plus the most recent keepRecent executed tombstones (for
// dedup-map seeding at restart).  Executed tombstones drop the command
// payload since dedup only needs the Seq — saves disk and makes the
// per-Drain rewrite cost O(keepRecent) instead of O(uptime).
//
// Designed to be called at end-of-Drain in place of the old Truncate
// path: bounded file size prevents MarkExecuted's rewriteLocked (which is
// O(file size) per call) from degrading over long uptime.  Fsync'd via
// rewriteLocked.
func (w *WAL) CompactExecuted(keepRecent int) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	entries, err := w.readEntriesLocked()
	if err != nil {
		return err
	}
	pending := entries[:0:0]
	executedTomb := make([]walEntry, 0, keepRecent)
	for _, e := range entries {
		if e.Executed {
			executedTomb = append(executedTomb, walEntry{
				Command:  Command{Seq: e.Command.Seq},
				Executed: true,
			})
		} else {
			pending = append(pending, e)
		}
	}
	if keepRecent >= 0 && len(executedTomb) > keepRecent {
		executedTomb = executedTomb[len(executedTomb)-keepRecent:]
	}
	compacted := append(pending, executedTomb...)
	return w.rewriteLocked(compacted)
}

// Close closes the underlying file.
func (w *WAL) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.file.Close()
}

// readEntriesLocked reads all WAL entries from disk. Caller must hold w.mu.
func (w *WAL) readEntriesLocked() ([]walEntry, error) {
	f, err := os.Open(w.path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()

	var entries []walEntry
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		var e walEntry
		if err := json.Unmarshal(line, &e); err != nil {
			continue // skip corrupt lines
		}
		entries = append(entries, e)
	}
	return entries, scanner.Err()
}

// rewriteLocked truncates the WAL and rewrites it with the given entries.
// Caller must hold w.mu.
func (w *WAL) rewriteLocked(entries []walEntry) error {
	// Seek to beginning and truncate.
	if _, err := w.file.Seek(0, 0); err != nil {
		return err
	}
	if err := w.file.Truncate(0); err != nil {
		return err
	}

	for _, e := range entries {
		data, err := json.Marshal(e)
		if err != nil {
			return err
		}
		data = append(data, '\n')
		if _, err := w.file.Write(data); err != nil {
			return err
		}
	}
	return w.file.Sync()
}
