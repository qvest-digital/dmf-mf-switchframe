package cmdqueue

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// maxWALLineBytes caps a single WAL line (one JSON-encoded entry). Command
// payloads are raw client JSON bounded upstream by the HTTP body limit
// (16MB, control/middleware.go maxCommandBodySize); 32MB leaves generous
// headroom. Append enforces the cap so an oversized entry can never be
// written, and readEntries sizes its scanner buffer to match so a large
// (but legal) entry can never trip bufio.ErrTooLong — with the default
// 64KB scanner limit, one >64KB payload used to permanently poison every
// subsequent WAL read until the file was deleted by hand.
const maxWALLineBytes = 32 << 20

// walCompactMinAppends is how many new lines (command appends + executed
// tombstones) must accumulate since the last rewrite before
// MaybeCompactExecuted performs a compaction pass. It bounds steady-state
// file growth at ~walCompactMinAppends lines beyond the compacted size
// while keeping the amortized compaction cost negligible.
const walCompactMinAppends = 1024

// ErrWALEntryTooLarge is returned by Append when a single encoded entry
// would exceed maxWALLineBytes.
var ErrWALEntryTooLarge = errors.New("cmdqueue: WAL entry exceeds maximum size")

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
// JSON-lines file. Commands are appended on Enqueue, and Drain appends
// executed tombstone lines (merged with their command entries at read
// time). On startup, Replay returns un-executed commands that are not
// stale relative to the provided maxAge.
type WAL struct {
	path string
	mu   sync.Mutex
	file walFile

	// closed blocks late background compactions from resurrecting the file
	// handle after Close.
	closed bool
	// lines is a best-effort count of the lines currently in the file
	// (refreshed by reads, advanced by appends, reset by rewrites);
	// linesAtRewrite is its value just after the last rewrite. Their
	// difference is the growth that gates MaybeCompactExecuted.
	lines          int
	linesAtRewrite int
	// rewriteGen increments on every rewrite/Truncate so an in-flight
	// background compaction can detect that its snapshot went stale.
	rewriteGen uint64
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
	entry := walEntry{Command: cmd, Executed: false}
	data, err := json.Marshal(entry)
	if err != nil {
		return err
	}
	if len(data)+1 > maxWALLineBytes {
		return fmt.Errorf("%w: entry is %d bytes, limit is %d", ErrWALEntryTooLarge, len(data), maxWALLineBytes)
	}
	data = append(data, '\n')

	w.mu.Lock()
	defer w.mu.Unlock()
	if _, err := w.file.Write(data); err != nil {
		return err
	}
	w.lines++
	return w.file.Sync()
}

// MarkExecuted marks the entry with the matching Seq as executed.
//
// Hot-path callers draining multiple commands per tick must prefer
// MarkExecutedCommands: this single-seq variant costs one write+fsync per
// call, so marking N drained commands one-by-one costs N fsyncs.
//
// Seq 0 is a no-op: a bare Seq-0 tombstone cannot be correlated with a
// specific pending entry (see MarkExecutedCommands).
func (w *WAL) MarkExecuted(seq uint64) error {
	return w.MarkExecutedBatch([]uint64{seq})
}

// MarkExecutedBatch marks every entry whose Seq is in seqs as executed.
// Seq 0 values are silently skipped: Seq-0 commands are untimed/legacy and
// dedup-exempt, so SEVERAL un-executed Seq-0 entries can coexist in the
// file — a bare {Seq:0} tombstone cannot be correlated with the one that
// actually executed, and applying it to all of them (the old behavior)
// silently dropped still-pending Seq-0 commands at crash replay. Callers
// that hold the executed commands themselves (Drain) must use
// MarkExecutedCommands, which writes correlatable full-command tombstones
// for Seq 0.
func (w *WAL) MarkExecutedBatch(seqs []uint64) error {
	cmds := make([]Command, 0, len(seqs))
	for _, s := range seqs {
		if s == 0 {
			continue
		}
		cmds = append(cmds, Command{Seq: s})
	}
	return w.MarkExecutedCommands(cmds)
}

