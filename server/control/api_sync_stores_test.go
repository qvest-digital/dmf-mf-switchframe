package control

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/zsiec/prism/distribution"
	"github.com/zsiec/switchframe/server/peer"
	"github.com/zsiec/switchframe/server/switcher"
)

// With a store syncer the engine serves its stores to the peer engine,
// which holds the API token, and to no operator: the stores hold the
// operators' tokens.
func TestStoreSyncRoutes(t *testing.T) {
	s := peer.NewStoreSyncer(peer.StoreSyncConfig{TmpDir: filepath.Join(t.TempDir(), "tmp"), Token: "api-token"})
	api := NewAPI(switcher.NewTestSwitcher(t, distribution.NewRelay()), WithStoreSyncer(s))
	get := func(path, token string) *httptest.ResponseRecorder {
		req := httptest.NewRequest("GET", path, nil)
		req.Header.Set("Authorization", "Bearer "+token)
		rec := httptest.NewRecorder()
		api.Mux().ServeHTTP(rec, req)
		return rec
	}
	rec := get("/api/sync/stores", "api-token")
	require.Equal(t, http.StatusOK, rec.Code)
	var m peer.StoreManifest
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&m))
	require.Equal(t, http.StatusNotFound, get("/api/sync/stores/item?store=none&key=x", "api-token").Code)

	require.Equal(t, http.StatusForbidden, get("/api/sync/stores", "operator-token").Code)
	require.Equal(t, http.StatusForbidden, get("/api/sync/stores/item?store=operators&key=x", "operator-token").Code)
}
