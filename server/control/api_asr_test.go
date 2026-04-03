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
	"github.com/zsiec/switchframe/server/switcher"
)

// mockASRManager is a test double for ASRManager.
type mockASRManager struct {
	available         bool
	active            bool
	backend           string
	availableBackends []string
	modelName         string
	models            []string
	backendConfig     json.RawMessage
	confidenceFilter  string
	speakerDetection  string
	lastErr           error
	switchBackendErr  error
	setConfigErr      error
}

func (m *mockASRManager) IsASRAvailable() bool           { return m.available }
func (m *mockASRManager) IsASRActive() bool              { return m.active }
func (m *mockASRManager) ActiveBackend() string          { return m.backend }
func (m *mockASRManager) AvailableBackends() []string    { return m.availableBackends }
func (m *mockASRManager) ASRModelName() string           { return m.modelName }
func (m *mockASRManager) BackendConfig() json.RawMessage { return m.backendConfig }

func (m *mockASRManager) SetASRActive(active bool) error {
	if m.lastErr != nil {
		return m.lastErr
	}
	m.active = active
	return nil
}

func (m *mockASRManager) SwitchBackend(name string) error {
	if m.switchBackendErr != nil {
		return m.switchBackendErr
	}
	m.backend = name
	return nil
}

func (m *mockASRManager) SetASRModel(name string) error {
	m.modelName = name
	return nil
}

func (m *mockASRManager) AvailableASRModels() []string {
	if m.models != nil {
		return m.models
	}
	return []string{"base", "medium.en", "large-v3"}
}

func (m *mockASRManager) SetBackendConfig(raw json.RawMessage) error {
	if m.setConfigErr != nil {
		return m.setConfigErr
	}
	m.backendConfig = raw
	return nil
}

func (m *mockASRManager) SetASRConfidence(preset string)       { m.confidenceFilter = preset }
func (m *mockASRManager) ASRConfidence() string                { return m.confidenceFilter }
func (m *mockASRManager) SetASRSpeakerDetection(preset string) { m.speakerDetection = preset }
func (m *mockASRManager) ASRSpeakerDetection() string          { return m.speakerDetection }

// setupASRAPI builds a test API with an optional ASR manager attached.
func setupASRAPI(t *testing.T, mgr ASRManager) *API {
	t.Helper()
	programRelay := distribution.NewRelay()
	sw := switcher.NewTestSwitcher(programRelay)
	r1 := distribution.NewRelay()
	sw.RegisterSource("camera1", r1)
	opts := []APIOption{}
	if mgr != nil {
		opts = append(opts, WithASRManager(mgr))
	}
	return NewAPI(sw, opts...)
}

// --- Status ---

func TestASRStatusEndpoint_NotAvailable(t *testing.T) {
	api := setupASRAPI(t, nil) // no manager

	req := httptest.NewRequest("GET", "/api/asr/status", nil)
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)

	var state internal.ASRState
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&state))
	require.False(t, state.Available)
	require.False(t, state.Active)
	require.Empty(t, state.ModelName)
	require.Empty(t, state.Backend)
}

func TestASRStatusEndpoint_Available(t *testing.T) {
	mgr := &mockASRManager{
		available:         true,
		active:            true,
		backend:           "sidecar",
		availableBackends: []string{"sidecar"},
		modelName:         "parakeet-tdt-1.1b",
		confidenceFilter:  "medium",
		speakerDetection:  "normal",
	}
	api := setupASRAPI(t, mgr)

	req := httptest.NewRequest("GET", "/api/asr/status", nil)
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)

	var state internal.ASRState
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&state))
	require.True(t, state.Available)
	require.True(t, state.Active)
	require.Equal(t, "parakeet-tdt-1.1b", state.ModelName)
	require.Equal(t, "sidecar", state.Backend)
	require.Equal(t, []string{"sidecar"}, state.AvailableBackends)
	require.Equal(t, "medium", state.ConfidenceFilter)
	require.Equal(t, "normal", state.SpeakerDetection)
}

// --- Config ---

func TestASRConfigEndpoint_NotAvailable(t *testing.T) {
	api := setupASRAPI(t, nil) // no manager

	body := `{"active":true}`
	req := httptest.NewRequest("PUT", "/api/asr/config", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)
	require.Equal(t, http.StatusNotImplemented, rec.Code)
}

func TestASRConfigEndpoint_ManagerNotAvailable(t *testing.T) {
	mgr := &mockASRManager{available: false}
	api := setupASRAPI(t, mgr)

	body := `{"active":true}`
	req := httptest.NewRequest("PUT", "/api/asr/config", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)
	require.Equal(t, http.StatusNotImplemented, rec.Code)
}

func TestASRConfigEndpoint_SetActive(t *testing.T) {
	mgr := &mockASRManager{
		available:         true,
		active:            false,
		backend:           "sidecar",
		availableBackends: []string{"sidecar"},
		modelName:         "parakeet-tdt-1.1b",
	}
	api := setupASRAPI(t, mgr)

	body := `{"active":true}`
	req := httptest.NewRequest("PUT", "/api/asr/config", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)

	var state internal.ASRState
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&state))
	require.True(t, state.Active)
	require.True(t, mgr.active)
}

func TestASRConfigEndpoint_SwitchBackend(t *testing.T) {
	mgr := &mockASRManager{
		available:         true,
		active:            true,
		backend:           "sidecar",
		availableBackends: []string{"sidecar", "other"},
		modelName:         "parakeet-tdt-1.1b",
	}
	api := setupASRAPI(t, mgr)

	body := `{"backend":"other"}`
	req := httptest.NewRequest("PUT", "/api/asr/config", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)

	var state internal.ASRState
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&state))
	require.Equal(t, "other", state.Backend)
	require.Equal(t, "other", mgr.backend)
}

