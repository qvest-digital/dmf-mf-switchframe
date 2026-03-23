package control

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/zsiec/prism/distribution"
	"github.com/zsiec/switchframe/server/colorgrade"
	"github.com/zsiec/switchframe/server/switcher"
)

// mockSourceCorrectorManager implements SourceCorrectorManager for testing.
type mockSourceCorrectorManager struct {
	states map[string]colorgrade.CorrectorState
}

func newMockSourceCorrectorManager() *mockSourceCorrectorManager {
	return &mockSourceCorrectorManager{
		states: make(map[string]colorgrade.CorrectorState),
	}
}

func (m *mockSourceCorrectorManager) Get(key string) colorgrade.CorrectorState {
	if s, ok := m.states[key]; ok {
		return s
	}
	return colorgrade.DefaultCorrectorState()
}

func (m *mockSourceCorrectorManager) Set(key string, state colorgrade.CorrectorState) {
	if state.IsDefault() {
		delete(m.states, key)
		return
	}
	m.states[key] = state
}

func (m *mockSourceCorrectorManager) Reset(key string) {
	delete(m.states, key)
}

func (m *mockSourceCorrectorManager) HasCorrection(key string) bool {
	_, ok := m.states[key]
	return ok
}

func setupSourceCorrectorAPI(t *testing.T) (*API, *mockSourceCorrectorManager) {
	t.Helper()
	programRelay := distribution.NewRelay()
	sw := switcher.NewTestSwitcher(programRelay)
	mgr := newMockSourceCorrectorManager()
	api := NewAPI(sw, WithSourceCorrectorManager(mgr))
	return api, mgr
}

func TestHandleSourceCorrectorGet(t *testing.T) {
	api, _ := setupSourceCorrectorAPI(t)

	req := httptest.NewRequest("GET", "/api/sources/cam1/corrector", nil)
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

func TestHandleSourceCorrectorGet_WithExistingState(t *testing.T) {
	api, mgr := setupSourceCorrectorAPI(t)

	// Pre-set a non-default state.
	mgr.states["cam1"] = colorgrade.CorrectorState{
		Lift:        [3]float32{0.1, 0.0, -0.1},
		Gamma:       [3]float32{1.0, 1.0, 1.0},
		Gain:        [3]float32{1.0, 1.0, 1.0},
		Temperature: 30,
		Saturation:  1.3,
	}

	req := httptest.NewRequest("GET", "/api/sources/cam1/corrector", nil)
	w := httptest.NewRecorder()
	api.Mux().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}

	var state colorgrade.CorrectorState
	if err := json.NewDecoder(w.Body).Decode(&state); err != nil {
		t.Fatalf("decode error: %v", err)
	}

	if state.Temperature != 30 {
		t.Errorf("temperature = %f, want 30", state.Temperature)
	}
	if state.Saturation != 1.3 {
		t.Errorf("saturation = %f, want 1.3", state.Saturation)
	}
	if state.Lift[0] != 0.1 {
		t.Errorf("lift[0] = %f, want 0.1", state.Lift[0])
	}
}

func TestHandleSourceCorrectorSet(t *testing.T) {
	api, mgr := setupSourceCorrectorAPI(t)

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
	req := httptest.NewRequest("PUT", "/api/sources/cam1/corrector", strings.NewReader(body))
	w := httptest.NewRecorder()
	api.Mux().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}

	stored := mgr.states["cam1"]
	if stored.Temperature != 25 {
		t.Errorf("temperature = %f, want 25", stored.Temperature)
	}
	if stored.Tint != -10 {
		t.Errorf("tint = %f, want -10", stored.Tint)
	}
	if stored.Saturation != 1.2 {
		t.Errorf("saturation = %f, want 1.2", stored.Saturation)
	}
	if stored.Lift[0] != 0.1 {
		t.Errorf("lift[0] = %f, want 0.1", stored.Lift[0])
	}
	if stored.Sharpness != 0.5 {
		t.Errorf("sharpness = %f, want 0.5", stored.Sharpness)
	}
}

func TestHandleSourceCorrectorSet_InvalidRange(t *testing.T) {
	api, _ := setupSourceCorrectorAPI(t)

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
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest("PUT", "/api/sources/cam1/corrector", strings.NewReader(tt.body))
			w := httptest.NewRecorder()
			api.Mux().ServeHTTP(w, req)

			if w.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400, body = %s", w.Code, w.Body.String())
			}
		})
	}
}

