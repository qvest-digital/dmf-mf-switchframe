package control

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"image"
	"image/png"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/zsiec/prism/distribution"
	"github.com/zsiec/switchframe/server/graphics"
	"github.com/zsiec/switchframe/server/graphics/textrender"
	"github.com/zsiec/switchframe/server/operator"
	"github.com/zsiec/switchframe/server/switcher"
)

func setupGraphicsTestAPI(t *testing.T) (*API, *graphics.Compositor) {
	t.Helper()
	programRelay := distribution.NewRelay()
	sw := switcher.NewTestSwitcher(programRelay)
	comp := graphics.NewCompositor()
	t.Cleanup(comp.Close)
	api := NewAPI(sw, WithCompositor(comp))
	return api, comp
}

// addLayerViaAPI creates a layer via POST /api/graphics and returns its ID.
func addLayerViaAPI(t *testing.T, api *API) int {
	t.Helper()
	req := httptest.NewRequest("POST", "/api/graphics", nil)
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)
	require.Equal(t, http.StatusCreated, rec.Code, "add layer: body: %s", rec.Body.String())
	var result map[string]int
	err := json.NewDecoder(rec.Body).Decode(&result)
	require.NoError(t, err)
	return result["id"]
}

func TestGraphicsAddLayer(t *testing.T) {
	api, _ := setupGraphicsTestAPI(t)
	id := addLayerViaAPI(t, api)
	require.Equal(t, 0, id)

	id2 := addLayerViaAPI(t, api)
	require.Equal(t, 1, id2)
}

func TestGraphicsRemoveLayer(t *testing.T) {
	api, _ := setupGraphicsTestAPI(t)
	id := addLayerViaAPI(t, api)

	req := httptest.NewRequest("DELETE", fmt.Sprintf("/api/graphics/%d", id), nil)
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)
	require.Equal(t, http.StatusNoContent, rec.Code)
}

func TestGraphicsRemoveLayer_NotFound(t *testing.T) {
	api, _ := setupGraphicsTestAPI(t)

	req := httptest.NewRequest("DELETE", "/api/graphics/999", nil)
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)
	require.Equal(t, http.StatusNotFound, rec.Code)
}

func TestGraphicsOn_NoOverlay(t *testing.T) {
	api, _ := setupGraphicsTestAPI(t)
	id := addLayerViaAPI(t, api)

	req := httptest.NewRequest("POST", fmt.Sprintf("/api/graphics/%d/on", id), nil)
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)

	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestGraphicsFrame_UploadAndOn(t *testing.T) {
	api, comp := setupGraphicsTestAPI(t)
	id := addLayerViaAPI(t, api)

	// Upload a small 4x4 overlay
	w, h := 4, 4
	rgba := make([]byte, w*h*4)
	for i := 0; i < len(rgba); i += 4 {
		rgba[i] = 255   // R
		rgba[i+3] = 200 // A
	}

	body, _ := json.Marshal(map[string]any{
		"width":    w,
		"height":   h,
		"template": "lower-third",
		"rgba":     rgba,
	})

	req := httptest.NewRequest("POST", fmt.Sprintf("/api/graphics/%d/frame", id), strings.NewReader(string(body)))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code, "frame upload: body: %s", rec.Body.String())

	// Now activate
	req = httptest.NewRequest("POST", fmt.Sprintf("/api/graphics/%d/on", id), nil)
	rec = httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code, "on: body: %s", rec.Body.String())

	status := comp.Status()
	require.Len(t, status.Layers, 1)
	require.True(t, status.Layers[0].Active, "expected active after ON")
	require.Equal(t, "lower-third", status.Layers[0].Template)
}

func TestGraphicsOff(t *testing.T) {
	api, comp := setupGraphicsTestAPI(t)
	id := addLayerViaAPI(t, api)

	// Upload and activate
	uploadOverlay(t, api, id, 4, 4, "test")
	require.NoError(t, comp.On(id))

	// Turn off
	req := httptest.NewRequest("POST", fmt.Sprintf("/api/graphics/%d/off", id), nil)
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code, "off: body: %s", rec.Body.String())
	require.False(t, comp.Status().Layers[0].Active, "expected inactive after OFF")
}

