package control

import (
	"bytes"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/zsiec/prism/distribution"
	"github.com/zsiec/switchframe/server/arrender"
	"github.com/zsiec/switchframe/server/switcher"
)

// =============================================================================
// Mock implementations
// =============================================================================

// mockARRenderer is a test double for ARRenderer.
type mockARRenderer struct {
	scene    *arrender.Scene
	settings arrender.RenderSettings
	setErr   error // injected error for SetSettings
}

func newMockARRenderer() *mockARRenderer {
	return &mockARRenderer{
		scene:    arrender.NewScene(),
		settings: arrender.DefaultRenderSettings(),
	}
}

func (m *mockARRenderer) Scene() *arrender.Scene       { return m.scene }
func (m *mockARRenderer) Settings() arrender.RenderSettings { return m.settings }
func (m *mockARRenderer) SetSettings(s arrender.RenderSettings) error {
	if m.setErr != nil {
		return m.setErr
	}
	m.settings = s
	return nil
}
func (m *mockARRenderer) SceneState() arrender.SceneState {
	state := m.scene.State()
	state.Settings = m.settings
	return state
}

// mockARAssetStore is a test double for ARAssetStore.
type mockARAssetStore struct {
	assets   map[string]*arrender.AssetInfo
	nextID   int
	maxCount int
	uploadErr error
}

func newMockARAssetStore() *mockARAssetStore {
	return &mockARAssetStore{
		assets:   make(map[string]*arrender.AssetInfo),
		maxCount: 64,
	}
}

func (m *mockARAssetStore) Upload(name, filename string, data []byte) (*arrender.AssetInfo, error) {
	if m.uploadErr != nil {
		return nil, m.uploadErr
	}
	if len(m.assets) >= m.maxCount {
		return nil, arrender.ErrTooManyAssets
	}
	if name == "" {
		return nil, arrender.ErrInvalidAsset
	}
	m.nextID++
	id := "asset-" + string(rune('0'+m.nextID))
	info := &arrender.AssetInfo{
		ID:       id,
		Name:     name,
		Filename: filename,
		Format:   "glb",
		ByteSize: int64(len(data)),
	}
	m.assets[id] = info
	cp := *info
	return &cp, nil
}

func (m *mockARAssetStore) Get(id string) (*arrender.AssetInfo, error) {
	info, ok := m.assets[id]
	if !ok {
		return nil, arrender.ErrAssetNotFound
	}
	cp := *info
	return &cp, nil
}

func (m *mockARAssetStore) List() []*arrender.AssetInfo {
	result := make([]*arrender.AssetInfo, 0, len(m.assets))
	for _, info := range m.assets {
		cp := *info
		result = append(result, &cp)
	}
	return result
}

func (m *mockARAssetStore) Delete(id string) error {
	if _, ok := m.assets[id]; !ok {
		return arrender.ErrAssetNotFound
	}
	delete(m.assets, id)
	return nil
}

// =============================================================================
// Setup helpers
// =============================================================================

func setupSceneAPI(t *testing.T) (*API, *mockARRenderer) {
	t.Helper()
	programRelay := distribution.NewRelay()
	sw := switcher.NewTestSwitcher(programRelay)
	renderer := newMockARRenderer()
	api := NewAPI(sw, WithARRenderer(renderer))
	return api, renderer
}

func setupAssetAPI(t *testing.T) (*API, *mockARAssetStore) {
	t.Helper()
	programRelay := distribution.NewRelay()
	sw := switcher.NewTestSwitcher(programRelay)
	store := newMockARAssetStore()
	api := NewAPI(sw, WithARAssetStore(store))
	return api, store
}

func setupFullARAPI(t *testing.T) (*API, *mockARRenderer, *mockARAssetStore) {
	t.Helper()
	programRelay := distribution.NewRelay()
	sw := switcher.NewTestSwitcher(programRelay)
	renderer := newMockARRenderer()
	store := newMockARAssetStore()
	api := NewAPI(sw, WithARRenderer(renderer), WithARAssetStore(store))
	return api, renderer, store
}

// =============================================================================
// Scene: GET /api/ar/scene
// =============================================================================

func TestARScene_Get_Empty(t *testing.T) {
	api, _ := setupSceneAPI(t)

	req := httptest.NewRequest("GET", "/api/ar/scene", nil)
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)

	var state arrender.SceneState
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&state))
	require.Empty(t, state.Objects)
	require.Empty(t, state.Lights)
	require.Equal(t, 1280, state.Settings.Width)
	require.Equal(t, 720, state.Settings.Height)
}

