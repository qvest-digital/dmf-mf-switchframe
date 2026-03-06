package control

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/zsiec/prism/distribution"
	"github.com/zsiec/switchframe/server/output"
	"github.com/zsiec/switchframe/server/switcher"
)

// mockOutputManager implements OutputManagerAPI for testing.
type mockOutputManager struct {
	recording      bool
	srtActive      bool
	startRecErr    error
	stopRecErr     error
	startSRTErr    error
	stopSRTErr     error
	lastRecConfig  output.RecorderConfig
	lastSRTConfig  output.SRTOutputConfig
	thumbnail      []byte
}

func (m *mockOutputManager) StartRecording(config output.RecorderConfig) error {
	if m.startRecErr != nil {
		return m.startRecErr
	}
	m.lastRecConfig = config
	m.recording = true
	return nil
}

func (m *mockOutputManager) StopRecording() error {
	if m.stopRecErr != nil {
		return m.stopRecErr
	}
	m.recording = false
	return nil
}

func (m *mockOutputManager) RecordingStatus() output.RecordingStatus {
	return output.RecordingStatus{
		Active:       m.recording,
		Filename:     "test-2026-03-04.ts",
		BytesWritten: 1024,
		DurationSecs: 10.5,
	}
}

func (m *mockOutputManager) StartSRTOutput(config output.SRTOutputConfig) error {
	if m.startSRTErr != nil {
		return m.startSRTErr
	}
	m.lastSRTConfig = config
	m.srtActive = true
	return nil
}

func (m *mockOutputManager) StopSRTOutput() error {
	if m.stopSRTErr != nil {
		return m.stopSRTErr
	}
	m.srtActive = false
	return nil
}

func (m *mockOutputManager) SRTOutputStatus() output.SRTOutputStatus {
	return output.SRTOutputStatus{
		Active:  m.srtActive,
		Mode:    "caller",
		Address: "192.168.1.100",
		Port:    9000,
	}
}

func (m *mockOutputManager) ConfidenceThumbnail() []byte {
	return m.thumbnail
}

func setupOutputTestAPI(t *testing.T) (*API, *mockOutputManager) {
	t.Helper()
	programRelay := distribution.NewRelay()
	sw := switcher.New(programRelay)
	mock := &mockOutputManager{}
	api := NewAPI(sw, WithOutputManager(mock))
	return api, mock
}

func setupOutputTestAPINoManager(t *testing.T) *API {
	t.Helper()
	programRelay := distribution.NewRelay()
	sw := switcher.New(programRelay)
	api := NewAPI(sw)
	return api
}

// --- Recording tests ---

func TestRecordingStart(t *testing.T) {
	api, mock := setupOutputTestAPI(t)

	body := `{"outputDir":"/tmp/recordings"}`
	req := httptest.NewRequest("POST", "/api/recording/start", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code, "body: %s", rec.Body.String())
	require.Equal(t, "/tmp/recordings", mock.lastRecConfig.Dir)
	require.True(t, mock.recording)
}

func TestRecordingStartWithRotationParams(t *testing.T) {
	api, mock := setupOutputTestAPI(t)

	body := `{"outputDir":"/tmp/recordings","rotateAfterMins":30,"maxFileSizeMB":500}`
	req := httptest.NewRequest("POST", "/api/recording/start", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code, "body: %s", rec.Body.String())
	require.True(t, mock.recording)
	require.Equal(t, "/tmp/recordings", mock.lastRecConfig.Dir)
	require.Equal(t, 30*time.Minute, mock.lastRecConfig.RotateAfter)
	require.Equal(t, int64(500*1024*1024), mock.lastRecConfig.MaxFileSize)
}

func TestRecordingStartDefaultRotation(t *testing.T) {
	api, mock := setupOutputTestAPI(t)

	body := `{"outputDir":"/tmp/recordings"}`
	req := httptest.NewRequest("POST", "/api/recording/start", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code, "body: %s", rec.Body.String())
	require.True(t, mock.recording)
	require.Equal(t, time.Hour, mock.lastRecConfig.RotateAfter,
		"should default to 1 hour rotation")
	require.Equal(t, int64(0), mock.lastRecConfig.MaxFileSize,
		"should default to no file size limit")
}

func TestRecordingStartAlreadyActive(t *testing.T) {
	api, mock := setupOutputTestAPI(t)
	mock.startRecErr = output.ErrRecorderActive

	body := `{"outputDir":"/tmp/recordings"}`
	req := httptest.NewRequest("POST", "/api/recording/start", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)

	require.Equal(t, http.StatusConflict, rec.Code)
}

