package cmdqueue_test

import (
	"errors"
	"testing"

	"github.com/zsiec/switchframe/server/cmdqueue"
)

var testStateKeys = map[string]string{"cut": "program", "transition": "program", "preview": "preview"}

// run applies what a queue drains to state, as the switcher does.
func run(q *cmdqueue.Queue, state map[string]string) {
	for _, c := range q.Drain() {
		state[c.Action] = string(c.Payload)
	}
}

// Two engines each run their own cut on arrival and relay it to the other,
// where it arrives after the other's own. Both end on the cut that falls
// later, whichever order they took the two in.
func TestStateKeys_RelayedConflictEndsAlike(t *testing.T) {
	const t0 = int64(1_000_000_000)
	clockA, advA := newClock(t0)
	clockB, advB := newClock(t0)
	qa := cmdqueue.New(clockA, cmdqueue.WithStateKeys(testStateKeys))
	qb := cmdqueue.New(clockB, cmdqueue.WithStateKeys(testStateKeys))
	sa, sb := map[string]string{}, map[string]string{}

	// a cuts to cam1 at t0+10ms, b to cam2 at t0+12ms, each on arrival.
	a := cmdqueue.Command{ExecuteAt: t0 + 10_000, Seq: 10, Action: "cut", Payload: []byte("cam1")}
	b := cmdqueue.Command{ExecuteAt: t0 + 12_000, Seq: 11, Action: "cut", Payload: []byte("cam2")}
	sa["cut"] = "cam1"
	qa.Ran(a.Action, a.ExecuteAt, a.Seq)
	sb["cut"] = "cam2"
	qb.Ran(b.Action, b.ExecuteAt, b.Seq)

	// Each relay arrives 5ms later.
	advA(17_000)
	advB(17_000)
	if err := qa.Enqueue(b); err != nil {
		t.Fatalf("a takes the later cut: %v", err)
	}
	if err := qb.Enqueue(a); !errors.Is(err, cmdqueue.ErrSuperseded) {
		t.Fatalf("b drops the earlier cut, got %v", err)
	}
	run(qa, sa)
	run(qb, sb)
	if sa["cut"] != "cam2" || sb["cut"] != "cam2" {
		t.Fatalf("engines ended on %q and %q, want cam2 on both", sa["cut"], sb["cut"])
	}
}

// At the same ExecuteAt the higher Seq holds.
func TestStateKeys_SameTimeHigherSeqHolds(t *testing.T) {
	clock, _ := newClock(1_000)
	q := cmdqueue.New(clock, cmdqueue.WithStateKeys(testStateKeys))
	q.Ran("cut", 500, 8)
	if err := q.Enqueue(cmdqueue.Command{ExecuteAt: 500, Seq: 7, Action: "cut"}); !errors.Is(err, cmdqueue.ErrSuperseded) {
		t.Fatalf("lower seq at the same time: got %v", err)
	}
	if err := q.Enqueue(cmdqueue.Command{ExecuteAt: 500, Seq: 9, Action: "cut"}); err != nil {
		t.Fatalf("higher seq at the same time: %v", err)
	}
}

// A command queued before a later one on its key ran outside the queue is
// dropped when due, not run after it.
func TestStateKeys_DueCommandDroppedAfterLaterRan(t *testing.T) {
	clock, advance := newClock(1_000)
	q := cmdqueue.New(clock, cmdqueue.WithStateKeys(testStateKeys))
	if err := q.Enqueue(cmdqueue.Command{ExecuteAt: 2_000, Seq: 1, Action: "cut", Payload: []byte("cam1")}); err != nil {
		t.Fatal(err)
	}
	if err := q.Enqueue(cmdqueue.Command{ExecuteAt: 2_000, Seq: 2, Action: "preview", Payload: []byte("cam3")}); err != nil {
		t.Fatal(err)
	}
	advance(1_500)
	q.Ran("transition", 2_400, 5)
	got := map[string]string{}
	run(q, got)
	if _, ok := got["cut"]; ok {
		t.Fatal("the cut ran after a later transition on program")
	}
	if got["preview"] != "cam3" {
		t.Fatal("a command on another state was dropped")
	}
}

// An action without a key, such as a toggle, is never dropped.
func TestStateKeys_UnkeyedActionsRunInOrder(t *testing.T) {
	clock, _ := newClock(1_000)
	q := cmdqueue.New(clock, cmdqueue.WithStateKeys(testStateKeys))
	q.Ran("ftb", 900, 9)
	if err := q.Enqueue(cmdqueue.Command{ExecuteAt: 800, Seq: 3, Action: "ftb"}); err != nil {
		t.Fatalf("unkeyed action: %v", err)
	}
	if n := len(q.Drain()); n != 1 {
		t.Fatalf("drained %d, want 1", n)
	}
}

// A late command keeps its place among the commands on its key although
// the queue runs it at once.
func TestStateKeys_LateCommandKeepsItsTime(t *testing.T) {
	clock, advance := newClock(10_000_000)
	q := cmdqueue.New(clock, cmdqueue.WithMaxAge(1_000_000), cmdqueue.WithStateKeys(testStateKeys))
	if err := q.Enqueue(cmdqueue.Command{ExecuteAt: 8_500_000, Seq: 4, Action: "cut"}); err != nil {
		t.Fatal(err)
	}
	if n := len(q.Drain()); n != 1 {
		t.Fatalf("drained %d, want 1", n)
	}
	advance(1)
	if err := q.Enqueue(cmdqueue.Command{ExecuteAt: 8_400_000, Seq: 5, Action: "cut"}); !errors.Is(err, cmdqueue.ErrSuperseded) {
		t.Fatalf("a late cut falling before the one run: got %v", err)
	}
	if err := q.Enqueue(cmdqueue.Command{ExecuteAt: 8_600_000, Seq: 6, Action: "cut"}); err != nil {
		t.Fatalf("a late cut falling after the one run: %v", err)
	}
}
