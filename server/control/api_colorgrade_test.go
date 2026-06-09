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
	"github.com/zsiec/switchframe/server/colorgrade"
	"github.com/zsiec/switchframe/server/internal"
	"github.com/zsiec/switchframe/server/switcher"
)

type mockColorGradeManager struct {
	enabled      bool
	lookName     string
	intensity    float32
	available    []internal.LookMetadata
	uploaded     map[string]bool
	corrector    colorgrade.CorrectorState
	savedLook    string
	lookSettings map[string]*colorgrade.CorrectorState
	err          error
}

func newMockColorGradeManager() *mockColorGradeManager {
	return &mockColorGradeManager{
		available: []internal.LookMetadata{
			{Name: "golden-hour", Group: "Light", Description: "Amber glow", Builtin: true},
			{Name: "kodachrome", Group: "Film", Description: "Legendary warmth", Builtin: true},
			{Name: "broadcast", Group: "Clean", Description: "Crisp standard", Builtin: true},
		},
		uploaded:     make(map[string]bool),
		intensity:    1.0,
		corrector:    colorgrade.DefaultCorrectorState(),
		lookSettings: make(map[string]*colorgrade.CorrectorState),
	}
}

func (m *mockColorGradeManager) IsEnabled() bool { return m.enabled }

func (m *mockColorGradeManager) SetColorGrade(lookName string, intensity float32) error {
	if m.err != nil {
		return m.err
	}
	found := false
	for _, a := range m.available {
		if a.Name == lookName {
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
	var lookGroup string
	for _, a := range m.available {
		if a.Name == m.lookName {
			lookGroup = a.Group
			break
		}
	}
	return internal.ColorGradeState{
		Enabled:   m.enabled,
		LookName:  m.lookName,
		LookGroup: lookGroup,
		Intensity: m.intensity,
		Available: m.available,
	}
}

func (m *mockColorGradeManager) UploadLUT(name string, data io.Reader) error {
	if m.err != nil {
		return m.err
	}
	m.uploaded[name] = true
	m.available = append(m.available, internal.LookMetadata{
		Name:        name,
		Group:       "Custom",
		Description: "Custom look",
		Builtin:     false,
	})
	return nil
}

func (m *mockColorGradeManager) DeleteLUT(name string) error {
	if m.err != nil {
		return m.err
	}
	return nil
}

func (m *mockColorGradeManager) SetCorrector(state colorgrade.CorrectorState) {
	m.corrector = state
}

func (m *mockColorGradeManager) GetCorrector() colorgrade.CorrectorState {
	return m.corrector
}

func (m *mockColorGradeManager) ResetCorrector() {
	m.corrector = colorgrade.DefaultCorrectorState()
}

func (m *mockColorGradeManager) SaveCustomLook(name string) error {
	if m.err != nil {
		return m.err
	}
	m.savedLook = name
	settings := m.corrector // copy
	m.lookSettings[name] = &settings
	m.available = append(m.available, internal.LookMetadata{
		Name:    name,
		Group:   "Custom",
		Builtin: false,
	})
	return nil
}

func (m *mockColorGradeManager) LoadLookSettings(name string) *colorgrade.CorrectorState {
	return m.lookSettings[name]
}

func setupColorGradeAPI(t *testing.T) (*API, *mockColorGradeManager) {
	t.Helper()
	programRelay := distribution.NewRelay()
	sw := switcher.NewTestSwitcher(t, programRelay)
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
		t.Errorf("lookName = %q, want kodachrome", mgr.lookName)
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

// ─── Corrector Tests ────────────────────────────────────────────────────────

func TestHandleCorrectorGet(t *testing.T) {
	api, _ := setupColorGradeAPI(t)

	req := httptest.NewRequest("GET", "/api/colorgrade/corrector", nil)
	w := httptest.NewRecorder()
	api.Mux().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}

	var state colorgrade.CorrectorState
	if err := json.NewDecoder(w.Body).Decode(&state); err != nil {
		t.Fatalf("decode error: %v", err)
	}

	// Default state: gamma and gain should be [1,1,1], saturation 1.0.
	dflt := colorgrade.DefaultCorrectorState()
	if state.Gamma != dflt.Gamma {
		t.Errorf("gamma = %v, want %v", state.Gamma, dflt.Gamma)
	}
	if state.Saturation != dflt.Saturation {
		t.Errorf("saturation = %f, want %f", state.Saturation, dflt.Saturation)
	}
}

func TestHandleCorrectorSet(t *testing.T) {
	api, mgr := setupColorGradeAPI(t)

	body := `{
		"lift": [0.1, 0.0, -0.1],
		"gamma": [1.0, 1.0, 1.0],
		"gain": [1.0, 1.0, 1.0],
		"temperature": 25,
		"tint": -10,
		"saturation": 1.2,
		"contrast": 0.1,
		"shadows": [0.0, 0.0, 0.0],
		"midtones": [0.0, 0.0, 0.0],
		"highlights": [0.0, 0.0, 0.0],
		"sharpness": 0.5
	}`
	req := httptest.NewRequest("PUT", "/api/colorgrade/corrector", strings.NewReader(body))
	w := httptest.NewRecorder()
	api.Mux().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}

	if mgr.corrector.Temperature != 25 {
		t.Errorf("temperature = %f, want 25", mgr.corrector.Temperature)
	}
	if mgr.corrector.Tint != -10 {
		t.Errorf("tint = %f, want -10", mgr.corrector.Tint)
	}
	if mgr.corrector.Saturation != 1.2 {
		t.Errorf("saturation = %f, want 1.2", mgr.corrector.Saturation)
	}
	if mgr.corrector.Lift[0] != 0.1 {
		t.Errorf("lift[0] = %f, want 0.1", mgr.corrector.Lift[0])
	}
	if mgr.corrector.Sharpness != 0.5 {
		t.Errorf("sharpness = %f, want 0.5", mgr.corrector.Sharpness)
	}
}

func TestHandleCorrectorSet_ValidationFails(t *testing.T) {
	api, _ := setupColorGradeAPI(t)

	tests := []struct {
		name string
		body string
	}{
		{"temperature too high", `{"lift":[0,0,0],"gamma":[1,1,1],"gain":[1,1,1],"temperature":200,"saturation":1}`},
		{"saturation negative", `{"lift":[0,0,0],"gamma":[1,1,1],"gain":[1,1,1],"saturation":-1}`},
		{"gamma too low", `{"lift":[0,0,0],"gamma":[0.01,1,1],"gain":[1,1,1],"saturation":1}`},
		{"gain too high", `{"lift":[0,0,0],"gamma":[1,1,1],"gain":[5,1,1],"saturation":1}`},
		{"lift out of range", `{"lift":[2,0,0],"gamma":[1,1,1],"gain":[1,1,1],"saturation":1}`},
		{"contrast out of range", `{"lift":[0,0,0],"gamma":[1,1,1],"gain":[1,1,1],"saturation":1,"contrast":2}`},
		{"sharpness out of range", `{"lift":[0,0,0],"gamma":[1,1,1],"gain":[1,1,1],"saturation":1,"sharpness":5}`},
		{"curve point out of range", `{"lift":[0,0,0],"gamma":[1,1,1],"gain":[1,1,1],"saturation":1,"curveMaster":[{"x":0,"y":0},{"x":1.5,"y":1}]}`},
		{"curve duplicate x", `{"lift":[0,0,0],"gamma":[1,1,1],"gain":[1,1,1],"saturation":1,"curveMaster":[{"x":0,"y":0},{"x":0.5,"y":0.4},{"x":0.5,"y":0.6},{"x":1,"y":1}]}`},
		{"curve unsorted x", `{"lift":[0,0,0],"gamma":[1,1,1],"gain":[1,1,1],"saturation":1,"curveMaster":[{"x":0,"y":0},{"x":0.7,"y":0.6},{"x":0.3,"y":0.4},{"x":1,"y":1}]}`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest("PUT", "/api/colorgrade/corrector", strings.NewReader(tt.body))
			w := httptest.NewRecorder()
			api.Mux().ServeHTTP(w, req)

			if w.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400, body = %s", w.Code, w.Body.String())
			}
		})
	}
}