func TestRecordingStartRelativeDir(t *testing.T) {
	api, _ := setupOutputTestAPI(t)

	body := `{"outputDir":"../../../etc/recordings"}`
	req := httptest.NewRequest("POST", "/api/recording/start", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)

	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestRecordingStartInvalidJSON(t *testing.T) {
	api, _ := setupOutputTestAPI(t)

	req := httptest.NewRequest("POST", "/api/recording/start", strings.NewReader("{bad"))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)

	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestRecordingStop(t *testing.T) {
	api, mock := setupOutputTestAPI(t)
	mock.recording = true

	req := httptest.NewRequest("POST", "/api/recording/stop", nil)
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code, "body: %s", rec.Body.String())
	require.False(t, mock.recording)
}

func TestRecordingStopNotActive(t *testing.T) {
	api, mock := setupOutputTestAPI(t)
	mock.stopRecErr = output.ErrRecorderNotActive

	req := httptest.NewRequest("POST", "/api/recording/stop", nil)
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)

	require.Equal(t, http.StatusConflict, rec.Code)
}

func TestRecordingStatus(t *testing.T) {
	api, mock := setupOutputTestAPI(t)
	mock.recording = true

	req := httptest.NewRequest("GET", "/api/recording/status", nil)
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)

	var status output.RecordingStatus
	err := json.NewDecoder(rec.Body).Decode(&status)
	require.NoError(t, err)
	require.True(t, status.Active)
	require.Equal(t, "test-2026-03-04.ts", status.Filename)
	require.Equal(t, int64(1024), status.BytesWritten)
	require.Equal(t, 10.5, status.DurationSecs)
}

func TestRecordingEndpointsNoManager(t *testing.T) {
	api := setupOutputTestAPINoManager(t)

	tests := []struct {
		method string
		path   string
		body   string
	}{
		{"POST", "/api/recording/start", `{"outputDir":"/tmp"}`},
		{"POST", "/api/recording/stop", ""},
		{"GET", "/api/recording/status", ""},
	}

	for _, tt := range tests {
		t.Run(tt.method+" "+tt.path, func(t *testing.T) {
			var bodyReader *strings.Reader
			if tt.body != "" {
				bodyReader = strings.NewReader(tt.body)
			}
			var req *http.Request
			if bodyReader != nil {
				req = httptest.NewRequest(tt.method, tt.path, bodyReader)
			} else {
				req = httptest.NewRequest(tt.method, tt.path, nil)
			}
			req.Header.Set("Content-Type", "application/json")
			rec := httptest.NewRecorder()
			api.Mux().ServeHTTP(rec, req)

			require.Equal(t, http.StatusNotImplemented, rec.Code,
				"path=%s body=%s", tt.path, rec.Body.String())
		})
	}
}

// --- SRT tests ---

func TestSRTStart(t *testing.T) {
	api, mock := setupOutputTestAPI(t)

	body := `{"mode":"caller","address":"192.168.1.100","port":9000,"latency":200,"streamID":"live"}`
	req := httptest.NewRequest("POST", "/api/output/srt/start", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code, "body: %s", rec.Body.String())
	require.True(t, mock.srtActive)
	require.Equal(t, "caller", mock.lastSRTConfig.Mode)
	require.Equal(t, "192.168.1.100", mock.lastSRTConfig.Address)
	require.Equal(t, 9000, mock.lastSRTConfig.Port)
	require.Equal(t, 200, mock.lastSRTConfig.Latency)
	require.Equal(t, "live", mock.lastSRTConfig.StreamID)
}

func TestSRTStartInvalidMode(t *testing.T) {
	api, _ := setupOutputTestAPI(t)

	body := `{"mode":"invalid","address":"192.168.1.100","port":9000}`
	req := httptest.NewRequest("POST", "/api/output/srt/start", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)

	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestSRTStartMissingPort(t *testing.T) {
	api, _ := setupOutputTestAPI(t)

	body := `{"mode":"caller","address":"192.168.1.100"}`
	req := httptest.NewRequest("POST", "/api/output/srt/start", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)

	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestSRTStartAlreadyActive(t *testing.T) {
	api, mock := setupOutputTestAPI(t)
	mock.startSRTErr = output.ErrSRTActive

	body := `{"mode":"caller","address":"192.168.1.100","port":9000}`
	req := httptest.NewRequest("POST", "/api/output/srt/start", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)

	require.Equal(t, http.StatusConflict, rec.Code)
}

func TestSRTStartInvalidJSON(t *testing.T) {
	api, _ := setupOutputTestAPI(t)

	req := httptest.NewRequest("POST", "/api/output/srt/start", strings.NewReader("{bad"))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)

	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestSRTStop(t *testing.T) {
	api, mock := setupOutputTestAPI(t)
	mock.srtActive = true

	req := httptest.NewRequest("POST", "/api/output/srt/stop", nil)
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code, "body: %s", rec.Body.String())
	require.False(t, mock.srtActive)
}

func TestSRTStopNotActive(t *testing.T) {
	api, mock := setupOutputTestAPI(t)
	mock.stopSRTErr = output.ErrSRTNotActive

	req := httptest.NewRequest("POST", "/api/output/srt/stop", nil)
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)

	require.Equal(t, http.StatusConflict, rec.Code)
}

