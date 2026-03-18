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
	recording         bool
	recFilename       string // active recording filename (default: "test-2026-03-04.ts")
	srtActive         bool
	startRecErr       error
	stopRecErr        error
	startSRTErr       error
	stopSRTErr        error
	lastRecConfig     output.RecorderConfig
	lastSRTConfig     output.SRTConfig
	thumbnail         []byte
	recDroppedPackets int64

	// Multi-destination fields.
	destinations   map[string]output.DestinationStatus
	addDestErr     error
	removeDestErr  error
	startDestErr   error
	stopDestErr    error
	lastDestConfig output.DestinationConfig
	lastDestID     string

	// CBR status.
	cbrStatus *output.CBRPacerStatus
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
	fn := m.recFilename
	if fn == "" {
		fn = "test-2026-03-04.ts"
	}
	return output.RecordingStatus{
		Active:         m.recording,
		Filename:       fn,
		BytesWritten:   1024,
		DurationSecs:   10.5,
		DroppedPackets: m.recDroppedPackets,
	}
}

func (m *mockOutputManager) StartSRTOutput(config output.SRTConfig) error {
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

func (m *mockOutputManager) SRTOutputStatus() output.SRTStatus {
	return output.SRTStatus{
		Active:  m.srtActive,
		Mode:    "caller",
		Address: "192.168.1.100",
		Port:    9000,
	}
}

func (m *mockOutputManager) ConfidenceThumbnail() []byte {
	return m.thumbnail
}

func (m *mockOutputManager) AddDestination(config output.DestinationConfig) (string, error) {
	if m.addDestErr != nil {
		return "", m.addDestErr
	}
	m.lastDestConfig = config
	id := "test-dest-1"
	if m.destinations == nil {
		m.destinations = make(map[string]output.DestinationStatus)
	}
	m.destinations[id] = output.DestinationStatus{
		ID:     id,
		Config: config,
		State:  "stopped",
	}
	return id, nil
}

func (m *mockOutputManager) RemoveDestination(id string) error {
	if m.removeDestErr != nil {
		return m.removeDestErr
	}
	m.lastDestID = id
	delete(m.destinations, id)
	return nil
}

func (m *mockOutputManager) StartDestination(id string) error {
	if m.startDestErr != nil {
		return m.startDestErr
	}
	m.lastDestID = id
	if d, ok := m.destinations[id]; ok {
		d.State = "active"
		m.destinations[id] = d
	}
	return nil
}

func (m *mockOutputManager) StopDestination(id string) error {
	if m.stopDestErr != nil {
		return m.stopDestErr
	}
	m.lastDestID = id
	if d, ok := m.destinations[id]; ok {
		d.State = "stopped"
		m.destinations[id] = d
	}
	return nil
}

func (m *mockOutputManager) ListDestinations() []output.DestinationStatus {
	result := make([]output.DestinationStatus, 0, len(m.destinations))
	for _, d := range m.destinations {
		result = append(result, d)
	}
	return result
}

func (m *mockOutputManager) GetDestination(id string) (output.DestinationStatus, error) {
	if d, ok := m.destinations[id]; ok {
		return d, nil
	}
	return output.DestinationStatus{}, output.ErrDestinationNotFound
}

func (m *mockOutputManager) CBRStatus() *output.CBRPacerStatus {
	return m.cbrStatus
}

func setupOutputTestAPI(t *testing.T) (*API, *mockOutputManager) {
	t.Helper()
	programRelay := distribution.NewRelay()
	sw := switcher.NewTestSwitcher(programRelay)
	mock := &mockOutputManager{}
	api := NewAPI(sw, WithOutputManager(mock))
	return api, mock
}

func setupOutputTestAPINoManager(t *testing.T) *API {
	t.Helper()
	programRelay := distribution.NewRelay()
	sw := switcher.NewTestSwitcher(programRelay)
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

func TestRecordingStartAbsolutePathTraversal(t *testing.T) {
	api, _ := setupOutputTestAPI(t)

	// An absolute path containing ".." resolves via filepath.Clean to a
	// different directory ("/tmp/../../etc/sensitive" -> "/etc/sensitive").
	// This should be rejected because the raw path contains "..".
	tests := []struct {
		name string
		dir  string
	}{
		{"traversal via tmp", "/tmp/../../etc/sensitive"},
		{"traversal via var", "/var/log/../../etc/passwd"},
		{"double dot in middle", "/recordings/../../../root"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			body := `{"outputDir":"` + tt.dir + `"}`
			req := httptest.NewRequest("POST", "/api/recording/start", strings.NewReader(body))
			req.Header.Set("Content-Type", "application/json")
			rec := httptest.NewRecorder()
			api.Mux().ServeHTTP(rec, req)

			require.Equal(t, http.StatusBadRequest, rec.Code, "path %q should be rejected", tt.dir)
		})
	}
}

func TestRecordingStartValidAbsolutePath(t *testing.T) {
	api, mock := setupOutputTestAPI(t)

	// A clean absolute path without ".." should be accepted.
	body := `{"outputDir":"/tmp/switchframe-recordings"}`
	req := httptest.NewRequest("POST", "/api/recording/start", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code, "body: %s", rec.Body.String())
	require.Equal(t, "/tmp/switchframe-recordings", mock.lastRecConfig.Dir)
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

	var status output.SRTStatus
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
	require.Equal(t, "no-store", rec.Header().Get("Cache-Control"))
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

// --- Multi-Destination tests ---

func TestAddDestination(t *testing.T) {
	api, mock := setupOutputTestAPI(t)

	body := `{"type":"srt-caller","address":"192.168.1.100","port":9000,"name":"YouTube"}`
	req := httptest.NewRequest("POST", "/api/output/destinations", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)

	require.Equal(t, http.StatusCreated, rec.Code, "body: %s", rec.Body.String())
	require.Equal(t, "srt-caller", mock.lastDestConfig.Type)
	require.Equal(t, "192.168.1.100", mock.lastDestConfig.Address)
	require.Equal(t, 9000, mock.lastDestConfig.Port)
	require.Equal(t, "YouTube", mock.lastDestConfig.Name)

	var status output.DestinationStatus
	err := json.NewDecoder(rec.Body).Decode(&status)
	require.NoError(t, err)
	require.Equal(t, "test-dest-1", status.ID)
	require.Equal(t, "stopped", status.State)
}

func TestAddDestination_InvalidType(t *testing.T) {
	api, _ := setupOutputTestAPI(t)

	body := `{"type":"rtmp","port":9000}`
	req := httptest.NewRequest("POST", "/api/output/destinations", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)

	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestAddDestination_MissingPort(t *testing.T) {
	api, _ := setupOutputTestAPI(t)

	body := `{"type":"srt-caller","address":"192.168.1.100"}`
	req := httptest.NewRequest("POST", "/api/output/destinations", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)

	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestAddDestination_CallerMissingAddress(t *testing.T) {
	api, _ := setupOutputTestAPI(t)

	body := `{"type":"srt-caller","port":9000}`
	req := httptest.NewRequest("POST", "/api/output/destinations", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)

	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestAddDestination_InvalidJSON(t *testing.T) {
	api, _ := setupOutputTestAPI(t)

	req := httptest.NewRequest("POST", "/api/output/destinations", strings.NewReader("{bad"))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)

	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestListDestinations(t *testing.T) {
	api, mock := setupOutputTestAPI(t)
	mock.destinations = map[string]output.DestinationStatus{
		"d1": {ID: "d1", Config: output.DestinationConfig{Type: "srt-caller", Name: "YouTube"}, State: "active"},
		"d2": {ID: "d2", Config: output.DestinationConfig{Type: "srt-listener", Name: "CDN"}, State: "stopped"},
	}

	req := httptest.NewRequest("GET", "/api/output/destinations", nil)
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)

	var dests []output.DestinationStatus
	err := json.NewDecoder(rec.Body).Decode(&dests)
	require.NoError(t, err)
	require.Len(t, dests, 2)
}

func TestListDestinations_Empty(t *testing.T) {
	api, _ := setupOutputTestAPI(t)

	req := httptest.NewRequest("GET", "/api/output/destinations", nil)
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)

	var dests []output.DestinationStatus
	err := json.NewDecoder(rec.Body).Decode(&dests)
	require.NoError(t, err)
	require.Empty(t, dests)
}

func TestGetDestination(t *testing.T) {
	api, mock := setupOutputTestAPI(t)
	mock.destinations = map[string]output.DestinationStatus{
		"d1": {ID: "d1", Config: output.DestinationConfig{Type: "srt-caller", Name: "YouTube"}, State: "active"},
	}

	req := httptest.NewRequest("GET", "/api/output/destinations/d1", nil)
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)

	var status output.DestinationStatus
	err := json.NewDecoder(rec.Body).Decode(&status)
	require.NoError(t, err)
	require.Equal(t, "d1", status.ID)
	require.Equal(t, "YouTube", status.Config.Name)
}

func TestGetDestination_NotFound(t *testing.T) {
	api, _ := setupOutputTestAPI(t)

	req := httptest.NewRequest("GET", "/api/output/destinations/nonexistent", nil)
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)

	require.Equal(t, http.StatusNotFound, rec.Code)
}

func TestRemoveDestination(t *testing.T) {
	api, mock := setupOutputTestAPI(t)
	mock.destinations = map[string]output.DestinationStatus{
		"d1": {ID: "d1", Config: output.DestinationConfig{Type: "srt-caller"}, State: "stopped"},
	}

	req := httptest.NewRequest("DELETE", "/api/output/destinations/d1", nil)
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)

	require.Equal(t, http.StatusNoContent, rec.Code)
	require.Equal(t, "d1", mock.lastDestID)
}

