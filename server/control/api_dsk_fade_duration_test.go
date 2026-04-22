package control

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/zsiec/prism/distribution"
	"github.com/zsiec/switchframe/server/graphics"
	"github.com/zsiec/switchframe/server/internal"
	"github.com/zsiec/switchframe/server/switcher"
)

// setupDSKFadeTestAPI builds an API wired to a real graphics.Compositor so
// the PUT handler flows end-to-end into compositor state. The enrich fn
// mirrors production wiring (app_state.go) so the handler's response body
// carries the top-level DSKFadeDurationMs that normally comes from the
// enriched state path.
func setupDSKFadeTestAPI(t *testing.T) (*API, *graphics.Compositor) {
	t.Helper()
	programRelay := distribution.NewRelay()
	sw := switcher.NewTestSwitcher(t, programRelay)
	comp := graphics.NewCompositor()
	t.Cleanup(comp.Close)
	api := NewAPI(sw, WithCompositor(comp))
	api.SetEnrichFunc(func(s internal.ControlRoomState) internal.ControlRoomState {
		s.DSKFadeDurationMs = comp.DSKFadeDurationMs()
		return s
	})
	return api, comp
}

func TestAPI_DSKFadeDurationValidation(t *testing.T) {
	api, comp := setupDSKFadeTestAPI(t)

	cases := []struct {
		name       string
		body       string
		wantStatus int
		wantValue  int // expected comp.DSKFadeDurationMs() after the call
	}{
		{"below_min_rejected", `{"durationMs":50}`, http.StatusBadRequest, 1000},
		{"just_below_min_rejected", `{"durationMs":99}`, http.StatusBadRequest, 1000},
		{"min_accepted", `{"durationMs":100}`, http.StatusOK, 100},
		{"mid_range_accepted", `{"durationMs":1500}`, http.StatusOK, 1500},
		{"max_accepted", `{"durationMs":5000}`, http.StatusOK, 5000},
		{"just_above_max_rejected", `{"durationMs":5001}`, http.StatusBadRequest, 5000},
		{"zero_rejected", `{"durationMs":0}`, http.StatusBadRequest, 5000},
		{"negative_rejected", `{"durationMs":-1}`, http.StatusBadRequest, 5000},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest("PUT", "/api/graphics/dsk-fade-duration", strings.NewReader(tc.body))
			req.Header.Set("Content-Type", "application/json")
			rec := httptest.NewRecorder()
			api.Mux().ServeHTTP(rec, req)

			require.Equal(t, tc.wantStatus, rec.Code, "body: %s", rec.Body.String())
			require.Equal(t, tc.wantValue, comp.DSKFadeDurationMs())
		})
	}
}

func TestAPI_DSKFadeDurationInvalidJSON(t *testing.T) {
	api, comp := setupDSKFadeTestAPI(t)

	req := httptest.NewRequest("PUT", "/api/graphics/dsk-fade-duration", strings.NewReader("{not-json"))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)

	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.Equal(t, 1000, comp.DSKFadeDurationMs(), "default preserved after rejection")
}

func TestAPI_DSKFadeDurationMissingField(t *testing.T) {
	api, comp := setupDSKFadeTestAPI(t)

	req := httptest.NewRequest("PUT", "/api/graphics/dsk-fade-duration", strings.NewReader(`{}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)

	require.Equal(t, http.StatusBadRequest, rec.Code,
		"empty body decodes durationMs=0 which is below the [100,5000] range")
	require.Equal(t, 1000, comp.DSKFadeDurationMs(), "default preserved after rejection")
}

func TestAPI_DSKFadeDurationResponseIncludesState(t *testing.T) {
	api, _ := setupDSKFadeTestAPI(t)

	req := httptest.NewRequest("PUT", "/api/graphics/dsk-fade-duration", strings.NewReader(`{"durationMs":2500}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code, "body: %s", rec.Body.String())

	var state internal.ControlRoomState
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&state))
	require.Equal(t, 2500, state.DSKFadeDurationMs)
}
