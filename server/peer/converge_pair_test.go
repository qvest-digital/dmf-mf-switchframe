package peer

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/zsiec/switchframe/server/internal"
)

// pairEngine is one engine of a pair: its state, its API and its converger.
type pairEngine struct {
	mu      sync.Mutex
	state   CompareState
	full    internal.FullStateSnapshot
	applies int
	srv     *httptest.Server
	conv    *Converger
}

func (e *pairEngine) compare() CompareState {
	e.mu.Lock()
	defer e.mu.Unlock()
	st := e.state
	st.Snapshot = internal.ComparableSnapshot(e.full)
	return st
}

func (e *pairEngine) apply(_ context.Context, snap internal.FullStateSnapshot, seq uint64) []string {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.applies++
	e.full = snap
	if seq > e.state.Seq {
		e.state.Seq = seq
	}
	return nil
}

// command changes the engine's state, as a command reaching only it does.
func (e *pairEngine) command(seq uint64, fn func(*internal.FullStateSnapshot)) {
	e.mu.Lock()
	defer e.mu.Unlock()
	fn(&e.full)
	e.state.Seq = seq
}

func newPairEngine(t *testing.T, label string, uptimeMs int64) *pairEngine {
	started := time.Unix(1_700_000_000, 0).UnixMilli() - uptimeMs
	e := &pairEngine{state: CompareState{Ready: true, StartedAtMs: started, ChangedAtMs: started},
		full: internal.FullStateSnapshot{ProgramSource: "cam1", PreviewSource: "cam2", GraphicsContent: true}}
	e.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/sync/state-compare":
			_ = json.NewEncoder(w).Encode(e.compare())
		case "/api/sync/state-snapshot":
			e.mu.Lock()
			body := map[string]any{"snapshot": e.full, "seq": e.state.Seq}
			e.mu.Unlock()
			_ = json.NewEncoder(w).Encode(body)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(e.srv.Close)
	return e
}

func connectPair(a, b *pairEngine, labelA, labelB string, clock *time.Time) {
	for _, side := range []struct {
		e, peer       *pairEngine
		local, remote string
	}{{a, b, labelA, labelB}, {b, a, labelB, labelA}} {
		p := NewHealthPoller(side.peer.srv.URL, WithLocalIdentity(side.local))
		p.SetPeerStateForTest(true, side.remote, 0)
		side.e.conv = NewConverger(p, ConvergeConfig{Tick: time.Second, Settle: testSettle,
			Backoff: testBackoff, MaxBackoff: 4 * testBackoff, StartTie: time.Minute, ChangeTie: 5 * time.Second},
			side.e.compare, side.e.apply)
		side.e.conv.now = func() time.Time { return *clock }
	}
}

// runPair steps both engines every 3s for d.
func runPair(a, b *pairEngine, clock *time.Time, d time.Duration) {
	for end := clock.Add(d); clock.Before(end); *clock = clock.Add(3 * time.Second) {
		a.conv.Step(context.Background())
		b.conv.Step(context.Background())
	}
}

// Both engines of a pair run their convergers against each other: a
// command that reached one only is repaired on the other, exactly once,
// and neither copies state back and forth afterwards.
func TestConvergePairRepairsDriftOnce(t *testing.T) {
	clock := time.Unix(1_700_000_000, 0)
	a, b := newPairEngine(t, "a", 3_600_000), newPairEngine(t, "b", 3_600_000)
	connectPair(a, b, "a", "b", &clock)

	runPair(a, b, &clock, time.Minute)
	if a.applies+b.applies != 0 {
		t.Fatalf("equal engines applied %d+%d states", a.applies, b.applies)
	}

	// A command reaches b only: b took the later command, so a follows.
	b.command(5, func(s *internal.FullStateSnapshot) {
		s.PreviewSource = "cam3"
		s.Graphics = []internal.GraphicsLayerSnapshot{{ID: 1, Active: true, Image: []byte("png")}}
	})
	runPair(a, b, &clock, 5*time.Minute)
	if a.applies != 1 || b.applies != 0 {
		t.Fatalf("applies a=%d b=%d, want a=1 b=0", a.applies, b.applies)
	}
	if diff := internal.SnapshotDiff(a.full, b.full); diff != nil {
		t.Fatalf("engines still differ in %v", diff)
	}
	if a.conv.Stats().Diverged || b.conv.Stats().Diverged {
		t.Fatal("converged pair still reported diverged")
	}
}

// After a restart the engine that went live alone, at the same command
// sequence and up for far shorter, takes the state of the one that kept
// the production, never the other way round.
func TestConvergePairRestartedEngineFollows(t *testing.T) {
	clock := time.Unix(1_700_000_000, 0)
	kept, restarted := newPairEngine(t, "b", 7_200_000), newPairEngine(t, "a", 30_000)
	connectPair(kept, restarted, "b", "a", &clock)
	kept.command(9, func(s *internal.FullStateSnapshot) {
		s.Graphics = []internal.GraphicsLayerSnapshot{{ID: 1}, {ID: 2}}
		s.Layout.ActivePreset = "pip"
	})
	restarted.command(9, func(s *internal.FullStateSnapshot) { s.ProgramSource = "cam4" })

	runPair(kept, restarted, &clock, 5*time.Minute)
	if kept.applies != 0 || restarted.applies != 1 {
		t.Fatalf("applies kept=%d restarted=%d, want 0 and 1", kept.applies, restarted.applies)
	}
	if len(kept.full.Graphics) != 2 || restarted.full.Layout.ActivePreset != "pip" {
		t.Fatal("the production the running engine kept was not what both hold")
	}
}
