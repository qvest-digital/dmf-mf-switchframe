package control

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/zsiec/prism/distribution"

	"github.com/zsiec/switchframe/server/output"
	"github.com/zsiec/switchframe/server/peer"
	"github.com/zsiec/switchframe/server/switcher"
)

// partitionedFollowerPoller builds a HealthPoller in the partitioned-follower
// role: local label "b", peer unreachable with known label "a" and equal epoch.
func partitionedFollowerPoller(t *testing.T) *peer.HealthPoller {
	t.Helper()
	p := peer.NewHealthPoller("http://unused", peer.WithLocalIdentity("b"))
	p.SetPeerStateForTest(false, "a", 0)
	return p
}

// leaderPoller builds a HealthPoller in the leader role: local label "a",
// peer unreachable, so the lexical tiebreak picks us.
func leaderPoller(t *testing.T) *peer.HealthPoller {
	t.Helper()
	p := peer.NewHealthPoller("http://unused", peer.WithLocalIdentity("a"))
	p.SetPeerStateForTest(false, "b", 0)
	return p
}

func setupOutputAPIWithPoller(t *testing.T, p *peer.HealthPoller) (*API, *mockOutputManager) {
	t.Helper()
	programRelay := distribution.NewRelay()
	sw := switcher.NewTestSwitcher(t, programRelay)
	mock := &mockOutputManager{}
	api := NewAPI(sw, WithOutputManager(mock), WithPeerPoller(p))
	return api, mock
}

func TestSRTStart_RejectsOnPartitionedFollower(t *testing.T) {
	api, mock := setupOutputAPIWithPoller(t, partitionedFollowerPoller(t))

	body := `{"mode":"caller","address":"10.0.0.1","port":5000}`
	req := httptest.NewRequest("POST", "/api/output/srt/start", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)

	require.Equal(t, http.StatusConflict, rec.Code, "body: %s", rec.Body.String())
	require.Contains(t, rec.Body.String(), "gated")
	require.False(t, mock.srtActive, "output manager must not have been called")
}

func TestSRTStart_AllowsOnLeader(t *testing.T) {
	api, mock := setupOutputAPIWithPoller(t, leaderPoller(t))

	body := `{"mode":"caller","address":"10.0.0.1","port":5000}`
	req := httptest.NewRequest("POST", "/api/output/srt/start", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code, "body: %s", rec.Body.String())
	require.True(t, mock.srtActive)
}

func TestSRTStart_AllowsOnStandalone(t *testing.T) {
	// Single-engine (no label) — must not gate.
	api, mock := setupOutputTestAPI(t)

	body := `{"mode":"caller","address":"10.0.0.1","port":5000}`
	req := httptest.NewRequest("POST", "/api/output/srt/start", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code, "body: %s", rec.Body.String())
	require.True(t, mock.srtActive)
}

func TestRecordingStart_RejectsOnPartitionedFollower(t *testing.T) {
	api, mock := setupOutputAPIWithPoller(t, partitionedFollowerPoller(t))

	body := `{"outputDir":"/tmp/recordings"}`
	req := httptest.NewRequest("POST", "/api/recording/start", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)

	require.Equal(t, http.StatusConflict, rec.Code, "body: %s", rec.Body.String())
	require.Contains(t, rec.Body.String(), "gated")
	require.False(t, mock.recording, "output manager must not have been called")
}

func TestRecordingStart_AllowsOnLeader(t *testing.T) {
	api, mock := setupOutputAPIWithPoller(t, leaderPoller(t))

	body := `{"outputDir":"/tmp/recordings"}`
	req := httptest.NewRequest("POST", "/api/recording/start", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code, "body: %s", rec.Body.String())
	require.True(t, mock.recording)
}