func TestASRConfigEndpoint_SwitchBackendError(t *testing.T) {
	mgr := &mockASRManager{
		available:        true,
		backend:          "sidecar",
		switchBackendErr: errTest("unknown backend"),
	}
	api := setupASRAPI(t, mgr)

	body := `{"backend":"invalid"}`
	req := httptest.NewRequest("PUT", "/api/asr/config", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)
	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestASRConfigEndpoint_InvalidJSON(t *testing.T) {
	mgr := &mockASRManager{available: true}
	api := setupASRAPI(t, mgr)

	req := httptest.NewRequest("PUT", "/api/asr/config", strings.NewReader("not json"))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)
	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestASRConfigEndpoint_SetActiveError(t *testing.T) {
	mgr := &mockASRManager{
		available: true,
		lastErr:   errTestASR,
	}
	api := setupASRAPI(t, mgr)

	body := `{"active":true}`
	req := httptest.NewRequest("PUT", "/api/asr/config", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)
	require.Equal(t, http.StatusInternalServerError, rec.Code)
}

func TestASRConfigEndpoint_PacerSettings(t *testing.T) {
	mgr := &mockASRManager{
		available:         true,
		active:            true,
		backend:           "sidecar",
		availableBackends: []string{"sidecar"},
		modelName:         "parakeet-tdt-1.1b",
		confidenceFilter:  "medium",
		speakerDetection:  "normal",
	}
	api := setupASRAPI(t, mgr)

	body := `{"confidenceFilter":"high","speakerDetection":"sensitive"}`
	req := httptest.NewRequest("PUT", "/api/asr/config", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)

	var state internal.ASRState
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&state))
	require.Equal(t, "high", state.ConfidenceFilter)
	require.Equal(t, "sensitive", state.SpeakerDetection)
	require.Equal(t, "high", mgr.confidenceFilter)
	require.Equal(t, "sensitive", mgr.speakerDetection)

	// GET /api/asr/status should also reflect the update.
	req2 := httptest.NewRequest("GET", "/api/asr/status", nil)
	rec2 := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec2, req2)
	require.Equal(t, http.StatusOK, rec2.Code)

	var status internal.ASRState
	require.NoError(t, json.NewDecoder(rec2.Body).Decode(&status))
	require.Equal(t, "high", status.ConfidenceFilter)
	require.Equal(t, "sensitive", status.SpeakerDetection)
}

// --- Models ---

func TestASRModelsEndpoint(t *testing.T) {
	mgr := &mockASRManager{
		available: true,
		models:    []string{"base", "medium.en", "large-v3"},
	}
	api := setupASRAPI(t, mgr)

	req := httptest.NewRequest("GET", "/api/asr/models", nil)
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)

	var models []string
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&models))
	require.Equal(t, []string{"base", "medium.en", "large-v3"}, models)
}

func TestASRModelsEndpoint_NoManager(t *testing.T) {
	api := setupASRAPI(t, nil)

	req := httptest.NewRequest("GET", "/api/asr/models", nil)
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)
	require.Equal(t, http.StatusNotImplemented, rec.Code)
}

// --- Backend Config ---

func TestASRBackendConfigGet(t *testing.T) {
	mgr := &mockASRManager{
		available:     true,
		backendConfig: json.RawMessage(`{"language":"en","translate":false}`),
	}
	api := setupASRAPI(t, mgr)

	req := httptest.NewRequest("GET", "/api/asr/backend/config", nil)
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
	require.JSONEq(t, `{"language":"en","translate":false}`, rec.Body.String())
}

func TestASRBackendConfigGet_NilConfig(t *testing.T) {
	mgr := &mockASRManager{
		available:     true,
		backendConfig: nil,
	}
	api := setupASRAPI(t, mgr)

	req := httptest.NewRequest("GET", "/api/asr/backend/config", nil)
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
	require.JSONEq(t, `{}`, rec.Body.String())
}

func TestASRBackendConfigGet_NotAvailable(t *testing.T) {
	api := setupASRAPI(t, nil)

	req := httptest.NewRequest("GET", "/api/asr/backend/config", nil)
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)
	require.Equal(t, http.StatusNotImplemented, rec.Code)
}

func TestASRBackendConfigPut(t *testing.T) {
	mgr := &mockASRManager{
		available:     true,
		backendConfig: json.RawMessage(`{}`),
	}
	api := setupASRAPI(t, mgr)

	body := `{"language":"fr","translate":true,"speed":"fast"}`
	req := httptest.NewRequest("PUT", "/api/asr/backend/config", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)

	// Verify the config was stored.
	require.JSONEq(t, body, string(mgr.backendConfig))
}

func TestASRBackendConfigPut_InvalidJSON(t *testing.T) {
	mgr := &mockASRManager{available: true}
	api := setupASRAPI(t, mgr)

	req := httptest.NewRequest("PUT", "/api/asr/backend/config", strings.NewReader("not json"))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)
	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestASRBackendConfigPut_SetConfigError(t *testing.T) {
	mgr := &mockASRManager{
		available:    true,
		setConfigErr: errTest("invalid config field"),
	}
	api := setupASRAPI(t, mgr)

	body := `{"badField":"x"}`
	req := httptest.NewRequest("PUT", "/api/asr/backend/config", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)
	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestASRBackendConfigPut_NotAvailable(t *testing.T) {
	api := setupASRAPI(t, nil)

	body := `{"language":"en"}`
	req := httptest.NewRequest("PUT", "/api/asr/backend/config", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)
	require.Equal(t, http.StatusNotImplemented, rec.Code)
}

var errTestASR = errTest("asr test error")

type errTest string

func (e errTest) Error() string { return string(e) }
