package control

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/zsiec/prism/distribution"
	"github.com/zsiec/switchframe/server/graphics"
	"github.com/zsiec/switchframe/server/operator"
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

	require.Equal(t, http.StatusBadRequest, rec.Code)
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

	require.Equal(t, http.StatusOK, rec.Code, "frame upload: body: %s", rec.Body.String())

	// Now activate
	req = httptest.NewRequest("POST", "/api/graphics/on", nil)
	rec = httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code, "on: body: %s", rec.Body.String())

	status := comp.Status()
	require.True(t, status.Active, "expected active after ON")
	require.Equal(t, "lower-third", status.Template)
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

	require.Equal(t, http.StatusOK, rec.Code, "off: body: %s", rec.Body.String())
	require.False(t, comp.Status().Active, "expected inactive after OFF")
}

func TestGraphicsAutoOn(t *testing.T) {
	api, comp := setupGraphicsTestAPI(t)

	// Upload overlay
	uploadOverlay(t, api, 4, 4, "ticker")

	// Auto-on
	req := httptest.NewRequest("POST", "/api/graphics/auto-on", nil)
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code, "auto-on: body: %s", rec.Body.String())
	require.True(t, comp.Status().Active, "expected active after AUTO ON")
}

func TestGraphicsStatus(t *testing.T) {
	api, _ := setupGraphicsTestAPI(t)

	req := httptest.NewRequest("GET", "/api/graphics/status", nil)
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)

	var status graphics.State
	err := json.NewDecoder(rec.Body).Decode(&status)
	require.NoError(t, err)
	require.False(t, status.Active, "expected inactive initially")
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

	require.Equal(t, http.StatusBadRequest, rec.Code)
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

	require.Equal(t, http.StatusBadRequest, rec.Code)
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
	require.True(t, rec.Code == http.StatusNotFound || rec.Code == http.StatusMethodNotAllowed,
		"status = %d, want 404 or 405 (route not registered)", rec.Code)
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
	require.Equal(t, http.StatusOK, rec.Code, "upload overlay: body: %s", rec.Body.String())
}

func TestGraphicsHandlers_SetLastOperator(t *testing.T) {
	storePath := filepath.Join(t.TempDir(), "operators.json")
	store, err := operator.NewStore(storePath)
	require.NoError(t, err)

	programRelay := distribution.NewRelay()
	sw := switcher.New(programRelay)
	comp := graphics.NewCompositor()
	t.Cleanup(comp.Close)

	api := NewAPI(sw, WithCompositor(comp), WithOperatorStore(store))

	// Register a director operator
	op, err := store.Register("TestDirector", operator.RoleDirector)
	require.NoError(t, err)
	token := op.Token

	// Upload overlay so On/AutoOn succeed
	uploadOverlay(t, api, 4, 4, "test")

	endpoints := []struct {
		name  string
		path  string
		setup func() // optional pre-step
	}{
		{"on", "/api/graphics/on", nil},
		{"off", "/api/graphics/off", nil},
		{"auto-on", "/api/graphics/auto-on", func() {
			// Ensure overlay is off so AutoOn can start a fade
			_ = comp.Off()
		}},
		{"auto-off", "/api/graphics/auto-off", func() {
			// Ensure overlay is active so AutoOff works
			_ = comp.On()
		}},
	}

	for _, ep := range endpoints {
		t.Run(ep.name, func(t *testing.T) {
			// Clear last operator
			api.SetLastOperator(nil)
			require.Nil(t, api.LastOperator())

			if ep.setup != nil {
				ep.setup()
			}

			req := httptest.NewRequest("POST", ep.path, nil)
			req.Header.Set("Authorization", "Bearer "+token)
			rec := httptest.NewRecorder()
			api.Mux().ServeHTTP(rec, req)

			require.Equal(t, http.StatusOK, rec.Code, "%s: body: %s", ep.name, rec.Body.String())

			last := api.LastOperator()
			require.NotNil(t, last, "%s should set lastOperator", ep.name)
			require.Equal(t, "TestDirector", *last)
		})
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
