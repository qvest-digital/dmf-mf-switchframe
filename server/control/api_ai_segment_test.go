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

// mockAISegmentManager is a test double for AISegmentManager.
type mockAISegmentManager struct {
	available bool
	configs   map[string]internal.AISegmentConfig
	lastErr   error
}

func newMockAISegmentManager(available bool) *mockAISegmentManager {
	return &mockAISegmentManager{
		available: available,
		configs:   make(map[string]internal.AISegmentConfig),
	}
}

func (m *mockAISegmentManager) EnableAISegment(source string, sensitivity, edgeSmooth float32, background string) error {
	if m.lastErr != nil {
		return m.lastErr
	}
	m.configs[source] = internal.AISegmentConfig{
		Enabled:     true,
		Sensitivity: sensitivity,
		EdgeSmooth:  edgeSmooth,
		Background:  background,
	}
	return nil
}

func (m *mockAISegmentManager) DisableAISegment(source string) {
	delete(m.configs, source)
}

func (m *mockAISegmentManager) GetAISegmentConfig(source string) (internal.AISegmentConfig, bool) {
	cfg, ok := m.configs[source]
	return cfg, ok
}

func (m *mockAISegmentManager) IsAISegmentAvailable() bool {
	return m.available
}

// setupAISegmentAPI builds a test API with an AI segment manager attached.
func setupAISegmentAPI(t *testing.T, available bool) (*API, *mockAISegmentManager) {
	t.Helper()
	programRelay := distribution.NewRelay()
	sw := switcher.NewTestSwitcher(programRelay)
	r1 := distribution.NewRelay()
	sw.RegisterSource("camera1", r1)
	r2 := distribution.NewRelay()
	sw.RegisterSource("camera2", r2)
	mgr := newMockAISegmentManager(available)
	api := NewAPI(sw, WithAISegmentManager(mgr))
	return api, mgr
}

// --- Status ---

func TestAISegmentAPI_Status_Available(t *testing.T) {
	api, _ := setupAISegmentAPI(t, true)

	req := httptest.NewRequest("GET", "/api/ai-segment/status", nil)
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)

	var resp map[string]bool
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&resp))
	require.True(t, resp["available"])
}

func TestAISegmentAPI_Status_Unavailable(t *testing.T) {
	api, _ := setupAISegmentAPI(t, false)

	req := httptest.NewRequest("GET", "/api/ai-segment/status", nil)
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)

	var resp map[string]bool
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&resp))
	require.False(t, resp["available"])
}

// --- Enable (PUT) ---

func TestAISegmentAPI_Enable_Defaults(t *testing.T) {
	api, mgr := setupAISegmentAPI(t, true)

	body := `{"sensitivity":0.7,"edgeSmooth":0.5}`
	req := httptest.NewRequest("PUT", "/api/sources/camera1/ai-segment", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)

	var cfg internal.AISegmentConfig
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&cfg))
	require.True(t, cfg.Enabled)
	require.InDelta(t, 0.7, cfg.Sensitivity, 0.001)
	require.InDelta(t, 0.5, cfg.EdgeSmooth, 0.001)
	require.Equal(t, "", cfg.Background)

	// Verify stored in manager
	stored, ok := mgr.GetAISegmentConfig("camera1")
	require.True(t, ok)
	require.True(t, stored.Enabled)
}

func TestAISegmentAPI_Enable_BackgroundTransparent(t *testing.T) {
	api, mgr := setupAISegmentAPI(t, true)

	body := `{"sensitivity":0.8,"edgeSmooth":0.3,"background":"transparent"}`
	req := httptest.NewRequest("PUT", "/api/sources/camera1/ai-segment", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)

	stored, ok := mgr.GetAISegmentConfig("camera1")
	require.True(t, ok)
	require.Equal(t, "transparent", stored.Background)
}

func TestAISegmentAPI_Enable_BackgroundBlur(t *testing.T) {
	api, mgr := setupAISegmentAPI(t, true)

	body := `{"sensitivity":0.6,"edgeSmooth":0.4,"background":"blur:15"}`
	req := httptest.NewRequest("PUT", "/api/sources/camera1/ai-segment", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)

	stored, ok := mgr.GetAISegmentConfig("camera1")
	require.True(t, ok)
	require.Equal(t, "blur:15", stored.Background)
}

func TestAISegmentAPI_Enable_BackgroundColor(t *testing.T) {
	api, mgr := setupAISegmentAPI(t, true)

	body := `{"sensitivity":0.5,"edgeSmooth":0.5,"background":"color:00FF00"}`
	req := httptest.NewRequest("PUT", "/api/sources/camera1/ai-segment", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)

	stored, ok := mgr.GetAISegmentConfig("camera1")
	require.True(t, ok)
	require.Equal(t, "color:00FF00", stored.Background)
}

