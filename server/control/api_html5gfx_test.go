package control

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/zsiec/prism/distribution"
	"github.com/zsiec/switchframe/server/graphics"
	"github.com/zsiec/switchframe/server/htmlgfx"
	"github.com/zsiec/switchframe/server/switcher"
)

// mockHTMLGfxManager is a test double for HTMLGfxManager.
type mockHTMLGfxManager struct {
	allocated   map[int]int // browserID -> layerID
	nextID      int
	poolSize    int
	urls        map[int]string
	data        map[int]string
	played      map[int]bool
	stopped     map[int]bool
	nexted      map[int]bool
	customs     map[int][]customCall
	freed       map[int]bool
	allocateErr error
	loadErr     error
	playErr     error
	stopErr     error
	nextErr     error
	updateErr   error
	customErr   error
}

type customCall struct {
	action  string
	payload string
}

func newMockHTMLGfxManager(poolSize int) *mockHTMLGfxManager {
	return &mockHTMLGfxManager{
		allocated: make(map[int]int),
		poolSize:  poolSize,
		urls:      make(map[int]string),
		data:      make(map[int]string),
		played:    make(map[int]bool),
		stopped:   make(map[int]bool),
		nexted:    make(map[int]bool),
		customs:   make(map[int][]customCall),
		freed:     make(map[int]bool),
	}
}

func (m *mockHTMLGfxManager) AllocateBrowser(layerID int) (int, error) {
	if m.allocateErr != nil {
		return 0, m.allocateErr
	}
	if len(m.allocated) >= m.poolSize {
		return 0, fmt.Errorf("browser pool exhausted (pool size: %d)", m.poolSize)
	}
	id := m.nextID
	m.nextID++
	m.allocated[id] = layerID
	return id, nil
}

func (m *mockHTMLGfxManager) FreeBrowser(browserID int) {
	delete(m.allocated, browserID)
	m.freed[browserID] = true
}

func (m *mockHTMLGfxManager) BrowserForLayer(layerID int) int {
	for bid, lid := range m.allocated {
		if lid == layerID {
			return bid
		}
	}
	return -1
}

func (m *mockHTMLGfxManager) LoadURL(browserID int, url string) error {
	if m.loadErr != nil {
		return m.loadErr
	}
	m.urls[browserID] = url
	return nil
}

func (m *mockHTMLGfxManager) Play(browserID int) error {
	if m.playErr != nil {
		return m.playErr
	}
	m.played[browserID] = true
	return nil
}

func (m *mockHTMLGfxManager) Stop(browserID int) error {
	if m.stopErr != nil {
		return m.stopErr
	}
	m.stopped[browserID] = true
	return nil
}

func (m *mockHTMLGfxManager) Next(browserID int) error {
	if m.nextErr != nil {
		return m.nextErr
	}
	m.nexted[browserID] = true
	return nil
}

func (m *mockHTMLGfxManager) Update(browserID int, data string) error {
	if m.updateErr != nil {
		return m.updateErr
	}
	m.data[browserID] = data
	return nil
}

func (m *mockHTMLGfxManager) CustomAction(browserID int, action, payload string) error {
	if m.customErr != nil {
		return m.customErr
	}
	m.customs[browserID] = append(m.customs[browserID], customCall{action, payload})
	return nil
}

func (m *mockHTMLGfxManager) DiscoverTemplates() []htmlgfx.TemplateInfo {
	return nil
}

func (m *mockHTMLGfxManager) Started() bool {
	return true
}

// setupHTML5GfxTestAPI creates a test API with a compositor and optional HTML5 manager.
func setupHTML5GfxTestAPI(t *testing.T, mgr HTMLGfxManager) (*API, *graphics.Compositor) {
	t.Helper()
	programRelay := distribution.NewRelay()
	sw := switcher.NewTestSwitcher(t, programRelay)
	comp := graphics.NewCompositor()
	t.Cleanup(comp.Close)
	api := NewAPI(sw, WithCompositor(comp))
	if mgr != nil {
		api.SetHTMLGfxManager(mgr)
	}
	return api, comp
}

