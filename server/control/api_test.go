package control

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/zsiec/prism/distribution"
	"github.com/zsiec/switchframe/server/internal"
	"github.com/zsiec/switchframe/server/switcher"
)

func setupTestAPI(t *testing.T) (*API, *switcher.Switcher) {
	t.Helper()
	programRelay := distribution.NewRelay()
	sw := switcher.New(programRelay)
	r1 := distribution.NewRelay()
	sw.RegisterSource("camera1", r1)
	r2 := distribution.NewRelay()
	sw.RegisterSource("camera2", r2)
	api := NewAPI(sw)
	return api, sw
}

func TestCutEndpoint(t *testing.T) {
	api, sw := setupTestAPI(t)
	sw.Cut(context.Background(), "camera1")
	body := `{"source":"camera2"}`
	req := httptest.NewRequest("POST", "/api/switch/cut", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body: %s", rec.Code, http.StatusOK, rec.Body.String())
	}
	state := sw.State()
	if state.ProgramSource != "camera2" {
		t.Errorf("ProgramSource = %q, want %q", state.ProgramSource, "camera2")
	}
}

func TestCutToMissingSourceReturns404(t *testing.T) {
	api, _ := setupTestAPI(t)
	body := `{"source":"nonexistent"}`
	req := httptest.NewRequest("POST", "/api/switch/cut", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusNotFound)
	}
}

func TestPreviewEndpoint(t *testing.T) {
	api, sw := setupTestAPI(t)
	body := `{"source":"camera1"}`
	req := httptest.NewRequest("POST", "/api/switch/preview", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	state := sw.State()
	if state.PreviewSource != "camera1" {
		t.Errorf("PreviewSource = %q, want %q", state.PreviewSource, "camera1")
	}
}

func TestStateEndpoint(t *testing.T) {
	api, sw := setupTestAPI(t)
	sw.Cut(context.Background(), "camera1")
	req := httptest.NewRequest("GET", "/api/switch/state", nil)
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	var state internal.ControlRoomState
	if err := json.NewDecoder(rec.Body).Decode(&state); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if state.ProgramSource != "camera1" {
		t.Errorf("ProgramSource = %q, want %q", state.ProgramSource, "camera1")
	}
}

func TestHandleSetLabel(t *testing.T) {
	api, sw := setupTestAPI(t)
	defer sw.Close()

	// Set label
	body := strings.NewReader(`{"label":"Camera 1"}`)
	req := httptest.NewRequest("POST", "/api/sources/camera1/label", body)
	w := httptest.NewRecorder()
	api.Mux().ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body: %s", w.Code, http.StatusOK, w.Body.String())
	}

	// Verify in state
	req = httptest.NewRequest("GET", "/api/switch/state", nil)
	w = httptest.NewRecorder()
	api.Mux().ServeHTTP(w, req)
	var state internal.ControlRoomState
	json.NewDecoder(w.Body).Decode(&state)
	if state.Sources["camera1"].Label != "Camera 1" {
		t.Errorf("Label = %q, want %q", state.Sources["camera1"].Label, "Camera 1")
	}

	// Unknown source
	body = strings.NewReader(`{"label":"Nope"}`)
	req = httptest.NewRequest("POST", "/api/sources/nonexistent/label", body)
	w = httptest.NewRecorder()
	api.Mux().ServeHTTP(w, req)
	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusNotFound)
	}
}

func TestCutInvalidJSON(t *testing.T) {
	api, _ := setupTestAPI(t)
	req := httptest.NewRequest("POST", "/api/switch/cut", strings.NewReader("{bad"))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusBadRequest)
	}
}

func TestCutEmptySource(t *testing.T) {
	api, _ := setupTestAPI(t)
	req := httptest.NewRequest("POST", "/api/switch/cut", strings.NewReader(`{"source":""}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusBadRequest)
	}
}

func TestPreviewInvalidJSON(t *testing.T) {
	api, _ := setupTestAPI(t)
	req := httptest.NewRequest("POST", "/api/switch/preview", strings.NewReader("{bad"))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusBadRequest)
	}
}

func TestPreviewEmptySource(t *testing.T) {
	api, _ := setupTestAPI(t)
	req := httptest.NewRequest("POST", "/api/switch/preview", strings.NewReader(`{"source":""}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusBadRequest)
	}
}

func TestSetLabelInvalidJSON(t *testing.T) {
	api, _ := setupTestAPI(t)
	req := httptest.NewRequest("POST", "/api/sources/camera1/label", strings.NewReader("{bad"))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusBadRequest)
	}
}

