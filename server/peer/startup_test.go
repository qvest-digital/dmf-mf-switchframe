package peer

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zsiec/switchframe/server/internal"
)

func TestNextStartupStep(t *testing.T) {
	t0 := time.Unix(1000, 0)
	before, after := t0.Add(-time.Second), t0.Add(time.Second)
	// polled is the peer as seen by a poll sent at sent.
	polled := func(src string, sent time.Time) PeerStatus {
		return PeerStatus{Reachable: true, ProgramSource: src, PolledAt: sent, SeenPolledAt: sent}
	}
	failed := func(st PeerStatus, sent time.Time) PeerStatus {
		st.PolledAt = sent
		return st
	}
	tests := []struct {
		name string
		s    startupState
		want startupStep
	}{
		// A returning engine follows the one that kept the program up,
		// whichever of the two would lead by label.
		{"peer on air is followed", startupState{peer: polled("mxl:cam2", after)}, stepFollow},
		{"peer on air is followed by the leader too", startupState{peer: polled("mxl:cam2", after), localLeads: true}, stepFollow},
		{"peer on air is followed over a restored program", startupState{local: "mxl:cam1", peer: polled("mxl:cam2", after), localLeads: true}, stepFollow},
		{"no poll sent since the apply", startupState{local: "mxl:cam2", peer: polled("mxl:cam2", before), synced: t0}, stepWait},
		{"only a failed poll since the apply", startupState{local: "mxl:cam2", peer: failed(polled("mxl:cam2", before), after), synced: t0}, stepWait},
		{"same program after the apply", startupState{local: "mxl:cam2", peer: polled("mxl:cam2", after), synced: t0}, stepDone},
		{"peer cut after the apply", startupState{local: "mxl:cam2", peer: polled("mxl:cam3", after), synced: t0}, stepFollow},
		{"peer source missing here", startupState{peer: polled("mxl:cam2", after), synced: t0}, stepFollow},

		{"on air with no peer on air", startupState{local: "mxl:cam1", peer: polled("", after)}, stepDone},
		{"leader goes live", startupState{peer: polled("", after), localLeads: true}, stepGoLive},
		{"follower waits for the leader", startupState{peer: polled("", after)}, stepWait},
		{"follower goes live once the leader wait is over", startupState{peer: polled("", after), waitedOut: true}, stepGoLive},
		{"waits for the first polls", startupState{localLeads: true, waitedOut: true}, stepWait},
		{"goes live with the peer down", startupState{peerDown: true}, stepGoLive},
		{"followed peer gone", startupState{peerDown: true, synced: t0}, stepGoLive},

		// Gone live on its own source: one poll sent afterwards decides.
		{"live, no poll sent since", startupState{local: "mxl:cam2", peer: polled("mxl:cam1", before), peerDown: true, live: t0}, stepWait},
		{"live, peer live alongside on another source", startupState{local: "mxl:cam2", peer: polled("mxl:cam1", after), live: t0}, stepFollow},
		{"live, leading a peer live alongside", startupState{local: "mxl:cam2", peer: polled("mxl:cam1", after), localLeads: true, live: t0}, stepDone},
		{"live, peer on the same source", startupState{local: "mxl:cam1", peer: polled("mxl:cam1", after), live: t0}, stepDone},
		{"live, leader still dark", startupState{local: "mxl:cam1", peer: polled("", after), live: t0}, stepWait},
		{"live, leading a dark peer", startupState{local: "mxl:cam1", peer: polled("", after), localLeads: true, live: t0}, stepDone},
		{"live, peer still down", startupState{local: "mxl:cam1", peer: failed(polled("mxl:cam3", before), after), peerDown: true, live: t0}, stepDone},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, nextStartupStep(tt.s))
		})
	}
}

// fakeEngine is a switcher reduced to its program, preview and the source
// it goes live on, served on the two peer endpoints RunStartup reads.
type fakeEngine struct {
	label string
	token string

	mu       sync.Mutex
	program  string
	preview  string
	seq      uint64
	initial  string // source GoLive cuts to; "" while none is available
	goLives  int
	applied  []uint64
	snapAuth []string
	srv      *httptest.Server
	snapFail bool
	// afterSnap runs, under mu, once a snapshot has been served.
	afterSnap func(e *fakeEngine)
	// known, when set, are the only sources Cut and an apply can put on air.
	known map[string]bool
}