// TestHandleCorrectorSet_TooManyCurvePoints rejects a curve that exceeds the
// MaxCurvePoints cap (allocation-amplification DoS guard).
func TestHandleCorrectorSet_TooManyCurvePoints(t *testing.T) {
	api, _ := setupColorGradeAPI(t)

	n := colorgrade.MaxCurvePoints + 1
	pts := make([]colorgrade.CurvePoint, n)
	for i := range pts {
		x := float32(i) / float32(n-1) // strictly increasing, in [0,1]
		pts[i] = colorgrade.CurvePoint{X: x, Y: x}
	}
	state := colorgrade.DefaultCorrectorState()
	state.CurveMaster = pts
	body, err := json.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest("PUT", "/api/colorgrade/corrector", bytes.NewReader(body))
	w := httptest.NewRecorder()
	api.Mux().ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400, body = %s", w.Code, w.Body.String())
	}
}

// TestHandleCorrectorSet_ValidCurveAccepted confirms a well-formed multi-point
// curve (strictly increasing X, within the cap) is still accepted.
func TestHandleCorrectorSet_ValidCurveAccepted(t *testing.T) {
	api, mgr := setupColorGradeAPI(t)

	state := colorgrade.DefaultCorrectorState()
	state.CurveMaster = []colorgrade.CurvePoint{{X: 0, Y: 0}, {X: 0.5, Y: 0.6}, {X: 1, Y: 1}}
	body, err := json.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest("PUT", "/api/colorgrade/corrector", bytes.NewReader(body))
	w := httptest.NewRecorder()
	api.Mux().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body = %s", w.Code, w.Body.String())
	}
	if len(mgr.corrector.CurveMaster) != 3 {
		t.Errorf("curveMaster len = %d, want 3", len(mgr.corrector.CurveMaster))
	}
}

