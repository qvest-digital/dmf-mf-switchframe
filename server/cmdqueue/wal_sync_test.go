package cmdqueue

import (
	"errors"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// syncCountingFile wraps an *os.File so tests can observe whether the WAL
// actually issues Sync() on the paths that claim durability.  All other
// operations pass straight through to the underlying file, so on-disk
// state is identical to production.
type syncCountingFile struct {
	*os.File
	syncs atomic.Int64
}

func (s *syncCountingFile) Sync() error {
	s.syncs.Add(1)
	return s.File.Sync()
}

// Compile-time guard: if walFile gains new methods, *os.File's embedded
// promotion would silently shadow them through syncCountingFile and hide
// the new durability surface.  Force a type-check failure instead.
var _ walFile = (*syncCountingFile)(nil)

// openCountingWAL returns a WAL with an injected sync-counting file wrapper
// and a handle to read the counter.  Uses the package-internal constructor
// so we can observe Sync() calls from inside the cmdqueue package.
func openCountingWAL(t *testing.T) (*WAL, *syncCountingFile) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "cmd.wal")
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR|os.O_APPEND, 0o644)
	require.NoError(t, err)
	counter := &syncCountingFile{File: f}
	wal := &WAL{path: path, file: counter}
	t.Cleanup(func() { _ = wal.Close() })
	return wal, counter
}

// TestWAL_AppendCallsSync pins the existing Append durability contract —
// a regression guard in case someone ever removes the Sync there.
func TestWAL_AppendCallsSync(t *testing.T) {
	t.Parallel()
	wal, counter := openCountingWAL(t)

	before := counter.syncs.Load()
	require.NoError(t, wal.Append(Command{Seq: 1, Action: "cut"}))
	require.Greater(t, counter.syncs.Load(), before,
		"Append must fsync the WAL before returning")
}

// TestWAL_MarkExecutedCallsSync verifies that MarkExecuted (via
// rewriteLocked) fsyncs before returning — existing durability guarantee
// per the Append contract; pin it so a future refactor of rewriteLocked
// can't silently drop the Sync.
func TestWAL_MarkExecutedCallsSync(t *testing.T) {
	t.Parallel()
	wal, counter := openCountingWAL(t)

	require.NoError(t, wal.Append(Command{Seq: 1, Action: "cut"}))
	before := counter.syncs.Load()
	require.NoError(t, wal.MarkExecuted(1))
	require.Greater(t, counter.syncs.Load(), before,
		"MarkExecuted must fsync the rewrite before returning")
}

// TestWAL_TruncateCallsSync gates crash-recovery durability.
// Prior to the fix, Truncate() called file.Truncate(0) without fsync —
// a crash between Drain's last MarkExecuted and the filesystem's lazy
// flush would resurrect already-dispatched commands at replay time.
func TestWAL_TruncateCallsSync(t *testing.T) {
	t.Parallel()
	wal, counter := openCountingWAL(t)

	require.NoError(t, wal.Append(Command{Seq: 1, Action: "cut"}))
	require.NoError(t, wal.MarkExecuted(1))
	before := counter.syncs.Load()
	require.NoError(t, wal.Truncate())
	require.Greater(t, counter.syncs.Load(), before,
		"Truncate must fsync the cleared file before returning — "+
			"otherwise a crash replays already-drained commands")
}