func newFakeEngine(t *testing.T, label, token string) *fakeEngine {
	e := &fakeEngine{label: label, token: token}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/peer/health", func(w http.ResponseWriter, _ *http.Request) {
		e.mu.Lock()
		defer e.mu.Unlock()
		_ = json.NewEncoder(w).Encode(HealthResponse{Status: "ok", ProgramSource: e.program, LastCommandSeq: e.seq, EngineLabel: e.label})
	})
	mux.HandleFunc("GET /api/sync/state-snapshot", func(w http.ResponseWriter, r *http.Request) {
		e.mu.Lock()
		defer e.mu.Unlock()
		e.snapAuth = append(e.snapAuth, r.Header.Get("Authorization")+" admin="+r.URL.Query().Get("admin"))
		if e.snapFail || r.Header.Get("Authorization") != "Bearer "+e.token {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		snap, _ := json.Marshal(internal.FullStateSnapshot{ProgramSource: e.program, PreviewSource: e.preview})
		_ = json.NewEncoder(w).Encode(map[string]any{"snapshot": json.RawMessage(snap), "seq": e.seq})
		if e.afterSnap != nil {
			e.afterSnap(e)
		}
	})
	e.srv = httptest.NewServer(mux)
	t.Cleanup(e.srv.Close)
	return e
}

func (e *fakeEngine) ProgramSource() string {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.program
}

func (e *fakeEngine) ApplyPeerSnapshot(_ context.Context, snap internal.FullStateSnapshot, seq uint64) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.known == nil || e.known[snap.ProgramSource] {
		e.program = snap.ProgramSource
	}
	e.preview = snap.PreviewSource
	e.applied = append(e.applied, seq)
	if seq > e.seq {
		e.seq = seq
	}
}

func (e *fakeEngine) Cut(_ context.Context, source string) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.known != nil && !e.known[source] {
		return errors.New("no such source")
	}
	e.program = source
	return nil
}

func (e *fakeEngine) GoLive(_ context.Context) bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.goLives++
	if e.initial == "" {
		return false
	}
	e.program = e.initial
	return true
}

func (e *fakeEngine) set(fn func(e *fakeEngine)) {
	e.mu.Lock()
	defer e.mu.Unlock()
	fn(e)
}

func (e *fakeEngine) get(fn func(e *fakeEngine)) {
	e.mu.Lock()
	defer e.mu.Unlock()
	fn(e)
}

// startEngine runs RunStartup for e against peerURL and returns a channel
// closed when it returns.
func startEngine(t *testing.T, e *fakeEngine, peerURL string) <-chan struct{} {
	return startEngineWith(t, e, peerURL, 20*time.Millisecond, 2, time.Minute)
}

func startEngineWith(t *testing.T, e *fakeEngine, peerURL string, poll time.Duration, failures int, leaderWait time.Duration) <-chan struct{} {
	p := NewHealthPoller(peerURL,
		WithLocalIdentity(e.label),
		WithPollInterval(poll),
		WithFailureThreshold(failures),
		WithHTTPClient(&http.Client{Timeout: time.Second}))
	p.Start()
	t.Cleanup(p.Stop)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	done := make(chan struct{})
	go func() {
		defer close(done)
		RunStartup(ctx, p, e, StartupConfig{Token: e.token, Tick: 5 * time.Millisecond, LeaderWait: leaderWait})
	}()
	return done
}

func waitDone(t *testing.T, done <-chan struct{}) {
	t.Helper()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("RunStartup did not return")
	}
}

// An engine restarted while its peer keeps the program up comes back on the
// peer's program, preview and command seq, from the peer's snapshot read
// with the shared token, and never takes a source of its own.
func TestRunStartup_RestartFollowsLivePeer(t *testing.T) {
	leader := newFakeEngine(t, "a", "tok")
	leader.set(func(e *fakeEngine) { e.program, e.preview, e.seq = "mxl:cam2", "mxl:cam3", 41 })

	returning := newFakeEngine(t, "b", "tok")
	returning.set(func(e *fakeEngine) { e.initial = "mxl:cam1" })
	waitDone(t, startEngine(t, returning, leader.srv.URL))

	returning.get(func(e *fakeEngine) {
		assert.Equal(t, "mxl:cam2", e.program)
		assert.Equal(t, "mxl:cam3", e.preview)
		assert.Equal(t, uint64(41), e.seq)
		assert.Zero(t, e.goLives, "a follower never goes live on its own source")
	})
	leader.get(func(e *fakeEngine) {
		require.NotEmpty(t, e.snapAuth)
		assert.Equal(t, "Bearer tok admin=true", e.snapAuth[0])
	})
}

// Engine a leads by label, but coming back to b on air it follows b.
func TestRunStartup_ReturningLabelLeaderFollowsLivePeer(t *testing.T) {
	live := newFakeEngine(t, "b", "tok")
	live.set(func(e *fakeEngine) { e.program = "mxl:cam2" })

	returning := newFakeEngine(t, "a", "tok")
	returning.set(func(e *fakeEngine) { e.initial = "mxl:cam1" })
	waitDone(t, startEngine(t, returning, live.srv.URL))

	returning.get(func(e *fakeEngine) {
		assert.Equal(t, "mxl:cam2", e.program)
		assert.Zero(t, e.goLives)
	})
}