func TestARScene_Get_WithObjects(t *testing.T) {
	api, renderer := setupSceneAPI(t)

	_ = renderer.scene.AddObject(arrender.SceneObject{
		ID:       "cube1",
		AssetID:  "asset-abc",
		Position: [3]float64{1, 2, 3},
		Scale:    [3]float64{1, 1, 1},
		Visible:  true,
	})

	req := httptest.NewRequest("GET", "/api/ar/scene", nil)
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)

	var state arrender.SceneState
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&state))
	require.Len(t, state.Objects, 1)
	require.Equal(t, "cube1", state.Objects[0].ID)
	require.Equal(t, "asset-abc", state.Objects[0].AssetID)
	require.InDelta(t, 1.0, state.Objects[0].Position[0], 0.001)
}

// =============================================================================
// Scene Objects: POST / PUT / DELETE
// =============================================================================

func TestARScene_AddObject(t *testing.T) {
	api, renderer := setupSceneAPI(t)

	body := `{"id":"cube1","assetId":"asset-abc","position":[1,2,3],"rotation":[0,90,0],"scale":[1,1,1],"visible":true}`
	req := httptest.NewRequest("POST", "/api/ar/scene/objects", strings.NewReader(body))
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)
	require.Equal(t, http.StatusCreated, rec.Code)

	var obj arrender.SceneObject
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&obj))
	require.Equal(t, "cube1", obj.ID)
	require.Equal(t, "asset-abc", obj.AssetID)
	require.True(t, obj.Visible)

	// Verify object is in the scene.
	objs := renderer.scene.Objects()
	require.Len(t, objs, 1)
	require.Equal(t, "cube1", objs[0].ID)
}

func TestARScene_AddObject_MissingID(t *testing.T) {
	api, _ := setupSceneAPI(t)

	body := `{"assetId":"asset-abc","position":[1,2,3]}`
	req := httptest.NewRequest("POST", "/api/ar/scene/objects", strings.NewReader(body))
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)
	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestARScene_AddObject_InvalidJSON(t *testing.T) {
	api, _ := setupSceneAPI(t)

	req := httptest.NewRequest("POST", "/api/ar/scene/objects", strings.NewReader("not json"))
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)
	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestARScene_AddObject_DuplicateID(t *testing.T) {
	api, renderer := setupSceneAPI(t)

	_ = renderer.scene.AddObject(arrender.SceneObject{
		ID:      "cube1",
		Visible: true,
	})

	body := `{"id":"cube1","visible":true}`
	req := httptest.NewRequest("POST", "/api/ar/scene/objects", strings.NewReader(body))
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)
	require.Equal(t, http.StatusConflict, rec.Code)
}

func TestARScene_UpdateObject(t *testing.T) {
	api, renderer := setupSceneAPI(t)

	_ = renderer.scene.AddObject(arrender.SceneObject{
		ID:       "cube1",
		Position: [3]float64{0, 0, 0},
		Visible:  true,
	})

	body := `{"position":[5,10,15],"visible":false}`
	req := httptest.NewRequest("PUT", "/api/ar/scene/objects/cube1", strings.NewReader(body))
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)

	var obj arrender.SceneObject
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&obj))
	require.Equal(t, "cube1", obj.ID) // ID preserved
	require.InDelta(t, 5.0, obj.Position[0], 0.001)
	require.False(t, obj.Visible)
}

func TestARScene_UpdateObject_NotFound(t *testing.T) {
	api, _ := setupSceneAPI(t)

	body := `{"position":[5,10,15]}`
	req := httptest.NewRequest("PUT", "/api/ar/scene/objects/nonexistent", strings.NewReader(body))
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)
	require.Equal(t, http.StatusNotFound, rec.Code)
}

