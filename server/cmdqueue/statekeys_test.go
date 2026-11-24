package cmdqueue_test

import (
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/zsiec/switchframe/server/cmdqueue"
)

var testStateKeys = map[string][]string{"cut": {"program", "preview"}, "transition": {"program"}, "preview": {"preview"}}

// ranHere runs a command on arrival, recording it.
func ranHere(t *testing.T, q *cmdqueue.Queue, action string, at int64, seq uint64, payload string, state map[string]string) {
	t.Helper()
	err := q.RunLocal(cmdqueue.Command{Action: action, OrderAt: at, Seq: seq, Payload: []byte(payload)}, func() bool {
		apply(state, action, payload)
		return true
	})
	if err != nil {
		t.Fatalf("RunLocal %s: %v", action, err)
	}
}

// apply runs action on a switcher reduced to its program and preview: a
// cut puts the old program on preview.
func apply(state map[string]string, action, payload string) {
	switch action {
	case "cut", "transition":
		if old := state["program"]; old != "" {
			state["preview"] = old
		}
		state["program"] = payload
	case "preview":
		state["preview"] = payload
	default:
		state[action] = payload
	}
}

// run applies what a queue drains to state, as the switcher does.
func run(q *cmdqueue.Queue, state map[string]string) {
	defer q.Hold()()
	for _, c := range q.Drain() {
		_ = q.Exec(c, func() error {
			apply(state, c.Action, string(c.Payload))
			return nil
		})
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
	ranHere(t, qa, a.Action, a.ExecuteAt, a.Seq, "cam1", sa)
	ranHere(t, qb, b.Action, b.ExecuteAt, b.Seq, "cam2", sb)

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
	if sa["program"] != "cam2" || sb["program"] != "cam2" {
		t.Fatalf("engines ended on %q and %q, want cam2 on both", sa["program"], sb["program"])
	}
}

// At the same ExecuteAt the higher Seq holds.
func TestStateKeys_SameTimeHigherSeqHolds(t *testing.T) {
	clock, _ := newClock(1_000)
	q := cmdqueue.New(clock, cmdqueue.WithStateKeys(testStateKeys))
	ranHere(t, q, "cut", 500, 8, "cam1", map[string]string{})
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
	got := map[string]string{}
	ranHere(t, q, "transition", 2_400, 5, "cam2", got)
	run(q, got)
	if got["program"] != "cam2" {
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
	got := map[string]string{}
	run(q, got)
	if _, ok := got["program"]; !ok {
		t.Fatal("the late cut did not run")
	}
	advance(1)
	if err := q.Enqueue(cmdqueue.Command{ExecuteAt: 8_400_000, Seq: 5, Action: "cut"}); !errors.Is(err, cmdqueue.ErrSuperseded) {
		t.Fatalf("a late cut falling before the one run: got %v", err)
	}
	if err := q.Enqueue(cmdqueue.Command{ExecuteAt: 8_600_000, Seq: 6, Action: "cut"}); err != nil {
		t.Fatalf("a late cut falling after the one run: %v", err)
	}
}

// A preview and a cut made at nearly the same moment on the two engines,
// each relayed to the other: a cut puts the old program on preview, so
// both end on the preview of whichever came later.
func TestStateKeys_PreviewAndCutCrossing(t *testing.T) {
	for name, previewFirst := range map[string]bool{"preview first": true, "cut first": false} {
		t.Run(name, func(t *testing.T) {
			const t0 = int64(1_000_000_000)
			clockA, advA := newClock(t0)
			clockB, advB := newClock(t0)
			qa := cmdqueue.New(clockA, cmdqueue.WithStateKeys(testStateKeys))
			qb := cmdqueue.New(clockB, cmdqueue.WithStateKeys(testStateKeys))
			sa := map[string]string{"program": "cam1", "preview": "cam1"}
			sb := map[string]string{"program": "cam1", "preview": "cam1"}

			pv := cmdqueue.Command{ExecuteAt: t0 + 10_000, Seq: 10, Action: "preview", Payload: []byte("cam3")}
			cut := cmdqueue.Command{ExecuteAt: t0 + 12_000, Seq: 11, Action: "cut", Payload: []byte("cam2")}
			want := "cam1" // the cut came later: the old program
			if !previewFirst {
				pv.ExecuteAt, cut.ExecuteAt = cut.ExecuteAt, pv.ExecuteAt
				want = "cam3"
			}
			ranHere(t, qa, pv.Action, pv.ExecuteAt, pv.Seq, "cam3", sa)
			ranHere(t, qb, cut.Action, cut.ExecuteAt, cut.Seq, "cam2", sb)
			advA(17_000)
			advB(17_000)
			_ = qa.Enqueue(cut)
			_ = qb.Enqueue(pv)
			// two ticks: a preview chosen again after an earlier cut runs
			// on the tick after it
			for range 2 {
				run(qa, sa)
				run(qb, sb)
			}
			if sa["program"] != "cam2" || sb["program"] != "cam2" {
				t.Fatalf("program %q and %q, want cam2", sa["program"], sb["program"])
			}
			if sa["preview"] != want || sb["preview"] != want {
				t.Fatalf("preview %q and %q, want %q on both", sa["preview"], sb["preview"], want)
			}
		})
	}
}

// RunLocal does not run while a drain's dispatch holds the queue, so a
// drained command never lands after one run on arrival that falls later.
func TestStateKeys_RunLocalWaitsForDispatch(t *testing.T) {
	clock, _ := newClock(1_000)
	q := cmdqueue.New(clock, cmdqueue.WithStateKeys(testStateKeys))
	release := q.Hold()
	ran := make(chan struct{})
	go func() {
		_ = q.RunLocal(cmdqueue.Command{Action: "cut", OrderAt: 900, Seq: 2}, func() bool { close(ran); return true })
	}()
	select {
	case <-ran:
		t.Fatal("RunLocal ran while the queue was held")
	case <-time.After(50 * time.Millisecond):
	}
	release()
	select {
	case <-ran:
	case <-time.After(time.Second):
		t.Fatal("RunLocal did not run once released")
	}
}

// A command run on arrival that fails is not recorded, and does not drop
// an earlier one arriving later.
func TestStateKeys_FailedLocalRunNotRecorded(t *testing.T) {
	clock, _ := newClock(1_000)
	q := cmdqueue.New(clock, cmdqueue.WithStateKeys(testStateKeys))
	if err := q.RunLocal(cmdqueue.Command{Action: "cut", OrderAt: 900, Seq: 2}, func() bool { return false }); err != nil {
		t.Fatal(err)
	}
	if err := q.Enqueue(cmdqueue.Command{ExecuteAt: 800, Seq: 1, Action: "cut"}); err != nil {
		t.Fatalf("an earlier cut after a failed later one: %v", err)
	}
}

// A late command keeps its place in the order across a WAL replay.
func TestStateKeys_OrderAtSurvivesWALReplay(t *testing.T) {
	path := filepath.Join(t.TempDir(), "q.wal")
	clock, _ := newClock(10_000_000)
	wal, err := cmdqueue.NewWAL(path)
	if err != nil {
		t.Fatal(err)
	}
	q := cmdqueue.New(clock, cmdqueue.WithMaxAge(1_000_000), cmdqueue.WithWAL(wal), cmdqueue.WithStateKeys(testStateKeys))
	if err := q.Enqueue(cmdqueue.Command{ExecuteAt: 8_500_000, Seq: 4, Action: "cut"}); err != nil {
		t.Fatal(err)
	}
	_ = wal.Close()

	wal2, err := cmdqueue.NewWAL(path)
	if err != nil {
		t.Fatal(err)
	}
	q2 := cmdqueue.New(clock, cmdqueue.WithMaxAge(1_000_000), cmdqueue.WithWAL(wal2), cmdqueue.WithStateKeys(testStateKeys))
	defer func() { _ = wal2.Close() }()
	cmds := q2.Drain()
	if len(cmds) != 1 || cmds[0].OrderAt != 8_500_000 {
		t.Fatalf("replayed %+v, want the cut at order 8500000", cmds)
	}
}

// After the clock steps back, a command run here still falls after the
// last one run, rather than every one being refused until the clock has
// caught up.
func TestStateKeys_ClockStepBackStillRunsLocally(t *testing.T) {
	clock, advance := newClock(10_000_000)
	q := cmdqueue.New(clock, cmdqueue.WithStateKeys(testStateKeys))
	st := map[string]string{}
	ranHere(t, q, "cut", 0, 1, "cam1", st)
	advance(-5_000_000)
	at := q.NextOrder()
	if at <= 10_000_000 {
		t.Fatalf("NextOrder %d after a step back, want past 10000000", at)
	}
	ranHere(t, q, "cut", 0, 2, "cam2", st)
	if st["program"] != "cam2" {
		t.Fatalf("program %q after the step back, want cam2", st["program"])
	}
}

// A drained command that fails is not recorded: a later command it would
// have dropped still runs.
func TestStateKeys_FailedDispatchNotRecorded(t *testing.T) {
	clock, _ := newClock(10_000)
	q := cmdqueue.New(clock, cmdqueue.WithStateKeys(testStateKeys))
	if err := q.Enqueue(cmdqueue.Command{ExecuteAt: 9_000, Seq: 5, Action: "cut"}); err != nil {
		t.Fatal(err)
	}
	release := q.Hold()
	for _, c := range q.Drain() {
		if err := q.Exec(c, func() error { return errors.New("not configured") }); err == nil {
			t.Fatal("the dispatch error was not returned")
		}
	}
	release()
	if err := q.Enqueue(cmdqueue.Command{ExecuteAt: 8_000, Seq: 4, Action: "cut"}); err != nil {
		t.Fatalf("an earlier cut after a failed later one: %v", err)
	}
}

// A command that leaves its further keys alone, as a cut to the source on
// program leaves the preview, does not record them.
func TestStateKeys_UnchangedSideKeyNotRecorded(t *testing.T) {
	clock, _ := newClock(10_000)
	q := cmdqueue.New(clock, cmdqueue.WithStateKeys(testStateKeys))
	q.SetChanges(func(c cmdqueue.Command) bool { return string(c.Payload) != "cam1" })
	st := map[string]string{"program": "cam1"}
	ranHere(t, q, "cut", 9_000, 2, "cam1", st)
	if err := q.Enqueue(cmdqueue.Command{ExecuteAt: 8_000, Seq: 1, Action: "preview", Payload: []byte("cam3")}); err != nil {
		t.Fatalf("an earlier preview after a cut that left the preview alone: %v", err)
	}
	run(q, st)
	if st["preview"] != "cam3" {
		t.Fatalf("preview %q, want cam3", st["preview"])
	}
}
