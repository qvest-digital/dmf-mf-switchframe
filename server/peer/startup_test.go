package peer

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zsiec/switchframe/server/internal"
)

func TestNextStartupStep(t *testing.T) {
	t0 := time.Unix(1000, 0)
	before, after := t0.Add(-time.Second), t0.Add(time.Second)
	live := func(src string, seen time.Time) PeerStatus {
		return PeerStatus{Reachable: true, ProgramSource: src, LastSeen: seen}
	}
	tests := []struct {
		name       string
		local      string
		st         PeerStatus
		peerDown   bool
		localLeads bool
		synced     time.Time
		want       startupStep
	}{
		// A returning engine follows the one that kept the program up,
		// whichever of the two would lead by label.
		{"peer on air is followed", "", live("mxl:cam2", after), false, false, time.Time{}, stepFollow},
		{"peer on air is followed by the leader too", "", live("mxl:cam2", after), false, true, time.Time{}, stepFollow},
		{"peer on air is followed over a restored program", "mxl:cam1", live("mxl:cam2", after), false, true, time.Time{}, stepFollow},
		{"no poll since the apply", "mxl:cam2", live("mxl:cam2", before), false, false, t0, stepWait},
		{"same program after the apply", "mxl:cam2", live("mxl:cam2", after), false, false, t0, stepDone},
		{"peer cut after the apply", "mxl:cam2", live("mxl:cam3", after), false, false, t0, stepFollow},
		{"peer source missing here", "", live("mxl:cam2", after), false, false, t0, stepFollow},

		{"on air with no peer on air", "mxl:cam1", PeerStatus{Reachable: true}, false, false, time.Time{}, stepDone},
		{"leader goes live", "", PeerStatus{Reachable: true}, false, true, time.Time{}, stepGoLive},
		{"follower waits for the leader", "", PeerStatus{Reachable: true}, false, false, time.Time{}, stepWait},
		{"waits for the first polls", "", PeerStatus{}, false, true, time.Time{}, stepWait},
		{"goes live with the peer down", "", PeerStatus{}, true, false, time.Time{}, stepGoLive},
		{"followed peer gone", "", PeerStatus{}, true, false, t0, stepGoLive},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, nextStartupStep(tt.local, tt.st, tt.peerDown, tt.localLeads, tt.synced))
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
	e.program, e.preview = snap.ProgramSource, snap.PreviewSource
	e.applied = append(e.applied, seq)
	if seq > e.seq {
		e.seq = seq
	}
}

func (e *fakeEngine) Cut(_ context.Context, source string) error {
	e.mu.Lock()
	defer e.mu.Unlock()
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
	p := NewHealthPoller(peerURL,
		WithLocalIdentity(e.label),
		WithPollInterval(20*time.Millisecond),
		WithFailureThreshold(2),
		WithHTTPClient(&http.Client{Timeout: time.Second}))
	p.Start()
	t.Cleanup(p.Stop)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	done := make(chan struct{})
	go func() {
		defer close(done)
		RunStartup(ctx, p, e, e.token, 5*time.Millisecond)
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

// A single engine goes live at once, retrying until a source is available.
func TestRunStartup_StandaloneGoesLive(t *testing.T) {
	e := newFakeEngine(t, "", "")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() {
		defer close(done)
		RunStartup(ctx, nil, e, "", 5*time.Millisecond)
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