func TestARScene_UpdateObject_InvalidJSON(t *testing.T) {
	api, _ := setupSceneAPI(t)

	req := httptest.NewRequest("PUT", "/api/ar/scene/objects/cube1", strings.NewReader("bad"))
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)
	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestARScene_DeleteObject(t *testing.T) {
	api, renderer := setupSceneAPI(t)

	_ = renderer.scene.AddObject(arrender.SceneObject{ID: "cube1", Visible: true})
	_ = renderer.scene.AddObject(arrender.SceneObject{ID: "sphere1", Visible: true})

	req := httptest.NewRequest("DELETE", "/api/ar/scene/objects/cube1", nil)
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)
	require.Equal(t, http.StatusNoContent, rec.Code)

	objs := renderer.scene.Objects()
	require.Len(t, objs, 1)
	require.Equal(t, "sphere1", objs[0].ID)
}

func TestARScene_DeleteObject_NotFound(t *testing.T) {
	api, _ := setupSceneAPI(t)

	req := httptest.NewRequest("DELETE", "/api/ar/scene/objects/nonexistent", nil)
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)
	require.Equal(t, http.StatusNotFound, rec.Code)
}

// =============================================================================
// Scene Lights: POST / PUT / DELETE
// =============================================================================

func TestARScene_AddLight(t *testing.T) {
	api, renderer := setupSceneAPI(t)

	body := `{"id":"key1","type":"point","position":[10,20,30],"color":[1,0.9,0.8],"intensity":100}`
	req := httptest.NewRequest("POST", "/api/ar/scene/lights", strings.NewReader(body))
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)
	require.Equal(t, http.StatusCreated, rec.Code)

	var light arrender.Light
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&light))
	require.Equal(t, "key1", light.ID)
	require.Equal(t, "point", light.Type)
	require.InDelta(t, 100.0, light.Intensity, 0.001)

	lights := renderer.scene.Lights()
	require.Len(t, lights, 1)
	require.Equal(t, "key1", lights[0].ID)
}

func TestARScene_AddLight_MissingID(t *testing.T) {
	api, _ := setupSceneAPI(t)

	body := `{"type":"point","intensity":100}`
	req := httptest.NewRequest("POST", "/api/ar/scene/lights", strings.NewReader(body))
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)
	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestARScene_AddLight_InvalidJSON(t *testing.T) {
	api, _ := setupSceneAPI(t)

	req := httptest.NewRequest("POST", "/api/ar/scene/lights", strings.NewReader("bad"))
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)
	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestARScene_AddLight_DuplicateID(t *testing.T) {
	api, renderer := setupSceneAPI(t)

	_ = renderer.scene.AddLight(arrender.Light{ID: "key1", Type: "point"})

	body := `{"id":"key1","type":"directional"}`
	req := httptest.NewRequest("POST", "/api/ar/scene/lights", strings.NewReader(body))
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)
	require.Equal(t, http.StatusConflict, rec.Code)
}

func TestARScene_UpdateLight(t *testing.T) {
	api, _ := setupSceneAPI(t)

	// Pre-populate via API.
	addBody := `{"id":"fill1","type":"area","intensity":50}`
	addReq := httptest.NewRequest("POST", "/api/ar/scene/lights", strings.NewReader(addBody))
	addRec := httptest.NewRecorder()
	api.Mux().ServeHTTP(addRec, addReq)
	require.Equal(t, http.StatusCreated, addRec.Code)

	// Update.
	updateBody := `{"type":"area","intensity":200,"color":[0.5,0.5,1.0]}`
	req := httptest.NewRequest("PUT", "/api/ar/scene/lights/fill1", strings.NewReader(updateBody))
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)

	var light arrender.Light
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&light))
	require.Equal(t, "fill1", light.ID) // ID preserved
	require.InDelta(t, 200.0, light.Intensity, 0.001)
}

func TestARScene_UpdateLight_NotFound(t *testing.T) {
	api, _ := setupSceneAPI(t)

	body := `{"type":"point","intensity":100}`
	req := httptest.NewRequest("PUT", "/api/ar/scene/lights/nonexistent", strings.NewReader(body))
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)
	require.Equal(t, http.StatusNotFound, rec.Code)
}

func TestARScene_UpdateLight_InvalidJSON(t *testing.T) {
	api, _ := setupSceneAPI(t)

	req := httptest.NewRequest("PUT", "/api/ar/scene/lights/fill1", strings.NewReader("bad"))
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)
	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestARScene_DeleteLight(t *testing.T) {
	api, renderer := setupSceneAPI(t)

	_ = renderer.scene.AddLight(arrender.Light{ID: "key1", Type: "point"})
	_ = renderer.scene.AddLight(arrender.Light{ID: "fill1", Type: "area"})

	req := httptest.NewRequest("DELETE", "/api/ar/scene/lights/key1", nil)
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)
	require.Equal(t, http.StatusNoContent, rec.Code)

	lights := renderer.scene.Lights()
	require.Len(t, lights, 1)
	require.Equal(t, "fill1", lights[0].ID)
}

