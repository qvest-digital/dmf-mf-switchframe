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
