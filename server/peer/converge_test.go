package peer

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/zsiec/switchframe/server/internal"
)

// convergeRig is an engine with a converger and a peer serving its state.
type convergeRig struct {
	t  *testing.T
	mu sync.Mutex

	local, peer CompareState
	peerFull    internal.FullStateSnapshot

	// applyChanges is whether an apply takes the peer's state; false
	// models a difference the apply cannot repair.
	applyChanges bool
	applies      int
	snapshotGets int
	// onSnapshotGet runs while the peer serves its full state.
	onSnapshotGet func()
	// warnings is what an apply reports it could not take; appliedAt
	// when each apply ran, from the rig's start.
	warnings  []string
	appliedAt []time.Duration
	// snapshotStatus, when set, is how the peer answers a snapshot read.
	snapshotStatus int

	conv  *Converger
	clock time.Time
}

const (
	testSettle  = 15 * time.Second
	testBackoff = 30 * time.Second
)

func newConvergeRig(t *testing.T) *convergeRig {
	r := &convergeRig{t: t, applyChanges: true, clock: time.Unix(1_700_000_000, 0)}
	base := internal.FullStateSnapshot{ProgramSource: "cam1", PreviewSource: "cam2"}
	started := time.Unix(1_700_000_000, 0).Add(-time.Hour).UnixMilli()
	r.local = CompareState{Ready: true, Seq: 10, StartedAtMs: started, ChangedAtMs: started, Snapshot: base}
	r.peer = CompareState{Ready: true, Seq: 10, StartedAtMs: started, ChangedAtMs: started, Snapshot: base}
	r.peerFull = base

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		r.mu.Lock()
		defer r.mu.Unlock()
		switch req.URL.Path {
		case "/api/sync/state-compare":
			_ = json.NewEncoder(w).Encode(r.peer)
		case "/api/sync/state-snapshot":
			r.snapshotGets++
			if r.snapshotStatus != 0 {
				w.WriteHeader(r.snapshotStatus)
				return
			}
			if r.onSnapshotGet != nil {
				r.onSnapshotGet()
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"snapshot": r.peerFull, "seq": r.peer.Seq})
		default:
			http.NotFound(w, req)
		}
	}))
	t.Cleanup(srv.Close)

	p := NewHealthPoller(srv.URL, WithLocalIdentity("b"))
	p.SetPeerStateForTest(true, "a", 0) // the peer leads
	r.conv = NewConverger(p, ConvergeConfig{
		Tick: time.Second, Settle: testSettle, Backoff: testBackoff, MaxBackoff: 4 * testBackoff,
		StartTie: time.Minute, ChangeTie: 5 * time.Second,
	}, func() CompareState {
		r.mu.Lock()
		defer r.mu.Unlock()
		return r.local
	}, func(_ context.Context, snap internal.FullStateSnapshot, seq uint64) []string {
		r.mu.Lock()
		defer r.mu.Unlock()
		r.applies++
		r.appliedAt = append(r.appliedAt, r.clock.Sub(time.Unix(1_700_000_000, 0)))
		if r.applyChanges {
			r.local.Snapshot = snap
		}
		if seq > r.local.Seq {
			r.local.Seq = seq
		}
		return r.warnings
	})
	r.conv.now = func() time.Time { return r.clock }
	return r
}

// setPeer changes the peer's state, as a command reaching only it does.
func (r *convergeRig) setPeer(fn func(s *internal.FullStateSnapshot)) {
	r.mu.Lock()
	defer r.mu.Unlock()
	fn(&r.peer.Snapshot)
	fn(&r.peerFull)
}

// stepAt runs one comparison d after the rig's start.
func (r *convergeRig) stepAt(d time.Duration) {
	r.clock = time.Unix(1_700_000_000, 0).Add(d)
	r.conv.Step(context.Background())
}

func (r *convergeRig) appliesSoFar() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.applies
}

// A command that reached only the peer is repaired on this engine once the
// difference has settled, with no browser involved.
func TestConvergeRepairsDriftWithoutBrowser(t *testing.T) {
	r := newConvergeRig(t)
	r.setPeer(func(s *internal.FullStateSnapshot) { s.PreviewSource = "cam3" })
	r.peer.Seq = 11

	r.stepAt(0)
	r.stepAt(testSettle - time.Second)
	if r.appliesSoFar() != 0 {
		t.Fatal("applied before the difference settled")
	}
	if st := r.conv.Stats(); !st.Diverged || st.Divergences != 1 || len(st.Fields) != 1 || st.Fields[0] != "previewSource" {
		t.Fatalf("stats while diverged: %+v", st)
	}
	r.stepAt(testSettle)
	if r.appliesSoFar() != 1 {
		t.Fatalf("applies after settling: %d, want 1", r.appliesSoFar())
	}
	if r.local.Snapshot.PreviewSource != "cam3" || r.local.Seq != 11 {
		t.Fatalf("local state after the apply: %+v", r.local)
	}
	r.stepAt(testSettle + 3*time.Second)
	if st := r.conv.Stats(); st.Diverged || st.Applies != 1 || st.ApplyBytes == 0 {
		t.Fatalf("stats once converged: %+v", st)
	}
}