func TestRemoveDestination_NotFound(t *testing.T) {
	api, mock := setupOutputTestAPI(t)
	mock.removeDestErr = output.ErrDestinationNotFound

	req := httptest.NewRequest("DELETE", "/api/output/destinations/nonexistent", nil)
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)

	require.Equal(t, http.StatusNotFound, rec.Code)
}

func TestStartDestination(t *testing.T) {
	api, mock := setupOutputTestAPI(t)
	mock.destinations = map[string]output.DestinationStatus{
		"d1": {ID: "d1", Config: output.DestinationConfig{Type: "srt-caller"}, State: "stopped"},
	}

	req := httptest.NewRequest("POST", "/api/output/destinations/d1/start", nil)
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code, "body: %s", rec.Body.String())
	require.Equal(t, "d1", mock.lastDestID)

	var status output.DestinationStatus
	err := json.NewDecoder(rec.Body).Decode(&status)
	require.NoError(t, err)
	require.Equal(t, "active", status.State)
}

func TestStartDestination_NotFound(t *testing.T) {
	api, mock := setupOutputTestAPI(t)
	mock.startDestErr = output.ErrDestinationNotFound

	req := httptest.NewRequest("POST", "/api/output/destinations/nonexistent/start", nil)
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)

	require.Equal(t, http.StatusNotFound, rec.Code)
}