func TestARScene_DeleteLight_NotFound(t *testing.T) {
	api, _ := setupSceneAPI(t)

	req := httptest.NewRequest("DELETE", "/api/ar/scene/lights/nonexistent", nil)
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)
	require.Equal(t, http.StatusNotFound, rec.Code)
}

// =============================================================================
// Render Settings: GET / PUT
// =============================================================================

func TestARScene_RenderSettingsGet(t *testing.T) {
	api, _ := setupSceneAPI(t)

	req := httptest.NewRequest("GET", "/api/ar/render/settings", nil)
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)

	var settings arrender.RenderSettings
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&settings))
	require.Equal(t, 1280, settings.Width)
	require.Equal(t, 720, settings.Height)
	require.Equal(t, 2, settings.SPP)
	require.True(t, settings.Denoise)
}

func TestARScene_RenderSettingsSet(t *testing.T) {
	api, renderer := setupSceneAPI(t)

	body := `{"width":1920,"height":1080,"spp":4,"denoise":false}`
	req := httptest.NewRequest("PUT", "/api/ar/render/settings", strings.NewReader(body))
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)

	var settings arrender.RenderSettings
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&settings))
	require.Equal(t, 1920, settings.Width)
	require.Equal(t, 1080, settings.Height)
	require.Equal(t, 4, settings.SPP)
	require.False(t, settings.Denoise)

	// Verify it was stored.
	require.Equal(t, 1920, renderer.settings.Width)
}

func TestARScene_RenderSettingsSet_InvalidJSON(t *testing.T) {
	api, _ := setupSceneAPI(t)

	req := httptest.NewRequest("PUT", "/api/ar/render/settings", strings.NewReader("bad"))
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)
	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestARScene_RenderSettingsSet_RendererError(t *testing.T) {
	api, renderer := setupSceneAPI(t)
	renderer.setErr = arrender.ErrRendererNotReady

	body := `{"width":1920,"height":1080,"spp":4,"denoise":false}`
	req := httptest.NewRequest("PUT", "/api/ar/render/settings", strings.NewReader(body))
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)
	require.Equal(t, http.StatusServiceUnavailable, rec.Code)
}

// =============================================================================
// Scene routes: nil renderer (routes not registered)
// =============================================================================

func TestARScene_NilRenderer_RoutesNotRegistered(t *testing.T) {
	programRelay := distribution.NewRelay()
	sw := switcher.NewTestSwitcher(programRelay)
	api := NewAPI(sw) // no WithARRenderer

	endpoints := []struct {
		method string
		path   string
	}{
		{"GET", "/api/ar/scene"},
		{"POST", "/api/ar/scene/objects"},
		{"PUT", "/api/ar/scene/objects/test"},
		{"DELETE", "/api/ar/scene/objects/test"},
		{"POST", "/api/ar/scene/lights"},
		{"PUT", "/api/ar/scene/lights/test"},
		{"DELETE", "/api/ar/scene/lights/test"},
		{"GET", "/api/ar/render/settings"},
		{"PUT", "/api/ar/render/settings"},
	}

	for _, ep := range endpoints {
		req := httptest.NewRequest(ep.method, ep.path, nil)
		rec := httptest.NewRecorder()
		api.Mux().ServeHTTP(rec, req)
		require.NotEqual(t, http.StatusOK, rec.Code, "%s %s should not be routed", ep.method, ep.path)
		require.NotEqual(t, http.StatusCreated, rec.Code, "%s %s should not be routed", ep.method, ep.path)
	}
}

// =============================================================================
// Asset Management: POST / GET / DELETE
// =============================================================================

func TestARAsset_Upload(t *testing.T) {
	api, store := setupAssetAPI(t)

	// Create multipart form.
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	_ = w.WriteField("name", "Test Model")
	part, _ := w.CreateFormFile("asset", "model.glb")
	_, _ = part.Write([]byte("fake-glb-data"))
	_ = w.Close()

	req := httptest.NewRequest("POST", "/api/ar/assets", &buf)
	req.Header.Set("Content-Type", w.FormDataContentType())
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)
	require.Equal(t, http.StatusCreated, rec.Code)

	var info arrender.AssetInfo
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&info))
	require.Equal(t, "Test Model", info.Name)
	require.Equal(t, "model.glb", info.Filename)
	require.Equal(t, int64(13), info.ByteSize) // len("fake-glb-data")

	// Verify stored.
	require.Len(t, store.assets, 1)
}

