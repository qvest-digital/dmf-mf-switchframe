package cmdqueue_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/zsiec/switchframe/server/cmdqueue"
)

func tempWALPath(t *testing.T) string {
	t.Helper()
	return filepath.Join(t.TempDir(), "cmd.wal")
}

// TestWAL_AppendAndReplay verifies that commands appended to the WAL
// are returned by Replay when they are not executed and not stale.
func TestWAL_AppendAndReplay(t *testing.T) {
	path := tempWALPath(t)
	w, err := cmdqueue.NewWAL(path)
	if err != nil {
		t.Fatalf("NewWAL: %v", err)
	}
	defer func() { _ = w.Close() }()

	now := time.Now().UnixMicro()

	cmd1 := cmdqueue.Command{
		ExecuteAt: now + 500_000, // 500ms in the future
		Seq:       1,
		Action:    "cut",
		Payload:   json.RawMessage(`{"src":"cam1"}`),
	}
	cmd2 := cmdqueue.Command{
		ExecuteAt: now + 1_000_000, // 1s in the future
		Seq:       2,
		Action:    "preview",
		Payload:   json.RawMessage(`{"src":"cam2"}`),
	}

	if err := w.Append(cmd1); err != nil {
		t.Fatalf("Append cmd1: %v", err)
	}
	if err := w.Append(cmd2); err != nil {
		t.Fatalf("Append cmd2: %v", err)
	}

	cmds, err := w.Replay(cmdqueue.DefaultMaxAge)
	if err != nil {
		t.Fatalf("Replay: %v", err)
	}
	if len(cmds) != 2 {
		t.Fatalf("expected 2 commands from Replay, got %d", len(cmds))
	}
	if cmds[0].Seq != 1 || cmds[1].Seq != 2 {
		t.Errorf("unexpected seqs: %d, %d", cmds[0].Seq, cmds[1].Seq)
	}
}

// TestWAL_MarkExecuted verifies that marking a command as executed causes
// Replay to skip it.
func TestWAL_MarkExecuted(t *testing.T) {
	path := tempWALPath(t)
	w, err := cmdqueue.NewWAL(path)
	if err != nil {
		t.Fatalf("NewWAL: %v", err)
	}
	defer func() { _ = w.Close() }()

	now := time.Now().UnixMicro()

	cmd1 := cmdqueue.Command{ExecuteAt: now + 500_000, Seq: 1, Action: "cut"}
	cmd2 := cmdqueue.Command{ExecuteAt: now + 500_000, Seq: 2, Action: "preview"}

	if err := w.Append(cmd1); err != nil {
		t.Fatalf("Append cmd1: %v", err)
	}
	if err := w.Append(cmd2); err != nil {
		t.Fatalf("Append cmd2: %v", err)
	}

	// Mark cmd1 as executed.
	if err := w.MarkExecuted(1); err != nil {
		t.Fatalf("MarkExecuted: %v", err)
	}

	cmds, err := w.Replay(cmdqueue.DefaultMaxAge)
	if err != nil {
		t.Fatalf("Replay: %v", err)
	}
	if len(cmds) != 1 {
		t.Fatalf("expected 1 command after MarkExecuted, got %d", len(cmds))
	}
	if cmds[0].Seq != 2 {
		t.Errorf("expected remaining command seq=2, got seq=%d", cmds[0].Seq)
	}
}

// TestWAL_StaleFiltering verifies that Replay filters out stale commands
// whose ExecuteAt is older than maxAge.
func TestWAL_StaleFiltering(t *testing.T) {
	path := tempWALPath(t)
	w, err := cmdqueue.NewWAL(path)
	if err != nil {
		t.Fatalf("NewWAL: %v", err)
	}
	defer func() { _ = w.Close() }()

	now := time.Now().UnixMicro()

	// Fresh command (500ms in the future).
	fresh := cmdqueue.Command{ExecuteAt: now + 500_000, Seq: 1, Action: "cut"}
	// Stale command (3s in the past, beyond default 2s maxAge).
	stale := cmdqueue.Command{ExecuteAt: now - 3_000_000, Seq: 2, Action: "preview"}
	// Immediate command (ExecuteAt=0, should always be replayed).
	immediate := cmdqueue.Command{ExecuteAt: 0, Seq: 3, Action: "ftb"}

	for _, cmd := range []cmdqueue.Command{fresh, stale, immediate} {
		if err := w.Append(cmd); err != nil {
			t.Fatalf("Append seq=%d: %v", cmd.Seq, err)
		}
	}

	cmds, err := w.Replay(cmdqueue.DefaultMaxAge)
	if err != nil {
		t.Fatalf("Replay: %v", err)
	}
	if len(cmds) != 2 {
		t.Fatalf("expected 2 commands (fresh + immediate), got %d", len(cmds))
	}
	if cmds[0].Seq != 1 || cmds[1].Seq != 3 {
		t.Errorf("unexpected seqs: %d, %d (expected 1, 3)", cmds[0].Seq, cmds[1].Seq)
	}
}

