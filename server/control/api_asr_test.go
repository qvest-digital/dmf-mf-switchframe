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
	available        bool
	active           bool
	translate        bool
	language         string
	modelName        string
	lastErr          error
	speed            string
	confidenceFilter string
	noiseFloor       string
	speakerDetection string
}

func (m *mockASRManager) IsASRAvailable() bool              { return m.available }
func (m *mockASRManager) IsASRActive() bool                 { return m.active }
func (m *mockASRManager) ASRLanguage() string               { return m.language }
func (m *mockASRManager) ASRTranslate() bool                { return m.translate }
func (m *mockASRManager) ASRModelName() string              { return m.modelName }
func (m *mockASRManager) SetASRLanguage(lang string)        { m.language = lang }
func (m *mockASRManager) SetASRTranslate(on bool)           { m.translate = on }
func (m *mockASRManager) SetASRModel(name string) error     { m.modelName = name; return nil }
func (m *mockASRManager) AvailableASRModels() []string      { return []string{"base", "medium.en", "large-v3"} }
func (m *mockASRManager) SetASRSpeed(preset string)         { m.speed = preset }
func (m *mockASRManager) ASRSpeed() string                  { return m.speed }
func (m *mockASRManager) SetASRConfidence(preset string)    { m.confidenceFilter = preset }
func (m *mockASRManager) ASRConfidence() string             { return m.confidenceFilter }
func (m *mockASRManager) SetASRNoiseFloor(preset string)    { m.noiseFloor = preset }
func (m *mockASRManager) ASRNoiseFloor() string             { return m.noiseFloor }
func (m *mockASRManager) SetASRSpeakerDetection(preset string) { m.speakerDetection = preset }
func (m *mockASRManager) ASRSpeakerDetection() string       { return m.speakerDetection }
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

func TestASRConfigTuning(t *testing.T) {
	mgr := &mockASRManager{
		available:        true,
		active:           true,
		language:         "en",
		modelName:        "large-v3-turbo",
		speed:            "balanced",
		confidenceFilter: "medium",
		noiseFloor:       "normal",
		speakerDetection: "normal",
	}
	api := setupASRAPI(t, mgr)

	// PUT all 4 tuning fields.
	body := `{"speed":"fast","confidenceFilter":"high","noiseFloor":"aggressive","speakerDetection":"sensitive"}`
	req := httptest.NewRequest("PUT", "/api/asr/config", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)

	var state internal.ASRState
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&state))

	// Verify tuning fields in the response.
	require.Equal(t, "fast", state.Speed)
	require.Equal(t, "high", state.ConfidenceFilter)
	require.Equal(t, "aggressive", state.NoiseFloor)
	require.Equal(t, "sensitive", state.SpeakerDetection)

	// Verify mock received them.
	require.Equal(t, "fast", mgr.speed)
	require.Equal(t, "high", mgr.confidenceFilter)
	require.Equal(t, "aggressive", mgr.noiseFloor)
	require.Equal(t, "sensitive", mgr.speakerDetection)

	// GET /api/asr/status should also include tuning fields.
	req2 := httptest.NewRequest("GET", "/api/asr/status", nil)
	rec2 := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec2, req2)
	require.Equal(t, http.StatusOK, rec2.Code)

	var status internal.ASRState
	require.NoError(t, json.NewDecoder(rec2.Body).Decode(&status))
	require.Equal(t, "fast", status.Speed)
	require.Equal(t, "high", status.ConfidenceFilter)
	require.Equal(t, "aggressive", status.NoiseFloor)
	require.Equal(t, "sensitive", status.SpeakerDetection)
}

var errTestASR = errTest("asr test error")

type errTest string

func (e errTest) Error() string { return string(e) }