// TestWAL_MarkExecutedPostConditionHoldsAcrossReopen pins the reopen
// semantic contract: after MarkExecuted, a fresh WAL on the same path
// surfaces the command as executed.  This does NOT verify crash durability
// (the VFS page cache serves the reopen within a single host either way;
// real crash durability is enforced by TestWAL_MarkExecutedCallsSync).
// Kept as a documentation-grade regression guard against API drift in
// ReplayExecutedSeqs / Replay.
func TestWAL_MarkExecutedPostConditionHoldsAcrossReopen(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "cmd.wal")

	w, err := NewWAL(path)
	require.NoError(t, err)

	now := time.Now().UnixMicro()
	cmd := Command{ExecuteAt: now + 500_000, Seq: 77, Action: "cut"}
	require.NoError(t, w.Append(cmd))
	require.NoError(t, w.MarkExecuted(77))

	// Don't Close — the AC models a crash where the process vanishes
	// without an orderly shutdown.  A fresh NewWAL on the same path must
	// still see the executed state (because MarkExecuted synced).
	w2, err := NewWAL(path)
	require.NoError(t, err)
	t.Cleanup(func() {
		_ = w.Close()
		_ = w2.Close()
	})

	seqs, err := w2.ReplayExecutedSeqs()
	require.NoError(t, err)
	require.Contains(t, seqs, uint64(77),
		"crashed+reopened WAL must surface seq 77 as executed")

	cmds, err := w2.Replay(DefaultMaxAge)
	require.NoError(t, err)
	for _, c := range cmds {
		require.NotEqual(t, uint64(77), c.Seq,
			"crashed+reopened WAL must not resurrect executed seq 77 as pending")
	}
}

// TestWAL_TruncatePostConditionEmpty is the Truncate analogue: after
// Truncate, a fresh WAL on the same path must report zero pending
// commands.  Same page-cache caveat as above: this is a post-condition
// pin, not a crash-durability proof.
func TestWAL_TruncatePostConditionEmpty(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "cmd.wal")

	w, err := NewWAL(path)
	require.NoError(t, err)

	now := time.Now().UnixMicro()
	for i := uint64(1); i <= 4; i++ {
		require.NoError(t, w.Append(Command{
			ExecuteAt: now + int64(i)*100_000, Seq: i, Action: "cut",
		}))
	}
	for i := uint64(1); i <= 4; i++ {
		require.NoError(t, w.MarkExecuted(i))
	}
	require.NoError(t, w.Truncate())

	w2, err := NewWAL(path)
	require.NoError(t, err)
	t.Cleanup(func() {
		_ = w.Close()
		_ = w2.Close()
	})

	cmds, err := w2.Replay(DefaultMaxAge)
	require.NoError(t, err)
	require.Empty(t, cmds, "crashed+reopened WAL must be empty after Truncate")
}

// TestQueue_DrainThenRestart_NoDuplicateDispatch — AC-named end-to-end
// check.  Drive one command through a real Queue+WAL, drain it (which
// internally MarkExecuted's and Truncate's the WAL), then construct a
// fresh Queue on the same WAL path and re-submit the same {Seq, Action}.
// The dedup map seeded from ReplayExecutedSeqs must reject it.
func TestQueue_DrainThenRestart_NoDuplicateDispatch(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "cmd.wal")

	w1, err := NewWAL(path)
	require.NoError(t, err)
	clock := func() int64 { return time.Now().UnixMicro() }
	q1 := New(clock, WithWAL(w1))
	cmd := Command{ExecuteAt: 0, Seq: 99, Action: "cut"}
	require.NoError(t, q1.Enqueue(cmd))
	drained := q1.Drain()
	require.Len(t, drained, 1)
	require.Equal(t, uint64(99), drained[0].Seq)
	require.NoError(t, w1.Close())

	// Fresh process would open a new WAL on the same path.
	w2, err := NewWAL(path)
	require.NoError(t, err)
	t.Cleanup(func() { _ = w2.Close() })
	q2 := New(clock, WithWAL(w2))

	err = q2.Enqueue(cmd)
	require.ErrorIs(t, err, ErrDuplicateCommand,
		"post-crash restart must dedup already-drained seq 99")
}