// Engines that agree are left alone, and their full state is not read.
func TestConvergeNoApplyWhenEqual(t *testing.T) {
	r := newConvergeRig(t)
	for i := 0; i < 20; i++ {
		r.stepAt(time.Duration(i) * 5 * time.Second)
	}
	if r.appliesSoFar() != 0 || r.snapshotGets != 0 {
		t.Fatalf("applies %d, snapshot reads %d over equal states", r.appliesSoFar(), r.snapshotGets)
	}
}

// A difference an apply cannot repair is taken again ever more rarely, the
// wait doubling up to its bound, rather than every round or never again.
func TestConvergeRetriesLastingDifferenceWithBackoff(t *testing.T) {
	r := newConvergeRig(t)
	r.applyChanges = false
	r.setPeer(func(s *internal.FullStateSnapshot) { s.Encoder = "h264_nvenc" })

	for d := time.Duration(0); d <= 10*time.Minute; d += 3 * time.Second {
		r.stepAt(d)
	}
	s := time.Second
	want := []time.Duration{15 * s, 45 * s, 105 * s, 225 * s, 345 * s, 465 * s, 585 * s}
	if fmt.Sprint(r.appliedAt) != fmt.Sprint(want) {
		t.Fatalf("applies at %v, want %v", r.appliedAt, want)
	}
	st := r.conv.Stats()
	if !st.Diverged || st.Unresolved != int64(len(want)-1) {
		t.Fatalf("stats over a lasting difference: %+v", st)
	}
}

// A difference an apply cannot repair stays the same difference while both
// engines take other commands alike: it is not taken for a new one, which
// would restart its backoff.
func TestConvergeLastingDifferenceWhileBothMoveOn(t *testing.T) {
	r := newConvergeRig(t)
	r.applyChanges = false
	r.setPeer(func(s *internal.FullStateSnapshot) { s.Encoder = "h264_nvenc" })
	for i, d := 0, time.Duration(0); d <= 30*time.Minute; i, d = i+1, d+3*time.Second {
		if i%20 == 0 {
			level := float64(-i)
			seq := uint64(100 + i)
			r.mu.Lock()
			r.local.Snapshot.AudioMaster.MasterLevel = level
			r.local.Seq = seq
			r.mu.Unlock()
			r.setPeer(func(s *internal.FullStateSnapshot) { s.AudioMaster.MasterLevel = level })
			r.mu.Lock()
			r.peer.Seq = seq
			r.mu.Unlock()
		}
		r.stepAt(d)
	}
	st := r.conv.Stats()
	if st.Applies < 2 || st.Unresolved != st.Applies-1 {
		t.Fatalf("applies %d, unresolved %d: the lasting difference was taken for new ones", st.Applies, st.Unresolved)
	}
}

// An apply that takes the peer's state only in part counts as failed, and
// is tried again after the backoff.
func TestConvergePartialApplyIsAFailure(t *testing.T) {
	r := newConvergeRig(t)
	r.peer.Seq = 11
	r.applyChanges = false
	r.warnings = []string{"encoder: not available"}
	r.setPeer(func(s *internal.FullStateSnapshot) { s.Encoder = "h264_nvenc" })
	r.stepAt(0)
	r.stepAt(testSettle)
	st := r.conv.Stats()
	if st.Applies != 1 || st.Failures != 1 {
		t.Fatalf("stats after a partial apply: %+v", st)
	}
}

// A failed read of the peer's state is tried again after the backoff,
// which doubles, not at every round.
func TestConvergeFailedPullBacksOff(t *testing.T) {
	r := newConvergeRig(t)
	r.peer.Seq = 11
	r.snapshotStatus = http.StatusInternalServerError
	r.setPeer(func(s *internal.FullStateSnapshot) { s.PreviewSource = "cam3" })
	reads := []time.Duration{}
	for d := time.Duration(0); d <= 2*time.Minute; d += 3 * time.Second {
		before := r.snapshotGets
		r.stepAt(d)
		if r.snapshotGets != before {
			reads = append(reads, d)
		}
	}
	s := time.Second
	want := []time.Duration{15 * s, 45 * s, 105 * s}
	if fmt.Sprint(reads) != fmt.Sprint(want) {
		t.Fatalf("peer state read at %v, want %v", reads, want)
	}
	if st := r.conv.Stats(); st.Failures != 3 || st.Applies != 0 {
		t.Fatalf("stats: %+v", st)
	}
}