func TestHTML5Load_Success(t *testing.T) {
	mgr := newMockHTMLGfxManager(4)
	api, comp := setupHTML5GfxTestAPI(t, mgr)

	// Create a layer first
	layerID := addLayerViaAPI(t, api)

	body := `{"url":"https://example.com/graphic.html"}`
	req := httptest.NewRequest("POST", fmt.Sprintf("/api/graphics/%d/html5", layerID), strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code, "body: %s", rec.Body.String())

	// Verify mock was called
	require.Len(t, mgr.allocated, 1)
	browserID := mgr.BrowserForLayer(layerID)
	require.NotEqual(t, -1, browserID)
	require.Equal(t, "https://example.com/graphic.html", mgr.urls[browserID])

	// Verify response contains graphics state
	var state graphics.State
	err := json.NewDecoder(rec.Body).Decode(&state)
	require.NoError(t, err)
	_ = comp // ensure compositor returned state
}

func TestHTML5Load_WithInitialData(t *testing.T) {
	mgr := newMockHTMLGfxManager(4)
	api, _ := setupHTML5GfxTestAPI(t, mgr)
	layerID := addLayerViaAPI(t, api)

	// Data is sent as a JSON object (not a string), matching how the UI sends it.
	body := `{"url":"https://example.com/scoreboard.html","data":{"home":0,"away":0}}`
	req := httptest.NewRequest("POST", fmt.Sprintf("/api/graphics/%d/html5", layerID), strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code, "body: %s", rec.Body.String())

	browserID := mgr.BrowserForLayer(layerID)
	require.NotEqual(t, -1, browserID)
	require.Equal(t, `{"home":0,"away":0}`, mgr.data[browserID])
}

func TestHTML5Load_MissingURL(t *testing.T) {
	mgr := newMockHTMLGfxManager(4)
	api, _ := setupHTML5GfxTestAPI(t, mgr)
	layerID := addLayerViaAPI(t, api)

	body := `{"data":"something"}`
	req := httptest.NewRequest("POST", fmt.Sprintf("/api/graphics/%d/html5", layerID), strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)

	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestHTML5Load_NoManager(t *testing.T) {
	api, _ := setupHTML5GfxTestAPI(t, nil) // no manager
	layerID := addLayerViaAPI(t, api)

	body := `{"url":"https://example.com/graphic.html"}`
	req := httptest.NewRequest("POST", fmt.Sprintf("/api/graphics/%d/html5", layerID), strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)

	require.Equal(t, http.StatusServiceUnavailable, rec.Code)
}

func TestHTML5Load_PoolExhausted(t *testing.T) {
	mgr := newMockHTMLGfxManager(1) // only 1 slot
	api, _ := setupHTML5GfxTestAPI(t, mgr)

	// Create two layers
	layer1 := addLayerViaAPI(t, api)
	layer2 := addLayerViaAPI(t, api)

	// Load first succeeds
	body := `{"url":"https://example.com/one.html"}`
	req := httptest.NewRequest("POST", fmt.Sprintf("/api/graphics/%d/html5", layer1), strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)

	// Load second fails (pool exhausted)
	req = httptest.NewRequest("POST", fmt.Sprintf("/api/graphics/%d/html5", layer2), strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec = httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)
	require.Equal(t, http.StatusConflict, rec.Code)
}

func TestHTML5UpdateData_Success(t *testing.T) {
	mgr := newMockHTMLGfxManager(4)
	api, _ := setupHTML5GfxTestAPI(t, mgr)
	layerID := addLayerViaAPI(t, api)

	// Load first
	loadBody := `{"url":"https://example.com/graphic.html"}`
	req := httptest.NewRequest("POST", fmt.Sprintf("/api/graphics/%d/html5", layerID), strings.NewReader(loadBody))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)

	// Update data — sent as a JSON object (not a string), matching how the UI sends it.
	updateBody := `{"data":{"score":42}}`
	req = httptest.NewRequest("PUT", fmt.Sprintf("/api/graphics/%d/html5/data", layerID), strings.NewReader(updateBody))
	req.Header.Set("Content-Type", "application/json")
	rec = httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code, "body: %s", rec.Body.String())
	browserID := mgr.BrowserForLayer(layerID)
	require.Equal(t, `{"score":42}`, mgr.data[browserID])
}

func TestHTML5UpdateData_NotLoaded(t *testing.T) {
	mgr := newMockHTMLGfxManager(4)
	api, _ := setupHTML5GfxTestAPI(t, mgr)
	layerID := addLayerViaAPI(t, api)

	updateBody := `{"data":"{\"score\":42}"}`
	req := httptest.NewRequest("PUT", fmt.Sprintf("/api/graphics/%d/html5/data", layerID), strings.NewReader(updateBody))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)

	require.Equal(t, http.StatusNotFound, rec.Code)
}