// TestDrain_PreservesExecutedSeqsOnDisk is the structural guard against
// someone "cleaning up" Drain and reintroducing a full Truncate on the
// drain-to-empty branch.  After drain-to-empty, the on-disk WAL must
// still carry the executed seq as a tombstone so a restart's
// ReplayExecutedSeqs seeds the dedup map.  If a future contributor deletes
// CompactExecuted or reinstates a Truncate, this test fires.
func TestDrain_PreservesExecutedSeqsOnDisk(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "cmd.wal")

	w, err := NewWAL(path)
	require.NoError(t, err)
	t.Cleanup(func() { _ = w.Close() })
	clock := func() int64 { return time.Now().UnixMicro() }
	q := New(clock, WithWAL(w))

	require.NoError(t, q.Enqueue(Command{ExecuteAt: 0, Seq: 55, Action: "cut"}))
	drained := q.Drain()
	require.Len(t, drained, 1)

	seqs, err := w.ReplayExecutedSeqs()
	require.NoError(t, err)
	require.Contains(t, seqs, uint64(55),
		"Drain-to-empty must leave the executed seq on disk for restart-side dedup")

	fi, err := os.Stat(path)
	require.NoError(t, err)
	require.Greater(t, fi.Size(), int64(0),
		"WAL file must not be empty after Drain — executed tombstones required for dedup")
}

// TestDrain_CompactExecutedBoundsFileSize exercises the bounded-growth
// guarantee: after Drain's compaction runs with a keepRecent threshold,
// the WAL holds at most keepRecent executed tombstones.
func TestDrain_CompactExecutedBoundsFileSize(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "cmd.wal")

	w, err := NewWAL(path)
	require.NoError(t, err)
	t.Cleanup(func() { _ = w.Close() })

	// Write more than maxSeen (100_000) entries would be slow; instead
	// exercise CompactExecuted directly with a small keep-recent so the
	// test is fast.
	for i := uint64(1); i <= 100; i++ {
		require.NoError(t, w.Append(Command{Seq: i, Action: "cut"}))
		require.NoError(t, w.MarkExecuted(i))
	}
	require.NoError(t, w.CompactExecuted(10))

	seqs, err := w.ReplayExecutedSeqs()
	require.NoError(t, err)
	require.Len(t, seqs, 10, "CompactExecuted must trim to keepRecent")
	require.Equal(t, uint64(91), seqs[0], "must keep the most-recent tombstones")
	require.Equal(t, uint64(100), seqs[len(seqs)-1])
}

// TestWAL_MarkExecutedBatch_SingleFsync pins the batching contract:
// MarkExecutedBatch must fsync exactly once no matter how many seqs it
// marks. Before batching, the Drain hot path called single-seq
// MarkExecuted per drained command, costing one full rewrite + one fsync
// each — O(commands) fsyncs per tick. This guards against a regression
// back to per-command fsync.
func TestWAL_MarkExecutedBatch_SingleFsync(t *testing.T) {
	t.Parallel()
	wal, counter := openCountingWAL(t)

	const n = 8
	seqs := make([]uint64, 0, n)
	for i := uint64(1); i <= n; i++ {
		require.NoError(t, wal.Append(Command{Seq: i, Action: "cut"}))
		seqs = append(seqs, i)
	}

	before := counter.syncs.Load()
	require.NoError(t, wal.MarkExecutedBatch(seqs))
	require.Equal(t, before+1, counter.syncs.Load(),
		"MarkExecutedBatch must fsync exactly once regardless of seq count")

	// All marked seqs must be surfaced as executed for restart-side dedup.
	executed, err := wal.ReplayExecutedSeqs()
	require.NoError(t, err)
	require.ElementsMatch(t, seqs, executed,
		"every batched seq must be persisted as an executed tombstone")
}

// TestWAL_MarkExecutedBatch_EmptyIsNoop verifies an empty batch performs
// no I/O — the Drain hot path skips the call when nothing was drained, but
// pin the no-op contract so callers can rely on it.
func TestWAL_MarkExecutedBatch_EmptyIsNoop(t *testing.T) {
	t.Parallel()
	wal, counter := openCountingWAL(t)
	require.NoError(t, wal.Append(Command{Seq: 1, Action: "cut"}))

	before := counter.syncs.Load()
	require.NoError(t, wal.MarkExecutedBatch(nil))
	require.Equal(t, before, counter.syncs.Load(),
		"empty MarkExecutedBatch must not fsync")
}