// MarkExecutedCommands marks every command in cmds as executed by APPENDING
// tombstone lines, with a single write and a single fsync — regardless of
// how many commands are supplied. Readers merge tombstones with their
// earlier command entries (see readEntries), so the effect matches
// rewriting the file, but the Drain hot path never pays an O(file size)
// read+rewrite, and a torn append can at worst lose the tombstone suffix —
// never previously committed state. Passing an empty slice is a no-op (no
// write, no fsync).
//
// Tombstone shape depends on Seq:
//   - Seq > 0: one slim {Seq, Executed:true} line per unique Seq — dedup
//     seeding (ReplayExecutedSeqs) only needs the Seq, and at most one
//     pending entry can carry it.
//   - Seq == 0: one FULL-command tombstone per command (no dedup — several
//     identical Seq-0 commands may be pending at once). Seq alone cannot
//     identify which Seq-0 entry executed, so the merge in readEntries
//     correlates these by command content instead.
func (w *WAL) MarkExecutedCommands(cmds []Command) error {
	var buf bytes.Buffer
	var lines int
	marked := make(map[uint64]struct{}, len(cmds))
	for _, c := range cmds {
		entry := walEntry{Executed: true}
		if c.Seq > 0 {
			if _, dup := marked[c.Seq]; dup {
				continue
			}
			marked[c.Seq] = struct{}{}
			entry.Command = Command{Seq: c.Seq}
		} else {
			entry.Command = c
		}
		data, err := json.Marshal(entry)
		if err != nil {
			return err
		}
		buf.Write(data)
		buf.WriteByte('\n')
		lines++
	}
	if lines == 0 {
		return nil
	}

	w.mu.Lock()
	defer w.mu.Unlock()
	if _, err := w.file.Write(buf.Bytes()); err != nil {
		return err
	}
	w.lines += lines
	return w.file.Sync()
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
	w.lines = 0
	w.linesAtRewrite = 0
	w.rewriteGen++
	return w.file.Sync()
}

// CompactExecuted rewrites the WAL keeping every un-executed entry (for
// crash-replay) plus the most recent keepRecent executed tombstones (for
// dedup-map seeding at restart).  Executed tombstones drop the command
// payload since dedup only needs the Seq — saves disk and keeps the file
// (and thus every full read) bounded instead of O(uptime).
//
// This synchronous variant holds the WAL mutex for the whole pass; the
// Drain hot path uses MaybeCompactExecuted from a background goroutine
// instead.  The rewrite is atomic (temp file + fsync + rename), so a crash
// or I/O failure at any point preserves the previous file.
func (w *WAL) CompactExecuted(keepRecent int) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed {
		return errors.New("cmdqueue: WAL is closed")
	}
	entries, err := w.readEntriesLocked()
	if err != nil {
		return err
	}
	return w.rewriteLocked(compactEntries(entries, keepRecent))
}

// MaybeCompactExecuted compacts like CompactExecuted, but only once at
// least walCompactMinAppends lines have accumulated since the last rewrite,
// and it is engineered to run from a background goroutine without stalling
// the frame loop: the O(file size) read and the bulk temp-file write+fsync
// happen WITHOUT holding the WAL mutex, so concurrent Append /
// MarkExecutedBatch calls are blocked only for the final suffix carry-over,
// an incremental fsync, and the rename+handle swap. Lines appended while
// the snapshot was being compacted are copied over verbatim; a concurrent
// rewrite or Truncate aborts the pass (rewriteGen guard).
func (w *WAL) MaybeCompactExecuted(keepRecent int) error {
	// Phase 1: snapshot boundaries under the lock (cheap).
	w.mu.Lock()
	if w.closed || w.lines-w.linesAtRewrite < walCompactMinAppends {
		w.mu.Unlock()
		return nil
	}
	snapSize, err := w.file.Seek(0, io.SeekEnd)
	if err != nil {
		w.mu.Unlock()
		return err
	}
	gen := w.rewriteGen
	w.mu.Unlock()

	// Phase 2 (unlocked): read, compact, and write the bulk of the new
	// file. Everything below snapSize is complete, immutable (append-only)
	// data, so reading it without the lock is safe; the only mutators
	// (rewrite/Truncate) are caught by the gen check below.
	entries, _, err := readEntries(w.path, snapSize)
	if err != nil {
		return err
	}
	compacted := compactEntries(entries, keepRecent)

	tmp, err := os.CreateTemp(filepath.Dir(w.path), filepath.Base(w.path)+".compact-*")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	discard := func(err error) error {
		_ = tmp.Close()
		_ = os.Remove(tmpPath)
		return err
	}
	lines, err := writeEntries(tmp, compacted)
	if err != nil {
		return discard(err)
	}
	if err := tmp.Sync(); err != nil { // bulk fsync outside the lock
		return discard(err)
	}

	// Phase 3 (locked): carry over lines appended since the snapshot, then
	// atomically swap the file in.
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed || w.rewriteGen != gen {
		return discard(nil)
	}
	suffixLines, err := copyFileSuffix(tmp, w.path, snapSize)
	if err != nil {
		return discard(err)
	}
	if err := tmp.Sync(); err != nil { // incremental: only the suffix is dirty
		return discard(err)
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpPath)
		return err
	}
	return w.replaceFileLocked(tmpPath, lines+suffixLines)
}

