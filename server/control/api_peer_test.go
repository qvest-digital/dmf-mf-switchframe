package control

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/zsiec/prism/distribution"

	"github.com/zsiec/switchframe/server/cmdqueue"
	"github.com/zsiec/switchframe/server/peer"
	"github.com/zsiec/switchframe/server/switcher"
)

func TestHandlePeerHealth(t *testing.T) {
	programRelay := distribution.NewRelay()
	sw := switcher.NewTestSwitcher(t, programRelay)
	api := NewAPI(sw)

	req := httptest.NewRequest(http.MethodGet, "/api/peer/health", nil)
	w := httptest.NewRecorder()
	api.Mux().ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)

	var resp peer.HealthResponse
	err := json.Unmarshal(w.Body.Bytes(), &resp)
	require.NoError(t, err)

	assert.Equal(t, "ok", resp.Status)
	assert.True(t, resp.UptimeMs >= 0)
}

func TestPeerHealthAuthExempt(t *testing.T) {
	// Verify that /api/peer/health is in the auth exempt list.
	assert.True(t, authExemptPaths["/api/peer/health"])
	assert.True(t, authExemptPaths["/api/v1/peer/health"])
}

func TestHandlePeerHealth_AdvertisesLabelAndEpoch(t *testing.T) {
	programRelay := distribution.NewRelay()
	sw := switcher.NewTestSwitcher(t, programRelay)
	poller := peer.NewHealthPoller("http://unused",
		peer.WithLocalIdentity("a"),
		peer.WithInitialEpoch(3),
	)
	api := NewAPI(sw, WithPeerPoller(poller))

	req := httptest.NewRequest(http.MethodGet, "/api/peer/health", nil)
	w := httptest.NewRecorder()
	api.Mux().ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)

	var resp peer.HealthResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))

	assert.Equal(t, "a", resp.EngineLabel)
	assert.Equal(t, uint64(3), resp.Epoch)
}

// Regression guard: /api/peer/health must report the enqueued seq
// (lastCommandSeq), not the executed seq. With the drain loop idle
// the two diverge, so a regression that reroutes this field to
// lastExecutedSeq reports 0 and fails the test.
func TestHandlePeerHealth_ReportsEnqueuedSeqBeforeExecution(t *testing.T) {
	programRelay := distribution.NewRelay()
	sw := switcher.NewTestSwitcher(t, programRelay)
	sw.TrackCommandSeq(42)

	api := NewAPI(sw)

	req := httptest.NewRequest(http.MethodGet, "/api/peer/health", nil)
	w := httptest.NewRecorder()
	api.Mux().ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)

	var resp peer.HealthResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Equal(t, uint64(42), resp.LastCommandSeq, "peer health must carry enqueued seq, not executed seq")

	// Cross-check on /api/sync/health, which exposes both fields, to
	// confirm the two are genuinely distinct in this state — otherwise
	// this test would pass trivially regardless of wiring.
	syncReq := httptest.NewRequest(http.MethodGet, "/api/sync/health", nil)
	syncW := httptest.NewRecorder()
	api.Mux().ServeHTTP(syncW, syncReq)
	require.Equal(t, http.StatusOK, syncW.Code)

	var syncResp struct {
		LastCommandSeq  uint64 `json:"lastCommandSeq"`
		LastExecutedSeq uint64 `json:"lastExecutedSeq"`
	}
	require.NoError(t, json.Unmarshal(syncW.Body.Bytes(), &syncResp))
	assert.Equal(t, uint64(42), syncResp.LastCommandSeq)
	assert.Equal(t, uint64(0), syncResp.LastExecutedSeq, "drain is idle — executed seq must still be 0")
}