// A partitioned follower accepting reassigns would diverge per-engine state:
// concurrent reassigns to opposite values on each side persist locally with no
// merge at heal. Rejecting on the follower forces cleanup through the leader
// (or force-leader promotion), which linearizes the write.
func TestReassignDestinationEngine_RejectsOnPartitionedFollower(t *testing.T) {
	api, mock := setupOutputAPIWithPoller(t, partitionedFollowerPoller(t))
	mock.destinations = map[string]output.DestinationStatus{
		"dest-1": {
			ID:     "dest-1",
			Config: output.DestinationConfig{Type: "srt-caller", Address: "10.0.0.1", Port: 5000, Engine: "a"},
			State:  "stopped",
		},
	}

	body := `{"engine":"b"}`
	req := httptest.NewRequest("PUT", "/api/output/destinations/dest-1/engine", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)

	require.Equal(t, http.StatusConflict, rec.Code, "body: %s", rec.Body.String())
	require.Contains(t, rec.Body.String(), "gated")
	require.Equal(t, "a", mock.destinations["dest-1"].Config.Engine,
		"follower must not have persisted the reassign locally")
}

func TestReassignDestinationEngine_AllowsOnLeader(t *testing.T) {
	api, mock := setupOutputAPIWithPoller(t, leaderPoller(t))
	mock.destinations = map[string]output.DestinationStatus{
		"dest-1": {
			ID:     "dest-1",
			Config: output.DestinationConfig{Type: "srt-caller", Address: "10.0.0.1", Port: 5000, Engine: "a"},
			State:  "stopped",
		},
	}

	body := `{"engine":"b"}`
	req := httptest.NewRequest("PUT", "/api/output/destinations/dest-1/engine", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code, "body: %s", rec.Body.String())
	require.Equal(t, "b", mock.destinations["dest-1"].Config.Engine)
}

func TestReassignDestinationEngine_AllowsOnStandalone(t *testing.T) {
	api, mock := setupOutputTestAPI(t)
	mock.destinations = map[string]output.DestinationStatus{
		"dest-1": {
			ID:     "dest-1",
			Config: output.DestinationConfig{Type: "srt-caller", Address: "10.0.0.1", Port: 5000, Engine: "a"},
			State:  "stopped",
		},
	}

	body := `{"engine":"b"}`
	req := httptest.NewRequest("PUT", "/api/output/destinations/dest-1/engine", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code, "body: %s", rec.Body.String())
	require.Equal(t, "b", mock.destinations["dest-1"].Config.Engine)
}

// Engine A (leader) and engine B (follower) each receive opposite reassigns
// during a partition. The follower's 409 leaves its record unchanged, so at
// heal the only surviving mutation is the leader's — no divergence.
func TestReassignDestinationEngine_FollowerRejectPreservesConvergence(t *testing.T) {
	apiLeader, mockLeader := setupOutputAPIWithPoller(t, leaderPoller(t))
	apiFollower, mockFollower := setupOutputAPIWithPoller(t, partitionedFollowerPoller(t))

	for _, m := range []*mockOutputManager{mockLeader, mockFollower} {
		m.destinations = map[string]output.DestinationStatus{
			"dest-1": {
				ID:     "dest-1",
				Config: output.DestinationConfig{Type: "srt-caller", Address: "10.0.0.1", Port: 5000, Engine: "a"},
				State:  "stopped",
			},
		}
	}

	reqLeader := httptest.NewRequest("PUT", "/api/output/destinations/dest-1/engine", strings.NewReader(`{"engine":"b"}`))
	reqLeader.Header.Set("Content-Type", "application/json")
	recLeader := httptest.NewRecorder()
	apiLeader.Mux().ServeHTTP(recLeader, reqLeader)

	reqFollower := httptest.NewRequest("PUT", "/api/output/destinations/dest-1/engine", strings.NewReader(`{"engine":"a"}`))
	reqFollower.Header.Set("Content-Type", "application/json")
	recFollower := httptest.NewRecorder()
	apiFollower.Mux().ServeHTTP(recFollower, reqFollower)

	require.Equal(t, http.StatusOK, recLeader.Code, "leader body: %s", recLeader.Body.String())
	require.Equal(t, http.StatusConflict, recFollower.Code, "follower body: %s", recFollower.Body.String())
	require.Equal(t, "b", mockLeader.destinations["dest-1"].Config.Engine)
	require.Equal(t, "a", mockFollower.destinations["dest-1"].Config.Engine,
		"follower's local state must remain unchanged so heal surfaces the leader's value")
}
