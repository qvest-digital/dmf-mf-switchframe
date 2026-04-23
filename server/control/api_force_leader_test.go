package control

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/stretchr/testify/require"
	"github.com/zsiec/prism/distribution"

	"github.com/zsiec/switchframe/server/metrics"
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

// TestForceLeader_ProxiedOperatorHeaderIsAudited verifies the engine
// records the browser-side operator attribution relayed by the cloud
// proxy.  The audit trail must identify WHO triggered the epoch bump,
// not just that the control plane's shared secret was used.
func TestForceLeader_ProxiedOperatorHeaderIsAudited(t *testing.T) {
	p := peer.NewHealthPoller("http://peer", peer.WithLocalIdentity("b"))
	p.SetPeerStateForTest(false, "a", 0)
	api := setupForceLeaderAPI(t, "s3cret", p)

	req := httptest.NewRequest("POST", "/api/peer/force-leader",
		strings.NewReader(`{"reason":"AZ-a dead — operator override"}`))
	req.Header.Set("X-Control-Plane-Secret", "s3cret")
	req.Header.Set("X-Cloud-Proxied-Operator", "alice@example.com")
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code, "body: %s", rec.Body.String())

	var resp struct {
		NewEpoch uint64 `json:"newEpoch"`
		Operator string `json:"operator"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	require.Equal(t, uint64(1), resp.NewEpoch)
	require.Equal(t, "alice@example.com", resp.Operator,
		"response must echo back the proxied operator for cloud-side audit correlation")
}

// TestForceLeader_OperatorHeaderSanitized rejects header values with
// control characters or excessive length — defense against log-injection
// and metric cardinality explosion.
func TestForceLeader_OperatorHeaderSanitized(t *testing.T) {
	p := peer.NewHealthPoller("http://peer", peer.WithLocalIdentity("b"))
	api := setupForceLeaderAPI(t, "s3cret", p)

	cases := []struct {
		name     string
		operator string
		wantEcho string
	}{
		{"control chars stripped", "alice\x00\n\rbob", "alicebob"},
		{"length capped at 128", strings.Repeat("x", 200), strings.Repeat("x", 128)},
		{"empty header falls back to control-plane", "", "control-plane"},
		{"leading/trailing whitespace trimmed", "  alice  ", "alice"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest("POST", "/api/peer/force-leader",
				strings.NewReader(`{"reason":"test"}`))
			req.Header.Set("X-Control-Plane-Secret", "s3cret")
			if tc.operator != "" {
				req.Header.Set("X-Cloud-Proxied-Operator", tc.operator)
			}
			rec := httptest.NewRecorder()
			api.Mux().ServeHTTP(rec, req)

			require.Equal(t, http.StatusOK, rec.Code)
			var resp struct {
				Operator string `json:"operator"`
			}
			require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
			require.Equal(t, tc.wantEcho, resp.Operator)
		})
	}
}

// TestForceLeader_SanitizeStripsUnicodeCardinalityVectors pins the
// sanitizer's defense against zero-width and line-separator runes that
// would otherwise let visually-identical operator strings register as
// distinct Prometheus series and explode cardinality.
func TestForceLeader_SanitizeStripsUnicodeCardinalityVectors(t *testing.T) {
	cases := []struct {
		name  string
		input string
		want  string
	}{
		{"zero-width space", "al​ice", "alice"},
		{"zero-width joiner", "al‍ice", "alice"},
		{"line separator", "al ice", "alice"},
		{"paragraph separator", "al ice", "alice"},
		{"C1 next-line", "alice", "alice"},
		{"bidi override rejected", "al‮ice", "alice"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, sanitizeOperatorHeader(tc.input),
				"Unicode cardinality vector %q must be stripped to %q", tc.input, tc.want)
		})
	}
}

// TestForceLeader_ReasonClassIsClosedEnum pins the reason_class label
// contract: only the listed values pass through; unknown tokens bucket
// to "other" so free-text reasons can't explode Prometheus cardinality.
func TestForceLeader_ReasonClassIsClosedEnum(t *testing.T) {
	cases := []struct {
		name   string
		reason string
		want   string
	}{
		{"partition matched", "partition", "partition"},
		{"partition first token", "partition recovery from AZ loss", "partition"},
		{"recovery", "recovery", "recovery"},
		{"maintenance", "maintenance window", "maintenance"},
		{"test matched", "test runbook", "test"},
		{"drill matched", "drill tabletop", "drill"},
		{"unknown buckets to other", "adhoc override", "other"},
		{"gibberish buckets to other", "xyzzy", "other"},
		{"empty -> unspecified", "", "unspecified"},
		{"whitespace-only -> unspecified", "   ", "unspecified"},
		{"case-insensitive match", "PARTITION", "partition"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, reasonClassOf(tc.reason))
		})
	}
}

// TestForceLeader_MetricIncremented exercises the metric surface —
// switchframe_force_leader_total{operator, reason_class} — so partition-
// ed failover rates are observable in Prometheus.
func TestForceLeader_MetricIncremented(t *testing.T) {
	reg := prometheus.NewRegistry()
	m := metrics.NewMetrics(reg)

	p := peer.NewHealthPoller("http://peer", peer.WithLocalIdentity("b"))
	programRelay := distribution.NewRelay()
	sw := switcher.NewTestSwitcher(t, programRelay)
	api := NewAPI(sw,
		WithPeerPoller(p),
		WithControlPlaneSecret("s3cret"),
		WithAppMetrics(m))

	req := httptest.NewRequest("POST", "/api/peer/force-leader",
		strings.NewReader(`{"reason":"partition recovery"}`))
	req.Header.Set("X-Control-Plane-Secret", "s3cret")
	req.Header.Set("X-Cloud-Proxied-Operator", "alice")
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)

	got := testutil.ToFloat64(m.ForceLeaderTotal.WithLabelValues("alice", "partition"))
	require.Equal(t, float64(1), got,
		"force_leader_total must tick once for the successful call")
}