func TestAISegmentAPI_Enable_InvalidSensitivity(t *testing.T) {
	api, _ := setupAISegmentAPI(t, true)

	body := `{"sensitivity":1.5,"edgeSmooth":0.5}`
	req := httptest.NewRequest("PUT", "/api/sources/camera1/ai-segment", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)
	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestAISegmentAPI_Enable_InvalidEdgeSmooth(t *testing.T) {
	api, _ := setupAISegmentAPI(t, true)

	body := `{"sensitivity":0.7,"edgeSmooth":-0.1}`
	req := httptest.NewRequest("PUT", "/api/sources/camera1/ai-segment", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)
	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestAISegmentAPI_Enable_InvalidBackground_BlurRadius(t *testing.T) {
	api, _ := setupAISegmentAPI(t, true)

	body := `{"sensitivity":0.7,"edgeSmooth":0.5,"background":"blur:0"}`
	req := httptest.NewRequest("PUT", "/api/sources/camera1/ai-segment", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)
	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestAISegmentAPI_Enable_InvalidBackground_BlurTooBig(t *testing.T) {
	api, _ := setupAISegmentAPI(t, true)

	body := `{"sensitivity":0.7,"edgeSmooth":0.5,"background":"blur:51"}`
	req := httptest.NewRequest("PUT", "/api/sources/camera1/ai-segment", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)
	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestAISegmentAPI_Enable_InvalidBackground_BadHex(t *testing.T) {
	api, _ := setupAISegmentAPI(t, true)

	body := `{"sensitivity":0.7,"edgeSmooth":0.5,"background":"color:ZZZZZZ"}`
	req := httptest.NewRequest("PUT", "/api/sources/camera1/ai-segment", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)
	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestAISegmentAPI_Enable_InvalidBackground_Unknown(t *testing.T) {
	api, _ := setupAISegmentAPI(t, true)

	body := `{"sensitivity":0.7,"edgeSmooth":0.5,"background":"greenscreen"}`
	req := httptest.NewRequest("PUT", "/api/sources/camera1/ai-segment", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)
	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestAISegmentAPI_Enable_InvalidJSON(t *testing.T) {
	api, _ := setupAISegmentAPI(t, true)

	req := httptest.NewRequest("PUT", "/api/sources/camera1/ai-segment", strings.NewReader("not json"))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)
	require.Equal(t, http.StatusBadRequest, rec.Code)
}

// --- Get (GET) ---

func TestAISegmentAPI_Get_Found(t *testing.T) {
	api, mgr := setupAISegmentAPI(t, true)
	mgr.configs["camera1"] = internal.AISegmentConfig{
		Enabled:     true,
		Sensitivity: 0.8,
		EdgeSmooth:  0.4,
		Background:  "blur:10",
	}

	req := httptest.NewRequest("GET", "/api/sources/camera1/ai-segment", nil)
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)

	var cfg internal.AISegmentConfig
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&cfg))
	require.True(t, cfg.Enabled)
	require.InDelta(t, 0.8, cfg.Sensitivity, 0.001)
	require.Equal(t, "blur:10", cfg.Background)
}

func TestAISegmentAPI_Get_NotFound(t *testing.T) {
	api, _ := setupAISegmentAPI(t, true)

	req := httptest.NewRequest("GET", "/api/sources/camera1/ai-segment", nil)
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)
	require.Equal(t, http.StatusNotFound, rec.Code)
}

// --- Disable (DELETE) ---

func TestAISegmentAPI_Disable(t *testing.T) {
	api, mgr := setupAISegmentAPI(t, true)
	mgr.configs["camera1"] = internal.AISegmentConfig{
		Enabled:     true,
		Sensitivity: 0.7,
	}

	req := httptest.NewRequest("DELETE", "/api/sources/camera1/ai-segment", nil)
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)
	require.Equal(t, http.StatusNoContent, rec.Code)

	_, ok := mgr.GetAISegmentConfig("camera1")
	require.False(t, ok)
}

func TestAISegmentAPI_Disable_NotConfigured(t *testing.T) {
	api, _ := setupAISegmentAPI(t, true)

	// Disable on a source that was never configured — should succeed (idempotent).
	req := httptest.NewRequest("DELETE", "/api/sources/camera2/ai-segment", nil)
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)
	require.Equal(t, http.StatusNoContent, rec.Code)
}

// --- No manager configured ---

func TestAISegmentAPI_NilManager_RoutesNotRegistered(t *testing.T) {
	programRelay := distribution.NewRelay()
	sw := switcher.NewTestSwitcher(programRelay)
	api := NewAPI(sw) // no WithAISegmentManager

	req := httptest.NewRequest("GET", "/api/ai-segment/status", nil)
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)
	// Routes not registered without manager — mux returns 405 or 404.
	require.NotEqual(t, http.StatusOK, rec.Code)
}

// --- validateAIBackground unit tests ---

func TestValidateAIBackground(t *testing.T) {
	cases := []struct {
		bg      string
		wantErr bool
	}{
		{"", false},
		{"transparent", false},
		{"blur:1", false},
		{"blur:50", false},
		{"blur:10", false},
		{"color:000000", false},
		{"color:FFFFFF", false},
		{"color:aAbBcC", false},
		{"blur:0", true},
		{"blur:51", true},
		{"blur:", true},
		{"blur:abc", true},
		{"color:GGGGGG", true},
		{"color:12345", true},   // too short
		{"color:1234567", true}, // too long
		{"greenscreen", true},
		{"none", true},
	}
	for _, tc := range cases {
		err := validateAIBackground(tc.bg)
		if tc.wantErr {
			require.Error(t, err, "bg=%q should be invalid", tc.bg)
		} else {
			require.NoError(t, err, "bg=%q should be valid", tc.bg)
		}
	}
}
