package cmdqueue_test

import (
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/zsiec/switchframe/server/cmdqueue"
)

// newClock returns a clock function whose time advances each time advance() is called.
func newClock(start int64) (clock func() int64, advance func(int64)) {
	var now = start
	return func() int64 { return now },
		func(delta int64) { now += delta }
}

func mustPayload(t *testing.T, v any) json.RawMessage {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	return b
}

// TestEnqueue_Drain_ExecutesAtCorrectTime verifies that a command with
// ExecuteAt = t+1.5s is NOT drained at t+1.0s but IS drained at t+1.5s.
func TestEnqueue_Drain_ExecutesAtCorrectTime(t *testing.T) {
	const start = int64(1_000_000_000) // arbitrary non-zero base (µs)
	clock, advance := newClock(start)
	q := cmdqueue.New(clock)

	cmd := cmdqueue.Command{
		ExecuteAt: start + 1_500_000, // 1.5 s after start
		Seq:       1,
		Action:    "cut",
		Payload:   mustPayload(t, map[string]any{"src": "cam1"}),
	}
	if err := q.Enqueue(cmd); err != nil {
		t.Fatalf("enqueue: %v", err)
	}

	// Advance to t+1.0s — command should NOT drain yet.
	advance(1_000_000)
	drained := q.Drain()
	if len(drained) != 0 {
		t.Fatalf("expected 0 commands at t+1.0s, got %d", len(drained))
	}

	// Advance to t+1.5s — command should now drain.
	advance(500_000)
	drained = q.Drain()
	if len(drained) != 1 {
		t.Fatalf("expected 1 command at t+1.5s, got %d", len(drained))
	}
	if drained[0].Seq != 1 {
		t.Errorf("expected seq=1, got seq=%d", drained[0].Seq)
	}
}

// TestEnqueue_DedupBySeq verifies that enqueueing the same Seq twice results
// in only one command being drained.
func TestEnqueue_DedupBySeq(t *testing.T) {
	clock, _ := newClock(0)
	q := cmdqueue.New(clock)

	cmd := cmdqueue.Command{
		ExecuteAt: 0,
		Seq:       42,
		Action:    "cut",
	}
	if err := q.Enqueue(cmd); err != nil {
		t.Fatalf("first enqueue: %v", err)
	}
	err := q.Enqueue(cmd)
	if !errors.Is(err, cmdqueue.ErrDuplicateCommand) {
		t.Fatalf("expected ErrDuplicateCommand on second enqueue, got %v", err)
	}

	drained := q.Drain()
	if len(drained) != 1 {
		t.Fatalf("expected 1 command after dedup, got %d", len(drained))
	}
}

// TestEnqueue_StaleCommandDropped verifies that a command older than 2 seconds
// returns ErrStaleCommand and is not queued.
func TestEnqueue_StaleCommandDropped(t *testing.T) {
	const now = int64(10_000_000) // 10 s (µs)
	clock, _ := newClock(now)
	q := cmdqueue.New(clock)

	stale := cmdqueue.Command{
		ExecuteAt: now - 2_000_001, // just over 2 s old
		Seq:       99,
		Action:    "cut",
	}
	err := q.Enqueue(stale)
	if !errors.Is(err, cmdqueue.ErrStaleCommand) {
		t.Fatalf("expected ErrStaleCommand, got %v", err)
	}

	if q.Pending() != 0 {
		t.Errorf("expected 0 pending after stale drop, got %d", q.Pending())
	}
}