func TestARAsset_Upload_NoNameUsesFilename(t *testing.T) {
	api, _ := setupAssetAPI(t)

	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	// No "name" field — should use filename.
	part, _ := w.CreateFormFile("asset", "helmet.glb")
	_, _ = part.Write([]byte("data"))
	_ = w.Close()

	req := httptest.NewRequest("POST", "/api/ar/assets", &buf)
	req.Header.Set("Content-Type", w.FormDataContentType())
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)
	require.Equal(t, http.StatusCreated, rec.Code)

	var info arrender.AssetInfo
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&info))
	require.Equal(t, "helmet.glb", info.Name)
}

func TestARAsset_Upload_MissingFile(t *testing.T) {
	api, _ := setupAssetAPI(t)

	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	_ = w.WriteField("name", "No File")
	_ = w.Close()

	req := httptest.NewRequest("POST", "/api/ar/assets", &buf)
	req.Header.Set("Content-Type", w.FormDataContentType())
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)
	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestARAsset_Upload_InvalidForm(t *testing.T) {
	api, _ := setupAssetAPI(t)

	req := httptest.NewRequest("POST", "/api/ar/assets", strings.NewReader("not a multipart form"))
	req.Header.Set("Content-Type", "text/plain")
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)
	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestARAsset_Upload_TooMany(t *testing.T) {
	api, store := setupAssetAPI(t)
	store.maxCount = 0 // already full

	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	_ = w.WriteField("name", "One Too Many")
	part, _ := w.CreateFormFile("asset", "model.glb")
	_, _ = part.Write([]byte("data"))
	_ = w.Close()

	req := httptest.NewRequest("POST", "/api/ar/assets", &buf)
	req.Header.Set("Content-Type", w.FormDataContentType())
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)
	require.Equal(t, http.StatusConflict, rec.Code)
}

func TestARAsset_List_Empty(t *testing.T) {
	api, _ := setupAssetAPI(t)

	req := httptest.NewRequest("GET", "/api/ar/assets", nil)
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)

	var assets []*arrender.AssetInfo
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&assets))
	require.Empty(t, assets)
}

func TestARAsset_List_WithAssets(t *testing.T) {
	api, store := setupAssetAPI(t)

	store.assets["a1"] = &arrender.AssetInfo{ID: "a1", Name: "Model A"}
	store.assets["a2"] = &arrender.AssetInfo{ID: "a2", Name: "Model B"}

	req := httptest.NewRequest("GET", "/api/ar/assets", nil)
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)

	var assets []*arrender.AssetInfo
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&assets))
	require.Len(t, assets, 2)
}

func TestARAsset_Get(t *testing.T) {
	api, store := setupAssetAPI(t)

	store.assets["abc123"] = &arrender.AssetInfo{
		ID:       "abc123",
		Name:     "Helmet",
		Filename: "abc123.glb",
		Format:   "glb",
		ByteSize: 1024,
	}

	req := httptest.NewRequest("GET", "/api/ar/assets/abc123", nil)
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)

	var info arrender.AssetInfo
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&info))
	require.Equal(t, "abc123", info.ID)
	require.Equal(t, "Helmet", info.Name)
	require.Equal(t, int64(1024), info.ByteSize)
}

func TestARAsset_Get_NotFound(t *testing.T) {
	api, _ := setupAssetAPI(t)

	req := httptest.NewRequest("GET", "/api/ar/assets/nonexistent", nil)
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)
	require.Equal(t, http.StatusNotFound, rec.Code)
}

func TestARAsset_Delete(t *testing.T) {
	api, store := setupAssetAPI(t)

	store.assets["abc123"] = &arrender.AssetInfo{ID: "abc123", Name: "Helmet"}

	req := httptest.NewRequest("DELETE", "/api/ar/assets/abc123", nil)
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)
	require.Equal(t, http.StatusNoContent, rec.Code)

	require.Empty(t, store.assets)
}