// Close closes the underlying file.
func (w *WAL) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.closed = true
	return w.file.Close()
}

// readEntriesLocked reads all WAL entries from disk. Caller must hold w.mu.
func (w *WAL) readEntriesLocked() ([]walEntry, error) {
	entries, lines, err := readEntries(w.path, -1)
	if err != nil {
		return nil, err
	}
	if lines > w.lines {
		w.lines = lines
	}
	return entries, nil
}

// readEntries reads and merges the WAL entries from path. limit >= 0
// restricts the read to the first limit bytes (used by
// MaybeCompactExecuted to read a consistent append-only prefix without
// holding the WAL mutex); limit < 0 reads the whole file.
//
// Merge semantics: a line with Executed=true and Seq > 0 marks every
// earlier un-executed entry with the same Seq as executed (these are the
// tombstone lines appended by MarkExecutedCommands); if no earlier entry
// matches, the line is retained as a standalone executed tombstone (the
// compacted form that seeds ReplayExecutedSeqs).
//
// Seq-0 tombstones are NEVER merged by Seq: Seq-0 commands are dedup-exempt,
// so several un-executed Seq-0 entries can coexist, and flipping them all
// from one tombstone would silently drop still-pending commands at crash
// replay. Instead a Seq-0 tombstone carries the full executed command (see
// MarkExecutedCommands) and marks exactly ONE entry: the oldest un-executed
// Seq-0 entry with identical command content. Oldest-first is exact —
// content-equal commands are interchangeable, and a differing command can
// never be matched by mistake. (Purely positional matching would be wrong:
// Drain pops Seq-0 commands in heap order — ExecuteAt order, ties arbitrary
// — not WAL append order.) An unmatched Seq-0 tombstone is retained
// standalone and dropped at the next compaction; it carries no dedup value.
//
// The scanner buffer is sized to maxWALLineBytes — matching the cap
// enforced by Append — so a large (but legal) payload can never trip
// bufio.ErrTooLong and permanently poison reads.
func readEntries(path string, limit int64) ([]walEntry, int, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, 0, err
	}
	defer func() { _ = f.Close() }()

	var r io.Reader = f
	if limit >= 0 {
		r = io.LimitReader(f, limit)
	}

	var (
		entries    []walEntry
		unexecuted = make(map[uint64][]int)
		lines      int
	)
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 64*1024), maxWALLineBytes)
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		lines++
		var e walEntry
		if err := json.Unmarshal(line, &e); err != nil {
			continue // skip corrupt lines
		}
		if !e.Executed {
			unexecuted[e.Command.Seq] = append(unexecuted[e.Command.Seq], len(entries))
			entries = append(entries, e)
			continue
		}
		if e.Command.Seq == 0 {
			// Content-correlated merge: mark only the oldest pending Seq-0
			// entry whose command matches the tombstone exactly. Legacy bare
			// {Seq:0} tombstones (pre-fix files) match nothing and fall
			// through to standalone retention — erring toward at-least-once
			// replay rather than silently dropping pending commands.
			idxs := unexecuted[0]
			matched := false
			for j, i := range idxs {
				if sameCommand(entries[i].Command, e.Command) {
					entries[i].Executed = true
					unexecuted[0] = append(idxs[:j], idxs[j+1:]...)
					matched = true
					break
				}
			}
			if !matched {
				entries = append(entries, e)
			}
			continue
		}
		if idxs := unexecuted[e.Command.Seq]; len(idxs) > 0 {
			for _, i := range idxs {
				entries[i].Executed = true
			}
			delete(unexecuted, e.Command.Seq)
		} else {
			entries = append(entries, e)
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, 0, err
	}
	return entries, lines, nil
}

// sameCommand reports whether two commands carry identical content. Used by
// readEntries to correlate a Seq-0 executed tombstone with the pending entry
// it belongs to. Both sides of the comparison round-trip through the same
// JSON encode→file→decode pipeline (Append and MarkExecutedCommands marshal
// the same in-memory Command, and encoding/json compacts RawMessage on
// marshal), so byte comparison of payloads is exact.
func sameCommand(a, b Command) bool {
	return a.Seq == b.Seq &&
		a.ExecuteAt == b.ExecuteAt &&
		a.Action == b.Action &&
		bytes.Equal(a.Payload, b.Payload)
}

