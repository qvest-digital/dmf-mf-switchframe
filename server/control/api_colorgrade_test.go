package control

import (
	"bytes"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/zsiec/prism/distribution"
	"github.com/zsiec/switchframe/server/internal"
	"github.com/zsiec/switchframe/server/switcher"
)

type mockColorGradeManager struct {
	enabled   bool
	lookName  string
	intensity float32
	available []string
	uploaded  map[string]bool
	err       error
}

func newMockColorGradeManager() *mockColorGradeManager {
	return &mockColorGradeManager{
		available: []string{"golden-hour", "kodachrome", "broadcast"},
		uploaded:  make(map[string]bool),
		intensity: 1.0,
	}
}

func (m *mockColorGradeManager) IsEnabled() bool { return m.enabled }

func (m *mockColorGradeManager) SetColorGrade(lookName string, intensity float32) error {
	if m.err != nil {
		return m.err
	}
	found := false
	for _, a := range m.available {
		if a == lookName {
			found = true
			break
		}
	}
	if !found {
		return io.EOF
	}
	m.enabled = true
	m.lookName = lookName
	m.intensity = intensity
	return nil
}

func (m *mockColorGradeManager) DisableColorGrade() {
	m.enabled = false
	m.lookName = ""
}

func (m *mockColorGradeManager) GetState() internal.ColorGradeState {
	return internal.ColorGradeState{
		Enabled:   m.enabled,
		LookName:  m.lookName,
		Intensity: m.intensity,
		Available: m.available,
	}
}

func (m *mockColorGradeManager) UploadLUT(name string, data io.Reader) error {
	if m.err != nil {
		return m.err
	}
	m.uploaded[name] = true
	m.available = append(m.available, name)
	return nil
}

func (m *mockColorGradeManager) DeleteLUT(name string) error {
	if m.err != nil {
		return m.err
	}
	return nil
}

func setupColorGradeAPI(t *testing.T) (*API, *mockColorGradeManager) {
	t.Helper()
	programRelay := distribution.NewRelay()
	sw := switcher.NewTestSwitcher(programRelay)
	mgr := newMockColorGradeManager()
	api := NewAPI(sw, WithColorGradeManager(mgr))
	return api, mgr
}

func TestColorGrade_GetState(t *testing.T) {
	api, _ := setupColorGradeAPI(t)

	req := httptest.NewRequest("GET", "/api/colorgrade", nil)
	w := httptest.NewRecorder()
	api.Mux().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}

	var state internal.ColorGradeState
	if err := json.NewDecoder(w.Body).Decode(&state); err != nil {
		t.Fatalf("decode error: %v", err)
	}

	if len(state.Available) != 3 {
		t.Errorf("available = %d, want 3", len(state.Available))
	}
	if state.Enabled {
		t.Error("expected disabled by default")
	}
}

func TestColorGrade_SetLook(t *testing.T) {
	api, mgr := setupColorGradeAPI(t)

	body := `{"lookName":"kodachrome","intensity":0.8}`
	req := httptest.NewRequest("PUT", "/api/colorgrade", strings.NewReader(body))
	w := httptest.NewRecorder()
	api.Mux().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}

	if !mgr.enabled {
		t.Error("expected enabled after set")
	}
	if mgr.lookName != "kodachrome" {
		t.Errorf("lookName = %q, want cinematic", mgr.lookName)
	}
	if mgr.intensity != 0.8 {
		t.Errorf("intensity = %f, want 0.8", mgr.intensity)
	}
}

func TestColorGrade_Disable(t *testing.T) {
	api, mgr := setupColorGradeAPI(t)
	mgr.enabled = true
	mgr.lookName = "kodachrome"

	body := `{"enabled":false}`
	req := httptest.NewRequest("PUT", "/api/colorgrade", strings.NewReader(body))
	w := httptest.NewRecorder()
	api.Mux().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}

	if mgr.enabled {
		t.Error("expected disabled")
	}
}

func TestColorGrade_InvalidLookName(t *testing.T) {
	api, _ := setupColorGradeAPI(t)

	body := `{"lookName":"nonexistent","intensity":1.0}`
	req := httptest.NewRequest("PUT", "/api/colorgrade", strings.NewReader(body))
	w := httptest.NewRecorder()
	api.Mux().ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400, body = %s", w.Code, w.Body.String())
	}
}

func TestColorGrade_Upload(t *testing.T) {
	api, mgr := setupColorGradeAPI(t)

	var buf bytes.Buffer
	writer := multipart.NewWriter(&buf)
	_ = writer.WriteField("name", "my-look")
	part, _ := writer.CreateFormFile("file", "my-look.cube")
	_, _ = part.Write([]byte("dummy cube data"))
	_ = writer.Close()

	req := httptest.NewRequest("POST", "/api/colorgrade/upload", &buf)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	w := httptest.NewRecorder()
	api.Mux().ServeHTTP(w, req)

	if w.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201, body = %s", w.Code, w.Body.String())
	}

	if !mgr.uploaded["my-look"] {
		t.Error("expected upload to be recorded")
	}
}

func TestColorGrade_Delete(t *testing.T) {
	api, _ := setupColorGradeAPI(t)

	req := httptest.NewRequest("DELETE", "/api/colorgrade/my-look", nil)
	w := httptest.NewRecorder()
	api.Mux().ServeHTTP(w, req)

	if w.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204", w.Code)
	}
}

func TestColorGrade_MissingLookName(t *testing.T) {
	api, _ := setupColorGradeAPI(t)

	body := `{"intensity":0.5}`
	req := httptest.NewRequest("PUT", "/api/colorgrade", strings.NewReader(body))
	w := httptest.NewRecorder()
	api.Mux().ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400, body = %s", w.Code, w.Body.String())
	}
}
