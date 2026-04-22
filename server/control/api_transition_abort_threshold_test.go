package control

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/zsiec/switchframe/server/internal"
)

func TestAPI_TransitionAbortThresholdValidation(t *testing.T) {
	api, sw := setupTestAPI(t)
	defer sw.Close()

	cases := []struct {
		name       string
		body       string
		wantStatus int
		wantValue  float64 // expected sw.State().TransitionAbortThreshold after the call
	}{
		{"below_min_rejected", `{"threshold":0.04}`, http.StatusBadRequest, 0.3},
		{"zero_rejected", `{"threshold":0}`, http.StatusBadRequest, 0.3},
		{"min_accepted", `{"threshold":0.05}`, http.StatusOK, 0.05},
		{"mid_range_accepted", `{"threshold":0.25}`, http.StatusOK, 0.25},
		{"max_accepted", `{"threshold":0.5}`, http.StatusOK, 0.5},
		{"just_above_max_rejected", `{"threshold":0.51}`, http.StatusBadRequest, 0.5},
		{"negative_rejected", `{"threshold":-0.1}`, http.StatusBadRequest, 0.5},
		{"one_rejected", `{"threshold":1.0}`, http.StatusBadRequest, 0.5},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest("PUT", "/api/switch/transition-abort-threshold", strings.NewReader(tc.body))
			req.Header.Set("Content-Type", "application/json")
			rec := httptest.NewRecorder()
			api.Mux().ServeHTTP(rec, req)

			require.Equal(t, tc.wantStatus, rec.Code, "body: %s", rec.Body.String())
			require.InDelta(t, tc.wantValue, sw.State().TransitionAbortThreshold, 1e-9)
		})
	}
}

func TestAPI_TransitionAbortThresholdInvalidJSON(t *testing.T) {
	api, sw := setupTestAPI(t)
	defer sw.Close()

	req := httptest.NewRequest("PUT", "/api/switch/transition-abort-threshold", strings.NewReader("{not-json"))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)

	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestAPI_TransitionAbortThresholdMissingField(t *testing.T) {
	api, sw := setupTestAPI(t)
	defer sw.Close()

	req := httptest.NewRequest("PUT", "/api/switch/transition-abort-threshold", strings.NewReader(`{}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)

	// Zero value fails range check → 400 and default preserved.
	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.InDelta(t, 0.3, sw.State().TransitionAbortThreshold, 1e-9)
}

func TestAPI_TransitionAbortThresholdResponseIncludesState(t *testing.T) {
	api, sw := setupTestAPI(t)
	defer sw.Close()

	req := httptest.NewRequest("PUT", "/api/switch/transition-abort-threshold", strings.NewReader(`{"threshold":0.2}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code, "body: %s", rec.Body.String())

	var state internal.ControlRoomState
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&state))
	require.InDelta(t, 0.2, state.TransitionAbortThreshold, 1e-9)
}