func TestTransitionReturns501(t *testing.T) {
	api, _ := setupTestAPI(t)
	req := httptest.NewRequest("POST", "/api/switch/transition", strings.NewReader(`{}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)
	if rec.Code != http.StatusNotImplemented {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusNotImplemented)
	}
}

func TestSourcesEndpoint(t *testing.T) {
	api, _ := setupTestAPI(t)
	req := httptest.NewRequest("GET", "/api/sources", nil)
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	var sources map[string]internal.SourceInfo
	if err := json.NewDecoder(rec.Body).Decode(&sources); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(sources) != 2 {
		t.Errorf("got %d sources, want 2", len(sources))
	}
}

// --- Audio API tests ---

// mockMixer implements AudioMixerAPI for testing.
type mockMixer struct {
	levelCalls  []mockLevelCall
	muteCalls   []mockMuteCall
	afvCalls    []mockAFVCall
	masterCalls []float64
	knownKeys   map[string]bool
}

type mockLevelCall struct {
	source string
	level  float64
}

type mockMuteCall struct {
	source string
	muted  bool
}

type mockAFVCall struct {
	source string
	afv    bool
}

func newMockMixer(keys ...string) *mockMixer {
	m := &mockMixer{knownKeys: make(map[string]bool)}
	for _, k := range keys {
		m.knownKeys[k] = true
	}
	return m
}

func (m *mockMixer) SetLevel(sourceKey string, levelDB float64) error {
	if !m.knownKeys[sourceKey] {
		return fmt.Errorf("channel %q not found", sourceKey)
	}
	m.levelCalls = append(m.levelCalls, mockLevelCall{sourceKey, levelDB})
	return nil
}

func (m *mockMixer) SetMuted(sourceKey string, muted bool) error {
	if !m.knownKeys[sourceKey] {
		return fmt.Errorf("channel %q not found", sourceKey)
	}
	m.muteCalls = append(m.muteCalls, mockMuteCall{sourceKey, muted})
	return nil
}

func (m *mockMixer) SetAFV(sourceKey string, afv bool) error {
	if !m.knownKeys[sourceKey] {
		return fmt.Errorf("channel %q not found", sourceKey)
	}
	m.afvCalls = append(m.afvCalls, mockAFVCall{sourceKey, afv})
	return nil
}

func (m *mockMixer) SetMasterLevel(level float64) {
	m.masterCalls = append(m.masterCalls, level)
}

func setupAudioTestAPI(t *testing.T) (*API, *mockMixer) {
	t.Helper()
	programRelay := distribution.NewRelay()
	sw := switcher.New(programRelay)
	mock := newMockMixer("camera1", "camera2")
	api := NewAPI(sw, WithMixer(mock))
	return api, mock
}

func TestAudioLevelEndpoint(t *testing.T) {
	api, mock := setupAudioTestAPI(t)

	body := `{"source":"camera1","level":-6.0}`
	req := httptest.NewRequest("POST", "/api/audio/level", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body: %s", rec.Code, http.StatusOK, rec.Body.String())
	}
	if len(mock.levelCalls) != 1 {
		t.Fatalf("expected 1 SetLevel call, got %d", len(mock.levelCalls))
	}
	if mock.levelCalls[0].source != "camera1" || mock.levelCalls[0].level != -6.0 {
		t.Errorf("SetLevel called with (%q, %f), want (%q, %f)",
			mock.levelCalls[0].source, mock.levelCalls[0].level, "camera1", -6.0)
	}
}

func TestAudioLevelUnknownSource(t *testing.T) {
	api, _ := setupAudioTestAPI(t)

	body := `{"source":"nonexistent","level":-6.0}`
	req := httptest.NewRequest("POST", "/api/audio/level", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d; body: %s", rec.Code, http.StatusNotFound, rec.Body.String())
	}
}

func TestAudioLevelInvalidJSON(t *testing.T) {
	api, _ := setupAudioTestAPI(t)

	req := httptest.NewRequest("POST", "/api/audio/level", strings.NewReader("{bad"))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusBadRequest)
	}
}

func TestAudioLevelEmptySource(t *testing.T) {
	api, _ := setupAudioTestAPI(t)

	body := `{"source":"","level":-6.0}`
	req := httptest.NewRequest("POST", "/api/audio/level", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusBadRequest)
	}
}

func TestAudioLevelNoMixer(t *testing.T) {
	api, _ := setupTestAPI(t) // no mixer attached

	body := `{"source":"camera1","level":-6.0}`
	req := httptest.NewRequest("POST", "/api/audio/level", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)

	if rec.Code != http.StatusNotImplemented {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusNotImplemented)
	}
}

func TestAudioMuteEndpoint(t *testing.T) {
	api, mock := setupAudioTestAPI(t)

	body := `{"source":"camera1","muted":true}`
	req := httptest.NewRequest("POST", "/api/audio/mute", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body: %s", rec.Code, http.StatusOK, rec.Body.String())
	}
	if len(mock.muteCalls) != 1 {
		t.Fatalf("expected 1 SetMuted call, got %d", len(mock.muteCalls))
	}
	if mock.muteCalls[0].source != "camera1" || mock.muteCalls[0].muted != true {
		t.Errorf("SetMuted called with (%q, %v), want (%q, %v)",
			mock.muteCalls[0].source, mock.muteCalls[0].muted, "camera1", true)
	}
}

func TestAudioMuteUnknownSource(t *testing.T) {
	api, _ := setupAudioTestAPI(t)

	body := `{"source":"nonexistent","muted":true}`
	req := httptest.NewRequest("POST", "/api/audio/mute", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusNotFound)
	}
}

func TestAudioMuteInvalidJSON(t *testing.T) {
	api, _ := setupAudioTestAPI(t)

	req := httptest.NewRequest("POST", "/api/audio/mute", strings.NewReader("{bad"))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusBadRequest)
	}
}

func TestAudioMuteEmptySource(t *testing.T) {
	api, _ := setupAudioTestAPI(t)

	body := `{"source":"","muted":true}`
	req := httptest.NewRequest("POST", "/api/audio/mute", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusBadRequest)
	}
}

func TestAudioAFVEndpoint(t *testing.T) {
	api, mock := setupAudioTestAPI(t)

	body := `{"source":"camera2","afv":true}`
	req := httptest.NewRequest("POST", "/api/audio/afv", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body: %s", rec.Code, http.StatusOK, rec.Body.String())
	}
	if len(mock.afvCalls) != 1 {
		t.Fatalf("expected 1 SetAFV call, got %d", len(mock.afvCalls))
	}
	if mock.afvCalls[0].source != "camera2" || mock.afvCalls[0].afv != true {
		t.Errorf("SetAFV called with (%q, %v), want (%q, %v)",
			mock.afvCalls[0].source, mock.afvCalls[0].afv, "camera2", true)
	}
}

func TestAudioAFVUnknownSource(t *testing.T) {
	api, _ := setupAudioTestAPI(t)

	body := `{"source":"nonexistent","afv":true}`
	req := httptest.NewRequest("POST", "/api/audio/afv", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusNotFound)
	}
}

func TestAudioAFVInvalidJSON(t *testing.T) {
	api, _ := setupAudioTestAPI(t)

	req := httptest.NewRequest("POST", "/api/audio/afv", strings.NewReader("{bad"))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusBadRequest)
	}
}

func TestAudioAFVEmptySource(t *testing.T) {
	api, _ := setupAudioTestAPI(t)

	body := `{"source":"","afv":true}`
	req := httptest.NewRequest("POST", "/api/audio/afv", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusBadRequest)
	}
}

func TestAudioMasterEndpoint(t *testing.T) {
	api, mock := setupAudioTestAPI(t)

	body := `{"level":-3.5}`
	req := httptest.NewRequest("POST", "/api/audio/master", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body: %s", rec.Code, http.StatusOK, rec.Body.String())
	}
	if len(mock.masterCalls) != 1 {
		t.Fatalf("expected 1 SetMasterLevel call, got %d", len(mock.masterCalls))
	}
	if mock.masterCalls[0] != -3.5 {
		t.Errorf("SetMasterLevel called with %f, want %f", mock.masterCalls[0], -3.5)
	}
}

func TestAudioMasterInvalidJSON(t *testing.T) {
	api, _ := setupAudioTestAPI(t)

	req := httptest.NewRequest("POST", "/api/audio/master", strings.NewReader("{bad"))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusBadRequest)
	}
}

func TestAudioMasterNoMixer(t *testing.T) {
	api, _ := setupTestAPI(t) // no mixer attached

	body := `{"level":-3.5}`
	req := httptest.NewRequest("POST", "/api/audio/master", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)

	if rec.Code != http.StatusNotImplemented {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusNotImplemented)
	}
}
