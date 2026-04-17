package control

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/zsiec/prism/distribution"

	"github.com/zsiec/switchframe/server/peer"
	"github.com/zsiec/switchframe/server/switcher"
)

func TestHandlePeerHealth(t *testing.T) {
	programRelay := distribution.NewRelay()
	sw := switcher.NewTestSwitcher(programRelay)
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
	sw := switcher.NewTestSwitcher(programRelay)
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
