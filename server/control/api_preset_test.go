package control

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zsiec/prism/distribution"
	"github.com/zsiec/switchframe/server/preset"
	"github.com/zsiec/switchframe/server/switcher"
)

func setupPresetTestAPI(t *testing.T) (*API, *switcher.Switcher, *preset.PresetStore) {
	t.Helper()
	programRelay := distribution.NewRelay()
	sw := switcher.New(programRelay)
	r1 := distribution.NewRelay()
	sw.RegisterSource("camera1", r1)
	r2 := distribution.NewRelay()
	sw.RegisterSource("camera2", r2)
	sw.Cut(context.Background(), "camera1")
	sw.SetPreview(context.Background(), "camera2")

	dir := t.TempDir()
	ps, err := preset.NewPresetStore(filepath.Join(dir, "presets.json"))
	if err != nil {
		t.Fatalf("NewPresetStore: %v", err)
	}

	mock := newMockMixer("camera1", "camera2")
	api := NewAPI(sw, WithMixer(mock), WithPresetStore(ps))
	return api, sw, ps
}

func TestListPresetsEmpty(t *testing.T) {
	api, _, _ := setupPresetTestAPI(t)

	req := httptest.NewRequest("GET", "/api/presets", nil)
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body: %s", rec.Code, http.StatusOK, rec.Body.String())
	}

	var presets []preset.Preset
	if err := json.NewDecoder(rec.Body).Decode(&presets); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(presets) != 0 {
		t.Errorf("expected 0 presets, got %d", len(presets))
	}
}

func TestCreatePresetEndpoint(t *testing.T) {
	api, _, _ := setupPresetTestAPI(t)

	body := `{"name":"Morning Service"}`
	req := httptest.NewRequest("POST", "/api/presets", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want %d; body: %s", rec.Code, http.StatusCreated, rec.Body.String())
	}

	var p preset.Preset
	if err := json.NewDecoder(rec.Body).Decode(&p); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if p.Name != "Morning Service" {
		t.Errorf("Name = %q, want %q", p.Name, "Morning Service")
	}
	if p.ProgramSource != "camera1" {
		t.Errorf("ProgramSource = %q, want %q", p.ProgramSource, "camera1")
	}
	if p.PreviewSource != "camera2" {
		t.Errorf("PreviewSource = %q, want %q", p.PreviewSource, "camera2")
	}
	if p.ID == "" {
		t.Error("expected non-empty ID")
	}
}

func TestCreatePresetEmptyName(t *testing.T) {
	api, _, _ := setupPresetTestAPI(t)

	body := `{"name":""}`
	req := httptest.NewRequest("POST", "/api/presets", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d; body: %s", rec.Code, http.StatusBadRequest, rec.Body.String())
	}
}

func TestCreatePresetInvalidJSON(t *testing.T) {
	api, _, _ := setupPresetTestAPI(t)

	req := httptest.NewRequest("POST", "/api/presets", strings.NewReader("{bad"))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusBadRequest)
	}
}

func TestGetPresetEndpoint(t *testing.T) {
	api, _, ps := setupPresetTestAPI(t)

	created, _ := ps.Create("Test", preset.ControlRoomSnapshot{ProgramSource: "camera1"})

	req := httptest.NewRequest("GET", "/api/presets/"+created.ID, nil)
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body: %s", rec.Code, http.StatusOK, rec.Body.String())
	}

	var p preset.Preset
	json.NewDecoder(rec.Body).Decode(&p)
	if p.Name != "Test" {
		t.Errorf("Name = %q, want %q", p.Name, "Test")
	}
}

func TestGetPresetNotFound(t *testing.T) {
	api, _, _ := setupPresetTestAPI(t)

	req := httptest.NewRequest("GET", "/api/presets/nonexistent", nil)
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusNotFound)
	}
}

func TestUpdatePresetEndpoint(t *testing.T) {
	api, _, ps := setupPresetTestAPI(t)

	created, _ := ps.Create("Original", preset.ControlRoomSnapshot{ProgramSource: "camera1"})

	body := `{"name":"Updated"}`
	req := httptest.NewRequest("PUT", "/api/presets/"+created.ID, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body: %s", rec.Code, http.StatusOK, rec.Body.String())
	}

	var p preset.Preset
	json.NewDecoder(rec.Body).Decode(&p)
	if p.Name != "Updated" {
		t.Errorf("Name = %q, want %q", p.Name, "Updated")
	}
}

func TestUpdatePresetNotFound(t *testing.T) {
	api, _, _ := setupPresetTestAPI(t)

	body := `{"name":"Updated"}`
	req := httptest.NewRequest("PUT", "/api/presets/nonexistent", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusNotFound)
	}
}

func TestDeletePresetEndpoint(t *testing.T) {
	api, _, ps := setupPresetTestAPI(t)

	created, _ := ps.Create("ToDelete", preset.ControlRoomSnapshot{ProgramSource: "camera1"})

	req := httptest.NewRequest("DELETE", "/api/presets/"+created.ID, nil)
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want %d; body: %s", rec.Code, http.StatusNoContent, rec.Body.String())
	}

	// Verify deleted
	if presets := ps.List(); len(presets) != 0 {
		t.Errorf("expected 0 presets after delete, got %d", len(presets))
	}
}

func TestDeletePresetNotFound(t *testing.T) {
	api, _, _ := setupPresetTestAPI(t)

	req := httptest.NewRequest("DELETE", "/api/presets/nonexistent", nil)
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusNotFound)
	}
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

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body: %s", rec.Code, http.StatusOK, rec.Body.String())
	}

	var resp recallPresetResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(resp.Warnings) != 0 {
		t.Errorf("expected no warnings, got %v", resp.Warnings)
	}

	// Verify program source was changed
	state := sw.State()
	if state.ProgramSource != "camera2" {
		t.Errorf("ProgramSource = %q, want %q", state.ProgramSource, "camera2")
	}
	if state.PreviewSource != "camera1" {
		t.Errorf("PreviewSource = %q, want %q", state.PreviewSource, "camera1")
	}
}

func TestRecallPresetNotFound(t *testing.T) {
	api, _, _ := setupPresetTestAPI(t)

	req := httptest.NewRequest("POST", "/api/presets/nonexistent/recall", nil)
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusNotFound)
	}
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

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body: %s", rec.Code, http.StatusOK, rec.Body.String())
	}

	var resp recallPresetResponse
	json.NewDecoder(rec.Body).Decode(&resp)
	if len(resp.Warnings) == 0 {
		t.Error("expected at least 1 warning for missing source")
	}
}

func TestListPresetsAfterCreate(t *testing.T) {
	api, _, _ := setupPresetTestAPI(t)

	// Create two presets
	body := `{"name":"Preset A"}`
	req := httptest.NewRequest("POST", "/api/presets", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create A: status = %d; body: %s", rec.Code, rec.Body.String())
	}

	body = `{"name":"Preset B"}`
	req = httptest.NewRequest("POST", "/api/presets", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec = httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create B: status = %d; body: %s", rec.Code, rec.Body.String())
	}

	// List
	req = httptest.NewRequest("GET", "/api/presets", nil)
	rec = httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)

	var presets []preset.Preset
	json.NewDecoder(rec.Body).Decode(&presets)
	if len(presets) != 2 {
		t.Errorf("expected 2 presets, got %d", len(presets))
	}
}