func TestARAsset_Delete_NotFound(t *testing.T) {
	api, _ := setupAssetAPI(t)

	req := httptest.NewRequest("DELETE", "/api/ar/assets/nonexistent", nil)
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)
	require.Equal(t, http.StatusNotFound, rec.Code)
}

// =============================================================================
// Asset routes: nil store (routes not registered)
// =============================================================================

func TestARAsset_NilStore_RoutesNotRegistered(t *testing.T) {
	programRelay := distribution.NewRelay()
	sw := switcher.NewTestSwitcher(programRelay)
	api := NewAPI(sw) // no WithARAssetStore

	endpoints := []struct {
		method string
		path   string
	}{
		{"POST", "/api/ar/assets"},
		{"GET", "/api/ar/assets"},
		{"GET", "/api/ar/assets/test"},
		{"DELETE", "/api/ar/assets/test"},
	}

	for _, ep := range endpoints {
		req := httptest.NewRequest(ep.method, ep.path, nil)
		rec := httptest.NewRecorder()
		api.Mux().ServeHTTP(rec, req)
		require.NotEqual(t, http.StatusOK, rec.Code, "%s %s should not be routed", ep.method, ep.path)
		require.NotEqual(t, http.StatusCreated, rec.Code, "%s %s should not be routed", ep.method, ep.path)
	}
}

// =============================================================================
// Error mapping tests
// =============================================================================

func TestARRender_ErrorMappings(t *testing.T) {
	require.Equal(t, http.StatusNotFound, errorStatus(arrender.ErrObjectNotFound))
	require.Equal(t, http.StatusNotFound, errorStatus(arrender.ErrLightNotFound))
	require.Equal(t, http.StatusNotFound, errorStatus(arrender.ErrAssetNotFound))
	require.Equal(t, http.StatusBadRequest, errorStatus(arrender.ErrInvalidAsset))
	require.Equal(t, http.StatusConflict, errorStatus(arrender.ErrTooManyAssets))
	require.Equal(t, http.StatusServiceUnavailable, errorStatus(arrender.ErrRendererNotReady))
	require.Equal(t, http.StatusConflict, errorStatus(arrender.ErrDuplicateID))
}

// =============================================================================
// Integration: scene + assets together
// =============================================================================

func TestARScene_FullWorkflow(t *testing.T) {
	api, _, _ := setupFullARAPI(t)

	// 1. Add two objects.
	addObj := func(body string) {
		req := httptest.NewRequest("POST", "/api/ar/scene/objects", strings.NewReader(body))
		rec := httptest.NewRecorder()
		api.Mux().ServeHTTP(rec, req)
		require.Equal(t, http.StatusCreated, rec.Code)
	}
	addObj(`{"id":"cube","assetId":"a1","position":[0,0,0],"scale":[1,1,1],"visible":true}`)
	addObj(`{"id":"sphere","assetId":"a2","position":[5,0,0],"scale":[2,2,2],"visible":true}`)

	// 2. Add a light.
	lightBody := `{"id":"sun","type":"directional","direction":[0,-1,0],"color":[1,1,1],"intensity":50}`
	req := httptest.NewRequest("POST", "/api/ar/scene/lights", strings.NewReader(lightBody))
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)
	require.Equal(t, http.StatusCreated, rec.Code)

	// 3. Get full scene.
	req = httptest.NewRequest("GET", "/api/ar/scene", nil)
	rec = httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)

	var state arrender.SceneState
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&state))
	require.Len(t, state.Objects, 2)
	require.Len(t, state.Lights, 1)

	// 4. Update object position.
	req = httptest.NewRequest("PUT", "/api/ar/scene/objects/cube", strings.NewReader(`{"position":[10,10,10]}`))
	rec = httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)

	// 5. Delete an object.
	req = httptest.NewRequest("DELETE", "/api/ar/scene/objects/sphere", nil)
	rec = httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)
	require.Equal(t, http.StatusNoContent, rec.Code)

	// 6. Verify final state.
	req = httptest.NewRequest("GET", "/api/ar/scene", nil)
	rec = httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)

	require.NoError(t, json.NewDecoder(rec.Body).Decode(&state))
	require.Len(t, state.Objects, 1)
	require.Equal(t, "cube", state.Objects[0].ID)
	require.InDelta(t, 10.0, state.Objects[0].Position[0], 0.001)
	require.Len(t, state.Lights, 1)
}
