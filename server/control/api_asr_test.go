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
	available bool
	active    bool
	language  string
	modelName string
	lastErr   error
}

func (m *mockASRManager) IsASRAvailable() bool          { return m.available }
func (m *mockASRManager) IsASRActive() bool             { return m.active }
func (m *mockASRManager) ASRLanguage() string            { return m.language }
func (m *mockASRManager) ASRModelName() string           { return m.modelName }
func (m *mockASRManager) SetASRLanguage(lang string)     { m.language = lang }
func (m *mockASRManager) SetASRActive(active bool) error {
	if m.lastErr != nil {
		return m.lastErr
	}
	m.active = active
	return nil
}

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
	require.Empty(t, state.Language)
}

func TestASRStatusEndpoint_Available(t *testing.T) {
	mgr := &mockASRManager{
		available: true,
		active:    true,
		language:  "en",
		modelName: "large-v3-turbo",
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
	require.Equal(t, "large-v3-turbo", state.ModelName)
	require.Equal(t, "en", state.Language)
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
		available: true,
		active:    false,
		language:  "en",
		modelName: "large-v3-turbo",
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

func TestASRConfigEndpoint_SetLanguage(t *testing.T) {
	mgr := &mockASRManager{
		available: true,
		active:    true,
		language:  "en",
		modelName: "large-v3-turbo",
	}
	api := setupASRAPI(t, mgr)

	body := `{"language":"fr"}`
	req := httptest.NewRequest("PUT", "/api/asr/config", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)

	var state internal.ASRState
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&state))
	require.Equal(t, "fr", state.Language)
	require.Equal(t, "fr", mgr.language)
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

var errTestASR = errTest("asr test error")

type errTest string

func (e errTest) Error() string { return string(e) }
