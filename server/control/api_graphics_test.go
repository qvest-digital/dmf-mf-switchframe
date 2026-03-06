package control

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/zsiec/prism/distribution"
	"github.com/zsiec/switchframe/server/graphics"
	"github.com/zsiec/switchframe/server/switcher"
)

func setupGraphicsTestAPI(t *testing.T) (*API, *graphics.Compositor) {
	t.Helper()
	programRelay := distribution.NewRelay()
	sw := switcher.New(programRelay)
	comp := graphics.NewCompositor()
	t.Cleanup(comp.Close)
	api := NewAPI(sw, WithCompositor(comp))
	return api, comp
}

func TestGraphicsOn_NoOverlay(t *testing.T) {
	api, _ := setupGraphicsTestAPI(t)

	req := httptest.NewRequest("POST", "/api/graphics/on", nil)
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusBadRequest)
	}
}

func TestGraphicsFrame_UploadAndOn(t *testing.T) {
	api, comp := setupGraphicsTestAPI(t)

	// Upload a small 4x4 overlay
	w, h := 4, 4
	rgba := make([]byte, w*h*4)
	for i := 0; i < len(rgba); i += 4 {
		rgba[i] = 255   // R
		rgba[i+3] = 200 // A
	}

	// JSON encode with base64 RGBA (json.Marshal auto-encodes []byte as base64)
	body, _ := json.Marshal(map[string]interface{}{
		"width":    w,
		"height":   h,
		"template": "lower-third",
		"rgba":     rgba, // will be base64-encoded
	})

	req := httptest.NewRequest("POST", "/api/graphics/frame", strings.NewReader(string(body)))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("frame upload: status = %d, want %d; body: %s", rec.Code, http.StatusOK, rec.Body.String())
	}

	// Now activate
	req = httptest.NewRequest("POST", "/api/graphics/on", nil)
	rec = httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("on: status = %d, want %d; body: %s", rec.Code, http.StatusOK, rec.Body.String())
	}

	status := comp.Status()
	if !status.Active {
		t.Error("expected active after ON")
	}
	if status.Template != "lower-third" {
		t.Errorf("template = %q, want %q", status.Template, "lower-third")
	}
}

func TestGraphicsOff(t *testing.T) {
	api, comp := setupGraphicsTestAPI(t)

	// Upload and activate
	uploadOverlay(t, api, 4, 4, "test")
	_ = comp.On()

	// Turn off
	req := httptest.NewRequest("POST", "/api/graphics/off", nil)
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("off: status = %d, want %d; body: %s", rec.Code, http.StatusOK, rec.Body.String())
	}

	if comp.Status().Active {
		t.Error("expected inactive after OFF")
	}
}

func TestGraphicsAutoOn(t *testing.T) {
	api, comp := setupGraphicsTestAPI(t)

	// Upload overlay
	uploadOverlay(t, api, 4, 4, "ticker")

	// Auto-on
	req := httptest.NewRequest("POST", "/api/graphics/auto-on", nil)
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("auto-on: status = %d, want %d; body: %s", rec.Code, http.StatusOK, rec.Body.String())
	}

	if !comp.Status().Active {
		t.Error("expected active after AUTO ON")
	}
}

func TestGraphicsStatus(t *testing.T) {
	api, _ := setupGraphicsTestAPI(t)

	req := httptest.NewRequest("GET", "/api/graphics/status", nil)
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status: status = %d, want %d", rec.Code, http.StatusOK)
	}

	var status graphics.State
	if err := json.NewDecoder(rec.Body).Decode(&status); err != nil {
		t.Fatalf("decode status: %v", err)
	}
	if status.Active {
		t.Error("expected inactive initially")
	}
}

func TestGraphicsFrame_InvalidSize(t *testing.T) {
	api, _ := setupGraphicsTestAPI(t)

	// Wrong RGBA size for 4x4 (should be 64 bytes, sending 10)
	smallRGBA := make([]byte, 10)
	body, _ := json.Marshal(map[string]interface{}{
		"width":    4,
		"height":   4,
		"template": "test",
		"rgba":     smallRGBA,
	})

	req := httptest.NewRequest("POST", "/api/graphics/frame", strings.NewReader(string(body)))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusBadRequest)
	}
}

func TestGraphicsFrame_InvalidDimensions(t *testing.T) {
	api, _ := setupGraphicsTestAPI(t)

	body, _ := json.Marshal(map[string]interface{}{
		"width":    0,
		"height":   4,
		"template": "test",
		"rgba":     []byte{},
	})

	req := httptest.NewRequest("POST", "/api/graphics/frame", strings.NewReader(string(body)))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusBadRequest)
	}
}

func TestGraphicsNotConfigured(t *testing.T) {
	// API without compositor — routes should not be registered
	programRelay := distribution.NewRelay()
	sw := switcher.New(programRelay)
	api := NewAPI(sw)

	req := httptest.NewRequest("GET", "/api/graphics/status", nil)
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)

	// Should get 404 since routes weren't registered
	if rec.Code != http.StatusNotFound && rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("status = %d, want 404 or 405 (route not registered)", rec.Code)
	}
}

// uploadOverlay is a test helper that uploads an RGBA overlay frame.
func uploadOverlay(t *testing.T, api *API, w, h int, template string) {
	t.Helper()
	rgba := make([]byte, w*h*4)
	for i := range rgba {
		rgba[i] = 128
	}
	// Use base64 manually to match JSON encoding of []byte
	body := `{"width":` + itoa(w) + `,"height":` + itoa(h) + `,"template":"` + template + `","rgba":"` + base64.StdEncoding.EncodeToString(rgba) + `"}`
	req := httptest.NewRequest("POST", "/api/graphics/frame", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("upload overlay: status = %d, body: %s", rec.Code, rec.Body.String())
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	s := ""
	for n > 0 {
		s = string(rune('0'+n%10)) + s
		n /= 10
	}
	return s
}