// At the same command and start, the engine whose state changed later, by
// a write that carries no command sequence, keeps its state.
func TestConvergeLaterChangeHoldsAuthority(t *testing.T) {
	for name, tc := range map[string]struct {
		peerChangedLater bool
		wantApplies      int
	}{"peer changed later": {true, 1}, "this engine changed later": {false, 0}} {
		t.Run(name, func(t *testing.T) {
			r := newConvergeRig(t)
			r.setPeer(func(s *internal.FullStateSnapshot) { s.PreviewSource = "cam3" })
			later := time.Unix(1_700_000_000, 0).Add(-time.Minute).UnixMilli()
			if tc.peerChangedLater {
				r.peer.ChangedAtMs = later
			} else {
				r.local.ChangedAtMs = later
			}
			for d := time.Duration(0); d <= time.Minute; d += 3 * time.Second {
				r.stepAt(d)
			}
			if r.appliesSoFar() != tc.wantApplies {
				t.Fatalf("applies %d, want %d", r.appliesSoFar(), tc.wantApplies)
			}
		})
	}
}

// A browser reconcile that starts while the peer's state is read keeps the
// engine from applying it.
func TestConvergeYieldsToBrowserStartedDuringPull(t *testing.T) {
	r := newConvergeRig(t)
	r.peer.Seq = 11
	r.setPeer(func(s *internal.FullStateSnapshot) { s.PreviewSource = "cam3" })
	r.onSnapshotGet = func() { r.local.Reconciling = true }
	r.stepAt(0)
	r.stepAt(testSettle)
	if r.appliesSoFar() != 0 {
		t.Fatal("applied while a browser reconciled")
	}
}

// The engine holding authority over a difference never takes the other's
// state: the other takes its.
func TestConvergeAuthoritativeEngineKeepsItsState(t *testing.T) {
	r := newConvergeRig(t)
	r.local.Seq = 12 // this engine took a command the peer missed
	r.setPeer(func(s *internal.FullStateSnapshot) { s.PreviewSource = "cam3" })
	for d := time.Duration(0); d <= 2*time.Minute; d += 3 * time.Second {
		r.stepAt(d)
	}
	if r.appliesSoFar() != 0 {
		t.Fatalf("authoritative engine took the peer's state %d times", r.appliesSoFar())
	}
}

// A starting or dark peer is never copied, however its seq compares.
func TestConvergeNeverCopiesStartingOrDarkPeer(t *testing.T) {
	for name, mutate := range map[string]func(*convergeRig){
		"starting": func(r *convergeRig) { r.peer.Ready = false },
		"dark": func(r *convergeRig) {
			r.setPeer(func(s *internal.FullStateSnapshot) { s.ProgramSource = "" })
		},
	} {
		t.Run(name, func(t *testing.T) {
			r := newConvergeRig(t)
			r.peer.Seq = 50
			r.setPeer(func(s *internal.FullStateSnapshot) { s.PreviewSource = "cam3" })
			mutate(r)
			for d := time.Duration(0); d <= 2*time.Minute; d += 3 * time.Second {
				r.stepAt(d)
			}
			if r.appliesSoFar() != 0 {
				t.Fatalf("took the state of a %s peer", name)
			}
		})
	}
}

// An engine that restarted and went live alone holds less than its peer
// that kept running: at the same command it is the one that follows.
func TestConvergeWentLiveAloneFollowsLongerRunningPeer(t *testing.T) {
	r := newConvergeRig(t)
	// The peer, the leader, came back and went live alone 40s ago.
	r.peer.StartedAtMs = time.Unix(1_700_000_000, 0).Add(-40 * time.Second).UnixMilli()
	r.peer.ChangedAtMs = r.peer.StartedAtMs
	r.setPeer(func(s *internal.FullStateSnapshot) { s.Graphics = nil; s.PreviewSource = "" })
	r.local.Snapshot.Graphics = []internal.GraphicsLayerSnapshot{{ID: 1, Active: true}}
	for d := time.Duration(0); d <= 2*time.Minute; d += 3 * time.Second {
		r.stepAt(d)
	}
	if r.appliesSoFar() != 0 {
		t.Fatal("engine that kept the production took the state of one that went live alone")
	}
}

// No apply while a transition runs or a browser reconciles; once both
// are over, the difference is repaired.
func TestConvergeWaitsForTransitionAndBrowser(t *testing.T) {
	r := newConvergeRig(t)
	r.peer.Seq = 11
	r.setPeer(func(s *internal.FullStateSnapshot) { s.PreviewSource = "cam3" })
	r.peer.InTransition = true
	r.stepAt(0)
	r.stepAt(testSettle)
	r.peer.InTransition = false
	r.local.Reconciling = true
	r.stepAt(testSettle + 3*time.Second)
	if r.appliesSoFar() != 0 {
		t.Fatal("applied during a transition or a browser's reconcile")
	}
	r.local.Reconciling = false
	r.stepAt(testSettle + 6*time.Second)
	if r.appliesSoFar() != 1 {
		t.Fatalf("applies once clear: %d, want 1", r.appliesSoFar())
	}
}

