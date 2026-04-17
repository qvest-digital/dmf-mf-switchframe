package control

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/zsiec/prism/distribution"

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
	sw := switcher.NewTestSwitcher(programRelay)
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
