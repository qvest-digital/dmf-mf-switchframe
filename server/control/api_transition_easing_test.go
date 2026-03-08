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

func TestTransitionWithEasingPreset(t *testing.T) {
	api, sw := setupTransitionTestAPI(t)
	defer sw.Close()

	body := `{"source":"camera2","type":"mix","durationMs":1000,"easing":{"type":"ease-in-out"}}`
	req := httptest.NewRequest("POST", "/api/switch/transition", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code, "body: %s", rec.Body.String())

	// Verify response is valid ControlRoomState with transition info
	var state internal.ControlRoomState
	err := json.NewDecoder(rec.Body).Decode(&state)
	require.NoError(t, err, "response should be valid ControlRoomState JSON")
	require.True(t, state.InTransition, "expected InTransition=true")
}

func TestTransitionWithEasingCustom(t *testing.T) {
	api, sw := setupTransitionTestAPI(t)
	defer sw.Close()

	body := `{"source":"camera2","type":"mix","durationMs":1000,"easing":{"type":"custom","x1":0.25,"y1":0.1,"x2":0.25,"y2":1.0}}`
	req := httptest.NewRequest("POST", "/api/switch/transition", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code, "body: %s", rec.Body.String())

	// Verify response is valid ControlRoomState with transition info
	var state internal.ControlRoomState
	err := json.NewDecoder(rec.Body).Decode(&state)
	require.NoError(t, err, "response should be valid ControlRoomState JSON")
	require.True(t, state.InTransition, "expected InTransition=true")
}

func TestTransitionWithInvalidEasingType(t *testing.T) {
	api, sw := setupTransitionTestAPI(t)
	defer sw.Close()

	body := `{"source":"camera2","type":"mix","durationMs":1000,"easing":{"type":"bogus"}}`
	req := httptest.NewRequest("POST", "/api/switch/transition", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)

	require.Equal(t, http.StatusBadRequest, rec.Code, "body: %s", rec.Body.String())
}

func TestTransitionWithInvalidCustomEasingX(t *testing.T) {
	api, sw := setupTransitionTestAPI(t)
	defer sw.Close()

	body := `{"source":"camera2","type":"mix","durationMs":1000,"easing":{"type":"custom","x1":-0.5,"y1":0,"x2":0.5,"y2":1}}`
	req := httptest.NewRequest("POST", "/api/switch/transition", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)

	require.Equal(t, http.StatusBadRequest, rec.Code, "body: %s", rec.Body.String())
}

func TestTransitionWithDefaultEasing(t *testing.T) {
	api, sw := setupTransitionTestAPI(t)
	defer sw.Close()

	// No easing field at all — backward compatible, should use default smoothstep.
	body := `{"source":"camera2","type":"mix","durationMs":1000}`
	req := httptest.NewRequest("POST", "/api/switch/transition", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code, "body: %s", rec.Body.String())
}