// A peer that cuts between the snapshot and the next poll is followed
// again, so the engines do not finish on different programs.
func TestRunStartup_RefollowsPeerThatCutAfterSnapshot(t *testing.T) {
	leader := newFakeEngine(t, "a", "tok")
	leader.set(func(e *fakeEngine) {
		e.program = "mxl:cam1"
		e.afterSnap = func(e *fakeEngine) { e.program, e.afterSnap = "mxl:cam2", nil }
	})

	returning := newFakeEngine(t, "b", "tok")
	waitDone(t, startEngine(t, returning, leader.srv.URL))

	returning.get(func(e *fakeEngine) {
		assert.Equal(t, "mxl:cam2", e.program)
		assert.Len(t, e.applied, 2)
	})
}

// Without the peer's snapshot the program still follows, from the health
// the peer publishes.
func TestRunStartup_SnapshotRefusedFollowsProgramAlone(t *testing.T) {
	leader := newFakeEngine(t, "a", "tok")
	leader.set(func(e *fakeEngine) { e.program = "mxl:cam2"; e.snapFail = true })

	returning := newFakeEngine(t, "b", "tok")
	returning.set(func(e *fakeEngine) { e.initial = "mxl:cam1" })
	waitDone(t, startEngine(t, returning, leader.srv.URL))

	returning.get(func(e *fakeEngine) {
		assert.Equal(t, "mxl:cam2", e.program)
		assert.Zero(t, e.goLives)
	})
}

// Two engines starting together: a, the leader by label, goes live on its
// own initial source, and b, whatever its own would be, follows a.
func TestRunStartup_ColdStartTogetherConvergesOnLeader(t *testing.T) {
	a := newFakeEngine(t, "a", "tok")
	a.set(func(e *fakeEngine) { e.initial = "mxl:cam1" })
	b := newFakeEngine(t, "b", "tok")
	b.set(func(e *fakeEngine) { e.initial = "mxl:cam2" })

	doneA := startEngine(t, a, b.srv.URL)
	doneB := startEngine(t, b, a.srv.URL)
	waitDone(t, doneA)
	waitDone(t, doneB)

	a.get(func(e *fakeEngine) { assert.Equal(t, "mxl:cam1", e.program) })
	b.get(func(e *fakeEngine) {
		assert.Equal(t, "mxl:cam1", e.program)
		assert.Zero(t, e.goLives, "the follower waits for the leader instead of going live")
	})
}

// The leader has no source yet: the follower stays dark with it rather than
// going live on a source the leader may not pick.
func TestRunStartup_FollowerWaitsForLeaderWithoutSource(t *testing.T) {
	a := newFakeEngine(t, "a", "tok")
	b := newFakeEngine(t, "b", "tok")
	b.set(func(e *fakeEngine) { e.initial = "mxl:cam2" })

	doneA := startEngine(t, a, b.srv.URL)
	doneB := startEngine(t, b, a.srv.URL)

	time.Sleep(200 * time.Millisecond)
	b.get(func(e *fakeEngine) { assert.Empty(t, e.program) })

	a.set(func(e *fakeEngine) { e.initial = "mxl:cam3" })
	waitDone(t, doneA)
	waitDone(t, doneB)
	b.get(func(e *fakeEngine) {
		assert.Equal(t, "mxl:cam3", e.program)
		assert.Zero(t, e.goLives)
	})
}

// b starts while a is not up: it goes live alone. a, starting later, follows
// b although it leads by label.
func TestRunStartup_StaggeredStartLaterEngineFollows(t *testing.T) {
	b := newFakeEngine(t, "b", "tok")
	b.set(func(e *fakeEngine) { e.initial = "mxl:cam2" })
	down := httptest.NewServer(http.NotFoundHandler())
	down.Close()
	waitDone(t, startEngine(t, b, down.URL))
	b.get(func(e *fakeEngine) { assert.Equal(t, "mxl:cam2", e.program) })

	a := newFakeEngine(t, "a", "tok")
	a.set(func(e *fakeEngine) { e.initial = "mxl:cam1" })
	waitDone(t, startEngine(t, a, b.srv.URL))
	a.get(func(e *fakeEngine) {
		assert.Equal(t, "mxl:cam2", e.program)
		assert.Zero(t, e.goLives)
	})
}

