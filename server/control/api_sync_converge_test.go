package control

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/zsiec/prism/distribution"
	"github.com/zsiec/switchframe/server/internal"
	"github.com/zsiec/switchframe/server/peer"
	"github.com/zsiec/switchframe/server/switcher"
)

func getCompareState(t *testing.T, api *API) peer.CompareState {
	t.Helper()
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, httptest.NewRequest("GET", "/api/sync/state-compare", nil))
	require.Equal(t, http.StatusOK, rec.Code)
	var st peer.CompareState
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&st))
	return st
}

// The peer engine compares its state with this one's comparable form: what
// each engine renders or measures itself is left out, an image is reduced
// to its hash, and readiness and a browser's reconcile are reported.
func TestGetStateCompare(t *testing.T) {
	provider := &readinessProvider{mockSnapshotProvider: mockSnapshotProvider{snapshot: internal.FullStateSnapshot{
		CapturedAt:    42,
		ProgramSource: "camera1",
		Graphics:      []internal.GraphicsLayerSnapshot{{ID: 1, Image: []byte("png bytes"), Frame: []byte("frame")}},
	}}}
	api := NewAPI(switcher.NewTestSwitcher(t, distribution.NewRelay()))
	api.SetSnapshotProvider(provider)

	st := getCompareState(t, api)
	require.False(t, st.Ready)
	require.False(t, st.Reconciling)
	require.Equal(t, "camera1", st.Snapshot.ProgramSource)
	require.Zero(t, st.Snapshot.CapturedAt)
	sum := sha256.Sum256([]byte("png bytes"))
	require.Equal(t, hex.EncodeToString(sum[:]), string(st.Snapshot.Graphics[0].Image))
	require.Nil(t, st.Snapshot.Graphics[0].Frame)

	provider.ready = true
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, httptest.NewRequest("POST", "/api/sync/reconcile-lock", strings.NewReader(`{"holder":"tab"}`)))
	require.Equal(t, http.StatusOK, rec.Code)
	st = getCompareState(t, api)
	require.True(t, st.Ready)
	require.True(t, st.Reconciling, "a browser holding the reconcile lock is not reported")
}

// A snapshot a browser pushes counts as a reconcile, lock or not.
func TestGetStateCompare_ReconcilingAfterPush(t *testing.T) {
	api := NewAPI(switcher.NewTestSwitcher(t, distribution.NewRelay()))
	api.SetSnapshotProvider(&mockSnapshotProvider{})
	require.False(t, getCompareState(t, api).Reconciling)
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, httptest.NewRequest("POST", "/api/sync/state-snapshot", strings.NewReader(`{"programSource":"camera1"}`)))
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.True(t, getCompareState(t, api).Reconciling)
}

// A browser's reconcile counts for a while after it releases the lock, so
// the engines do not start converging in its wake.
func TestReconcileLockActiveAfterRelease(t *testing.T) {
	l := newReconcileLockState()
	require.False(t, l.active())
	id, _, ok := l.tryAcquire("tab")
	require.True(t, ok)
	require.True(t, l.release(id))
	require.True(t, l.active())
	l.lastActive = time.Now().Add(-reconcileQuiet - time.Second)
	require.False(t, l.active())
}

// The compared state reports when the engine started, and when the state
// last changed: the start until it first does.
func TestGetStateCompare_ChangedAt(t *testing.T) {
	provider := &mockSnapshotProvider{snapshot: internal.FullStateSnapshot{ProgramSource: "camera1"}}
	api := NewAPI(switcher.NewTestSwitcher(t, distribution.NewRelay()))
	api.SetSnapshotProvider(provider)

	first := getCompareState(t, api)
	require.Equal(t, api.startTime.UnixMilli(), first.StartedAtMs)
	require.Equal(t, first.StartedAtMs, first.ChangedAtMs, "an unchanged state is dated by the start")

	time.Sleep(5 * time.Millisecond)
	require.Equal(t, first.ChangedAtMs, getCompareState(t, api).ChangedAtMs)
	provider.snapshot.PreviewSource = "camera2"
	before := time.Now().UnixMilli()
	changed := getCompareState(t, api)
	require.GreaterOrEqual(t, changed.ChangedAtMs, before)
	require.Equal(t, changed.ChangedAtMs, getCompareState(t, api).ChangedAtMs)
}
