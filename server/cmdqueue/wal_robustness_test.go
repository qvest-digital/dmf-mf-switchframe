package cmdqueue

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// armedFailWriteFile wraps an *os.File and fails every Write while armed.
// Unlike failingAppendFile (single-shot), this models a disk that stays
// broken for the duration of a multi-write operation such as a rewrite.
type armedFailWriteFile struct {
	*os.File
	armed atomic.Bool
}

func (f *armedFailWriteFile) Write(p []byte) (int, error) {
	if f.armed.Load() {
		return 0, errors.New("simulated write failure")
	}
	return f.File.Write(p)
}

// ── Finding #1: bufio.Scanner 64KB line limit poisons the WAL ───────────────

// TestWAL_LargePayloadDoesNotPoisonReplay reproduces the reviewed finding:
// Command.Payload is raw client JSON bounded only by the 16MB HTTP body cap,
// so a single command over bufio.Scanner's default 64KB token limit used to
// make EVERY subsequent read (Replay, ReplayExecutedSeqs, MarkExecutedBatch,
// CompactExecuted) fail with bufio.ErrTooLong forever — permanently
// disabling crash recovery and restart dedup until the file was deleted by
// hand.
func TestWAL_LargePayloadDoesNotPoisonReplay(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "cmd.wal")
	w, err := NewWAL(path)
	require.NoError(t, err)
	t.Cleanup(func() { _ = w.Close() })

	big := make([]byte, 100*1024) // 100KB payload — over the 64KB scanner default
	for i := range big {
		big[i] = 'a'
	}
	payload, err := json.Marshal(map[string]string{"data": string(big)})
	require.NoError(t, err)

	now := time.Now().UnixMicro()
	require.NoError(t, w.Append(Command{ExecuteAt: now + 500_000, Seq: 1, Action: "macro", Payload: payload}))
	// A command appended AFTER the big one must remain readable too.
	require.NoError(t, w.Append(Command{ExecuteAt: now + 500_000, Seq: 2, Action: "cut"}))

	cmds, err := w.Replay(DefaultMaxAge)
	require.NoError(t, err, "a large (but legal) payload must not poison WAL reads")
	require.Len(t, cmds, 2)
	require.Equal(t, uint64(1), cmds[0].Seq)
	require.Equal(t, uint64(2), cmds[1].Seq)

	// Executed marks and dedup seeding must keep working as well.
	require.NoError(t, w.MarkExecutedBatch([]uint64{1, 2}))
	seqs, err := w.ReplayExecutedSeqs()
	require.NoError(t, err)
	require.ElementsMatch(t, []uint64{1, 2}, seqs)
}

// ── Finding #7: WAL rewrite must be crash/failure-atomic ────────────────────

// TestWAL_CompactFailurePreservesEntries pins the failure-atomicity half of
// the crash-atomicity contract: a rewrite that fails partway through must
// leave the previously committed WAL state intact. The old implementation
// did Seek(0)+Truncate(0) on the live file BEFORE writing, so an I/O error
// (or power loss) mid-rewrite destroyed every pending entry and every
// executed tombstone — the exact state the WAL exists to protect.
func TestWAL_CompactFailurePreservesEntries(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "cmd.wal")
	osFile, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR|os.O_APPEND, 0o644)
	require.NoError(t, err)
	ff := &armedFailWriteFile{File: osFile}
	w := &WAL{path: path, file: ff}
	t.Cleanup(func() { _ = w.Close() })

	now := time.Now().UnixMicro()
	for i := uint64(1); i <= 3; i++ {
		require.NoError(t, w.Append(Command{ExecuteAt: now + 500_000, Seq: i, Action: "cut"}))
	}

	// The "disk" breaks; a compaction pass runs (error or not — the point is
	// what survives on disk).
	ff.armed.Store(true)
	_ = w.CompactExecuted(10)
	ff.armed.Store(false)

	cmds, err := w.Replay(DefaultMaxAge)
	require.NoError(t, err)
	require.Len(t, cmds, 3,
		"an interrupted rewrite must never lose previously committed WAL entries")
}

