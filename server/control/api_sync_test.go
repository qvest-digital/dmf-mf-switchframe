package control

import (
	"context"
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

func TestSyncHealth_ReturnsFields(t *testing.T) {
	api, _ := setupTestAPI(t)
	req := httptest.NewRequest("GET", "/api/sync/health", nil)
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)

	var resp struct {
		PTPOffsetUS    int64  `json:"ptpOffsetUs"`
		LastCommandSeq uint64 `json:"lastCommandSeq"`
		UptimeMS       int64  `json:"uptimeMs"`
	}
	err := json.NewDecoder(rec.Body).Decode(&resp)
	require.NoError(t, err)
	require.True(t, resp.UptimeMS >= 0, "uptime should be non-negative")
}

// --- Mock SnapshotProvider ---

type mockSnapshotProvider struct {
	snapshot internal.FullStateSnapshot
	applied  *internal.FullStateSnapshot
	warnings []string
}

func (m *mockSnapshotProvider) CaptureFullSnapshot() internal.FullStateSnapshot {
	return m.snapshot
}

func (m *mockSnapshotProvider) ApplyTarget() internal.SnapshotApplyTarget {
	return &mockApplyTarget{provider: m}
}

// mockApplyTarget is a minimal SnapshotApplyTarget that records the fact that
// apply was called and returns the provider's configured warnings.
type mockApplyTarget struct {
	provider *mockSnapshotProvider
}

func (m *mockApplyTarget) Cut(_ context.Context, _ string) error                { return nil }
func (m *mockApplyTarget) SetPreview(_ context.Context, _ string) error         { return nil }
func (m *mockApplyTarget) SetTransitionConfig(_ string, _ int, _ string) error  { return nil }
func (m *mockApplyTarget) SetFTB(_ bool) error                                  { return nil }
func (m *mockApplyTarget) SetLevel(_ string, _ float64) error                   { return nil }
func (m *mockApplyTarget) SetTrim(_ string, _ float64) error                    { return nil }
func (m *mockApplyTarget) SetMuted(_ string, _ bool) error                      { return nil }
func (m *mockApplyTarget) SetAFV(_ string, _ bool) error                        { return nil }
func (m *mockApplyTarget) SetPhaseInvert(_ string, _ bool) error                { return nil }
func (m *mockApplyTarget) SetBalance(_ string, _ float64) error                 { return nil }
func (m *mockApplyTarget) SetHPF(_ string, _ float64, _ bool) error             { return nil }
func (m *mockApplyTarget) SetEQ(_ string, _ int, _, _, _ float64, _ bool) error { return nil }
func (m *mockApplyTarget) SetCompressor(_ string, _, _, _, _, _ float64) error  { return nil }
func (m *mockApplyTarget) SetGate(_ string, _, _, _, _, _ float64, _ bool) error {
	return nil
}
func (m *mockApplyTarget) SetAudioDelay(_ string, _ int) error                        { return nil }
func (m *mockApplyTarget) SetMasterLevel(_ float64) error                             { return nil }
func (m *mockApplyTarget) SetLimiterThreshold(_ float64) error                        { return nil }
func (m *mockApplyTarget) SetLimiterCeiling(_ float64) error                          { return nil }
func (m *mockApplyTarget) SetGraphicsLayerActive(_ int, _ bool) error                 { return nil }
func (m *mockApplyTarget) SetGraphicsLayerZOrder(_ int, _ int) error                  { return nil }
func (m *mockApplyTarget) SetGraphicsLayerRect(_ int, _, _, _, _ int) error           { return nil }
func (m *mockApplyTarget) SetDVELayout(_ internal.LayoutSnapshot) error               { return nil }
func (m *mockApplyTarget) SetSourceLabel(_ context.Context, _, _ string) error        { return nil }
func (m *mockApplyTarget) SetSourcePosition(_ string, _ int) error                    { return nil }
func (m *mockApplyTarget) SetUpstreamKey(_ string, _ internal.UpstreamKeySnapshot) error { return nil }
func (m *mockApplyTarget) SetCaptionMode(_ string) error                              { return nil }
func (m *mockApplyTarget) SetColorGrade(_ string, _ float32) error                    { return nil }
func (m *mockApplyTarget) DisableColorGrade() error                                   { return nil }
func (m *mockApplyTarget) AssignSTMapSource(_, _ string) error                        { return nil }
func (m *mockApplyTarget) AssignSTMapProgram(_ string) error                          { return nil }

// --- Helpers ---

func setupTestAPIWithSnapshot(t *testing.T, provider *mockSnapshotProvider) *API {
	t.Helper()
	programRelay := distribution.NewRelay()
	sw := switcher.NewTestSwitcher(programRelay)
	r1 := distribution.NewRelay()
	sw.RegisterSource("camera1", r1)
	r2 := distribution.NewRelay()
	sw.RegisterSource("camera2", r2)
	api := NewAPI(sw, WithSnapshotProvider(provider))
	return api
}

// --- Tests ---

func TestGetStateSnapshot_ReturnsSnapshot(t *testing.T) {
	provider := &mockSnapshotProvider{
		snapshot: internal.FullStateSnapshot{
			Version:       "1",
			CapturedAt:    1234567890,
			ProgramSource: "camera1",
			PreviewSource: "camera2",
		},
	}
	api := setupTestAPIWithSnapshot(t, provider)

	req := httptest.NewRequest("GET", "/api/sync/state-snapshot", nil)
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)

	var snap internal.FullStateSnapshot
	err := json.NewDecoder(rec.Body).Decode(&snap)
	require.NoError(t, err)
	require.Equal(t, "1", snap.Version)
	require.Equal(t, int64(1234567890), snap.CapturedAt)
	require.Equal(t, "camera1", snap.ProgramSource)
	require.Equal(t, "camera2", snap.PreviewSource)
}

func TestApplyStateSnapshot_AppliesAndReturns(t *testing.T) {
	provider := &mockSnapshotProvider{}
	api := setupTestAPIWithSnapshot(t, provider)

	body := `{
		"version": "1",
		"capturedAt": 1234567890,
		"programSource": "camera1",
		"previewSource": "camera2",
		"captionMode": "off"
	}`
	req := httptest.NewRequest("POST", "/api/sync/state-snapshot", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)

	var resp struct {
		Applied  bool     `json:"applied"`
		Warnings []string `json:"warnings,omitempty"`
	}
	err := json.NewDecoder(rec.Body).Decode(&resp)
	require.NoError(t, err)
	require.True(t, resp.Applied)
}

func TestApplyStateSnapshot_InvalidJSON(t *testing.T) {
	provider := &mockSnapshotProvider{}
	api := setupTestAPIWithSnapshot(t, provider)

	req := httptest.NewRequest("POST", "/api/sync/state-snapshot", strings.NewReader("{invalid"))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)

	require.Equal(t, http.StatusBadRequest, rec.Code)

	var resp map[string]string
	err := json.NewDecoder(rec.Body).Decode(&resp)
	require.NoError(t, err)
	require.Contains(t, resp["error"], "invalid snapshot JSON")
}

func TestGetStateSnapshot_NotConfigured(t *testing.T) {
	// No snapshot provider configured — routes should not be registered.
	api, _ := setupTestAPI(t)

	req := httptest.NewRequest("GET", "/api/sync/state-snapshot", nil)
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)

	require.Equal(t, http.StatusNotFound, rec.Code)
}