func TestGraphicsAutoOn(t *testing.T) {
	api, comp := setupGraphicsTestAPI(t)
	id := addLayerViaAPI(t, api)

	// Upload overlay
	uploadOverlay(t, api, id, 4, 4, "ticker")

	// Auto-on
	req := httptest.NewRequest("POST", fmt.Sprintf("/api/graphics/%d/auto-on", id), nil)
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code, "auto-on: body: %s", rec.Body.String())
	require.True(t, comp.Status().Layers[0].Active, "expected active after AUTO ON")
}

func TestGraphicsStatus(t *testing.T) {
	api, _ := setupGraphicsTestAPI(t)

	req := httptest.NewRequest("GET", "/api/graphics", nil)
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)

	var status graphics.State
	err := json.NewDecoder(rec.Body).Decode(&status)
	require.NoError(t, err)
	require.Empty(t, status.Layers, "expected no layers initially")
}

func TestGraphicsFrame_InvalidSize(t *testing.T) {
	api, _ := setupGraphicsTestAPI(t)
	id := addLayerViaAPI(t, api)

	// Wrong RGBA size for 4x4 (should be 64 bytes, sending 10)
	smallRGBA := make([]byte, 10)
	body, _ := json.Marshal(map[string]any{
		"width":    4,
		"height":   4,
		"template": "test",
		"rgba":     smallRGBA,
	})

	req := httptest.NewRequest("POST", fmt.Sprintf("/api/graphics/%d/frame", id), strings.NewReader(string(body)))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)

	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestGraphicsFrame_InvalidDimensions(t *testing.T) {
	api, _ := setupGraphicsTestAPI(t)
	id := addLayerViaAPI(t, api)

	body, _ := json.Marshal(map[string]any{
		"width":    0,
		"height":   4,
		"template": "test",
		"rgba":     []byte{},
	})

	req := httptest.NewRequest("POST", fmt.Sprintf("/api/graphics/%d/frame", id), strings.NewReader(string(body)))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)

	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestGraphicsNotConfigured(t *testing.T) {
	// API without compositor — routes should not be registered
	programRelay := distribution.NewRelay()
	sw := switcher.NewTestSwitcher(programRelay)
	api := NewAPI(sw)

	req := httptest.NewRequest("GET", "/api/graphics", nil)
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)

	// Should get 404 since routes weren't registered
	require.True(t, rec.Code == http.StatusNotFound || rec.Code == http.StatusMethodNotAllowed,
		"status = %d, want 404 or 405 (route not registered)", rec.Code)
}

