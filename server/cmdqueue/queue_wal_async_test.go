package cmdqueue

import (
	"log/slog"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// blockingSyncFile wraps an *os.File so a test can park exactly one Sync()
// call (the WAL append fsync) until released, modelling a slow disk.
type blockingSyncFile struct {
	*os.File
	armed   atomic.Bool
	started chan struct{}
	release chan struct{}
}

func (b *blockingSyncFile) Sync() error {
	if b.armed.Swap(false) {
		close(b.started)
		<-b.release
	}
	return b.File.Sync()
}

func newBlockingWAL(t *testing.T) (*WAL, *blockingSyncFile) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "cmd.wal")
	osFile, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR|os.O_APPEND, 0o644)
	require.NoError(t, err)
	bf := &blockingSyncFile{
		File:    osFile,
		started: make(chan struct{}),
		release: make(chan struct{}),
	}
	wal := &WAL{path: path, file: bf}
	t.Cleanup(func() { _ = wal.Close() })
	return wal, bf
}

// ── Finding #37: Enqueue must not hold q.mu across the WAL fsync ────────────

// TestEnqueue_WALFsyncDoesNotBlockDrain reproduces the reviewed finding:
// Enqueue used to hold the queue mutex across wal.Append (a file write plus
// fsync, commonly 1-20ms), so any HTTP-driven Enqueue stalled the video
// frame loop's Drain for the duration of the fsync. With the fix, the
// append runs outside q.mu (guarded by an in-flight Seq reservation), so
// Drain and Pending proceed while the fsync is parked.
func TestEnqueue_WALFsyncDoesNotBlockDrain(t *testing.T) {
	t.Parallel()
	wal, bf := newBlockingWAL(t)
	q := New(func() int64 { return 1_000_000 }, WithWAL(wal))

	bf.armed.Store(true)
	enqDone := make(chan error, 1)
	go func() { enqDone <- q.Enqueue(Command{ExecuteAt: 0, Seq: 1, Action: "cut"}) }()
	<-bf.started // Enqueue is now parked inside the WAL fsync

	drainDone := make(chan []Command, 1)
	go func() { drainDone <- q.Drain() }()
	select {
	case out := <-drainDone:
		// The in-flight command is not committed yet, so nothing drains —
		// the point is that Drain RETURNED while the fsync was parked.
		require.Empty(t, out)
	case <-time.After(2 * time.Second):
		t.Error("Drain blocked behind an Enqueue WAL fsync — queue mutex held across fsync")
	}

	close(bf.release)
	require.NoError(t, <-enqDone)

	out := q.Drain()
	require.Len(t, out, 1, "command must drain once its WAL append completes")
	require.Equal(t, uint64(1), out[0].Seq)
}

// TestEnqueue_ConcurrentSameSeqDuringFsync guards the dedup atomicity of the
// off-lock append: a retransmit of the SAME Seq arriving while the first
// copy's WAL append is still in flight must wait for the in-flight
// reservation to resolve — exactly one copy is accepted, the other is
// rejected as a duplicate, and the command executes once.
func TestEnqueue_ConcurrentSameSeqDuringFsync(t *testing.T) {
	t.Parallel()
	wal, bf := newBlockingWAL(t)
	q := New(func() int64 { return 1_000_000 }, WithWAL(wal))

	cmd := Command{ExecuteAt: 0, Seq: 42, Action: "cut"}

	bf.armed.Store(true)
	res := make(chan error, 2)
	go func() { res <- q.Enqueue(cmd) }()
	<-bf.started // first copy parked in fsync
	go func() { res <- q.Enqueue(cmd) }()
	time.Sleep(50 * time.Millisecond) // let the retransmit reach the dedup gate
	close(bf.release)

	err1, err2 := <-res, <-res
	var nils, dups int
	for _, err := range []error{err1, err2} {
		switch {
		case err == nil:
			nils++
		case err == ErrDuplicateCommand:
			dups++
		default:
			t.Fatalf("unexpected error: %v", err)
		}
	}
	require.Equal(t, 1, nils, "exactly one copy must be accepted")
	require.Equal(t, 1, dups, "the retransmit must be rejected as a duplicate")

	out := q.Drain()
	require.Len(t, out, 1, "the command must execute exactly once")
}

// ── Finding #34: New must log WAL replay failures ───────────────────────────

// TestNew_LogsWALReplayFailure verifies that an unreadable WAL at startup is
// no longer silent: both the executed-seq (dedup) replay failure and the
// pending-command replay failure must be logged so operators learn that
// crash recovery and restart dedup are disabled. Not parallel — it swaps
// the process-default slog logger.
func TestNew_LogsWALReplayFailure(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cmd.wal")
	w, err := NewWAL(path)
	require.NoError(t, err)
	t.Cleanup(func() { _ = w.Close() })
	// Make every WAL read fail at startup (reads open by path).
	require.NoError(t, os.Remove(path))

	var buf syncBuffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, nil)))
	t.Cleanup(func() { slog.SetDefault(prev) })

	_ = New(func() int64 { return 1_000_000 }, WithWAL(w))

	logged := buf.String()
	require.Contains(t, logged, "executed-seq replay failed",
		"dedup-seed failure must be logged — restart dedup is silently disabled otherwise")
	require.Contains(t, logged, "WAL replay failed",
		"pending-command replay failure must be logged — crash recovery is silently disabled otherwise")
}
