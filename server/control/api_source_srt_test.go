package control

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/zsiec/prism/distribution"
	"github.com/zsiec/switchframe/server/srt"
	"github.com/zsiec/switchframe/server/switcher"
)

// mockSRTManager implements SRTManager for testing.
type mockSRTManager struct {
	createPullCalls []mockCreatePullCall
	stopPullCalls   []string
	getStatsCalls   []string
	updateLatCalls  []mockUpdateLatCall

	createPullErr error
	getStatsResp  interface{}
	getStatsFound bool
	updateLatErr  error

	// Track which keys are "SRT" sources (for delete/update validation)
	srtKeys map[string]bool
}

type mockCreatePullCall struct {
	address   string
	streamID  string
	label     string
	latencyMs int
}

type mockUpdateLatCall struct {
	key       string
	latencyMs int
}

func newMockSRTManager() *mockSRTManager {
	return &mockSRTManager{
		srtKeys:       make(map[string]bool),
		getStatsFound: true,
		getStatsResp: map[string]interface{}{
			"connected":  true,
			"rttMs":      1.5,
			"lossRate":   0.01,
			"remoteAddr": "192.168.1.100:6464",
		},
	}
}

func (m *mockSRTManager) CreatePull(ctx context.Context, address, streamID, label string, latencyMs int) (string, error) {
	m.createPullCalls = append(m.createPullCalls, mockCreatePullCall{address, streamID, label, latencyMs})
	if m.createPullErr != nil {
		return "", m.createPullErr
	}
	key := "srt:" + streamID
	m.srtKeys[key] = true
	return key, nil
}

func (m *mockSRTManager) StopPull(key string) error {
	m.stopPullCalls = append(m.stopPullCalls, key)
	if !m.srtKeys[key] {
		return ErrNotSRTSource
	}
	return nil
}

func (m *mockSRTManager) GetStats(key string) (interface{}, bool) {
	m.getStatsCalls = append(m.getStatsCalls, key)
	return m.getStatsResp, m.getStatsFound
}

func (m *mockSRTManager) UpdateLatency(key string, latencyMs int) error {
	m.updateLatCalls = append(m.updateLatCalls, mockUpdateLatCall{key, latencyMs})
	if !m.srtKeys[key] {
		return ErrNotSRTSource
	}
	return m.updateLatErr
}

func setupSRTTestAPI(t *testing.T) (*API, *switcher.Switcher, *mockSRTManager) {
	t.Helper()
	programRelay := distribution.NewRelay()
	sw := switcher.New(programRelay)
	r1 := distribution.NewRelay()
	sw.RegisterSource("camera1", r1)
	r2 := distribution.NewRelay()
	sw.RegisterSource("camera2", r2)

	mock := newMockSRTManager()
	api := NewAPI(sw, WithSRTManager(mock))
	return api, sw, mock
}

// --- POST /api/sources (create SRT pull) ---

func TestCreateSource_ValidSRTPull(t *testing.T) {
	api, _, mock := setupSRTTestAPI(t)

	body := `{"type":"srt","mode":"caller","address":"srt://192.168.1.100:6464","streamID":"camera3","label":"Camera 3","latencyMs":200}`
	req := httptest.NewRequest("POST", "/api/sources", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)

	require.Equal(t, http.StatusCreated, rec.Code, "body: %s", rec.Body.String())
	require.Len(t, mock.createPullCalls, 1)
	require.Equal(t, "srt://192.168.1.100:6464", mock.createPullCalls[0].address)
	require.Equal(t, "camera3", mock.createPullCalls[0].streamID)
	require.Equal(t, "Camera 3", mock.createPullCalls[0].label)
	require.Equal(t, 200, mock.createPullCalls[0].latencyMs)

	// Verify response contains the key
	var resp map[string]interface{}
	err := json.NewDecoder(rec.Body).Decode(&resp)
	require.NoError(t, err)
	require.Equal(t, "srt:camera3", resp["key"])
}

func TestCreateSource_MissingAddress(t *testing.T) {
	api, _, _ := setupSRTTestAPI(t)

	body := `{"type":"srt","mode":"caller","streamID":"camera3"}`
	req := httptest.NewRequest("POST", "/api/sources", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)

	require.Equal(t, http.StatusBadRequest, rec.Code, "body: %s", rec.Body.String())
}

func TestCreateSource_MissingStreamID(t *testing.T) {
	api, _, _ := setupSRTTestAPI(t)

	body := `{"type":"srt","mode":"caller","address":"srt://192.168.1.100:6464"}`
	req := httptest.NewRequest("POST", "/api/sources", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)

	require.Equal(t, http.StatusBadRequest, rec.Code, "body: %s", rec.Body.String())
}