func TestHandleCorrectorReset(t *testing.T) {
	api, mgr := setupColorGradeAPI(t)

	// Set a non-default corrector state first.
	mgr.corrector.Temperature = 50
	mgr.corrector.Saturation = 1.5

	req := httptest.NewRequest("DELETE", "/api/colorgrade/corrector", nil)
	w := httptest.NewRecorder()
	api.Mux().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}

	dflt := colorgrade.DefaultCorrectorState()
	if mgr.corrector.Temperature != dflt.Temperature {
		t.Errorf("temperature = %f, want %f", mgr.corrector.Temperature, dflt.Temperature)
	}
	if mgr.corrector.Saturation != dflt.Saturation {
		t.Errorf("saturation = %f, want %f", mgr.corrector.Saturation, dflt.Saturation)
	}
}

func TestHandleSaveLook(t *testing.T) {
	api, mgr := setupColorGradeAPI(t)

	body := `{"name":"my-custom-look"}`
	req := httptest.NewRequest("POST", "/api/colorgrade/save", strings.NewReader(body))
	w := httptest.NewRecorder()
	api.Mux().ServeHTTP(w, req)

	if w.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201, body = %s", w.Code, w.Body.String())
	}

	if mgr.savedLook != "my-custom-look" {
		t.Errorf("savedLook = %q, want my-custom-look", mgr.savedLook)
	}
}

func TestHandleSaveLook_EmptyName(t *testing.T) {
	api, _ := setupColorGradeAPI(t)

	body := `{"name":""}`
	req := httptest.NewRequest("POST", "/api/colorgrade/save", strings.NewReader(body))
	w := httptest.NewRecorder()
	api.Mux().ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400, body = %s", w.Code, w.Body.String())
	}
}

func TestHandleSaveLook_MissingName(t *testing.T) {
	api, _ := setupColorGradeAPI(t)

	body := `{}`
	req := httptest.NewRequest("POST", "/api/colorgrade/save", strings.NewReader(body))
	w := httptest.NewRecorder()
	api.Mux().ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400, body = %s", w.Code, w.Body.String())
	}
}

func TestHandleCorrectorSet_InvalidJSON(t *testing.T) {
	api, _ := setupColorGradeAPI(t)

	body := `{not valid json}`
	req := httptest.NewRequest("PUT", "/api/colorgrade/corrector", strings.NewReader(body))
	w := httptest.NewRecorder()
	api.Mux().ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400, body = %s", w.Code, w.Body.String())
	}
}

// ─── Look Settings Tests ────────────────────────────────────────────────────

func TestHandleLookSettings(t *testing.T) {
	api, mgr := setupColorGradeAPI(t)

	// Save a custom look (which stores corrector settings in the mock).
	mgr.corrector.Temperature = 40
	mgr.corrector.Saturation = 1.3

	body := `{"name":"warm-look"}`
	req := httptest.NewRequest("POST", "/api/colorgrade/save", strings.NewReader(body))
	w := httptest.NewRecorder()
	api.Mux().ServeHTTP(w, req)

	if w.Code != http.StatusCreated {
		t.Fatalf("save: status = %d, want 201, body = %s", w.Code, w.Body.String())
	}

	// Now retrieve the settings.
	req = httptest.NewRequest("GET", "/api/colorgrade/looks/warm-look/settings", nil)
	w = httptest.NewRecorder()
	api.Mux().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body = %s", w.Code, w.Body.String())
	}

	var state colorgrade.CorrectorState
	if err := json.NewDecoder(w.Body).Decode(&state); err != nil {
		t.Fatalf("decode error: %v", err)
	}

	if state.Temperature != 40 {
		t.Errorf("temperature = %f, want 40", state.Temperature)
	}
	if state.Saturation != 1.3 {
		t.Errorf("saturation = %f, want 1.3", state.Saturation)
	}
}

func TestHandleLookSettings_NotFound(t *testing.T) {
	api, _ := setupColorGradeAPI(t)

	req := httptest.NewRequest("GET", "/api/colorgrade/looks/nonexistent/settings", nil)
	w := httptest.NewRecorder()
	api.Mux().ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404, body = %s", w.Code, w.Body.String())
	}
}