// b counts a down and is waiting for a source when a comes up, sees b dark
// and, leading, goes live. b gets its source before its next poll and goes
// live too, on another one. The poll after that shows a on air, and b, not
// leading, follows it.
func TestRunStartup_GoneLiveAlongsideLeaderFollows(t *testing.T) {
	a := newFakeEngine(t, "a", "tok")
	a.set(func(e *fakeEngine) { e.initial = "mxl:cam1" })
	b := newFakeEngine(t, "b", "tok")

	var up atomic.Bool
	toA := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !up.Load() {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		a.srv.Config.Handler.ServeHTTP(w, r)
	}))
	t.Cleanup(toA.Close)

	doneB := startEngineWith(t, b, toA.URL, 300*time.Millisecond, 1, time.Minute)
	time.Sleep(50 * time.Millisecond) // b's first poll has failed
	waitDone(t, startEngine(t, a, b.srv.URL))
	a.get(func(e *fakeEngine) { require.Equal(t, "mxl:cam1", e.program) })

	up.Store(true)
	b.set(func(e *fakeEngine) { e.initial = "mxl:cam2" })
	waitDone(t, doneB)
	b.get(func(e *fakeEngine) { assert.Equal(t, "mxl:cam1", e.program) })
	a.get(func(e *fakeEngine) { assert.Equal(t, "mxl:cam1", e.program) })
}

// A leader that stays dark does not keep the follower dark with it. Once the
// leader gets a source the two end on one program: the leader follows the
// follower on air, or, having gone live before it saw that, is followed.
func TestRunStartup_FollowerGoesLivePastLeaderWait(t *testing.T) {
	a := newFakeEngine(t, "a", "tok")
	b := newFakeEngine(t, "b", "tok")
	b.set(func(e *fakeEngine) { e.initial = "mxl:cam2" })

	doneA := startEngine(t, a, b.srv.URL)
	doneB := startEngineWith(t, b, a.srv.URL, 20*time.Millisecond, 2, 100*time.Millisecond)
	require.Eventually(t, func() bool { return b.ProgramSource() == "mxl:cam2" }, 5*time.Second, 5*time.Millisecond)

	a.set(func(e *fakeEngine) { e.initial = "mxl:cam1" })
	waitDone(t, doneA)
	waitDone(t, doneB)
	assert.Equal(t, a.ProgramSource(), b.ProgramSource())
	assert.NotEmpty(t, a.ProgramSource())
}

// Gone live past the leader wait, the follower follows a leader that comes
// on air on another source, rather than staying apart from it.
func TestRunStartup_FollowerLivePastLeaderWaitFollowsLeader(t *testing.T) {
	a := newFakeEngine(t, "a", "tok")
	b := newFakeEngine(t, "b", "tok")
	b.set(func(e *fakeEngine) { e.initial = "mxl:cam2" })

	doneB := startEngineWith(t, b, a.srv.URL, 20*time.Millisecond, 2, 100*time.Millisecond)
	require.Eventually(t, func() bool { return b.ProgramSource() == "mxl:cam2" }, 5*time.Second, 5*time.Millisecond)
	time.Sleep(100 * time.Millisecond) // b has seen a dark since

	a.set(func(e *fakeEngine) { e.program = "mxl:cam1" })
	waitDone(t, doneB)
	assert.Equal(t, "mxl:cam1", b.ProgramSource())
}

// A peer program this engine does not have is followed a bounded number of
// times, then the engine goes live on its own source rather than staying
// dark and re-applying the peer's state for good.
func TestRunStartup_PeerSourceMissingHereGivesUp(t *testing.T) {
	leader := newFakeEngine(t, "a", "tok")
	leader.set(func(e *fakeEngine) { e.program = "srt:only-on-a" })

	returning := newFakeEngine(t, "b", "tok")
	returning.set(func(e *fakeEngine) {
		e.initial = "mxl:cam1"
		e.known = map[string]bool{"mxl:cam1": true}
	})
	waitDone(t, startEngine(t, returning, leader.srv.URL))

	returning.get(func(e *fakeEngine) {
		assert.Len(t, e.applied, maxStartupFollows)
		assert.Equal(t, "mxl:cam1", e.program)
	})
}

// A single engine goes live at once, retrying until a source is available.
func TestRunStartup_StandaloneGoesLive(t *testing.T) {
	e := newFakeEngine(t, "", "")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() {
		defer close(done)
		RunStartup(ctx, nil, e, StartupConfig{Tick: 5 * time.Millisecond})
	}()
	time.Sleep(50 * time.Millisecond)
	e.set(func(e *fakeEngine) { e.initial = "mxl:cam1" })
	waitDone(t, done)
	e.get(func(e *fakeEngine) {
		assert.Equal(t, "mxl:cam1", e.program)
		assert.Greater(t, e.goLives, 1)
	})
}

func TestPeerDown(t *testing.T) {
	assert.True(t, (*HealthPoller)(nil).PeerDown())
	p := NewHealthPoller("http://unused", WithFailureThreshold(2))
	assert.False(t, p.PeerDown(), "no poll out yet")
	p.recordFailure("x")
	assert.False(t, p.PeerDown())
	p.recordFailure("x")
	assert.True(t, p.PeerDown())
	p.recordSuccess(HealthResponse{Status: "ok"})
	assert.False(t, p.PeerDown())
}