// TestDrain_OrderedByExecuteAt verifies that three commands enqueued out of
// ExecuteAt order are drained in ascending ExecuteAt order.
func TestDrain_OrderedByExecuteAt(t *testing.T) {
	const now = int64(5_000_000)
	clock, _ := newClock(now)
	q := cmdqueue.New(clock)

	// Enqueue in reverse order.
	for _, cmd := range []cmdqueue.Command{
		{ExecuteAt: now - 300_000, Seq: 3, Action: "third"},
		{ExecuteAt: now - 100_000, Seq: 1, Action: "first"},
		{ExecuteAt: now - 200_000, Seq: 2, Action: "second"},
	} {
		if err := q.Enqueue(cmd); err != nil {
			t.Fatalf("enqueue seq=%d: %v", cmd.Seq, err)
		}
	}

	drained := q.Drain()
	if len(drained) != 3 {
		t.Fatalf("expected 3 drained, got %d", len(drained))
	}
	if drained[0].Action != "third" || drained[1].Action != "second" || drained[2].Action != "first" {
		t.Errorf("unexpected order: %v %v %v",
			drained[0].Action, drained[1].Action, drained[2].Action)
	}
}

// TestEnqueue_ZeroExecuteAt_PassesThrough verifies that a command with
// ExecuteAt=0 is treated as "execute immediately" and drained on the next call.
func TestEnqueue_ZeroExecuteAt_PassesThrough(t *testing.T) {
	// Use a large "now" so zero is well in the past.
	clock, _ := newClock(int64(time.Second.Microseconds()))
	q := cmdqueue.New(clock)

	cmd := cmdqueue.Command{
		ExecuteAt: 0,
		Seq:       7,
		Action:    "preview",
	}
	if err := q.Enqueue(cmd); err != nil {
		t.Fatalf("enqueue: %v", err)
	}

	drained := q.Drain()
	if len(drained) != 1 {
		t.Fatalf("expected 1 command with ExecuteAt=0, got %d", len(drained))
	}
	if drained[0].Seq != 7 {
		t.Errorf("expected seq=7, got seq=%d", drained[0].Seq)
	}
}

// TestQueue_DedupPruneKeepsRecent verifies that when the seen map exceeds
// maxSeen, recent entries are retained (not cleared entirely).
func TestQueue_DedupPruneKeepsRecent(t *testing.T) {
	q := cmdqueue.New(func() int64 { return 1_000_000 })

	// Enqueue maxSeen+1 commands (10001) to trigger the prune.
	for i := uint64(1); i <= 10001; i++ {
		if err := q.Enqueue(cmdqueue.Command{Seq: i, Action: "test"}); err != nil {
			t.Fatalf("enqueue seq=%d: %v", i, err)
		}
	}

	// Recent command should still be deduped (it was retained during prune).
	err := q.Enqueue(cmdqueue.Command{Seq: 10001, Action: "test"})
	if !errors.Is(err, cmdqueue.ErrDuplicateCommand) {
		t.Fatalf("expected ErrDuplicateCommand for recent seq=10001, got %v", err)
	}

	// A command from around the middle should also still be deduped
	// (minSeq = 10001 - 5000 = 5001, so seq=6000 should be retained).
	err = q.Enqueue(cmdqueue.Command{Seq: 6000, Action: "test"})
	if !errors.Is(err, cmdqueue.ErrDuplicateCommand) {
		t.Fatalf("expected ErrDuplicateCommand for mid-range seq=6000, got %v", err)
	}

	// Very old command (seq=1) should have been pruned and can be re-enqueued.
	err = q.Enqueue(cmdqueue.Command{Seq: 1, Action: "test"})
	if err != nil {
		t.Fatalf("expected seq=1 to be accepted after prune, got %v", err)
	}
}

// TestPending reflects the correct in-flight count.
func TestPending(t *testing.T) {
	const now = int64(5_000_000)
	clock, _ := newClock(now)
	q := cmdqueue.New(clock)

	// future command — won't drain yet
	future := cmdqueue.Command{ExecuteAt: now + 1_000_000, Seq: 1, Action: "cut"}
	if err := q.Enqueue(future); err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	if q.Pending() != 1 {
		t.Errorf("expected 1 pending, got %d", q.Pending())
	}

	q.Drain() // nothing should drain
	if q.Pending() != 1 {
		t.Errorf("expected still 1 pending after premature drain, got %d", q.Pending())
	}
}