// Regression guard: POSTing a timed command (execute_at in the future) to
// any route wrapped in timedHandler / timedHandlerWithPath must advance the
// switcher's lastCommandSeq such that GET /api/peer/health reports it. A
// future refactor that drops the a.seqTracker argument from any one
// handler's timedHandler call would pass the unit-level tests in
// timed_test.go but break this end-to-end chain.
//
// Parametrized across three routes to catch a dropped argument on any
// single handler file: one timedHandler call (cut), one timedHandler call
// in a different file (transition), and one timedHandlerWithPath call
// (source label, exercising the path-injection variant).
func TestTimedRoutes_AdvanceLastCommandSeq(t *testing.T) {
	now := int64(1_000_000_000)
	future := now + 500_000

	routes := []struct {
		name   string
		method string
		path   string
		// body template with exactly one %d for command_seq.
		body string
	}{
		{
			name:   "cut",
			method: http.MethodPost,
			path:   "/api/switch/cut",
			body:   fmt.Sprintf(`{"source":"camera1","execute_at":%d,"command_seq":%%d}`, future),
		},
		{
			name:   "transition",
			method: http.MethodPost,
			path:   "/api/switch/transition",
			body:   fmt.Sprintf(`{"source":"camera1","type":"mix","durationMs":1000,"execute_at":%d,"command_seq":%%d}`, future),
		},
		{
			name:   "source_label",
			method: http.MethodPost,
			path:   "/api/sources/camera1/label",
			body:   fmt.Sprintf(`{"label":"HELLO","execute_at":%d,"command_seq":%%d}`, future),
		},
	}

	const postedSeq uint64 = 4242

	for _, r := range routes {
		t.Run(r.name, func(t *testing.T) {
			programRelay := distribution.NewRelay()
			sw := switcher.NewTestSwitcher(t, programRelay)

			q := cmdqueue.New(func() int64 { return now })
			api := NewAPI(sw,
				WithCommandQueue(q),
				WithSeqTracker(sw.TrackCommandSeq),
			)

			body := fmt.Sprintf(r.body, postedSeq)
			req := httptest.NewRequest(r.method, r.path, strings.NewReader(body))
			w := httptest.NewRecorder()
			api.Mux().ServeHTTP(w, req)
			require.Equal(t, http.StatusAccepted, w.Code,
				"timed enqueue must 202 Accept; body=%s", w.Body.String())

			// Verify the enqueue actually landed — if the route short-circuited
			// past the queue, the seqTracker might have been called on the
			// immediate path instead, which is a different bug than the one
			// this test guards.
			require.Equal(t, 1, q.Pending(),
				"timed POST must enqueue the command, not execute immediately")

			healthReq := httptest.NewRequest(http.MethodGet, "/api/peer/health", nil)
			healthW := httptest.NewRecorder()
			api.Mux().ServeHTTP(healthW, healthReq)
			require.Equal(t, http.StatusOK, healthW.Code)

			var resp peer.HealthResponse
			require.NoError(t, json.Unmarshal(healthW.Body.Bytes(), &resp))
			assert.Equal(t, postedSeq, resp.LastCommandSeq,
				"POST %s with command_seq=%d must advance lastCommandSeq reported by /api/peer/health",
				r.path, postedSeq)
		})
	}
}

// TestPeerHealth_SeqSurvivesRestart asserts seq persists after a simulated
// restart, /api/peer/health reports the pre-restart lastCommandSeq BEFORE
// any new HTTP traffic arrives. Exercises the post-persist startup path —
// Switcher.SetInitialCommandSeq must seed the tracker such that the first
// health probe sees the high-water mark, not 0.
func TestPeerHealth_SeqSurvivesRestart(t *testing.T) {
	const restoredSeq uint64 = 50

	// Simulated restart: construct a fresh switcher (lastCommandSeq=0)
	// and seed it from the "persisted" value — same sequence the app's
	// initSwitcherCommon path runs at startup.
	programRelay := distribution.NewRelay()
	sw := switcher.NewTestSwitcher(t, programRelay)
	sw.SetInitialCommandSeq(restoredSeq)

	api := NewAPI(sw)

	// Probe /api/peer/health BEFORE any new API traffic runs.
	req := httptest.NewRequest(http.MethodGet, "/api/peer/health", nil)
	w := httptest.NewRecorder()
	api.Mux().ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code)

	var resp peer.HealthResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.GreaterOrEqual(t, resp.LastCommandSeq, restoredSeq,
		"post-restart /api/peer/health must report >= persisted seq; got %d, want >= %d",
		resp.LastCommandSeq, restoredSeq)
}

// The peer reads which destinations this engine sends from its health poll.
func TestHandlePeerHealth_ListsSendingDestinations(t *testing.T) {
	programRelay := distribution.NewRelay()
	sw := switcher.NewTestSwitcher(t, programRelay)
	mock := &mockOutputManager{
		sendingIDs: []string{"0a1b2c3d"},
		pairIDs:    []string{"0a1b2c3d", "4e5f6a7b"},
		armedIDs:   []string{"0a1b2c3d"},
	}
	api := NewAPI(sw, WithOutputManager(mock))

	req := httptest.NewRequest(http.MethodGet, "/api/peer/health", nil)
	w := httptest.NewRecorder()
	api.Mux().ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code)

	var resp peer.HealthResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Equal(t, []string{"0a1b2c3d"}, resp.Sending)
	assert.Equal(t, []string{"0a1b2c3d", "4e5f6a7b"}, resp.Destinations)
	assert.Equal(t, []string{"0a1b2c3d"}, resp.Armed)
}
