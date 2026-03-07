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

func TestSetSourcePosition(t *testing.T) {
	api, sw := setupTestAPI(t)
	defer sw.Close()

	// Set camera1 to position 5
	body := strings.NewReader(`{"position":5}`)
	req := httptest.NewRequest("PUT", "/api/sources/camera1/position", body)
	w := httptest.NewRecorder()
	api.Mux().ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code, "body: %s", w.Body.String())

	// Verify in state
	req = httptest.NewRequest("GET", "/api/switch/state", nil)
	w = httptest.NewRecorder()
	api.Mux().ServeHTTP(w, req)
	var state internal.ControlRoomState
	_ = json.NewDecoder(w.Body).Decode(&state)
	require.Equal(t, 5, state.Sources["camera1"].Position)
}

func TestSetSourcePosition_InvalidJSON(t *testing.T) {
	api, sw := setupTestAPI(t)
	defer sw.Close()

	req := httptest.NewRequest("PUT", "/api/sources/camera1/position", strings.NewReader("{bad"))
	w := httptest.NewRecorder()
	api.Mux().ServeHTTP(w, req)
	require.Equal(t, http.StatusBadRequest, w.Code)
}

func TestSetSourcePosition_UnknownSource(t *testing.T) {
	api, sw := setupTestAPI(t)
	defer sw.Close()

	body := strings.NewReader(`{"position":1}`)
	req := httptest.NewRequest("PUT", "/api/sources/nonexistent/position", body)
	w := httptest.NewRecorder()
	api.Mux().ServeHTTP(w, req)
	require.Equal(t, http.StatusNotFound, w.Code)
}

func TestSetSourcePosition_ZeroPosition(t *testing.T) {
	api, sw := setupTestAPI(t)
	defer sw.Close()

	body := strings.NewReader(`{"position":0}`)
	req := httptest.NewRequest("PUT", "/api/sources/camera1/position", body)
	w := httptest.NewRecorder()
	api.Mux().ServeHTTP(w, req)
	require.Equal(t, http.StatusBadRequest, w.Code)
}

func TestSetSourcePosition_NegativePosition(t *testing.T) {
	api, sw := setupTestAPI(t)
	defer sw.Close()

	body := strings.NewReader(`{"position":-1}`)
	req := httptest.NewRequest("PUT", "/api/sources/camera1/position", body)
	w := httptest.NewRecorder()
	api.Mux().ServeHTTP(w, req)
	require.Equal(t, http.StatusBadRequest, w.Code)
}

func TestSetSourcePosition_SwapPositions(t *testing.T) {
	api, sw := setupTestAPI(t)
	defer sw.Close()

	// Get initial positions (camera1=1, camera2=2 from setupTestAPI registration order)
	req := httptest.NewRequest("GET", "/api/switch/state", nil)
	w := httptest.NewRecorder()
	api.Mux().ServeHTTP(w, req)
	var state internal.ControlRoomState
	_ = json.NewDecoder(w.Body).Decode(&state)
	cam1Pos := state.Sources["camera1"].Position
	cam2Pos := state.Sources["camera2"].Position
	require.NotEqual(t, cam1Pos, cam2Pos, "initial positions must differ")

	// Set camera1 to camera2's position — should trigger a swap
	body := strings.NewReader(`{"position":` + strings.TrimSpace(jsonInt(cam2Pos)) + `}`)
	req = httptest.NewRequest("PUT", "/api/sources/camera1/position", body)
	w = httptest.NewRecorder()
	api.Mux().ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code, "body: %s", w.Body.String())

	// Verify positions were swapped
	req = httptest.NewRequest("GET", "/api/switch/state", nil)
	w = httptest.NewRecorder()
	api.Mux().ServeHTTP(w, req)
	_ = json.NewDecoder(w.Body).Decode(&state)
	require.Equal(t, cam2Pos, state.Sources["camera1"].Position, "camera1 should now have camera2's old position")
	require.Equal(t, cam1Pos, state.Sources["camera2"].Position, "camera2 should now have camera1's old position")
}

// jsonInt converts an int to its JSON string representation.
func jsonInt(v int) string {
	b, _ := json.Marshal(v)
	return string(b)
}