// TestWAL_AppendAfterCompactPersists verifies the append handle follows the
// atomically swapped file: entries appended after a compaction must land in
// the live WAL (not a renamed-away inode) and survive alongside the
// compacted state.
func TestWAL_AppendAfterCompactPersists(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "cmd.wal")
	w, err := NewWAL(path)
	require.NoError(t, err)
	t.Cleanup(func() { _ = w.Close() })

	now := time.Now().UnixMicro()
	require.NoError(t, w.Append(Command{ExecuteAt: 0, Seq: 1, Action: "cut"}))
	require.NoError(t, w.MarkExecuted(1))
	require.NoError(t, w.Append(Command{ExecuteAt: now + 500_000, Seq: 2, Action: "preview"}))

	require.NoError(t, w.CompactExecuted(10))

	require.NoError(t, w.Append(Command{ExecuteAt: now + 500_000, Seq: 3, Action: "ftb"}))

	cmds, err := w.Replay(DefaultMaxAge)
	require.NoError(t, err)
	gotSeqs := make([]uint64, 0, len(cmds))
	for _, c := range cmds {
		gotSeqs = append(gotSeqs, c.Seq)
	}
	require.ElementsMatch(t, []uint64{2, 3}, gotSeqs)

	seqs, err := w.ReplayExecutedSeqs()
	require.NoError(t, err)
	require.Contains(t, seqs, uint64(1))

	// And the on-disk file (by path) must reflect all of it after a "crash".
	w2, err := NewWAL(path)
	require.NoError(t, err)
	t.Cleanup(func() { _ = w2.Close() })
	cmds2, err := w2.Replay(DefaultMaxAge)
	require.NoError(t, err)
	require.Len(t, cmds2, 2)
}

// ── Finding #38: Drain must not pay O(file size) WAL I/O on the frame loop ──

// TestDrain_FrameLoopCostsSingleFsync pins the hot-path I/O budget: a Drain
// that executes commands (including the drain-to-empty case) must cost
// exactly ONE fsync on the calling (frame-loop) goroutine — the executed
// marks are appended as tombstones, and compaction happens off-thread.
// Before the fix, drain-to-empty paid two full-file read+rewrite passes and
// two fsyncs (MarkExecutedBatch rewrite + CompactExecuted rewrite) inline.
func TestDrain_FrameLoopCostsSingleFsync(t *testing.T) {
	t.Parallel()
	wal, counter := openCountingWAL(t)

	q := New(func() int64 { return 1_000_000 }, WithWAL(wal))
	for i := uint64(1); i <= 3; i++ {
		require.NoError(t, q.Enqueue(Command{ExecuteAt: 0, Seq: i, Action: "cut"}))
	}

	before := counter.syncs.Load()
	out := q.Drain() // drains to empty
	require.Len(t, out, 3)
	require.Equal(t, before+1, counter.syncs.Load(),
		"Drain must cost exactly one fsync on the frame loop — executed marks are appended, compaction is backgrounded")

	// The marks must still be durable for restart-side dedup.
	seqs, err := wal.ReplayExecutedSeqs()
	require.NoError(t, err)
	require.ElementsMatch(t, []uint64{1, 2, 3}, seqs)
}

// ── Finding #34: New must not silently swallow WAL replay errors ────────────

// syncBuffer is a goroutine-safe bytes buffer for capturing slog output
// (other parallel tests may log through the swapped default logger).
type syncBuffer struct {
	mu  sync.Mutex
	buf []byte
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.buf = append(b.buf, p...)
	return len(p), nil
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return string(b.buf)
}

// ── Guards for the new WAL mechanics introduced by the fixes ────────────────