func TestStartDestination_AlreadyActive(t *testing.T) {
	api, mock := setupOutputTestAPI(t)
	mock.startDestErr = output.ErrDestinationActive

	req := httptest.NewRequest("POST", "/api/output/destinations/d1/start", nil)
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)

	require.Equal(t, http.StatusConflict, rec.Code)
}

func TestStopDestination(t *testing.T) {
	api, mock := setupOutputTestAPI(t)
	mock.destinations = map[string]output.DestinationStatus{
		"d1": {ID: "d1", Config: output.DestinationConfig{Type: "srt-caller"}, State: "active"},
	}

	req := httptest.NewRequest("POST", "/api/output/destinations/d1/stop", nil)
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code, "body: %s", rec.Body.String())
	require.Equal(t, "d1", mock.lastDestID)

	var status output.DestinationStatus
	err := json.NewDecoder(rec.Body).Decode(&status)
	require.NoError(t, err)
	require.Equal(t, "stopped", status.State)
}

func TestStopDestination_NotFound(t *testing.T) {
	api, mock := setupOutputTestAPI(t)
	mock.stopDestErr = output.ErrDestinationNotFound

	req := httptest.NewRequest("POST", "/api/output/destinations/nonexistent/stop", nil)
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)

	require.Equal(t, http.StatusNotFound, rec.Code)
}

func TestStopDestination_NotActive(t *testing.T) {
	api, mock := setupOutputTestAPI(t)
	mock.stopDestErr = output.ErrDestinationStopped

	req := httptest.NewRequest("POST", "/api/output/destinations/d1/stop", nil)
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)

	require.Equal(t, http.StatusConflict, rec.Code)
}

