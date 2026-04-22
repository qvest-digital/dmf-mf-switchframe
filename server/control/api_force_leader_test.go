package control

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/zsiec/prism/distribution"

	"github.com/zsiec/switchframe/server/peer"
	"github.com/zsiec/switchframe/server/switcher"
)

func setupForceLeaderAPI(t *testing.T, secret string, p *peer.HealthPoller) *API {
	t.Helper()
	programRelay := distribution.NewRelay()
	sw := switcher.NewTestSwitcher(t, programRelay)
	return NewAPI(sw, WithPeerPoller(p), WithControlPlaneSecret(secret))
}

func TestForceLeader_501WhenNoSecretConfigured(t *testing.T) {
	p := peer.NewHealthPoller("http://peer", peer.WithLocalIdentity("b"))
	api := setupForceLeaderAPI(t, "", p)

	req := httptest.NewRequest("POST", "/api/peer/force-leader",
		strings.NewReader(`{"reason":"operator takeover"}`))
	req.Header.Set("X-Control-Plane-Secret", "anything")
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)

	require.Equal(t, http.StatusNotImplemented, rec.Code, "body: %s", rec.Body.String())
}

func TestForceLeader_501WhenNoPollerConfigured(t *testing.T) {
	programRelay := distribution.NewRelay()
	sw := switcher.NewTestSwitcher(t, programRelay)
	api := NewAPI(sw, WithControlPlaneSecret("s3cret"))

	req := httptest.NewRequest("POST", "/api/peer/force-leader",
		strings.NewReader(`{"reason":"operator takeover"}`))
	req.Header.Set("X-Control-Plane-Secret", "s3cret")
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)

	require.Equal(t, http.StatusNotImplemented, rec.Code, "body: %s", rec.Body.String())
}

func TestForceLeader_403OnMissingSecret(t *testing.T) {
	p := peer.NewHealthPoller("http://peer", peer.WithLocalIdentity("b"))
	api := setupForceLeaderAPI(t, "s3cret", p)

	req := httptest.NewRequest("POST", "/api/peer/force-leader",
		strings.NewReader(`{"reason":"hack"}`))
	// No header.
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)

	require.Equal(t, http.StatusForbidden, rec.Code, "body: %s", rec.Body.String())
}

func TestForceLeader_403OnWrongSecret(t *testing.T) {
	p := peer.NewHealthPoller("http://peer", peer.WithLocalIdentity("b"))
	api := setupForceLeaderAPI(t, "s3cret", p)

	req := httptest.NewRequest("POST", "/api/peer/force-leader",
		strings.NewReader(`{"reason":"hack"}`))
	req.Header.Set("X-Control-Plane-Secret", "wrong")
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)

	require.Equal(t, http.StatusForbidden, rec.Code, "body: %s", rec.Body.String())
}

func TestForceLeader_BumpsEpochAndReturnsRole(t *testing.T) {
	// Start in partitioned-follower; after force-leader we should be leader.
	p := peer.NewHealthPoller("http://peer", peer.WithLocalIdentity("b"))
	p.SetPeerStateForTest(false, "a", 0)
	require.Equal(t, peer.RoleFollowerPartitioned, p.Role(), "precondition")

	api := setupForceLeaderAPI(t, "s3cret", p)

	req := httptest.NewRequest("POST", "/api/peer/force-leader",
		strings.NewReader(`{"reason":"AZ-a dead, taking over from B"}`))
	req.Header.Set("X-Control-Plane-Secret", "s3cret")
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code, "body: %s", rec.Body.String())

	var resp struct {
		NewEpoch uint64 `json:"newEpoch"`
		Role     string `json:"role"`
		Label    string `json:"label"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	require.Equal(t, uint64(1), resp.NewEpoch)
	require.Equal(t, "leader", resp.Role)
	require.Equal(t, "b", resp.Label)

	// Poller's state reflects the bump.
	require.Equal(t, uint64(1), p.LocalEpoch())
	require.Equal(t, peer.RoleLeader, p.Role())
}

func TestForceLeader_MultipleBumpsAreCumulative(t *testing.T) {
	p := peer.NewHealthPoller("http://peer", peer.WithLocalIdentity("a"))
	api := setupForceLeaderAPI(t, "s3cret", p)

	for i := uint64(1); i <= 3; i++ {
		req := httptest.NewRequest("POST", "/api/peer/force-leader",
			strings.NewReader(`{"reason":"repeat"}`))
		req.Header.Set("X-Control-Plane-Secret", "s3cret")
		rec := httptest.NewRecorder()
		api.Mux().ServeHTTP(rec, req)
		require.Equal(t, http.StatusOK, rec.Code)

		var resp struct {
			NewEpoch uint64 `json:"newEpoch"`
		}
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
		require.Equal(t, i, resp.NewEpoch, "expected monotonic bumps")
	}
}

func TestForceLeader_EndpointIsAuthExempt(t *testing.T) {
	// The endpoint is secret-authenticated, not API-key-authenticated —
	// must be in the auth-exempt set so the middleware doesn't 401
	// before the handler gets to check the header.
	require.True(t, authExemptPaths["/api/peer/force-leader"])
}