// TestWAL_AppendRejectsOversizedEntry pins the write-side half of the #1
// fix: an entry that could not be read back (larger than the scanner's
// maxWALLineBytes budget) must be rejected at Append time instead of being
// written and poisoning the file.
func TestWAL_AppendRejectsOversizedEntry(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "cmd.wal")
	w, err := NewWAL(path)
	require.NoError(t, err)
	t.Cleanup(func() { _ = w.Close() })

	huge := make([]byte, maxWALLineBytes+2)
	huge[0] = '"'
	for i := 1; i < len(huge)-1; i++ {
		huge[i] = 'a'
	}
	huge[len(huge)-1] = '"'

	err = w.Append(Command{Seq: 1, Action: "macro", Payload: json.RawMessage(huge)})
	require.ErrorIs(t, err, ErrWALEntryTooLarge)

	// The reject must leave the file readable and empty.
	cmds, err := w.Replay(DefaultMaxAge)
	require.NoError(t, err)
	require.Empty(t, cmds)
}

// TestWAL_MaybeCompactExecuted_ThresholdGate verifies the growth gate: no
// compaction below walCompactMinAppends of new lines, a real (trimming)
// compaction once the gate is crossed.
func TestWAL_MaybeCompactExecuted_ThresholdGate(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "cmd.wal")
	w, err := NewWAL(path)
	require.NoError(t, err)
	t.Cleanup(func() { _ = w.Close() })

	for i := uint64(1); i <= 20; i++ {
		require.NoError(t, w.Append(Command{Seq: i, Action: "cut"}))
	}
	seqs := make([]uint64, 20)
	for i := range seqs {
		seqs[i] = uint64(i + 1)
	}
	require.NoError(t, w.MarkExecutedBatch(seqs))

	// Below the growth threshold: a no-op, nothing is trimmed.
	require.NoError(t, w.MaybeCompactExecuted(5))
	got, err := w.ReplayExecutedSeqs()
	require.NoError(t, err)
	require.Len(t, got, 20, "below the growth threshold MaybeCompactExecuted must not rewrite")
	w.mu.Lock()
	require.Zero(t, w.rewriteGen)
	w.mu.Unlock()

	// Cross the threshold: compaction must run and trim to keepRecent.
	w.mu.Lock()
	w.lines = w.linesAtRewrite + walCompactMinAppends
	w.mu.Unlock()
	require.NoError(t, w.MaybeCompactExecuted(5))

	got, err = w.ReplayExecutedSeqs()
	require.NoError(t, err)
	require.Len(t, got, 5, "past the growth threshold MaybeCompactExecuted must trim to keepRecent")
	require.ElementsMatch(t, []uint64{16, 17, 18, 19, 20}, got)
	w.mu.Lock()
	require.Equal(t, uint64(1), w.rewriteGen)
	w.mu.Unlock()
}

// TestDrain_TriggersBackgroundCompaction verifies the Drain wiring: once
// the WAL has grown past the threshold, a Drain that executes commands
// kicks off a compaction on a background goroutine (observable as a
// rewriteGen bump) without losing any executed tombstones.
func TestDrain_TriggersBackgroundCompaction(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "cmd.wal")
	w, err := NewWAL(path)
	require.NoError(t, err)
	t.Cleanup(func() { _ = w.Close() })

	q := New(func() int64 { return 1_000_000 }, WithWAL(w))

	// Pretend the file has grown past the compaction gate.
	w.mu.Lock()
	w.lines = w.linesAtRewrite + walCompactMinAppends
	w.mu.Unlock()

	require.NoError(t, q.Enqueue(Command{ExecuteAt: 0, Seq: 1, Action: "cut"}))
	out := q.Drain()
	require.Len(t, out, 1)

	require.Eventually(t, func() bool {
		w.mu.Lock()
		defer w.mu.Unlock()
		return w.rewriteGen > 0
	}, 2*time.Second, 5*time.Millisecond,
		"Drain must trigger WAL compaction on a background goroutine")

	seqs, err := w.ReplayExecutedSeqs()
	require.NoError(t, err)
	require.Contains(t, seqs, uint64(1),
		"background compaction must preserve executed tombstones")
}