// A command this engine takes while the peer's state is read is not
// overwritten by the older state.
func TestConvergeKeepsCommandTakenDuringPull(t *testing.T) {
	r := newConvergeRig(t)
	r.peer.Seq = 11
	r.setPeer(func(s *internal.FullStateSnapshot) { s.PreviewSource = "cam3" })
	r.onSnapshotGet = func() { r.local.Seq = 12 }
	r.stepAt(0)
	r.stepAt(testSettle)
	if r.appliesSoFar() != 0 {
		t.Fatal("applied a peer state older than a command this engine took meanwhile")
	}
	if st := r.conv.Stats(); st.Failures != 1 {
		t.Fatalf("failures %d, want 1", st.Failures)
	}
}

// A peer state without graphics, or naming no command, does not remove the
// layers this engine holds, as a pushed one does not.
func TestConvergeKeepsLayersAgainstEmptyPeer(t *testing.T) {
	for name, seq := range map[string]uint64{"no command": 0, "no layers": 11} {
		t.Run(name, func(t *testing.T) {
			r := newConvergeRig(t)
			r.local.Seq = 0
			r.local.Snapshot.Graphics = []internal.GraphicsLayerSnapshot{{ID: 1}, {ID: 2}}
			r.peer.Seq = seq
			r.peer.StartedAtMs -= 9_000_000
			r.setPeer(func(s *internal.FullStateSnapshot) { s.GraphicsContent = true })
			if seq == 0 {
				r.setPeer(func(s *internal.FullStateSnapshot) {
					s.Graphics = []internal.GraphicsLayerSnapshot{{ID: 1}}
				})
			}
			r.stepAt(0)
			r.stepAt(testSettle)
			if r.appliesSoFar() != 0 {
				t.Fatal("took a peer state that removes graphics layers")
			}
		})
	}
}

// A difference that keeps changing, as through a fader drag reaching the
// engines at different moments, is not acted on.
func TestConvergeWaitsWhileStateMoves(t *testing.T) {
	r := newConvergeRig(t)
	r.peer.Seq = 11
	for i := 0; i < 20; i++ {
		level := float64(-i)
		r.setPeer(func(s *internal.FullStateSnapshot) { s.AudioMaster.MasterLevel = level })
		r.stepAt(time.Duration(i) * 3 * time.Second)
	}
	if r.appliesSoFar() != 0 {
		t.Fatal("applied while the state kept changing")
	}
}

func TestPeerHoldsAuthority(t *testing.T) {
	const min = 60_000
	for _, tc := range []struct {
		name                      string
		localSeq, peerSeq         uint64
		localStart, peerStart     int64
		localChanged, peerChanged int64
		localLeads, want          bool
	}{
		{"peer took a later command", 1, 2, 0, 9 * min, 9 * min, 9 * min, true, true},
		{"local took a later command", 3, 2, 9 * min, 0, 9 * min, 9 * min, false, false},
		{"peer started far earlier", 2, 2, 15 * min, 0, 15 * min, 16 * min, true, true},
		{"local started far earlier", 2, 2, 0, 15 * min, 16 * min, 15 * min, false, false},
		{"same start, peer changed later", 2, 2, 0, 0, 10 * min, 11 * min, true, true},
		{"same start, local changed later", 2, 2, 0, 0, 11 * min, 10 * min, false, false},
		{"same start and change, local leads", 2, 2, 0, 30_000, 10 * min, 10*min + 2000, true, false},
		{"same start and change, peer leads", 2, 2, 30_000, 0, 10*min + 2000, 10 * min, false, true},
		{"change unknown, peer leads", 2, 2, 0, 0, 0, 11 * min, false, true},
	} {
		local := CompareState{Seq: tc.localSeq, StartedAtMs: tc.localStart, ChangedAtMs: tc.localChanged}
		peer := CompareState{Seq: tc.peerSeq, StartedAtMs: tc.peerStart, ChangedAtMs: tc.peerChanged}
		if got := peerHoldsAuthority(local, peer, tc.localLeads, time.Minute, 5*time.Second); got != tc.want {
			t.Errorf("%s: peer holds authority %v, want %v", tc.name, got, tc.want)
		}
		// Both engines decide alike: the other side's view is the converse.
		if back := peerHoldsAuthority(peer, local, !tc.localLeads, time.Minute, 5*time.Second); back == tc.want {
			t.Errorf("%s: both engines take authority %v", tc.name, back)
		}
	}
}
