package control

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/zsiec/prism/distribution"
	"github.com/zsiec/switchframe/server/internal"
	"github.com/zsiec/switchframe/server/replay"
	"github.com/zsiec/switchframe/server/switcher"
)

func setupTestAPIWithReplay(t *testing.T) (*API, *replay.Manager) {
	t.Helper()
	programRelay := distribution.NewRelay()
	sw := switcher.New(programRelay)
	r1 := distribution.NewRelay()
	sw.RegisterSource("camera1", r1)
	r2 := distribution.NewRelay()
	sw.RegisterSource("camera2", r2)

	replayRelay := distribution.NewRelay()
	mgr := replay.NewManager(replayRelay, replay.DefaultConfig(), nil, nil)

	api := NewAPI(sw, WithReplayManager(mgr))
	return api, mgr
}

func TestReplayPause_NotEnabled(t *testing.T) {
	programRelay := distribution.NewRelay()
	sw := switcher.New(programRelay)
	api := NewAPI(sw) // no replay manager
	req := httptest.NewRequest("POST", "/api/replay/pause", nil)
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)
	// Route not registered when replayMgr is nil, so 404.
	require.Equal(t, http.StatusNotFound, rec.Code)
}

func TestReplayPause_NoActivePlayer(t *testing.T) {
	api, _ := setupTestAPIWithReplay(t)
	req := httptest.NewRequest("POST", "/api/replay/pause", nil)
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)
	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestReplayResume_NoActivePlayer(t *testing.T) {
	api, _ := setupTestAPIWithReplay(t)
	req := httptest.NewRequest("POST", "/api/replay/resume", nil)
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)
	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestReplaySeek_NoActivePlayer(t *testing.T) {
	api, _ := setupTestAPIWithReplay(t)
	body := `{"position":0.5}`
	req := httptest.NewRequest("PATCH", "/api/replay/seek", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)
	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestReplaySeek_InvalidJSON(t *testing.T) {
	api, _ := setupTestAPIWithReplay(t)
	body := `not json`
	req := httptest.NewRequest("PATCH", "/api/replay/seek", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)
	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestReplaySpeed_NoActivePlayer(t *testing.T) {
	api, _ := setupTestAPIWithReplay(t)
	body := `{"speed":0.5}`
	req := httptest.NewRequest("PATCH", "/api/replay/speed", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)
	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestReplaySpeed_InvalidJSON(t *testing.T) {
	api, _ := setupTestAPIWithReplay(t)
	body := `not json`
	req := httptest.NewRequest("PATCH", "/api/replay/speed", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)
	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestReplayMarks_InvalidJSON(t *testing.T) {
	api, _ := setupTestAPIWithReplay(t)
	body := `not json`
	req := httptest.NewRequest("PATCH", "/api/replay/marks", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)
	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestReplayMarks_Success(t *testing.T) {
	api, mgr := setupTestAPIWithReplay(t)
	// Add a source and set mark-in first so SetMarks has a context.
	_ = mgr.AddSource("camera1")

	markIn := int64(1000)
	body := `{"markIn":1000}`
	req := httptest.NewRequest("PATCH", "/api/replay/marks", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)

	var state internal.ControlRoomState
	err := json.NewDecoder(rec.Body).Decode(&state)
	require.NoError(t, err)
	_ = markIn // used in body
}

func TestReplayQuick_InvalidJSON(t *testing.T) {
	api, _ := setupTestAPIWithReplay(t)
	body := `not json`
	req := httptest.NewRequest("POST", "/api/replay/quick", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)
	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestReplayQuick_ZeroSeconds(t *testing.T) {
	api, _ := setupTestAPIWithReplay(t)
	body := `{"seconds":0,"speed":0.5}`
	req := httptest.NewRequest("POST", "/api/replay/quick", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)
	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestReplayQuick_NegativeSeconds(t *testing.T) {
	api, _ := setupTestAPIWithReplay(t)
	body := `{"seconds":-5,"speed":0.5}`
	req := httptest.NewRequest("POST", "/api/replay/quick", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)
	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestReplayPeek_MissingSource(t *testing.T) {
	api, _ := setupTestAPIWithReplay(t)
	req := httptest.NewRequest("GET", "/api/replay/peek", nil)
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)
	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestReplayPeek_UnknownSource(t *testing.T) {
	api, _ := setupTestAPIWithReplay(t)
	req := httptest.NewRequest("GET", "/api/replay/peek?source=nonexistent", nil)
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)
	require.Equal(t, http.StatusNotFound, rec.Code)
}

func TestReplayPeek_NoContent(t *testing.T) {
	api, mgr := setupTestAPIWithReplay(t)
	_ = mgr.AddSource("camera1")
	req := httptest.NewRequest("GET", "/api/replay/peek?source=camera1", nil)
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)
	// PeekFrame returns nil data for now, so 204.
	require.Equal(t, http.StatusNoContent, rec.Code)
}
