package control

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/zsiec/prism/distribution"
	"github.com/zsiec/switchframe/server/preset"
	"github.com/zsiec/switchframe/server/switcher"
)

func setupPresetTestAPI(t *testing.T) (*API, *switcher.Switcher, *preset.Store) {
	t.Helper()
	programRelay := distribution.NewRelay()
	sw := switcher.NewTestSwitcher(programRelay)
	r1 := distribution.NewRelay()
	sw.RegisterSource("camera1", r1)
	r2 := distribution.NewRelay()
	sw.RegisterSource("camera2", r2)
	_ = sw.Cut(context.Background(), "camera1")
	_ = sw.SetPreview(context.Background(), "camera2")

	dir := t.TempDir()
	ps, err := preset.NewStore(filepath.Join(dir, "presets.json"))
	require.NoError(t, err)

	mock := newMockMixer("camera1", "camera2")
	api := NewAPI(sw, WithMixer(mock), WithPresetStore(ps))
	return api, sw, ps
}

func TestListPresetsEmpty(t *testing.T) {
	api, _, _ := setupPresetTestAPI(t)

	req := httptest.NewRequest("GET", "/api/presets", nil)
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code, "body: %s", rec.Body.String())

	var presets []preset.Preset
	err := json.NewDecoder(rec.Body).Decode(&presets)
	require.NoError(t, err)
	require.Empty(t, presets)
}

func TestCreatePresetEndpoint(t *testing.T) {
	api, _, _ := setupPresetTestAPI(t)

	body := `{"name":"Morning Service"}`
	req := httptest.NewRequest("POST", "/api/presets", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)

	require.Equal(t, http.StatusCreated, rec.Code, "body: %s", rec.Body.String())

	var p preset.Preset
	err := json.NewDecoder(rec.Body).Decode(&p)
	require.NoError(t, err)
	require.Equal(t, "Morning Service", p.Name)
	require.Equal(t, "camera1", p.ProgramSource)
	require.Equal(t, "camera2", p.PreviewSource)
	require.NotEmpty(t, p.ID)
}

func TestCreatePresetEmptyName(t *testing.T) {
	api, _, _ := setupPresetTestAPI(t)

	body := `{"name":""}`
	req := httptest.NewRequest("POST", "/api/presets", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)

	require.Equal(t, http.StatusBadRequest, rec.Code, "body: %s", rec.Body.String())
}

func TestCreatePresetInvalidJSON(t *testing.T) {
	api, _, _ := setupPresetTestAPI(t)

	req := httptest.NewRequest("POST", "/api/presets", strings.NewReader("{bad"))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)

	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestGetPresetEndpoint(t *testing.T) {
	api, _, ps := setupPresetTestAPI(t)

	created, _ := ps.Create("Test", preset.ControlRoomSnapshot{ProgramSource: "camera1"})

	req := httptest.NewRequest("GET", "/api/presets/"+created.ID, nil)
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code, "body: %s", rec.Body.String())

	var p preset.Preset
	_ = json.NewDecoder(rec.Body).Decode(&p)
	require.Equal(t, "Test", p.Name)
}

func TestGetPresetNotFound(t *testing.T) {
	api, _, _ := setupPresetTestAPI(t)

	req := httptest.NewRequest("GET", "/api/presets/nonexistent", nil)
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)

	require.Equal(t, http.StatusNotFound, rec.Code)
}

func TestUpdatePresetEndpoint(t *testing.T) {
	api, _, ps := setupPresetTestAPI(t)

	created, _ := ps.Create("Original", preset.ControlRoomSnapshot{ProgramSource: "camera1"})

	body := `{"name":"Updated"}`
	req := httptest.NewRequest("PUT", "/api/presets/"+created.ID, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code, "body: %s", rec.Body.String())

	var p preset.Preset
	_ = json.NewDecoder(rec.Body).Decode(&p)
	require.Equal(t, "Updated", p.Name)
}

func TestUpdatePresetNotFound(t *testing.T) {
	api, _, _ := setupPresetTestAPI(t)

	body := `{"name":"Updated"}`
	req := httptest.NewRequest("PUT", "/api/presets/nonexistent", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)

	require.Equal(t, http.StatusNotFound, rec.Code)
}

