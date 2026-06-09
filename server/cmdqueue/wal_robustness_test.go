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

// ── Finding: Seq-0 tombstones must not mark OTHER pending Seq-0 commands ────

// TestWAL_Seq0TombstoneMarksOnlyExecutedCommand reproduces the reviewed
// finding: Seq-0 commands are untimed/legacy and dedup-exempt, so several
// un-executed Seq-0 entries can coexist in the WAL. The old tombstone merge
// keyed on raw Seq, so executing ONE Seq-0 command flipped EVERY pending
// Seq-0 entry to executed — silently dropping the still-pending ones at
// crash replay. The fix correlates Seq-0 tombstones by full command content,
// so exactly the executed command is marked.
func TestWAL_Seq0TombstoneMarksOnlyExecutedCommand(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "cmd.wal")
	w, err := NewWAL(path)
	require.NoError(t, err)

	// Two pending Seq-0 commands; execute the SECOND-appended one so a
	// purely positional (oldest-first) merge would also fail this test —
	// Drain pops in heap (ExecuteAt) order, not WAL append order.
	require.NoError(t, w.Append(Command{ExecuteAt: 0, Seq: 0, Action: "cut"}))
	require.NoError(t, w.Append(Command{ExecuteAt: 0, Seq: 0, Action: "ftb"}))
	require.NoError(t, w.MarkExecutedCommands([]Command{{ExecuteAt: 0, Seq: 0, Action: "ftb"}}))

	cmds, err := w.Replay(DefaultMaxAge)
	require.NoError(t, err)
	require.Len(t, cmds, 1,
		"executing one Seq-0 command must not mark other pending Seq-0 commands executed")
	require.Equal(t, "cut", cmds[0].Action)

	// Crash: reopen the file by path and replay again.
	require.NoError(t, w.Close())
	w2, err := NewWAL(path)
	require.NoError(t, err)
	t.Cleanup(func() { _ = w2.Close() })
	cmds, err = w2.Replay(DefaultMaxAge)
	require.NoError(t, err)
	require.Len(t, cmds, 1)
	require.Equal(t, "cut", cmds[0].Action)

	// Seq-0 entries must never leak into dedup seeding.
	seqs, err := w2.ReplayExecutedSeqs()
	require.NoError(t, err)
	require.Empty(t, seqs)
}

// TestWAL_Seq0IdenticalDuplicates_ExecuteOneKeepsOne pins the
// content-correlation tie-break: content-identical Seq-0 commands are
// interchangeable, so one tombstone must consume exactly one of them
// (oldest-first), leaving the other pending for crash replay.
func TestWAL_Seq0IdenticalDuplicates_ExecuteOneKeepsOne(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "cmd.wal")
	w, err := NewWAL(path)
	require.NoError(t, err)
	t.Cleanup(func() { _ = w.Close() })

	payload := json.RawMessage(`{"bus":"pgm"}`)
	cmd := Command{ExecuteAt: 0, Seq: 0, Action: "cut", Payload: payload}
	require.NoError(t, w.Append(cmd))
	require.NoError(t, w.Append(cmd))
	require.NoError(t, w.MarkExecutedCommands([]Command{cmd}))

	cmds, err := w.Replay(DefaultMaxAge)
	require.NoError(t, err)
	require.Len(t, cmds, 1,
		"one tombstone must consume exactly one of two identical pending Seq-0 commands")
	require.Equal(t, "cut", cmds[0].Action)
	require.JSONEq(t, string(payload), string(cmds[0].Payload))
}

// TestWAL_MarkExecutedBatchSkipsSeq0 pins the write-side guard for callers
// that only hold seqs: a bare Seq-0 tombstone cannot be correlated with a
// specific pending entry, so MarkExecutedBatch / MarkExecuted must skip
// Seq 0 entirely rather than write an uncorrelatable tombstone.
func TestWAL_MarkExecutedBatchSkipsSeq0(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "cmd.wal")
	w, err := NewWAL(path)
	require.NoError(t, err)
	t.Cleanup(func() { _ = w.Close() })

	require.NoError(t, w.Append(Command{ExecuteAt: 0, Seq: 0, Action: "cut"}))
	require.NoError(t, w.Append(Command{ExecuteAt: 0, Seq: 0, Action: "ftb"}))
	require.NoError(t, w.Append(Command{ExecuteAt: 0, Seq: 7, Action: "preview"}))

	require.NoError(t, w.MarkExecuted(0))
	require.NoError(t, w.MarkExecutedBatch([]uint64{0, 7}))

	cmds, err := w.Replay(DefaultMaxAge)
	require.NoError(t, err)
	actions := make([]string, 0, len(cmds))
	for _, c := range cmds {
		actions = append(actions, c.Action)
	}
	require.ElementsMatch(t, []string{"cut", "ftb"}, actions,
		"Seq-0 marks must be skipped; the Seq-7 mark must still apply")

	seqs, err := w.ReplayExecutedSeqs()
	require.NoError(t, err)
	require.ElementsMatch(t, []uint64{7}, seqs)
}