// TestDrain_BatchesWALFsyncsAcrossDrainedCommands is the hot-path
// regression guard for the reviewed finding: Drain marking N drained
// commands must not issue N fsyncs. With a non-empty residual heap (no
// drain-to-empty compaction), draining several commands in one tick must
// cost exactly one fsync from the batched MarkExecuted. Before the fix
// this was one fsync per drained command, blowing the 30fps frame budget
// under disk contention.
func TestDrain_BatchesWALFsyncsAcrossDrainedCommands(t *testing.T) {
	t.Parallel()
	wal, counter := openCountingWAL(t)

	clock := func() int64 { return 1_000_000 }
	q := New(clock, WithWAL(wal))

	// Enqueue several immediately-drainable commands plus one far-future
	// command so the heap is NOT empty after Drain (suppresses the
	// drain-to-empty CompactExecuted, isolating the MarkExecuted fsync).
	const drainable = 6
	for i := uint64(1); i <= drainable; i++ {
		require.NoError(t, q.Enqueue(Command{ExecuteAt: 0, Seq: i, Action: "cut"}))
	}
	require.NoError(t, q.Enqueue(Command{ExecuteAt: 9_000_000, Seq: 100, Action: "cut"}))

	before := counter.syncs.Load()
	out := q.Drain()
	require.Len(t, out, drainable, "all immediate commands must drain in one call")
	require.Equal(t, before+1, counter.syncs.Load(),
		"Drain must batch all drained-command WAL marks into a single fsync")

	// Correctness: the drained seqs are persisted as executed tombstones so
	// a restart dedups retransmits.
	executed, err := wal.ReplayExecutedSeqs()
	require.NoError(t, err)
	for i := uint64(1); i <= drainable; i++ {
		require.Contains(t, executed, i,
			"drained seq must be marked executed on disk")
	}
}

// TestDrain_ConcurrentEnqueueDuringWALMark is a race-detector guard for the
// reviewed fix: Drain now performs its WAL marking/compaction AFTER releasing
// q.mu, so Enqueue's wal.Append can interleave with Drain's MarkExecutedBatch
// and CompactExecuted. WAL operations are serialized only by the WAL's own
// mutex. This test hammers Enqueue concurrently with repeated Drain calls and
// must run clean under -race, and must never lose or double-dispatch a seq.
func TestDrain_ConcurrentEnqueueDuringWALMark(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "cmd.wal")
	wal, err := NewWAL(path)
	require.NoError(t, err)
	t.Cleanup(func() { _ = wal.Close() })

	clock := func() int64 { return time.Now().UnixMicro() }
	q := New(clock, WithWAL(wal))

	const total = 200
	enqueued := make(chan struct{})
	go func() {
		defer close(enqueued)
		for i := uint64(1); i <= total; i++ {
			// ExecuteAt=0 → immediately drainable, exercising the drain path.
			_ = q.Enqueue(Command{ExecuteAt: 0, Seq: i, Action: "cut"})
		}
	}()

	drained := make(map[uint64]struct{}, total)
	collect := func(cmds []Command) {
		for _, c := range cmds {
			_, dup := drained[c.Seq]
			require.False(t, dup, "seq %d drained twice", c.Seq)
			drained[c.Seq] = struct{}{}
		}
	}

	for {
		collect(q.Drain())
		select {
		case <-enqueued:
			// Producer done; flush any remaining queued commands.
			for q.Pending() > 0 {
				collect(q.Drain())
			}
			collect(q.Drain())
			require.Len(t, drained, total,
				"every enqueued command must drain exactly once")
			return
		default:
		}
	}
}

// failingAppendFile wraps an *os.File and fails the first Write (simulating a
// transient disk error: disk full / EIO / fsync failure) so a test can drive
// Enqueue's wal.Append into the error branch. Subsequent Writes pass through to
// the real file, modelling the disk recovering before a retransmit.
type failingAppendFile struct {
	*os.File
	failNext atomic.Bool
}

