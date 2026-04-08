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

// TestEnqueue_StaleCommandDropped verifies that a command older than 2x maxAge
// returns ErrStaleCommand and is not queued.
func TestEnqueue_StaleCommandDropped(t *testing.T) {
	const now = int64(10_000_000) // 10 s (µs)
	clock, _ := newClock(now)
	q := cmdqueue.New(clock)

	stale := cmdqueue.Command{
		ExecuteAt: now - 4_000_001, // just over 2x default maxAge (4s)
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
// maxSeen (100_000), recent entries are retained (not cleared entirely).
func TestQueue_DedupPruneKeepsRecent(t *testing.T) {
	q := cmdqueue.New(func() int64 { return 1_000_000 })

	// Enqueue maxSeen+1 commands (100001) to trigger the prune.
	for i := uint64(1); i <= 100_001; i++ {
		if err := q.Enqueue(cmdqueue.Command{Seq: i, Action: "test"}); err != nil {
			t.Fatalf("enqueue seq=%d: %v", i, err)
		}
	}

	// Recent command should still be deduped (it was retained during prune).
	err := q.Enqueue(cmdqueue.Command{Seq: 100_001, Action: "test"})
	if !errors.Is(err, cmdqueue.ErrDuplicateCommand) {
		t.Fatalf("expected ErrDuplicateCommand for recent seq=100001, got %v", err)
	}

	// A command from around the middle should also still be deduped
	// (minSeq = 100001 - 50000 = 50001, so seq=60000 should be retained).
	err = q.Enqueue(cmdqueue.Command{Seq: 60_000, Action: "test"})
	if !errors.Is(err, cmdqueue.ErrDuplicateCommand) {
		t.Fatalf("expected ErrDuplicateCommand for mid-range seq=60000, got %v", err)
	}

	// Very old command (seq=1) should have been pruned and can be re-enqueued.
	err = q.Enqueue(cmdqueue.Command{Seq: 1, Action: "test"})
	if err != nil {
		t.Fatalf("expected seq=1 to be accepted after prune, got %v", err)
	}
}

// TestQueue_ConfigurableMaxAge verifies that WithMaxAge overrides the default
// staleness threshold so that commands older than 2s but within the custom
// maxAge are accepted, and commands outside the custom maxAge are rejected.
func TestQueue_ConfigurableMaxAge(t *testing.T) {
	const now = int64(20_000_000) // 20s (µs)
	clock, _ := newClock(now)

	// Custom maxAge of 10 seconds (active-active mode).
	q := cmdqueue.New(clock, cmdqueue.WithMaxAge(10_000_000))

	// Command 5s old — would be stale with default 2s, but fine with 10s.
	ok := cmdqueue.Command{
		ExecuteAt: now - 5_000_000,
		Seq:       1,
		Action:    "cut",
	}
	if err := q.Enqueue(ok); err != nil {
		t.Fatalf("expected 5s-old command to be accepted with 10s maxAge, got %v", err)
	}

	// Command 21s old — stale even with 10s maxAge (beyond 2x grace period).
	stale := cmdqueue.Command{
		ExecuteAt: now - 21_000_000,
		Seq:       2,
		Action:    "cut",
	}
	err := q.Enqueue(stale)
	if !errors.Is(err, cmdqueue.ErrStaleCommand) {
		t.Fatalf("expected ErrStaleCommand for 21s-old command with 10s maxAge, got %v", err)
	}

	// Default maxAge queue — 5s old command should be stale.
	qDefault := cmdqueue.New(clock)
	err = qDefault.Enqueue(ok)
	if !errors.Is(err, cmdqueue.ErrStaleCommand) {
		t.Fatalf("expected ErrStaleCommand for 5s-old command with default maxAge, got %v", err)
	}
}

// TestDrain_MaxBurstLimit verifies that Drain returns at most MaxDrainPerCall
// commands per invocation, even when more are ready.
func TestDrain_MaxBurstLimit(t *testing.T) {
	clock, _ := newClock(0)
	q := cmdqueue.New(clock)

	// Enqueue 50 commands with ExecuteAt=0 (immediate).
	for i := uint64(1); i <= 50; i++ {
		if err := q.Enqueue(cmdqueue.Command{Seq: i, Action: "test"}); err != nil {
			t.Fatalf("enqueue seq=%d: %v", i, err)
		}
	}

	// First drain: exactly MaxDrainPerCall (32).
	drained := q.Drain()
	if len(drained) != cmdqueue.MaxDrainPerCall {
		t.Fatalf("expected %d commands on first drain, got %d", cmdqueue.MaxDrainPerCall, len(drained))
	}

	// Second drain: remaining 18.
	drained = q.Drain()
	if len(drained) != 18 {
		t.Fatalf("expected 18 commands on second drain, got %d", len(drained))
	}

	// Third drain: nothing left.
	drained = q.Drain()
	if len(drained) != 0 {
		t.Fatalf("expected 0 commands on third drain, got %d", len(drained))
	}
}

// TestEnqueue_ZeroSeq_SkipsDedup verifies that commands with Seq=0 bypass
// dedup and are all accepted.
func TestEnqueue_ZeroSeq_SkipsDedup(t *testing.T) {
	clock, _ := newClock(0)
	q := cmdqueue.New(clock)

	cmd := cmdqueue.Command{
		ExecuteAt: 0,
		Seq:       0,
		Action:    "cut",
	}
	if err := q.Enqueue(cmd); err != nil {
		t.Fatalf("first enqueue with Seq=0: %v", err)
	}
	if err := q.Enqueue(cmd); err != nil {
		t.Fatalf("second enqueue with Seq=0 should succeed (no dedup), got: %v", err)
	}

	drained := q.Drain()
	if len(drained) != 2 {
		t.Fatalf("expected 2 commands with Seq=0, got %d", len(drained))
	}
}

// TestWithMaxSize_RejectsAtLimit verifies that when the queue is at capacity,
// further enqueue attempts return ErrQueueFull.
func TestWithMaxSize_RejectsAtLimit(t *testing.T) {
	clock, _ := newClock(0)
	q := cmdqueue.New(clock, cmdqueue.WithMaxSize(3))

	// Fill to capacity.
	for i := uint64(1); i <= 3; i++ {
		if err := q.Enqueue(cmdqueue.Command{Seq: i, Action: "test"}); err != nil {
			t.Fatalf("enqueue seq=%d: %v", i, err)
		}
	}

	// Next enqueue should be rejected.
	err := q.Enqueue(cmdqueue.Command{Seq: 4, Action: "test"})
	if !errors.Is(err, cmdqueue.ErrQueueFull) {
		t.Fatalf("expected ErrQueueFull, got %v", err)
	}

	// Pending should still be 3 (rejected command not added).
	if q.Pending() != 3 {
		t.Errorf("expected 3 pending, got %d", q.Pending())
	}
}

// TestWithMaxSize_AcceptsUnderLimit verifies that commands are accepted when
// the queue is below the max size.
func TestWithMaxSize_AcceptsUnderLimit(t *testing.T) {
	clock, _ := newClock(0)
	q := cmdqueue.New(clock, cmdqueue.WithMaxSize(5))

	for i := uint64(1); i <= 5; i++ {
		if err := q.Enqueue(cmdqueue.Command{Seq: i, Action: "test"}); err != nil {
			t.Fatalf("enqueue seq=%d should succeed under limit: %v", i, err)
		}
	}

	if q.Pending() != 5 {
		t.Errorf("expected 5 pending, got %d", q.Pending())
	}
}

// TestWithMaxSize_AcceptsAfterDrain verifies that draining commands frees
// space so new commands can be enqueued again.
func TestWithMaxSize_AcceptsAfterDrain(t *testing.T) {
	clock, _ := newClock(0)
	q := cmdqueue.New(clock, cmdqueue.WithMaxSize(2))

	// Fill to capacity.
	for i := uint64(1); i <= 2; i++ {
		if err := q.Enqueue(cmdqueue.Command{Seq: i, Action: "test"}); err != nil {
			t.Fatalf("enqueue seq=%d: %v", i, err)
		}
	}

	// Drain all.
	drained := q.Drain()
	if len(drained) != 2 {
		t.Fatalf("expected 2 drained, got %d", len(drained))
	}

	// Should accept new commands now.
	if err := q.Enqueue(cmdqueue.Command{Seq: 3, Action: "test"}); err != nil {
		t.Fatalf("enqueue after drain should succeed: %v", err)
	}
	if q.Pending() != 1 {
		t.Errorf("expected 1 pending after drain+enqueue, got %d", q.Pending())
	}
}

// TestWithMaxSize_ZeroDisablesLimit verifies that WithMaxSize(0) — or omitting
// the option entirely — allows unbounded growth (backward compatible).
func TestWithMaxSize_ZeroDisablesLimit(t *testing.T) {
	clock, _ := newClock(0)

	// Explicit WithMaxSize(0).
	q := cmdqueue.New(clock, cmdqueue.WithMaxSize(0))
	for i := uint64(1); i <= 100; i++ {
		if err := q.Enqueue(cmdqueue.Command{Seq: i, Action: "test"}); err != nil {
			t.Fatalf("enqueue seq=%d with maxSize=0: %v", i, err)
		}
	}
	if q.Pending() != 100 {
		t.Errorf("expected 100 pending with maxSize=0, got %d", q.Pending())
	}

	// No WithMaxSize option at all (default).
	q2 := cmdqueue.New(clock)
	for i := uint64(101); i <= 200; i++ {
		if err := q2.Enqueue(cmdqueue.Command{Seq: i, Action: "test"}); err != nil {
			t.Fatalf("enqueue seq=%d with default (no limit): %v", i, err)
		}
	}
	if q2.Pending() != 100 {
		t.Errorf("expected 100 pending with default, got %d", q2.Pending())
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

// ── Task 2: Late command grace period ────────────────────────────────────────

func TestEnqueue_LateButNotStale_ExecutesImmediately(t *testing.T) {
	now := int64(100_000_000)
	q := cmdqueue.New(func() int64 { return now }, cmdqueue.WithMaxAge(2_000_000))
	cmd := cmdqueue.Command{ExecuteAt: now - 3_000_000, Seq: 1, Action: "cut"}
	err := q.Enqueue(cmd)
	if err != nil {
		t.Fatalf("expected late command accepted, got: %v", err)
	}
	out := q.Drain()
	if len(out) != 1 {
		t.Fatalf("expected 1, got %d", len(out))
	}
	if out[0].ExecuteAt != 0 {
		t.Errorf("expected ExecuteAt=0, got %d", out[0].ExecuteAt)
	}
}

func TestEnqueue_LateCommandCountsAsLate(t *testing.T) {
	now := int64(100_000_000)
	q := cmdqueue.New(func() int64 { return now }, cmdqueue.WithMaxAge(2_000_000))
	_ = q.Enqueue(cmdqueue.Command{ExecuteAt: now - 3_000_000, Seq: 1, Action: "cut"})
	if q.LateCommands() != 1 {
		t.Errorf("expected 1 late, got %d", q.LateCommands())
	}
}

func TestEnqueue_VeryStaleCommand_StillDropped(t *testing.T) {
	now := int64(100_000_000)
	q := cmdqueue.New(func() int64 { return now }, cmdqueue.WithMaxAge(2_000_000))
	err := q.Enqueue(cmdqueue.Command{ExecuteAt: now - 5_000_000, Seq: 1, Action: "cut"})
	if err != cmdqueue.ErrStaleCommand {
		t.Fatalf("expected ErrStaleCommand, got: %v", err)
	}
}