// TestWAL_Seq0CompactionConsistent verifies the compaction half of the fix
// keeps holding: executed Seq-0 state (merged entries and unmatched
// tombstones) is dropped at compaction, pending Seq-0 entries survive, and
// a dropped tombstone can never resurface to mark a LATER pending Seq-0
// command executed.
func TestWAL_Seq0CompactionConsistent(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "cmd.wal")
	w, err := NewWAL(path)
	require.NoError(t, err)

	require.NoError(t, w.Append(Command{ExecuteAt: 0, Seq: 0, Action: "cut"}))
	require.NoError(t, w.Append(Command{ExecuteAt: 0, Seq: 0, Action: "ftb"}))
	require.NoError(t, w.Append(Command{ExecuteAt: 0, Seq: 5, Action: "preview"}))
	require.NoError(t, w.MarkExecutedCommands([]Command{
		{ExecuteAt: 0, Seq: 0, Action: "ftb"},
		{ExecuteAt: 0, Seq: 5, Action: "preview"},
	}))

	require.NoError(t, w.CompactExecuted(10))

	cmds, err := w.Replay(DefaultMaxAge)
	require.NoError(t, err)
	require.Len(t, cmds, 1, "compaction must keep the pending Seq-0 entry")
	require.Equal(t, "cut", cmds[0].Action)

	seqs, err := w.ReplayExecutedSeqs()
	require.NoError(t, err)
	require.ElementsMatch(t, []uint64{5}, seqs,
		"Seq-0 executed state must not leak into dedup tombstones")

	// A NEW pending command with the same content as the compacted-away
	// executed one must stay pending — the old tombstone is gone for good.
	require.NoError(t, w.Append(Command{ExecuteAt: 0, Seq: 0, Action: "ftb"}))
	cmds, err = w.Replay(DefaultMaxAge)
	require.NoError(t, err)
	actions := make([]string, 0, len(cmds))
	for _, c := range cmds {
		actions = append(actions, c.Action)
	}
	require.ElementsMatch(t, []string{"cut", "ftb"}, actions)

	// And all of it must hold across a crash-reopen.
	require.NoError(t, w.Close())
	w2, err := NewWAL(path)
	require.NoError(t, err)
	t.Cleanup(func() { _ = w2.Close() })
	cmds, err = w2.Replay(DefaultMaxAge)
	require.NoError(t, err)
	require.Len(t, cmds, 2)
}

// TestQueue_Seq0CrashReplay_OnlyUnexecutedReplays drives the full
// Enqueue→Drain→crash→New path: two pending Seq-0 commands, one drains
// (and it is NOT the oldest WAL entry — Drain pops in ExecuteAt order),
// then a restart must replay exactly the un-executed one. Before the fix,
// Drain's bare Seq-0 tombstone marked both entries executed and the
// pending command was silently lost.
func TestQueue_Seq0CrashReplay_OnlyUnexecutedReplays(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "cmd.wal")
	w, err := NewWAL(path)
	require.NoError(t, err)

	now := time.Now().UnixMicro()
	var clk atomic.Int64
	clk.Store(now)
	clock := func() int64 { return clk.Load() }

	q := New(clock, WithWAL(w))
	// Append order is [future, due]: the command that drains first is the
	// SECOND WAL entry, so positional (append-order) tombstone matching
	// would mark the wrong one.
	future := Command{ExecuteAt: now + 60_000_000, Seq: 0, Action: "ftb"}
	due := Command{ExecuteAt: now, Seq: 0, Action: "cut"}
	require.NoError(t, q.Enqueue(future))
	require.NoError(t, q.Enqueue(due))

	out := q.Drain()
	require.Len(t, out, 1)
	require.Equal(t, "cut", out[0].Action)

	// Crash: drop the queue, reopen the WAL by path, rebuild the queue.
	require.NoError(t, w.Close())
	w2, err := NewWAL(path)
	require.NoError(t, err)
	t.Cleanup(func() { _ = w2.Close() })

	q2 := New(clock, WithWAL(w2))
	require.Equal(t, 1, q2.Pending(),
		"exactly the un-executed Seq-0 command must replay after a crash")

	clk.Store(now + 60_000_000)
	out = q2.Drain()
	require.Len(t, out, 1)
	require.Equal(t, "ftb", out[0].Action,
		"the executed Seq-0 command must not re-execute; the pending one must")
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