func TestSRTStatus(t *testing.T) {
	api, mock := setupOutputTestAPI(t)
	mock.srtActive = true

	req := httptest.NewRequest("GET", "/api/output/srt/status", nil)
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)

	var status output.SRTOutputStatus
	err := json.NewDecoder(rec.Body).Decode(&status)
	require.NoError(t, err)
	require.True(t, status.Active)
	require.Equal(t, "caller", status.Mode)
	require.Equal(t, "192.168.1.100", status.Address)
	require.Equal(t, 9000, status.Port)
}

func TestSRTEndpointsNoManager(t *testing.T) {
	api := setupOutputTestAPINoManager(t)

	tests := []struct {
		method string
		path   string
		body   string
	}{
		{"POST", "/api/output/srt/start", `{"mode":"caller","address":"192.168.1.100","port":9000}`},
		{"POST", "/api/output/srt/stop", ""},
		{"GET", "/api/output/srt/status", ""},
	}

	for _, tt := range tests {
		t.Run(tt.method+" "+tt.path, func(t *testing.T) {
			var req *http.Request
			if tt.body != "" {
				req = httptest.NewRequest(tt.method, tt.path, strings.NewReader(tt.body))
			} else {
				req = httptest.NewRequest(tt.method, tt.path, nil)
			}
			req.Header.Set("Content-Type", "application/json")
			rec := httptest.NewRecorder()
			api.Mux().ServeHTTP(rec, req)

			require.Equal(t, http.StatusNotImplemented, rec.Code,
				"path=%s body=%s", tt.path, rec.Body.String())
		})
	}
}

func TestSRTStartListenerMode(t *testing.T) {
	api, mock := setupOutputTestAPI(t)

	body := `{"mode":"listener","port":9000,"latency":125}`
	req := httptest.NewRequest("POST", "/api/output/srt/start", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code, "body: %s", rec.Body.String())
	require.True(t, mock.srtActive)
	require.Equal(t, "listener", mock.lastSRTConfig.Mode)
	require.Equal(t, 9000, mock.lastSRTConfig.Port)
	require.Equal(t, 125, mock.lastSRTConfig.Latency)
}

// --- Confidence thumbnail tests ---

func TestConfidenceEndpoint_ReturnsThumbnail(t *testing.T) {
	api, mock := setupOutputTestAPI(t)
	// Set a fake JPEG (SOI marker)
	mock.thumbnail = []byte{0xFF, 0xD8, 0xFF, 0xE0, 0x00, 0x10}

	req := httptest.NewRequest("GET", "/api/output/confidence", nil)
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, "image/jpeg", rec.Header().Get("Content-Type"))
	require.Equal(t, "no-cache, max-age=1", rec.Header().Get("Cache-Control"))
	require.Equal(t, mock.thumbnail, rec.Body.Bytes())
}

func TestConfidenceEndpoint_NoContent(t *testing.T) {
	api, _ := setupOutputTestAPI(t)

	req := httptest.NewRequest("GET", "/api/output/confidence", nil)
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)

	require.Equal(t, http.StatusNoContent, rec.Code)
}

func TestConfidenceEndpoint_NoManager(t *testing.T) {
	api := setupOutputTestAPINoManager(t)

	req := httptest.NewRequest("GET", "/api/output/confidence", nil)
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)

	require.Equal(t, http.StatusNotImplemented, rec.Code)
}

func TestSRTStartCallerMissingAddress(t *testing.T) {
	api, _ := setupOutputTestAPI(t)

	body := `{"mode":"caller","port":9000}`
	req := httptest.NewRequest("POST", "/api/output/srt/start", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)

	require.Equal(t, http.StatusBadRequest, rec.Code)
}