func TestHTML5Play_Success(t *testing.T) {
	mgr := newMockHTMLGfxManager(4)
	api, _ := setupHTML5GfxTestAPI(t, mgr)
	layerID := addLayerViaAPI(t, api)

	// Load first
	loadBody := `{"url":"https://example.com/graphic.html"}`
	req := httptest.NewRequest("POST", fmt.Sprintf("/api/graphics/%d/html5", layerID), strings.NewReader(loadBody))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)

	// Play
	req = httptest.NewRequest("POST", fmt.Sprintf("/api/graphics/%d/html5/play", layerID), nil)
	rec = httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code, "body: %s", rec.Body.String())
	browserID := mgr.BrowserForLayer(layerID)
	require.True(t, mgr.played[browserID])
}

func TestHTML5Stop_Success(t *testing.T) {
	mgr := newMockHTMLGfxManager(4)
	api, _ := setupHTML5GfxTestAPI(t, mgr)
	layerID := addLayerViaAPI(t, api)

	// Load first
	loadBody := `{"url":"https://example.com/graphic.html"}`
	req := httptest.NewRequest("POST", fmt.Sprintf("/api/graphics/%d/html5", layerID), strings.NewReader(loadBody))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)

	// Stop
	req = httptest.NewRequest("POST", fmt.Sprintf("/api/graphics/%d/html5/stop", layerID), nil)
	rec = httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code, "body: %s", rec.Body.String())
	browserID := mgr.BrowserForLayer(layerID)
	require.True(t, mgr.stopped[browserID])
}

func TestHTML5Next_Success(t *testing.T) {
	mgr := newMockHTMLGfxManager(4)
	api, _ := setupHTML5GfxTestAPI(t, mgr)
	layerID := addLayerViaAPI(t, api)

	// Load first
	loadBody := `{"url":"https://example.com/graphic.html"}`
	req := httptest.NewRequest("POST", fmt.Sprintf("/api/graphics/%d/html5", layerID), strings.NewReader(loadBody))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)

	// Next
	req = httptest.NewRequest("POST", fmt.Sprintf("/api/graphics/%d/html5/next", layerID), nil)
	rec = httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code, "body: %s", rec.Body.String())
	browserID := mgr.BrowserForLayer(layerID)
	require.True(t, mgr.nexted[browserID])
}

func TestHTML5Custom_Success(t *testing.T) {
	mgr := newMockHTMLGfxManager(4)
	api, _ := setupHTML5GfxTestAPI(t, mgr)
	layerID := addLayerViaAPI(t, api)

	// Load first
	loadBody := `{"url":"https://example.com/graphic.html"}`
	req := httptest.NewRequest("POST", fmt.Sprintf("/api/graphics/%d/html5", layerID), strings.NewReader(loadBody))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)

	// Custom action — payload is sent as a JSON object (not a string), matching how the UI sends it.
	customBody := `{"action":"highlight","payload":{"player":"Smith"}}`
	req = httptest.NewRequest("POST", fmt.Sprintf("/api/graphics/%d/html5/custom", layerID), strings.NewReader(customBody))
	req.Header.Set("Content-Type", "application/json")
	rec = httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code, "body: %s", rec.Body.String())
	browserID := mgr.BrowserForLayer(layerID)
	require.Len(t, mgr.customs[browserID], 1)
	require.Equal(t, "highlight", mgr.customs[browserID][0].action)
	require.Equal(t, `{"player":"Smith"}`, mgr.customs[browserID][0].payload)
}