// TestWAL_Truncate verifies that Truncate clears the WAL file so Replay
// returns nothing.
func TestWAL_Truncate(t *testing.T) {
	path := tempWALPath(t)
	w, err := cmdqueue.NewWAL(path)
	if err != nil {
		t.Fatalf("NewWAL: %v", err)
	}
	defer func() { _ = w.Close() }()

	now := time.Now().UnixMicro()
	cmd := cmdqueue.Command{ExecuteAt: now + 500_000, Seq: 1, Action: "cut"}
	if err := w.Append(cmd); err != nil {
		t.Fatalf("Append: %v", err)
	}

	if err := w.Truncate(); err != nil {
		t.Fatalf("Truncate: %v", err)
	}

	cmds, err := w.Replay(cmdqueue.DefaultMaxAge)
	if err != nil {
		t.Fatalf("Replay after Truncate: %v", err)
	}
	if len(cmds) != 0 {
		t.Fatalf("expected 0 commands after Truncate, got %d", len(cmds))
	}
}

// TestWAL_PersistAcrossReopen verifies that WAL entries survive closing
// and reopening the WAL file.
func TestWAL_PersistAcrossReopen(t *testing.T) {
	path := tempWALPath(t)
	w, err := cmdqueue.NewWAL(path)
	if err != nil {
		t.Fatalf("NewWAL: %v", err)
	}

	now := time.Now().UnixMicro()
	cmd := cmdqueue.Command{ExecuteAt: now + 500_000, Seq: 42, Action: "cut"}
	if err := w.Append(cmd); err != nil {
		t.Fatalf("Append: %v", err)
	}
	_ = w.Close()

	// Reopen.
	w2, err := cmdqueue.NewWAL(path)
	if err != nil {
		t.Fatalf("NewWAL reopen: %v", err)
	}
	defer func() { _ = w2.Close() }()

	cmds, err := w2.Replay(cmdqueue.DefaultMaxAge)
	if err != nil {
		t.Fatalf("Replay after reopen: %v", err)
	}
	if len(cmds) != 1 {
		t.Fatalf("expected 1 command after reopen, got %d", len(cmds))
	}
	if cmds[0].Seq != 42 {
		t.Errorf("expected seq=42, got seq=%d", cmds[0].Seq)
	}
}

// TestQueue_WithWAL_IntegrationTest verifies that Queue with a WAL replays
// un-executed commands on construction.
func TestQueue_WithWAL_IntegrationTest(t *testing.T) {
	path := tempWALPath(t)
	now := time.Now().UnixMicro()

	// Phase 1: Create a WAL with an un-executed command.
	w1, err := cmdqueue.NewWAL(path)
	if err != nil {
		t.Fatalf("NewWAL: %v", err)
	}
	cmd := cmdqueue.Command{ExecuteAt: 0, Seq: 10, Action: "cut"}
	if err := w1.Append(cmd); err != nil {
		t.Fatalf("Append: %v", err)
	}
	_ = w1.Close()

	// Phase 2: Reopen WAL and create Queue — the command should be replayed.
	w2, err := cmdqueue.NewWAL(path)
	if err != nil {
		t.Fatalf("NewWAL reopen: %v", err)
	}
	defer func() { _ = w2.Close() }()

	q := cmdqueue.New(func() int64 { return now }, cmdqueue.WithWAL(w2))

	// The replayed command should be drainable immediately (ExecuteAt=0).
	drained := q.Drain()
	if len(drained) != 1 {
		t.Fatalf("expected 1 replayed command, got %d", len(drained))
	}
	if drained[0].Seq != 10 {
		t.Errorf("expected seq=10, got seq=%d", drained[0].Seq)
	}
}

// TestQueue_WithWAL_DuplicateSkipped verifies that duplicate commands in
// the WAL are not double-enqueued.
func TestQueue_WithWAL_DuplicateSkipped(t *testing.T) {
	path := tempWALPath(t)
	now := time.Now().UnixMicro()

	w, err := cmdqueue.NewWAL(path)
	if err != nil {
		t.Fatalf("NewWAL: %v", err)
	}
	// Write the same command twice (simulates crash during re-append).
	cmd := cmdqueue.Command{ExecuteAt: 0, Seq: 5, Action: "cut"}
	_ = w.Append(cmd)
	_ = w.Append(cmd)
	_ = w.Close()

	w2, err := cmdqueue.NewWAL(path)
	if err != nil {
		t.Fatalf("NewWAL reopen: %v", err)
	}
	defer func() { _ = w2.Close() }()

	q := cmdqueue.New(func() int64 { return now }, cmdqueue.WithWAL(w2))

	drained := q.Drain()
	if len(drained) != 1 {
		t.Fatalf("expected 1 command (deduped), got %d", len(drained))
	}
}

// TestWAL_EmptyFile verifies that Replay on a fresh/empty WAL returns nothing.
func TestWAL_EmptyFile(t *testing.T) {
	path := tempWALPath(t)

	// Create an empty file.
	f, err := os.Create(path)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	_ = f.Close()

	w, err := cmdqueue.NewWAL(path)
	if err != nil {
		t.Fatalf("NewWAL: %v", err)
	}
	defer func() { _ = w.Close() }()

	cmds, err := w.Replay(cmdqueue.DefaultMaxAge)
	if err != nil {
		t.Fatalf("Replay: %v", err)
	}
	if len(cmds) != 0 {
		t.Fatalf("expected 0 commands from empty WAL, got %d", len(cmds))
	}
}