func TestCreateSource_NonSRTType(t *testing.T) {
	api, _, _ := setupSRTTestAPI(t)

	body := `{"type":"rtmp","mode":"caller","address":"rtmp://example.com/live","streamID":"cam1"}`
	req := httptest.NewRequest("POST", "/api/sources", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)

	require.Equal(t, http.StatusBadRequest, rec.Code, "body: %s", rec.Body.String())
}

func TestCreateSource_InvalidMode(t *testing.T) {
	api, _, _ := setupSRTTestAPI(t)

	// Only "caller" mode is supported for creation via API.
	body := `{"type":"srt","mode":"listener","address":"srt://192.168.1.100:6464","streamID":"cam1"}`
	req := httptest.NewRequest("POST", "/api/sources", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)

	require.Equal(t, http.StatusBadRequest, rec.Code, "body: %s", rec.Body.String())
}

func TestCreateSource_InvalidJSON(t *testing.T) {
	api, _, _ := setupSRTTestAPI(t)

	req := httptest.NewRequest("POST", "/api/sources", strings.NewReader("{bad"))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)

	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestCreateSource_NoSRTManager(t *testing.T) {
	api, _ := setupTestAPI(t) // no SRT manager attached

	body := `{"type":"srt","mode":"caller","address":"srt://192.168.1.100:6464","streamID":"cam1"}`
	req := httptest.NewRequest("POST", "/api/sources", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)

	require.Equal(t, http.StatusNotImplemented, rec.Code)
}

func TestCreateSource_ManagerError(t *testing.T) {
	api, _, mock := setupSRTTestAPI(t)
	mock.createPullErr = errors.New("connection refused")

	body := `{"type":"srt","mode":"caller","address":"srt://192.168.1.100:6464","streamID":"cam1"}`
	req := httptest.NewRequest("POST", "/api/sources", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)

	require.Equal(t, http.StatusInternalServerError, rec.Code, "body: %s", rec.Body.String())
}

func TestCreateSource_ValidationError(t *testing.T) {
	api, _, mock := setupSRTTestAPI(t)
	// Simulate a validation error from the SRT manager (wraps srt.ErrInvalidConfig).
	mock.createPullErr = fmt.Errorf("srt caller: %w: streamID is required", srt.ErrInvalidConfig)

	body := `{"type":"srt","mode":"caller","address":"srt://192.168.1.100:6464","streamID":"cam1"}`
	req := httptest.NewRequest("POST", "/api/sources", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)

	require.Equal(t, http.StatusBadRequest, rec.Code, "validation errors should return 400, body: %s", rec.Body.String())
}

func TestCreateSource_DefaultLatency(t *testing.T) {
	api, _, mock := setupSRTTestAPI(t)

	// No latencyMs specified — should pass 0 (manager decides default)
	body := `{"type":"srt","mode":"caller","address":"srt://192.168.1.100:6464","streamID":"cam1"}`
	req := httptest.NewRequest("POST", "/api/sources", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)

	require.Equal(t, http.StatusCreated, rec.Code, "body: %s", rec.Body.String())
	require.Len(t, mock.createPullCalls, 1)
	require.Equal(t, 0, mock.createPullCalls[0].latencyMs)
}

// --- DELETE /api/sources/{key} ---

func TestDeleteSource_SRTSource(t *testing.T) {
	api, _, mock := setupSRTTestAPI(t)
	mock.srtKeys["srt:camera3"] = true

	req := httptest.NewRequest("DELETE", "/api/sources/srt:camera3", nil)
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)

	require.Equal(t, http.StatusNoContent, rec.Code)
	require.Len(t, mock.stopPullCalls, 1)
	require.Equal(t, "srt:camera3", mock.stopPullCalls[0])
}

func TestDeleteSource_NonSRTSource(t *testing.T) {
	api, _, _ := setupSRTTestAPI(t)

	// "camera1" is a demo/prism source — should reject deletion
	req := httptest.NewRequest("DELETE", "/api/sources/camera1", nil)
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)

	require.Equal(t, http.StatusMethodNotAllowed, rec.Code, "body: %s", rec.Body.String())
}

func TestDeleteSource_NoSRTManager(t *testing.T) {
	api, _ := setupTestAPI(t) // no SRT manager

	req := httptest.NewRequest("DELETE", "/api/sources/srt:cam1", nil)
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)

	require.Equal(t, http.StatusNotImplemented, rec.Code)
}

// --- GET /api/sources/{key}/srt/stats ---