func TestDestinationEndpointsNoManager(t *testing.T) {
	api := setupOutputTestAPINoManager(t)

	tests := []struct {
		method string
		path   string
		body   string
	}{
		{"POST", "/api/output/destinations", `{"type":"srt-caller","address":"192.168.1.100","port":9000}`},
		{"GET", "/api/output/destinations", ""},
		{"GET", "/api/output/destinations/d1", ""},
		{"DELETE", "/api/output/destinations/d1", ""},
		{"POST", "/api/output/destinations/d1/start", ""},
		{"POST", "/api/output/destinations/d1/stop", ""},
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

// --- CBR status tests ---

func TestCBRStatus_Disabled(t *testing.T) {
	api, _ := setupOutputTestAPI(t)

	req := httptest.NewRequest("GET", "/api/output/cbr", nil)
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)

	var result map[string]any
	err := json.NewDecoder(rec.Body).Decode(&result)
	require.NoError(t, err)
	require.Equal(t, false, result["enabled"])
}

func TestCBRStatus_Enabled(t *testing.T) {
	api, mock := setupOutputTestAPI(t)
	mock.cbrStatus = &output.CBRPacerStatus{
		Enabled:          true,
		MuxrateBps:       10_000_000,
		NullPacketsTotal: 42,
		RealBytesTotal:   1024,
		PadBytesTotal:    42 * 188,
		BurstTicksTotal:  3,
	}

	req := httptest.NewRequest("GET", "/api/output/cbr", nil)
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)

	var result output.CBRPacerStatus
	err := json.NewDecoder(rec.Body).Decode(&result)
	require.NoError(t, err)
	require.True(t, result.Enabled)
	require.Equal(t, int64(10_000_000), result.MuxrateBps)
	require.Equal(t, int64(42), result.NullPacketsTotal)
	require.Equal(t, int64(1024), result.RealBytesTotal)
	require.Equal(t, int64(42*188), result.PadBytesTotal)
	require.Equal(t, int64(3), result.BurstTicksTotal)
}

func TestCBRStatus_NoManager(t *testing.T) {
	api := setupOutputTestAPINoManager(t)

	req := httptest.NewRequest("GET", "/api/output/cbr", nil)
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

func TestAddDestination_AllowedOutputPorts(t *testing.T) {
	allowedPorts := []int{7464, 7465, 7466, 7467}

	t.Run("allowed port succeeds", func(t *testing.T) {
		programRelay := distribution.NewRelay()
		sw := switcher.NewTestSwitcher(programRelay)
		mock := &mockOutputManager{}
		api := NewAPI(sw, WithOutputManager(mock), WithAllowedOutputPorts(allowedPorts))

		body := `{"type":"srt-listener","port":7464,"name":"Output 1"}`
		req := httptest.NewRequest("POST", "/api/output/destinations", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		api.Mux().ServeHTTP(rec, req)

		require.Equal(t, http.StatusCreated, rec.Code, "body: %s", rec.Body.String())
		require.Equal(t, 7464, mock.lastDestConfig.Port)
	})

	t.Run("disallowed port rejected", func(t *testing.T) {
		programRelay := distribution.NewRelay()
		sw := switcher.NewTestSwitcher(programRelay)
		mock := &mockOutputManager{}
		api := NewAPI(sw, WithOutputManager(mock), WithAllowedOutputPorts(allowedPorts))

		body := `{"type":"srt-listener","port":9999,"name":"Output 1"}`
		req := httptest.NewRequest("POST", "/api/output/destinations", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		api.Mux().ServeHTTP(rec, req)

		require.Equal(t, http.StatusBadRequest, rec.Code)
		require.Contains(t, rec.Body.String(), "not in the allowed output port range")
	})

	t.Run("caller mode ignores constraint", func(t *testing.T) {
		programRelay := distribution.NewRelay()
		sw := switcher.NewTestSwitcher(programRelay)
		mock := &mockOutputManager{}
		api := NewAPI(sw, WithOutputManager(mock), WithAllowedOutputPorts(allowedPorts))

		body := `{"type":"srt-caller","address":"192.168.1.100","port":9999,"name":"External"}`
		req := httptest.NewRequest("POST", "/api/output/destinations", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		api.Mux().ServeHTTP(rec, req)

		require.Equal(t, http.StatusCreated, rec.Code, "body: %s", rec.Body.String())
	})

	t.Run("no constraint allows any port", func(t *testing.T) {
		api, _ := setupOutputTestAPI(t) // no WithAllowedOutputPorts

		body := `{"type":"srt-listener","port":12345,"name":"Anywhere"}`
		req := httptest.NewRequest("POST", "/api/output/destinations", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		api.Mux().ServeHTTP(rec, req)

		require.Equal(t, http.StatusCreated, rec.Code, "body: %s", rec.Body.String())
	})
}
