package control

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/zsiec/switchframe/server/layout"
)

// TestLayoutStoreNilDoesNotPanic verifies that layout preset endpoints
// return proper HTTP errors (not a nil-pointer panic) when the API has a
// layoutCompositor but no layoutStore.
func TestLayoutStoreNilDoesNotPanic(t *testing.T) {
	_, sw := setupTestAPI(t)
	lc := layout.NewCompositor(1920, 1080)

	// Create API with layoutCompositor but WITHOUT layoutStore.
	api := NewAPI(sw, WithLayoutCompositor(lc))

	t.Run("preset routes not registered when layoutStore is nil", func(t *testing.T) {
		// When layoutStore is nil, preset routes should not be registered.
		// The mux will return 404 for unregistered paths.
		endpoints := []struct {
			method string
			path   string
			body   string
		}{
			{"GET", "/api/layout/presets", ""},
			{"POST", "/api/layout/presets", `{"name":"test"}`},
			{"DELETE", "/api/layout/presets/test", ""},
		}

		for _, ep := range endpoints {
			var req *http.Request
			if ep.body != "" {
				req = httptest.NewRequest(ep.method, ep.path, strings.NewReader(ep.body))
				req.Header.Set("Content-Type", "application/json")
			} else {
				req = httptest.NewRequest(ep.method, ep.path, nil)
			}
			rec := httptest.NewRecorder()

			// Must not panic.
			api.Mux().ServeHTTP(rec, req)

			// Unregistered routes return 404 or 405 from the mux.
			require.True(t, rec.Code >= 400 && rec.Code < 500,
				"%s %s: expected 4xx, got %d: %s", ep.method, ep.path, rec.Code, rec.Body.String())
		}
	})

	t.Run("handleSetLayout with preset name and nil layoutStore", func(t *testing.T) {
		// PUT /api/layout with a preset name that is not a built-in preset
		// should return 404, not panic on nil layoutStore.
		body := `{"preset":"nonexistent_custom_preset"}`
		req := httptest.NewRequest("PUT", "/api/layout", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()

		// Must not panic.
		api.Mux().ServeHTTP(rec, req)

		require.Equal(t, http.StatusNotFound, rec.Code,
			"expected 404 for unknown preset with nil layoutStore, got: %s", rec.Body.String())
	})
}