func TestGetSourceSRTStats_Success(t *testing.T) {
	api, _, mock := setupSRTTestAPI(t)
	mock.srtKeys["srt:camera3"] = true

	req := httptest.NewRequest("GET", "/api/sources/srt:camera3/srt/stats", nil)
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code, "body: %s", rec.Body.String())
	require.Len(t, mock.getStatsCalls, 1)
	require.Equal(t, "srt:camera3", mock.getStatsCalls[0])

	var resp map[string]interface{}
	err := json.NewDecoder(rec.Body).Decode(&resp)
	require.NoError(t, err)
	require.Equal(t, true, resp["connected"])
}

func TestGetSourceSRTStats_NotFound(t *testing.T) {
	api, _, mock := setupSRTTestAPI(t)
	mock.getStatsFound = false

	req := httptest.NewRequest("GET", "/api/sources/srt:nonexistent/srt/stats", nil)
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)

	require.Equal(t, http.StatusNotFound, rec.Code, "body: %s", rec.Body.String())
}

func TestGetSourceSRTStats_NoSRTManager(t *testing.T) {
	api, _ := setupTestAPI(t) // no SRT manager

	req := httptest.NewRequest("GET", "/api/sources/srt:cam1/srt/stats", nil)
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)

	require.Equal(t, http.StatusNotImplemented, rec.Code)
}

// --- PUT /api/sources/{key}/srt ---

func TestUpdateSourceSRT_Success(t *testing.T) {
	api, _, mock := setupSRTTestAPI(t)
	mock.srtKeys["srt:camera3"] = true

	body := `{"latencyMs":300}`
	req := httptest.NewRequest("PUT", "/api/sources/srt:camera3/srt", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code, "body: %s", rec.Body.String())
	require.Len(t, mock.updateLatCalls, 1)
	require.Equal(t, "srt:camera3", mock.updateLatCalls[0].key)
	require.Equal(t, 300, mock.updateLatCalls[0].latencyMs)
}

func TestUpdateSourceSRT_NotSRT(t *testing.T) {
	api, _, _ := setupSRTTestAPI(t)

	body := `{"latencyMs":300}`
	req := httptest.NewRequest("PUT", "/api/sources/camera1/srt", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)

	require.Equal(t, http.StatusNotFound, rec.Code, "body: %s", rec.Body.String())
}

func TestUpdateSourceSRT_InvalidJSON(t *testing.T) {
	api, _, _ := setupSRTTestAPI(t)

	req := httptest.NewRequest("PUT", "/api/sources/srt:cam1/srt", strings.NewReader("{bad"))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)

	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestUpdateSourceSRT_NoSRTManager(t *testing.T) {
	api, _ := setupTestAPI(t) // no SRT manager

	body := `{"latencyMs":300}`
	req := httptest.NewRequest("PUT", "/api/sources/srt:cam1/srt", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)

	require.Equal(t, http.StatusNotImplemented, rec.Code)
}

func TestUpdateSourceSRT_NegativeLatency(t *testing.T) {
	api, _, mock := setupSRTTestAPI(t)
	mock.srtKeys["srt:camera3"] = true

	body := `{"latencyMs":-1}`
	req := httptest.NewRequest("PUT", "/api/sources/srt:camera3/srt", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)

	require.Equal(t, http.StatusBadRequest, rec.Code, "body: %s", rec.Body.String())
}

func TestUpdateSourceSRT_LatencyTooHigh(t *testing.T) {
	api, _, mock := setupSRTTestAPI(t)
	mock.srtKeys["srt:camera3"] = true

	body := `{"latencyMs":15000}`
	req := httptest.NewRequest("PUT", "/api/sources/srt:camera3/srt", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)

	require.Equal(t, http.StatusBadRequest, rec.Code, "body: %s", rec.Body.String())
}

// --- GET /api/sources/{key} ---

func TestGetSource_Found(t *testing.T) {
	api, _, _ := setupSRTTestAPI(t)

	// "camera1" is registered by setupSRTTestAPI.
	req := httptest.NewRequest("GET", "/api/sources/camera1", nil)
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code, "body: %s", rec.Body.String())

	var resp map[string]interface{}
	err := json.NewDecoder(rec.Body).Decode(&resp)
	require.NoError(t, err)
	require.Equal(t, "camera1", resp["key"])
}

func TestGetSource_NotFound(t *testing.T) {
	api, _, _ := setupSRTTestAPI(t)

	req := httptest.NewRequest("GET", "/api/sources/nonexistent", nil)
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)

	require.Equal(t, http.StatusNotFound, rec.Code, "body: %s", rec.Body.String())
}
