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

func TestAPI_FTBDurationValidation(t *testing.T) {
	api, sw := setupTestAPI(t)
	defer sw.Close()

	cases := []struct {
		name       string
		body       string
		wantStatus int
		wantValue  int // expected sw.State().FTBDurationMs after the call
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
			req := httptest.NewRequest("PUT", "/api/switch/ftb-duration", strings.NewReader(tc.body))
			req.Header.Set("Content-Type", "application/json")
			rec := httptest.NewRecorder()
			api.Mux().ServeHTTP(rec, req)

			require.Equal(t, tc.wantStatus, rec.Code, "body: %s", rec.Body.String())
			require.Equal(t, tc.wantValue, sw.State().FTBDurationMs)
		})
	}
}

func TestAPI_FTBDurationInvalidJSON(t *testing.T) {
	api, sw := setupTestAPI(t)
	defer sw.Close()

	req := httptest.NewRequest("PUT", "/api/switch/ftb-duration", strings.NewReader("{not-json"))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)

	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestAPI_FTBDurationMissingField(t *testing.T) {
	api, sw := setupTestAPI(t)
	defer sw.Close()

	req := httptest.NewRequest("PUT", "/api/switch/ftb-duration", strings.NewReader(`{}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)

	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.Equal(t, 1000, sw.State().FTBDurationMs, "default preserved after rejection")
}

func TestAPI_FTBDurationResponseIncludesState(t *testing.T) {
	api, sw := setupTestAPI(t)
	defer sw.Close()

	req := httptest.NewRequest("PUT", "/api/switch/ftb-duration", strings.NewReader(`{"durationMs":2500}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code, "body: %s", rec.Body.String())

	var state internal.ControlRoomState
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&state))
	require.Equal(t, 2500, state.FTBDurationMs)
}
