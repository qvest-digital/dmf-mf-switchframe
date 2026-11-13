package control

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/zsiec/prism/distribution"
	"github.com/zsiec/switchframe/server/internal"
	"github.com/zsiec/switchframe/server/peer"
	"github.com/zsiec/switchframe/server/switcher"
)

type readinessProvider struct {
	mockSnapshotProvider
	ready bool
}

func (r *readinessProvider) SnapshotReady() bool { return r.ready }

// pullRig is an engine whose peer serves its state over HTTP.
type pullRig struct {
	local, peerAPI *API
	peerSw         *switcher.Switcher
	localSw        *switcher.Switcher
	peerProvider   *readinessProvider
	tracked        uint64
	peerAuth       string
	peerQuery      string
}

func newPullRig(t *testing.T) *pullRig {
	r := &pullRig{}
	r.peerSw = switcher.NewTestSwitcher(t, distribution.NewRelay())
	r.peerProvider = &readinessProvider{
		mockSnapshotProvider: mockSnapshotProvider{snapshot: internal.FullStateSnapshot{ProgramSource: "camera1"}},
		ready:                true,
	}
	r.peerAPI = NewAPI(r.peerSw)
	r.peerAPI.SetSnapshotProvider(r.peerProvider)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		r.peerAuth, r.peerQuery = req.Header.Get("Authorization"), req.URL.RawQuery
		r.peerAPI.Mux().ServeHTTP(w, req)
	}))
	t.Cleanup(srv.Close)

	r.localSw = switcher.NewTestSwitcher(t, distribution.NewRelay())
	r.local = NewAPI(r.localSw, WithPeerPoller(peer.NewHealthPoller(srv.URL)),
		WithSeqTracker(func(s uint64) { r.tracked = s }))
	r.local.SetSnapshotProvider(&mockSnapshotProvider{})
	return r
}

func (r *pullRig) pull(t *testing.T, query string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest("POST", "/api/sync/pull-from-peer"+query, nil)
	req.Header.Set("Authorization", "Bearer operator-token")
	rec := httptest.NewRecorder()
	r.local.Mux().ServeHTTP(rec, req)
	return rec
}

// A browser has the engine that lags take its peer's state straight from
// the peer, as a push of that state would apply it, seq included.
func TestPullFromPeer_AppliesThePeersState(t *testing.T) {
	r := newPullRig(t)
	r.peerSw.TrackCommandSeq(7)
	rec := r.pull(t, "")
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	var resp struct {
		Applied bool `json:"applied"`
	}
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&resp))
	require.True(t, resp.Applied)
	require.EqualValues(t, 7, r.tracked)
	require.Equal(t, "Bearer operator-token", r.peerAuth, "caller's credentials not passed on")
	require.Equal(t, "admin=true&ready=true", r.peerQuery)
}

// A peer state older than this engine's is refused as a stale push is.
func TestPullFromPeer_RefusesStaleState(t *testing.T) {
	r := newPullRig(t)
	r.peerSw.TrackCommandSeq(7)
	r.localSw.TrackCommandSeq(9)
	rec := r.pull(t, "")
	require.Equal(t, http.StatusConflict, rec.Code)
	var body map[string]any
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&body))
	require.EqualValues(t, 7, body["snapshot_seq"])
	require.EqualValues(t, 9, body["current_seq"])
	require.Zero(t, r.tracked)
}

// Under the caller's reconcile lock on the peer, a peer that is still
// starting is not copied.
func TestPullFromPeer_PeerStarting(t *testing.T) {
	r := newPullRig(t)
	lockID, _, ok := r.peerAPI.reconcileLock.tryAcquire("tab")
	require.True(t, ok)
	r.peerProvider.ready = false
	rec := r.pull(t, "?lockId="+lockID)
	require.Equal(t, http.StatusServiceUnavailable, rec.Code)
	require.Equal(t, "lockId="+lockID, r.peerQuery)

	rec = r.pull(t, "?lockId=not-held")
	require.Equal(t, http.StatusBadGateway, rec.Code)

	// Without a lock as well: the peer's admin read for a pull refuses a
	// starting engine, which a startup read past it would not.
	rec = r.pull(t, "")
	require.Equal(t, http.StatusServiceUnavailable, rec.Code)
}