func TestHTML5Custom_MissingAction(t *testing.T) {
	mgr := newMockHTMLGfxManager(4)
	api, _ := setupHTML5GfxTestAPI(t, mgr)
	layerID := addLayerViaAPI(t, api)

	// Load first
	loadBody := `{"url":"https://example.com/graphic.html"}`
	req := httptest.NewRequest("POST", fmt.Sprintf("/api/graphics/%d/html5", layerID), strings.NewReader(loadBody))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)

	// Custom with no action
	customBody := `{"payload":"data"}`
	req = httptest.NewRequest("POST", fmt.Sprintf("/api/graphics/%d/html5/custom", layerID), strings.NewReader(customBody))
	req.Header.Set("Content-Type", "application/json")
	rec = httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)

	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestHTML5Unload_Success(t *testing.T) {
	mgr := newMockHTMLGfxManager(4)
	api, _ := setupHTML5GfxTestAPI(t, mgr)
	layerID := addLayerViaAPI(t, api)

	// Load first
	loadBody := `{"url":"https://example.com/graphic.html"}`
	req := httptest.NewRequest("POST", fmt.Sprintf("/api/graphics/%d/html5", layerID), strings.NewReader(loadBody))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)

	browserID := mgr.BrowserForLayer(layerID)
	require.NotEqual(t, -1, browserID)

	// Unload
	req = httptest.NewRequest("DELETE", fmt.Sprintf("/api/graphics/%d/html5", layerID), nil)
	rec = httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)

	require.Equal(t, http.StatusNoContent, rec.Code)
	require.True(t, mgr.freed[browserID])
	require.Equal(t, -1, mgr.BrowserForLayer(layerID))
}

func TestHTML5Unload_NotLoaded(t *testing.T) {
	mgr := newMockHTMLGfxManager(4)
	api, _ := setupHTML5GfxTestAPI(t, mgr)
	layerID := addLayerViaAPI(t, api)

	// Unload without loading
	req := httptest.NewRequest("DELETE", fmt.Sprintf("/api/graphics/%d/html5", layerID), nil)
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)

	require.Equal(t, http.StatusNotFound, rec.Code)
}

func TestHTML5Play_NotLoaded(t *testing.T) {
	mgr := newMockHTMLGfxManager(4)
	api, _ := setupHTML5GfxTestAPI(t, mgr)
	layerID := addLayerViaAPI(t, api)

	req := httptest.NewRequest("POST", fmt.Sprintf("/api/graphics/%d/html5/play", layerID), nil)
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)

	require.Equal(t, http.StatusNotFound, rec.Code)
}

func TestHTML5Stop_NotLoaded(t *testing.T) {
	mgr := newMockHTMLGfxManager(4)
	api, _ := setupHTML5GfxTestAPI(t, mgr)
	layerID := addLayerViaAPI(t, api)

	req := httptest.NewRequest("POST", fmt.Sprintf("/api/graphics/%d/html5/stop", layerID), nil)
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)

	require.Equal(t, http.StatusNotFound, rec.Code)
}

func TestHTML5Next_NotLoaded(t *testing.T) {
	mgr := newMockHTMLGfxManager(4)
	api, _ := setupHTML5GfxTestAPI(t, mgr)
	layerID := addLayerViaAPI(t, api)

	req := httptest.NewRequest("POST", fmt.Sprintf("/api/graphics/%d/html5/next", layerID), nil)
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)

	require.Equal(t, http.StatusNotFound, rec.Code)
}

func TestHTML5_InvalidLayerID(t *testing.T) {
	mgr := newMockHTMLGfxManager(4)
	api, _ := setupHTML5GfxTestAPI(t, mgr)

	body := `{"url":"https://example.com/graphic.html"}`
	req := httptest.NewRequest("POST", "/api/graphics/abc/html5", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)

	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestHTML5_InvalidJSON(t *testing.T) {
	mgr := newMockHTMLGfxManager(4)
	api, _ := setupHTML5GfxTestAPI(t, mgr)
	layerID := addLayerViaAPI(t, api)

	req := httptest.NewRequest("POST", fmt.Sprintf("/api/graphics/%d/html5", layerID), strings.NewReader("{invalid"))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)

	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestHTML5UpdateData_NoManager(t *testing.T) {
	api, _ := setupHTML5GfxTestAPI(t, nil)
	layerID := addLayerViaAPI(t, api)

	body := `{"data":"test"}`
	req := httptest.NewRequest("PUT", fmt.Sprintf("/api/graphics/%d/html5/data", layerID), strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)

	require.Equal(t, http.StatusServiceUnavailable, rec.Code)
}

func TestHTML5Play_NoManager(t *testing.T) {
	api, _ := setupHTML5GfxTestAPI(t, nil)
	layerID := addLayerViaAPI(t, api)

	req := httptest.NewRequest("POST", fmt.Sprintf("/api/graphics/%d/html5/play", layerID), nil)
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)

	require.Equal(t, http.StatusServiceUnavailable, rec.Code)
}