// compactEntries keeps every un-executed entry (for crash-replay) plus the
// most recent keepRecent executed tombstones (for dedup-map seeding at
// restart). Executed entries drop the command payload since dedup only
// needs the Seq. Executed Seq-0 entries — both merged command entries and
// unmatched standalone tombstones — are dropped entirely: dedup ignores
// Seq 0 (ReplayExecutedSeqs skips them), and rewriting a Seq-0 tombstone
// after the pending entries could wrongly match a later pending Seq-0
// command under the content-correlated merge in readEntries.
func compactEntries(entries []walEntry, keepRecent int) []walEntry {
	pending := entries[:0:0]
	var executedTomb []walEntry
	for _, e := range entries {
		switch {
		case !e.Executed:
			pending = append(pending, e)
		case e.Command.Seq > 0:
			executedTomb = append(executedTomb, walEntry{
				Command:  Command{Seq: e.Command.Seq},
				Executed: true,
			})
		}
	}
	if keepRecent >= 0 && len(executedTomb) > keepRecent {
		executedTomb = executedTomb[len(executedTomb)-keepRecent:]
	}
	return append(pending, executedTomb...)
}

// writeEntries writes entries as JSON lines to dst and returns the number
// of lines written.
func writeEntries(dst io.Writer, entries []walEntry) (int, error) {
	bw := bufio.NewWriter(dst)
	for _, e := range entries {
		data, err := json.Marshal(e)
		if err != nil {
			return 0, err
		}
		if _, err := bw.Write(data); err != nil {
			return 0, err
		}
		if err := bw.WriteByte('\n'); err != nil {
			return 0, err
		}
	}
	if err := bw.Flush(); err != nil {
		return 0, err
	}
	return len(entries), nil
}

// copyFileSuffix appends the bytes of path beyond offset to dst and returns
// the number of newline-terminated lines copied.
func copyFileSuffix(dst io.Writer, path string, offset int64) (int, error) {
	src, err := os.Open(path)
	if err != nil {
		return 0, err
	}
	defer func() { _ = src.Close() }()
	if _, err := src.Seek(offset, io.SeekStart); err != nil {
		return 0, err
	}
	data, err := io.ReadAll(src)
	if err != nil {
		return 0, err
	}
	if len(data) == 0 {
		return 0, nil
	}
	if _, err := dst.Write(data); err != nil {
		return 0, err
	}
	return bytes.Count(data, []byte{'\n'}), nil
}

// rewriteLocked atomically rewrites the WAL with the given entries using
// the temp-file + fsync + rename pattern (mirroring preset/store.go and
// macro/store.go): a crash or I/O failure at ANY point leaves either the
// complete old file or the complete new file on disk — never a truncated
// or torn WAL. The previous in-place Seek+Truncate+Write implementation
// could persist an empty or arbitrarily torn file on power loss, silently
// destroying both crash-replay and dedup-tombstone state. Caller must
// hold w.mu.
func (w *WAL) rewriteLocked(entries []walEntry) error {
	tmp, err := os.CreateTemp(filepath.Dir(w.path), filepath.Base(w.path)+".rewrite-*")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	discard := func(err error) error {
		_ = tmp.Close()
		_ = os.Remove(tmpPath)
		return err
	}
	lines, err := writeEntries(tmp, entries)
	if err != nil {
		return discard(err)
	}
	if err := tmp.Sync(); err != nil {
		return discard(err)
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpPath)
		return err
	}
	return w.replaceFileLocked(tmpPath, lines)
}

// replaceFileLocked atomically replaces the WAL file with the fully synced
// file at tmpPath and swaps the append handle onto the new inode. The new
// handle is opened BEFORE the rename so a failure at any step leaves the
// previous file and handle fully intact. Caller must hold w.mu.
func (w *WAL) replaceFileLocked(tmpPath string, lines int) error {
	newFile, err := os.OpenFile(tmpPath, os.O_RDWR|os.O_APPEND, 0o644)
	if err != nil {
		_ = os.Remove(tmpPath)
		return err
	}
	if err := os.Rename(tmpPath, w.path); err != nil {
		_ = newFile.Close()
		_ = os.Remove(tmpPath)
		return err
	}
	syncDir(filepath.Dir(w.path))
	old := w.file
	w.file = newFile
	_ = old.Close()
	w.rewriteGen++
	w.lines = lines
	w.linesAtRewrite = lines
	return nil
}

// syncDir best-effort fsyncs a directory so a rename inside it is durable.
func syncDir(dir string) {
	d, err := os.Open(dir)
	if err != nil {
		return
	}
	_ = d.Sync()
	_ = d.Close()
}