func TestHandleSourceCorrectorSet_InvalidJSON(t *testing.T) {
	api, _ := setupSourceCorrectorAPI(t)

	body := `{not valid json}`
	req := httptest.NewRequest("PUT", "/api/sources/cam1/corrector", strings.NewReader(body))
	w := httptest.NewRecorder()
	api.Mux().ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400, body = %s", w.Code, w.Body.String())
	}
}

func TestHandleSourceCorrectorReset(t *testing.T) {
	api, mgr := setupSourceCorrectorAPI(t)

	// Set a non-default corrector state first.
	mgr.states["cam1"] = colorgrade.CorrectorState{
		Lift:        [3]float32{0, 0, 0},
		Gamma:       [3]float32{1, 1, 1},
		Gain:        [3]float32{1, 1, 1},
		Temperature: 50,
		Saturation:  1.5,
	}

	if !mgr.HasCorrection("cam1") {
		t.Fatal("expected cam1 to have correction before reset")
	}

	req := httptest.NewRequest("DELETE", "/api/sources/cam1/corrector", nil)
	w := httptest.NewRecorder()
	api.Mux().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}

	if mgr.HasCorrection("cam1") {
		t.Error("expected cam1 correction to be removed after reset")
	}

	// Verify GET returns default.
	got := mgr.Get("cam1")
	dflt := colorgrade.DefaultCorrectorState()
	if got.Temperature != dflt.Temperature {
		t.Errorf("temperature = %f, want %f", got.Temperature, dflt.Temperature)
	}
	if got.Saturation != dflt.Saturation {
		t.Errorf("saturation = %f, want %f", got.Saturation, dflt.Saturation)
	}
}

func TestHandleSourceCorrectorSet_DifferentSources(t *testing.T) {
	api, mgr := setupSourceCorrectorAPI(t)

	// Set corrector for cam1.
	body1 := `{
		"lift": [0.1, 0.0, 0.0],
		"gamma": [1.0, 1.0, 1.0],
		"gain": [1.0, 1.0, 1.0],
		"temperature": 20,
		"saturation": 1.1,
		"shadows": [0.0, 0.0, 0.0],
		"midtones": [0.0, 0.0, 0.0],
		"highlights": [0.0, 0.0, 0.0]
	}`
	req := httptest.NewRequest("PUT", "/api/sources/cam1/corrector", strings.NewReader(body1))
	w := httptest.NewRecorder()
	api.Mux().ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("cam1 set: status = %d, body = %s", w.Code, w.Body.String())
	}

	// Set corrector for cam2 with different values.
	body2 := `{
		"lift": [0.0, 0.0, 0.0],
		"gamma": [1.0, 1.0, 1.0],
		"gain": [1.0, 1.0, 1.0],
		"temperature": -30,
		"saturation": 0.8,
		"shadows": [0.0, 0.0, 0.0],
		"midtones": [0.0, 0.0, 0.0],
		"highlights": [0.0, 0.0, 0.0]
	}`
	req = httptest.NewRequest("PUT", "/api/sources/cam2/corrector", strings.NewReader(body2))
	w = httptest.NewRecorder()
	api.Mux().ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("cam2 set: status = %d, body = %s", w.Code, w.Body.String())
	}

	// Verify they're stored independently.
	if mgr.states["cam1"].Temperature != 20 {
		t.Errorf("cam1 temperature = %f, want 20", mgr.states["cam1"].Temperature)
	}
	if mgr.states["cam2"].Temperature != -30 {
		t.Errorf("cam2 temperature = %f, want -30", mgr.states["cam2"].Temperature)
	}
	if mgr.states["cam1"].Saturation != 1.1 {
		t.Errorf("cam1 saturation = %f, want 1.1", mgr.states["cam1"].Saturation)
	}
	if mgr.states["cam2"].Saturation != 0.8 {
		t.Errorf("cam2 saturation = %f, want 0.8", mgr.states["cam2"].Saturation)
	}
}

func TestHandleSourceCorrectorNotRegistered(t *testing.T) {
	// Without WithSourceCorrectorManager, the routes should not be registered.
	programRelay := distribution.NewRelay()
	sw := switcher.NewTestSwitcher(programRelay)
	api := NewAPI(sw)

	req := httptest.NewRequest("GET", "/api/sources/cam1/corrector", nil)
	w := httptest.NewRecorder()
	api.Mux().ServeHTTP(w, req)

	// Should get 404 or 405 since routes not registered.
	if w.Code == http.StatusOK {
		t.Fatal("expected non-200 when source corrector manager not configured")
	}
}