func (f *failingAppendFile) Write(p []byte) (int, error) {
	if f.failNext.Swap(false) {
		return 0, errors.New("simulated WAL write failure")
	}
	return f.File.Write(p)
}

// TestEnqueue_WALAppendFailure_DoesNotMarkSeen verifies that when wal.Append
// fails, the command's Seq is NOT recorded in the dedup map. Otherwise the
// command would be neither persisted nor executed, yet its retransmit (the
// normal recovery after a 5xx/timeout) would be rejected as a duplicate —
// silently swallowing a cut/transition on a live switcher.
func TestEnqueue_WALAppendFailure_DoesNotMarkSeen(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "cmd.wal")
	osFile, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR|os.O_APPEND, 0o644)
	require.NoError(t, err)
	ff := &failingAppendFile{File: osFile}
	wal := &WAL{path: path, file: ff}
	t.Cleanup(func() { _ = wal.Close() })

	q := New(func() int64 { return 1000 }, WithWAL(wal))

	cmd := Command{ExecuteAt: 0, Seq: 7, Action: "cut"}

	// First attempt: the disk is transiently broken, so Append fails and the
	// error propagates to the caller.
	ff.failNext.Store(true)
	require.Error(t, q.Enqueue(cmd), "Enqueue must surface the WAL append error")

	// The failed command must not have landed on the heap.
	require.Equal(t, 0, q.Pending(), "failed-WAL command must not be queued")

	// Retransmit of the SAME Seq after the disk recovers must be ACCEPTED,
	// not rejected as a duplicate — the Seq must not have been marked seen.
	err = q.Enqueue(cmd)
	require.NoError(t, err, "retransmit after a failed enqueue must be accepted")
	require.False(t, errors.Is(err, ErrDuplicateCommand),
		"retransmit must not be rejected as a duplicate")

	// And it must now be persisted and queued for execution.
	require.Equal(t, 1, q.Pending(), "retransmitted command must be queued")
	drained := q.Drain()
	require.Len(t, drained, 1)
	require.Equal(t, uint64(7), drained[0].Seq)
}

// TestEnqueue_WALAppendFailure_PrunedHighWaterUnchanged verifies the failed
// append does not advance prunedHighWater either: a same-or-lower Seq must not
// be wrongly rejected after a WAL write failure.
func TestEnqueue_WALAppendFailure_PrunedHighWaterUnchanged(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "cmd.wal")
	osFile, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR|os.O_APPEND, 0o644)
	require.NoError(t, err)
	ff := &failingAppendFile{File: osFile}
	wal := &WAL{path: path, file: ff}
	t.Cleanup(func() { _ = wal.Close() })

	q := New(func() int64 { return 1000 }, WithWAL(wal))

	require.Zero(t, q.prunedHighWater)

	ff.failNext.Store(true)
	require.Error(t, q.Enqueue(Command{Seq: 5, Action: "cut"}))

	require.Zero(t, q.prunedHighWater,
		"a failed WAL append must not advance prunedHighWater")
}

// BenchmarkWAL_Truncate measures the fsync cost of the cleared-WAL path
// so future operators can confirm Drain doesn't stall the 16.67ms frame
// budget when the WAL is on the local SSD.  The AC target is <2ms p99.
func BenchmarkWAL_Truncate(b *testing.B) {
	path := filepath.Join(b.TempDir(), "cmd.wal")
	w, err := NewWAL(path)
	if err != nil {
		b.Fatalf("NewWAL: %v", err)
	}
	defer func() { _ = w.Close() }()
	if err := w.Append(Command{Seq: 1, Action: "cut"}); err != nil {
		b.Fatalf("Append: %v", err)
	}
	if err := w.MarkExecuted(1); err != nil {
		b.Fatalf("MarkExecuted: %v", err)
	}

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		if err := w.Truncate(); err != nil {
			b.Fatalf("Truncate: %v", err)
		}
	}
}