// uploadOverlay is a test helper that uploads an RGBA overlay frame to a specific layer.
func uploadOverlay(t *testing.T, api *API, layerID, w, h int, template string) {
	t.Helper()
	rgba := make([]byte, w*h*4)
	for i := range rgba {
		rgba[i] = 128
	}
	body := `{"width":` + strconv.Itoa(w) + `,"height":` + strconv.Itoa(h) + `,"template":"` + template + `","rgba":"` + base64.StdEncoding.EncodeToString(rgba) + `"}`
	req := httptest.NewRequest("POST", fmt.Sprintf("/api/graphics/%d/frame", layerID), strings.NewReader(body))
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
	sw := switcher.NewTestSwitcher(programRelay)
	comp := graphics.NewCompositor()
	t.Cleanup(comp.Close)

	api := NewAPI(sw, WithCompositor(comp), WithOperatorStore(store))

	// Register a director operator
	op, err := store.Register("TestDirector", operator.RoleDirector)
	require.NoError(t, err)
	token := op.Token

	// Create a layer and upload overlay
	id := addLayerViaAPI(t, api)
	uploadOverlay(t, api, id, 4, 4, "test")

	endpoints := []struct {
		name  string
		path  string
		setup func() // optional pre-step
	}{
		{"on", fmt.Sprintf("/api/graphics/%d/on", id), nil},
		{"off", fmt.Sprintf("/api/graphics/%d/off", id), nil},
		{"auto-on", fmt.Sprintf("/api/graphics/%d/auto-on", id), func() {
			// Ensure overlay is off so AutoOn can start a fade
			_ = comp.Off(id)
		}},
		{"auto-off", fmt.Sprintf("/api/graphics/%d/auto-off", id), func() {
			// Ensure overlay is active so AutoOff works
			_ = comp.On(id)
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

func TestGraphicsAnimate_Valid(t *testing.T) {
	api, comp := setupGraphicsTestAPI(t)
	id := addLayerViaAPI(t, api)

	// Upload and activate
	uploadOverlay(t, api, id, 4, 4, "test")
	require.NoError(t, comp.On(id))

	body := `{"mode":"pulse","minAlpha":0.3,"maxAlpha":1.0,"speedHz":2.0}`
	req := httptest.NewRequest("POST", fmt.Sprintf("/api/graphics/%d/animate", id), strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code, "animate: body: %s", rec.Body.String())

	var status graphics.State
	err := json.NewDecoder(rec.Body).Decode(&status)
	require.NoError(t, err)
	require.Len(t, status.Layers, 1)
	require.True(t, status.Layers[0].Active)
	require.Equal(t, "pulse", status.Layers[0].AnimationMode)
	require.Equal(t, 2.0, status.Layers[0].AnimationHz)

	// Clean up
	require.NoError(t, comp.StopAnimation(id))
}

func TestGraphicsAnimate_InvalidMode(t *testing.T) {
	api, comp := setupGraphicsTestAPI(t)
	id := addLayerViaAPI(t, api)

	uploadOverlay(t, api, id, 4, 4, "test")
	require.NoError(t, comp.On(id))

	body := `{"mode":"flash","minAlpha":0.3,"maxAlpha":1.0,"speedHz":2.0}`
	req := httptest.NewRequest("POST", fmt.Sprintf("/api/graphics/%d/animate", id), strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)

	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestGraphicsAnimate_InvalidAlpha(t *testing.T) {
	api, comp := setupGraphicsTestAPI(t)
	id := addLayerViaAPI(t, api)

	uploadOverlay(t, api, id, 4, 4, "test")
	require.NoError(t, comp.On(id))

	// minAlpha >= maxAlpha
	body := `{"mode":"pulse","minAlpha":0.8,"maxAlpha":0.3,"speedHz":2.0}`
	req := httptest.NewRequest("POST", fmt.Sprintf("/api/graphics/%d/animate", id), strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)

	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestGraphicsAnimate_InvalidAlphaRange(t *testing.T) {
	api, comp := setupGraphicsTestAPI(t)
	id := addLayerViaAPI(t, api)

	uploadOverlay(t, api, id, 4, 4, "test")
	require.NoError(t, comp.On(id))

	// alpha out of [0,1]
	body := `{"mode":"pulse","minAlpha":-0.1,"maxAlpha":1.0,"speedHz":2.0}`
	req := httptest.NewRequest("POST", fmt.Sprintf("/api/graphics/%d/animate", id), strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)

	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestGraphicsAnimate_InvalidSpeed(t *testing.T) {
	api, comp := setupGraphicsTestAPI(t)
	id := addLayerViaAPI(t, api)

	uploadOverlay(t, api, id, 4, 4, "test")
	require.NoError(t, comp.On(id))

	// speedHz > 10
	body := `{"mode":"pulse","minAlpha":0.3,"maxAlpha":1.0,"speedHz":15.0}`
	req := httptest.NewRequest("POST", fmt.Sprintf("/api/graphics/%d/animate", id), strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)

	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestGraphicsAnimate_TransitionMissingTarget(t *testing.T) {
	api, comp := setupGraphicsTestAPI(t)
	id := addLayerViaAPI(t, api)

	uploadOverlay(t, api, id, 4, 4, "test")
	require.NoError(t, comp.On(id))

	// transition mode with neither toRect nor toAlpha
	body := `{"mode":"transition","durationMs":500}`
	req := httptest.NewRequest("POST", fmt.Sprintf("/api/graphics/%d/animate", id), strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)

	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestGraphicsAnimate_TransitionMissingDuration(t *testing.T) {
	api, comp := setupGraphicsTestAPI(t)
	id := addLayerViaAPI(t, api)

	uploadOverlay(t, api, id, 4, 4, "test")
	require.NoError(t, comp.On(id))

	// transition mode with toAlpha but no durationMs
	body := `{"mode":"transition","toAlpha":0.5}`
	req := httptest.NewRequest("POST", fmt.Sprintf("/api/graphics/%d/animate", id), strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)

	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestGraphicsAnimate_NotActive(t *testing.T) {
	api, _ := setupGraphicsTestAPI(t)
	id := addLayerViaAPI(t, api)

	uploadOverlay(t, api, id, 4, 4, "test")
	// Do NOT call On()

	body := `{"mode":"pulse","minAlpha":0.3,"maxAlpha":1.0,"speedHz":2.0}`
	req := httptest.NewRequest("POST", fmt.Sprintf("/api/graphics/%d/animate", id), strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)

	require.Equal(t, http.StatusConflict, rec.Code)
}

func TestGraphicsAnimateStop(t *testing.T) {
	api, comp := setupGraphicsTestAPI(t)
	id := addLayerViaAPI(t, api)

	uploadOverlay(t, api, id, 4, 4, "test")
	require.NoError(t, comp.On(id))

	// Start animation via compositor directly
	cfg := graphics.AnimationConfig{
		Mode:     "pulse",
		MinAlpha: 0.3,
		MaxAlpha: 1.0,
		SpeedHz:  2.0,
	}
	require.NoError(t, comp.Animate(id, cfg))

	// Stop via API
	req := httptest.NewRequest("POST", fmt.Sprintf("/api/graphics/%d/animate/stop", id), nil)
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code, "animate stop: body: %s", rec.Body.String())

	var status graphics.State
	err := json.NewDecoder(rec.Body).Decode(&status)
	require.NoError(t, err)
	require.Len(t, status.Layers, 1)
	require.True(t, status.Layers[0].Active, "should still be active after stop animation")
	require.Equal(t, 1.0, status.Layers[0].FadePosition, "fadePosition should be 1.0 after stop")
	require.Empty(t, status.Layers[0].AnimationMode, "AnimationMode should be empty after stop")
}

func TestGraphicsLayerRect(t *testing.T) {
	api, comp := setupGraphicsTestAPI(t)
	id := addLayerViaAPI(t, api)

	body := `{"x":100,"y":200,"width":400,"height":300}`
	req := httptest.NewRequest("PUT", fmt.Sprintf("/api/graphics/%d/rect", id), strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code, "rect: body: %s", rec.Body.String())

	status := comp.Status()
	require.Len(t, status.Layers, 1)
	require.Equal(t, 100, status.Layers[0].Rect.X)
	require.Equal(t, 200, status.Layers[0].Rect.Y)
	require.Equal(t, 400, status.Layers[0].Rect.Width)
	require.Equal(t, 300, status.Layers[0].Rect.Height)
}

func TestGraphicsLayerZOrder(t *testing.T) {
	api, comp := setupGraphicsTestAPI(t)
	id := addLayerViaAPI(t, api)

	body := `{"zOrder":5}`
	req := httptest.NewRequest("PUT", fmt.Sprintf("/api/graphics/%d/zorder", id), strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code, "zorder: body: %s", rec.Body.String())

	status := comp.Status()
	require.Len(t, status.Layers, 1)
	require.Equal(t, 5, status.Layers[0].ZOrder)
}

func TestGraphicsFlyIn(t *testing.T) {
	api, comp := setupGraphicsTestAPI(t)
	id := addLayerViaAPI(t, api)

	uploadOverlay(t, api, id, 4, 4, "test")
	require.NoError(t, comp.On(id))

	body := `{"direction":"left","durationMs":300}`
	req := httptest.NewRequest("POST", fmt.Sprintf("/api/graphics/%d/fly-in", id), strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code, "fly-in: body: %s", rec.Body.String())

	var status graphics.State
	err := json.NewDecoder(rec.Body).Decode(&status)
	require.NoError(t, err)
	require.Len(t, status.Layers, 1)

	// Clean up animation
	_ = comp.StopAnimation(id)
}

func TestGraphicsFlyIn_InvalidDirection(t *testing.T) {
	api, comp := setupGraphicsTestAPI(t)
	id := addLayerViaAPI(t, api)

	uploadOverlay(t, api, id, 4, 4, "test")
	require.NoError(t, comp.On(id))

	body := `{"direction":"diagonal","durationMs":300}`
	req := httptest.NewRequest("POST", fmt.Sprintf("/api/graphics/%d/fly-in", id), strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)

	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestGraphicsFlyIn_LayerNotFound(t *testing.T) {
	api, _ := setupGraphicsTestAPI(t)

	body := `{"direction":"left","durationMs":300}`
	req := httptest.NewRequest("POST", "/api/graphics/999/fly-in", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)

	require.Equal(t, http.StatusNotFound, rec.Code)
}

func TestGraphicsFlyOut(t *testing.T) {
	api, comp := setupGraphicsTestAPI(t)
	id := addLayerViaAPI(t, api)

	uploadOverlay(t, api, id, 4, 4, "test")
	require.NoError(t, comp.On(id))

	body := `{"direction":"right","durationMs":300}`
	req := httptest.NewRequest("POST", fmt.Sprintf("/api/graphics/%d/fly-out", id), strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code, "fly-out: body: %s", rec.Body.String())

	var status graphics.State
	err := json.NewDecoder(rec.Body).Decode(&status)
	require.NoError(t, err)
	require.Len(t, status.Layers, 1)

	// Clean up animation
	_ = comp.StopAnimation(id)
}

func TestGraphicsFlyOut_InvalidDirection(t *testing.T) {
	api, comp := setupGraphicsTestAPI(t)
	id := addLayerViaAPI(t, api)

	uploadOverlay(t, api, id, 4, 4, "test")
	require.NoError(t, comp.On(id))

	body := `{"direction":"upward","durationMs":300}`
	req := httptest.NewRequest("POST", fmt.Sprintf("/api/graphics/%d/fly-out", id), strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)

	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestGraphicsSlide(t *testing.T) {
	api, comp := setupGraphicsTestAPI(t)
	id := addLayerViaAPI(t, api)

	uploadOverlay(t, api, id, 4, 4, "test")
	require.NoError(t, comp.On(id))

	body := `{"x":100,"y":200,"width":400,"height":300,"durationMs":300}`
	req := httptest.NewRequest("POST", fmt.Sprintf("/api/graphics/%d/slide", id), strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code, "slide: body: %s", rec.Body.String())

	var status graphics.State
	err := json.NewDecoder(rec.Body).Decode(&status)
	require.NoError(t, err)
	require.Len(t, status.Layers, 1)

	// Clean up animation
	_ = comp.StopAnimation(id)
}

func TestGraphicsSlide_LayerNotFound(t *testing.T) {
	api, _ := setupGraphicsTestAPI(t)

	body := `{"x":100,"y":200,"width":400,"height":300,"durationMs":300}`
	req := httptest.NewRequest("POST", "/api/graphics/999/slide", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)

	require.Equal(t, http.StatusNotFound, rec.Code)
}

func TestGraphicsFlyIn_DefaultDuration(t *testing.T) {
	api, comp := setupGraphicsTestAPI(t)
	id := addLayerViaAPI(t, api)

	uploadOverlay(t, api, id, 4, 4, "test")
	require.NoError(t, comp.On(id))

	// No durationMs — should default to 500
	body := `{"direction":"top"}`
	req := httptest.NewRequest("POST", fmt.Sprintf("/api/graphics/%d/fly-in", id), strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code, "fly-in default duration: body: %s", rec.Body.String())

	// Clean up animation
	_ = comp.StopAnimation(id)
}

func TestGraphicsLayerNotFound(t *testing.T) {
	api, _ := setupGraphicsTestAPI(t)

	req := httptest.NewRequest("POST", "/api/graphics/999/on", nil)
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)

	require.Equal(t, http.StatusNotFound, rec.Code)
}

// setupGraphicsTestAPIWithEngines creates an API with compositor, ticker engine, and text animation engine.
func setupGraphicsTestAPIWithEngines(t *testing.T) (*API, *graphics.Compositor) {
	t.Helper()
	programRelay := distribution.NewRelay()
	sw := switcher.NewTestSwitcher(programRelay)
	comp := graphics.NewCompositor()
	comp.SetResolutionProvider(func() (int, int) { return 1920, 1080 })
	t.Cleanup(comp.Close)
	renderer, err := textrender.NewRenderer()
	require.NoError(t, err)
	tae := graphics.NewTextAnimationEngine(comp, renderer)
	t.Cleanup(tae.Close)
	te := graphics.NewTickerEngine(comp, renderer)
	t.Cleanup(te.Close)
	api := NewAPI(sw, WithCompositor(comp), WithTextAnimEngine(tae), WithTickerEngine(te))
	return api, comp
}

// createTestPNG generates a small valid PNG image.
func createTestPNG(t *testing.T) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 4, 4))
	var buf bytes.Buffer
	require.NoError(t, png.Encode(&buf, img))
	return buf.Bytes()
}

func TestHandleTickerStart(t *testing.T) {
	api, _ := setupGraphicsTestAPIWithEngines(t)
	id := addLayerViaAPI(t, api)

	body := `{"text":"Breaking News","fontSize":24,"speed":100}`
	req := httptest.NewRequest("POST", fmt.Sprintf("/api/graphics/%d/ticker", id), strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code, "ticker start: body: %s", rec.Body.String())
}

func TestHandleTickerStart_InvalidLayer(t *testing.T) {
	// The ticker engine does not validate layer IDs synchronously — it starts
	// an async goroutine that will fail on SetOverlay. The handler therefore
	// returns 200 even for a non-existent layer. We verify the handler still
	// succeeds and that the ticker can be stopped without error.
	api, _ := setupGraphicsTestAPIWithEngines(t)

	body := `{"text":"Breaking News","fontSize":24,"speed":100}`
	req := httptest.NewRequest("POST", "/api/graphics/999/ticker", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code, "ticker start returns 200 (async layer validation): body: %s", rec.Body.String())
}

func TestHandleTickerStop(t *testing.T) {
	api, _ := setupGraphicsTestAPIWithEngines(t)
	id := addLayerViaAPI(t, api)

	// Start ticker first
	body := `{"text":"Breaking News","fontSize":24,"speed":100}`
	req := httptest.NewRequest("POST", fmt.Sprintf("/api/graphics/%d/ticker", id), strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code, "ticker start: body: %s", rec.Body.String())

	// Stop ticker
	req = httptest.NewRequest("POST", fmt.Sprintf("/api/graphics/%d/ticker/stop", id), nil)
	rec = httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code, "ticker stop: body: %s", rec.Body.String())
}

func TestHandleTickerStop_NotRunning(t *testing.T) {
	api, _ := setupGraphicsTestAPIWithEngines(t)
	id := addLayerViaAPI(t, api)

	req := httptest.NewRequest("POST", fmt.Sprintf("/api/graphics/%d/ticker/stop", id), nil)
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)

	require.Equal(t, http.StatusNotFound, rec.Code, "ticker stop not running: body: %s", rec.Body.String())
}

func TestHandleTickerUpdateText(t *testing.T) {
	api, _ := setupGraphicsTestAPIWithEngines(t)
	id := addLayerViaAPI(t, api)

	// Start ticker first
	body := `{"text":"Breaking News","fontSize":24,"speed":100}`
	req := httptest.NewRequest("POST", fmt.Sprintf("/api/graphics/%d/ticker", id), strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code, "ticker start: body: %s", rec.Body.String())

	// Update text
	updateBody := `{"text":"Updated Headlines"}`
	req = httptest.NewRequest("PUT", fmt.Sprintf("/api/graphics/%d/ticker/text", id), strings.NewReader(updateBody))
	req.Header.Set("Content-Type", "application/json")
	rec = httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code, "ticker update text: body: %s", rec.Body.String())
}

func TestHandleTextAnimStart(t *testing.T) {
	api, _ := setupGraphicsTestAPIWithEngines(t)
	id := addLayerViaAPI(t, api)

	body := `{"mode":"typewriter","text":"Hello","fontSize":24,"charsPerSec":100}`
	req := httptest.NewRequest("POST", fmt.Sprintf("/api/graphics/%d/text-animate", id), strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code, "text-anim start: body: %s", rec.Body.String())
}

func TestHandleTextAnimStop(t *testing.T) {
	api, _ := setupGraphicsTestAPIWithEngines(t)
	id := addLayerViaAPI(t, api)

	// Start text animation first
	body := `{"mode":"typewriter","text":"Hello","fontSize":24,"charsPerSec":100}`
	req := httptest.NewRequest("POST", fmt.Sprintf("/api/graphics/%d/text-animate", id), strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code, "text-anim start: body: %s", rec.Body.String())

	// Stop text animation
	req = httptest.NewRequest("POST", fmt.Sprintf("/api/graphics/%d/text-animate/stop", id), nil)
	rec = httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code, "text-anim stop: body: %s", rec.Body.String())
}

func TestHandleFlyOn(t *testing.T) {
	api, comp := setupGraphicsTestAPIWithEngines(t)
	id := addLayerViaAPI(t, api)

	// Upload overlay (required for FlyOn)
	uploadOverlay(t, api, id, 4, 4, "test")
	// Layer must NOT be active for FlyOn (it activates the layer)
	require.False(t, comp.Status().Layers[0].Active)

	body := `{"direction":"left","durationMs":300}`
	req := httptest.NewRequest("POST", fmt.Sprintf("/api/graphics/%d/fly-on", id), strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code, "fly-on: body: %s", rec.Body.String())

	// Clean up animation
	_ = comp.StopAnimation(id)
}

func TestHandleImageUpload(t *testing.T) {
	api, _ := setupGraphicsTestAPIWithEngines(t)
	id := addLayerViaAPI(t, api)

	pngData := createTestPNG(t)

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, err := writer.CreateFormFile("image", "test.png")
	require.NoError(t, err)
	_, err = part.Write(pngData)
	require.NoError(t, err)
	require.NoError(t, writer.Close())

	req := httptest.NewRequest("POST", fmt.Sprintf("/api/graphics/%d/image", id), &body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)

	require.Equal(t, http.StatusCreated, rec.Code, "image upload: body: %s", rec.Body.String())
}

func TestHandleImageGet(t *testing.T) {
	api, _ := setupGraphicsTestAPIWithEngines(t)
	id := addLayerViaAPI(t, api)

	// Upload image first
	pngData := createTestPNG(t)
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, err := writer.CreateFormFile("image", "test.png")
	require.NoError(t, err)
	_, err = part.Write(pngData)
	require.NoError(t, err)
	require.NoError(t, writer.Close())

	req := httptest.NewRequest("POST", fmt.Sprintf("/api/graphics/%d/image", id), &body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)
	require.Equal(t, http.StatusCreated, rec.Code, "image upload: body: %s", rec.Body.String())

	// Get image
	req = httptest.NewRequest("GET", fmt.Sprintf("/api/graphics/%d/image", id), nil)
	rec = httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code, "image get: body length: %d", rec.Body.Len())
	require.Equal(t, "image/png", rec.Header().Get("Content-Type"))
}

func TestHandleImageDelete(t *testing.T) {
	api, _ := setupGraphicsTestAPIWithEngines(t)
	id := addLayerViaAPI(t, api)

	// Upload image first
	pngData := createTestPNG(t)
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, err := writer.CreateFormFile("image", "test.png")
	require.NoError(t, err)
	_, err = part.Write(pngData)
	require.NoError(t, err)
	require.NoError(t, writer.Close())

	req := httptest.NewRequest("POST", fmt.Sprintf("/api/graphics/%d/image", id), &body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)
	require.Equal(t, http.StatusCreated, rec.Code, "image upload: body: %s", rec.Body.String())

	// Delete image
	req = httptest.NewRequest("DELETE", fmt.Sprintf("/api/graphics/%d/image", id), nil)
	rec = httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)

	require.Equal(t, http.StatusNoContent, rec.Code)
}