func TestDeletePresetEndpoint(t *testing.T) {
	api, _, ps := setupPresetTestAPI(t)

	created, _ := ps.Create("ToDelete", preset.ControlRoomSnapshot{ProgramSource: "camera1"})

	req := httptest.NewRequest("DELETE", "/api/presets/"+created.ID, nil)
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)

	require.Equal(t, http.StatusNoContent, rec.Code, "body: %s", rec.Body.String())

	// Verify deleted
	require.Empty(t, ps.List(), "expected 0 presets after delete")
}

func TestDeletePresetNoContentType(t *testing.T) {
	api, _, ps := setupPresetTestAPI(t)

	created, _ := ps.Create("ToDelete", preset.ControlRoomSnapshot{ProgramSource: "camera1"})

	req := httptest.NewRequest("DELETE", "/api/presets/"+created.ID, nil)
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)

	require.Equal(t, http.StatusNoContent, rec.Code)
	// 204 No Content MUST NOT include a Content-Type header (RFC 9110 sec 6.4.1).
	require.Empty(t, rec.Header().Get("Content-Type"),
		"204 No Content should not set Content-Type header")
}

func TestDeletePresetNotFound(t *testing.T) {
	api, _, _ := setupPresetTestAPI(t)

	req := httptest.NewRequest("DELETE", "/api/presets/nonexistent", nil)
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)

	require.Equal(t, http.StatusNotFound, rec.Code)
}

func TestRecallPresetEndpoint(t *testing.T) {
	api, sw, ps := setupPresetTestAPI(t)
	defer sw.Close()

	// Create a preset while on camera1/camera2
	p, _ := ps.Create("Test", preset.ControlRoomSnapshot{
		ProgramSource: "camera2",
		PreviewSource: "camera1",
		MasterLevel:   -3.0,
	})

	req := httptest.NewRequest("POST", "/api/presets/"+p.ID+"/recall", nil)
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code, "body: %s", rec.Body.String())

	var resp recallPresetResponse
	err := json.NewDecoder(rec.Body).Decode(&resp)
	require.NoError(t, err)
	require.Empty(t, resp.Warnings)

	// Verify program source was changed
	state := sw.State()
	require.Equal(t, "camera2", state.ProgramSource)
	require.Equal(t, "camera1", state.PreviewSource)
}

func TestRecallPresetNotFound(t *testing.T) {
	api, _, _ := setupPresetTestAPI(t)

	req := httptest.NewRequest("POST", "/api/presets/nonexistent/recall", nil)
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)

	require.Equal(t, http.StatusNotFound, rec.Code)
}

func TestRecallPresetWithWarnings(t *testing.T) {
	api, sw, ps := setupPresetTestAPI(t)
	defer sw.Close()

	// Create a preset with a source that doesn't exist
	p, _ := ps.Create("Bad Source", preset.ControlRoomSnapshot{
		ProgramSource: "nonexistent",
		PreviewSource: "camera1",
		MasterLevel:   0,
	})

	req := httptest.NewRequest("POST", "/api/presets/"+p.ID+"/recall", nil)
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code, "body: %s", rec.Body.String())

	var resp recallPresetResponse
	_ = json.NewDecoder(rec.Body).Decode(&resp)
	require.NotEmpty(t, resp.Warnings, "expected at least 1 warning for missing source")
}

func TestListPresetsAfterCreate(t *testing.T) {
	api, _, _ := setupPresetTestAPI(t)

	// Create two presets
	body := `{"name":"Preset A"}`
	req := httptest.NewRequest("POST", "/api/presets", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)
	require.Equal(t, http.StatusCreated, rec.Code, "create A: body: %s", rec.Body.String())

	body = `{"name":"Preset B"}`
	req = httptest.NewRequest("POST", "/api/presets", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec = httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)
	require.Equal(t, http.StatusCreated, rec.Code, "create B: body: %s", rec.Body.String())

	// List
	req = httptest.NewRequest("GET", "/api/presets", nil)
	rec = httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)

	var presets []preset.Preset
	_ = json.NewDecoder(rec.Body).Decode(&presets)
	require.Len(t, presets, 2)
}