// TestWAL_ConcurrentAppendsDuringCompactionCarriedOver hammers the
// MaybeCompactExecuted suffix carry-over: lines appended while the
// compaction snapshot is being built must survive the rename, and nothing
// committed may be lost or duplicated. Run under -race this also guards
// the unlocked phase of the compaction.
func TestWAL_ConcurrentAppendsDuringCompactionCarriedOver(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "cmd.wal")
	w, err := NewWAL(path)
	require.NoError(t, err)
	t.Cleanup(func() { _ = w.Close() })

	// Seed: three executed, two pending.
	for i := uint64(1); i <= 5; i++ {
		require.NoError(t, w.Append(Command{ExecuteAt: 0, Seq: i, Action: "cut"}))
	}
	require.NoError(t, w.MarkExecutedBatch([]uint64{1, 2, 3}))

	w.mu.Lock()
	w.lines = w.linesAtRewrite + walCompactMinAppends
	w.mu.Unlock()

	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		require.NoError(t, w.MaybeCompactExecuted(1000))
	}()
	go func() {
		defer wg.Done()
		for i := uint64(100); i < 150; i++ {
			require.NoError(t, w.Append(Command{ExecuteAt: 0, Seq: i, Action: "cut"}))
			require.NoError(t, w.MarkExecutedBatch([]uint64{i}))
		}
	}()
	wg.Wait()

	cmds, err := w.Replay(DefaultMaxAge)
	require.NoError(t, err)
	pending := make([]uint64, 0, len(cmds))
	for _, c := range cmds {
		pending = append(pending, c.Seq)
	}
	require.ElementsMatch(t, []uint64{4, 5}, pending,
		"pending entries must survive a concurrent compaction exactly once")

	executed, err := w.ReplayExecutedSeqs()
	require.NoError(t, err)
	executedSet := make(map[uint64]struct{}, len(executed))
	for _, s := range executed {
		executedSet[s] = struct{}{}
	}
	for _, want := range []uint64{1, 2, 3} {
		require.Contains(t, executedSet, want)
	}
	for i := uint64(100); i < 150; i++ {
		require.Contains(t, executedSet, i,
			"tombstones appended during compaction must be carried over")
	}
}

// TestQueue_CompactionRaceWithEnqueueDrain drives repeated forced
// background compactions concurrently with the Enqueue/Drain hot path and
// verifies no executed tombstone is ever lost across the file swaps.
func TestQueue_CompactionRaceWithEnqueueDrain(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "cmd.wal")
	w, err := NewWAL(path)
	require.NoError(t, err)
	t.Cleanup(func() { _ = w.Close() })

	q := New(func() int64 { return time.Now().UnixMicro() }, WithWAL(w))

	const total = 50
	drained := 0
	for i := uint64(1); i <= total; i++ {
		// Re-arm the compaction gate every iteration so Drain's background
		// compaction actually runs and races the appends.
		w.mu.Lock()
		if w.lines-w.linesAtRewrite < walCompactMinAppends {
			w.lines = w.linesAtRewrite + walCompactMinAppends
		}
		w.mu.Unlock()

		require.NoError(t, q.Enqueue(Command{ExecuteAt: 0, Seq: i, Action: "cut"}))
		drained += len(q.Drain())
	}
	require.Equal(t, total, drained)

	// Wait for any in-flight background compaction to settle.
	require.Eventually(t, func() bool { return !q.compacting.Load() },
		2*time.Second, 5*time.Millisecond)

	executed, err := w.ReplayExecutedSeqs()
	require.NoError(t, err)
	executedSet := make(map[uint64]struct{}, len(executed))
	for _, s := range executed {
		executedSet[s] = struct{}{}
	}
	for i := uint64(1); i <= total; i++ {
		require.Contains(t, executedSet, i,
			"no executed tombstone may be lost across background compactions")
	}
}
