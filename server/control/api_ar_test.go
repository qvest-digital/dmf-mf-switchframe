package control

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/zsiec/prism/distribution"
	"github.com/zsiec/switchframe/server/arinfer"
	"github.com/zsiec/switchframe/server/switcher"
	"github.com/zsiec/switchframe/server/tracking"
)

// mockARInferenceEngine is a test double for ARInferenceEngine.
type mockARInferenceEngine struct {
	depthEnabled bool
	samEnabled   bool
	prompts      []arinfer.SAMPrompt
	enableErr    error // injected error for EnableDepth/EnableSAM
	promptErr    error // injected error for AddPrompt/RemovePrompt
}

func newMockARInferenceEngine() *mockARInferenceEngine {
	return &mockARInferenceEngine{}
}

func (m *mockARInferenceEngine) EnableDepth(planPath string) error {
	if m.enableErr != nil {
		return m.enableErr
	}
	m.depthEnabled = true
	return nil
}

func (m *mockARInferenceEngine) DisableDepth() {
	m.depthEnabled = false
}

func (m *mockARInferenceEngine) EnableSAM(encoderPath, decoderPath string) error {
	if m.enableErr != nil {
		return m.enableErr
	}
	m.samEnabled = true
	return nil
}

func (m *mockARInferenceEngine) DisableSAM() {
	m.samEnabled = false
	m.prompts = nil
}

func (m *mockARInferenceEngine) AddPrompt(p arinfer.SAMPrompt) error {
	if m.promptErr != nil {
		return m.promptErr
	}
	m.prompts = append(m.prompts, p)
	return nil
}

func (m *mockARInferenceEngine) RemovePrompt(id string) error {
	if m.promptErr != nil {
		return m.promptErr
	}
	for i, p := range m.prompts {
		if p.ID == id {
			m.prompts = append(m.prompts[:i], m.prompts[i+1:]...)
			return nil
		}
	}
	return arinfer.ErrNotReady
}

func (m *mockARInferenceEngine) Prompts() []arinfer.SAMPrompt {
	if len(m.prompts) == 0 {
		return nil
	}
	out := make([]arinfer.SAMPrompt, len(m.prompts))
	copy(out, m.prompts)
	return out
}

func (m *mockARInferenceEngine) Status() arinfer.Status {
	return arinfer.Status{
		DepthReady: m.depthEnabled,
		SAMReady:   m.samEnabled,
	}
}

// setupARAPI builds a test API with an AR inference engine attached.
func setupARAPI(t *testing.T) (*API, *mockARInferenceEngine) {
	t.Helper()
	programRelay := distribution.NewRelay()
	sw := switcher.NewTestSwitcher(programRelay)
	engine := newMockARInferenceEngine()
	api := NewAPI(sw, WithARInference(engine))
	return api, engine
}

// --- Enable ---

func TestAR_Enable_Depth(t *testing.T) {
	api, engine := setupARAPI(t)

	body := `{"mode":"depth"}`
	req := httptest.NewRequest("POST", "/api/ar/occlusion/enable", strings.NewReader(body))
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)

	require.True(t, engine.depthEnabled)
	require.False(t, engine.samEnabled)

	var status arinfer.Status
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&status))
	require.True(t, status.DepthReady)
	require.False(t, status.SAMReady)
}

func TestAR_Enable_SAM(t *testing.T) {
	api, engine := setupARAPI(t)

	body := `{"mode":"sam"}`
	req := httptest.NewRequest("POST", "/api/ar/occlusion/enable", strings.NewReader(body))
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)

	require.False(t, engine.depthEnabled)
	require.True(t, engine.samEnabled)
}

func TestAR_Enable_Both(t *testing.T) {
	api, engine := setupARAPI(t)

	body := `{"mode":"both"}`
	req := httptest.NewRequest("POST", "/api/ar/occlusion/enable", strings.NewReader(body))
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)

	require.True(t, engine.depthEnabled)
	require.True(t, engine.samEnabled)

	var status arinfer.Status
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&status))
	require.True(t, status.DepthReady)
	require.True(t, status.SAMReady)
}

func TestAR_Enable_InvalidMode(t *testing.T) {
	api, _ := setupARAPI(t)

	body := `{"mode":"invalid"}`
	req := httptest.NewRequest("POST", "/api/ar/occlusion/enable", strings.NewReader(body))
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)
	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestAR_Enable_InvalidJSON(t *testing.T) {
	api, _ := setupARAPI(t)

	req := httptest.NewRequest("POST", "/api/ar/occlusion/enable", strings.NewReader("not json"))
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)
	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestAR_Enable_DepthError(t *testing.T) {
	api, engine := setupARAPI(t)
	engine.enableErr = arinfer.ErrModelMissing

	body := `{"mode":"depth"}`
	req := httptest.NewRequest("POST", "/api/ar/occlusion/enable", strings.NewReader(body))
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)
	require.Equal(t, http.StatusNotFound, rec.Code)
}

func TestAR_Enable_SAMError(t *testing.T) {
	api, engine := setupARAPI(t)
	engine.enableErr = arinfer.ErrModelMissing

	body := `{"mode":"sam"}`
	req := httptest.NewRequest("POST", "/api/ar/occlusion/enable", strings.NewReader(body))
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)
	require.Equal(t, http.StatusNotFound, rec.Code)
}

// --- Disable ---

func TestAR_Disable(t *testing.T) {
	api, engine := setupARAPI(t)
	engine.depthEnabled = true
	engine.samEnabled = true

	req := httptest.NewRequest("POST", "/api/ar/occlusion/disable", nil)
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)

	require.False(t, engine.depthEnabled)
	require.False(t, engine.samEnabled)

	var status arinfer.Status
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&status))
	require.False(t, status.DepthReady)
	require.False(t, status.SAMReady)
}

func TestAR_Disable_AlreadyDisabled(t *testing.T) {
	api, _ := setupARAPI(t)

	// Disabling when already disabled is a no-op — should succeed.
	req := httptest.NewRequest("POST", "/api/ar/occlusion/disable", nil)
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
}

// --- Prompts ---

func TestAR_PromptAdd(t *testing.T) {
	api, engine := setupARAPI(t)
	engine.samEnabled = true

	body := `{"id":"anchor1","type":"point","points":[[960,540]],"label":"News anchor"}`
	req := httptest.NewRequest("POST", "/api/ar/occlusion/prompt", strings.NewReader(body))
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)
	require.Equal(t, http.StatusCreated, rec.Code)

	var prompt arinfer.SAMPrompt
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&prompt))
	require.Equal(t, "anchor1", prompt.ID)
	require.Equal(t, "point", prompt.Type)
	require.Equal(t, "News anchor", prompt.Label)

	require.Len(t, engine.prompts, 1)
	require.Equal(t, "anchor1", engine.prompts[0].ID)
}

func TestAR_PromptAdd_Box(t *testing.T) {
	api, engine := setupARAPI(t)
	engine.samEnabled = true

	body := `{"id":"desk1","type":"box","box":[100,200,500,600],"label":"News desk"}`
	req := httptest.NewRequest("POST", "/api/ar/occlusion/prompt", strings.NewReader(body))
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)
	require.Equal(t, http.StatusCreated, rec.Code)

	require.Len(t, engine.prompts, 1)
	require.Equal(t, "box", engine.prompts[0].Type)
	require.Equal(t, [4]int{100, 200, 500, 600}, engine.prompts[0].Box)
}

func TestAR_PromptAdd_MissingID(t *testing.T) {
	api, _ := setupARAPI(t)

	body := `{"type":"point","points":[[960,540]]}`
	req := httptest.NewRequest("POST", "/api/ar/occlusion/prompt", strings.NewReader(body))
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)
	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestAR_PromptAdd_InvalidType(t *testing.T) {
	api, _ := setupARAPI(t)

	body := `{"id":"anchor1","type":"polygon"}`
	req := httptest.NewRequest("POST", "/api/ar/occlusion/prompt", strings.NewReader(body))
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)
	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestAR_PromptAdd_InvalidJSON(t *testing.T) {
	api, _ := setupARAPI(t)

	req := httptest.NewRequest("POST", "/api/ar/occlusion/prompt", strings.NewReader("not json"))
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)
	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestAR_PromptAdd_SAMNotReady(t *testing.T) {
	api, engine := setupARAPI(t)
	engine.promptErr = arinfer.ErrNotReady

	body := `{"id":"anchor1","type":"point","points":[[960,540]]}`
	req := httptest.NewRequest("POST", "/api/ar/occlusion/prompt", strings.NewReader(body))
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)
	require.Equal(t, http.StatusConflict, rec.Code)
}

func TestAR_PromptList_Empty(t *testing.T) {
	api, _ := setupARAPI(t)

	req := httptest.NewRequest("GET", "/api/ar/occlusion/prompts", nil)
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)

	var prompts []arinfer.SAMPrompt
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&prompts))
	require.Empty(t, prompts)
}

func TestAR_PromptList_WithPrompts(t *testing.T) {
	api, engine := setupARAPI(t)
	engine.prompts = []arinfer.SAMPrompt{
		{ID: "anchor1", Type: "point", Label: "News anchor"},
		{ID: "desk1", Type: "box", Label: "News desk"},
	}

	req := httptest.NewRequest("GET", "/api/ar/occlusion/prompts", nil)
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)

	var prompts []arinfer.SAMPrompt
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&prompts))
	require.Len(t, prompts, 2)
	require.Equal(t, "anchor1", prompts[0].ID)
	require.Equal(t, "desk1", prompts[1].ID)
}

func TestAR_PromptDelete(t *testing.T) {
	api, engine := setupARAPI(t)
	engine.prompts = []arinfer.SAMPrompt{
		{ID: "anchor1", Type: "point"},
		{ID: "desk1", Type: "box"},
	}

	req := httptest.NewRequest("DELETE", "/api/ar/occlusion/prompts/anchor1", nil)
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)
	require.Equal(t, http.StatusNoContent, rec.Code)

	require.Len(t, engine.prompts, 1)
	require.Equal(t, "desk1", engine.prompts[0].ID)
}

func TestAR_PromptDelete_NotFound(t *testing.T) {
	api, _ := setupARAPI(t)

	req := httptest.NewRequest("DELETE", "/api/ar/occlusion/prompts/nonexistent", nil)
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)
	// RemovePrompt returns ErrNotReady (SAM not enabled), mapped to 409
	require.Equal(t, http.StatusConflict, rec.Code)
}

func TestAR_PromptDelete_SAMNotReady(t *testing.T) {
	api, engine := setupARAPI(t)
	engine.promptErr = arinfer.ErrNotReady

	req := httptest.NewRequest("DELETE", "/api/ar/occlusion/prompts/anchor1", nil)
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)
	require.Equal(t, http.StatusConflict, rec.Code)
}

// --- Status ---

func TestAR_Status(t *testing.T) {
	api, engine := setupARAPI(t)
	engine.depthEnabled = true

	req := httptest.NewRequest("GET", "/api/ar/occlusion/status", nil)
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)

	var status arinfer.Status
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&status))
	require.True(t, status.DepthReady)
	require.False(t, status.SAMReady)
}

func TestAR_Status_AllDisabled(t *testing.T) {
	api, _ := setupARAPI(t)

	req := httptest.NewRequest("GET", "/api/ar/occlusion/status", nil)
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)

	var status arinfer.Status
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&status))
	require.False(t, status.DepthReady)
	require.False(t, status.SAMReady)
}

// --- Nil engine (routes not registered) ---

func TestAR_NilEngine_RoutesNotRegistered(t *testing.T) {
	programRelay := distribution.NewRelay()
	sw := switcher.NewTestSwitcher(programRelay)
	api := NewAPI(sw) // no WithARInference

	endpoints := []struct {
		method string
		path   string
	}{
		{"POST", "/api/ar/occlusion/enable"},
		{"POST", "/api/ar/occlusion/disable"},
		{"POST", "/api/ar/occlusion/prompt"},
		{"GET", "/api/ar/occlusion/prompts"},
		{"DELETE", "/api/ar/occlusion/prompts/test"},
		{"GET", "/api/ar/occlusion/status"},
	}

	for _, ep := range endpoints {
		req := httptest.NewRequest(ep.method, ep.path, nil)
		rec := httptest.NewRecorder()
		api.Mux().ServeHTTP(rec, req)
		// Routes not registered without engine — mux returns 404 or 405.
		require.NotEqual(t, http.StatusOK, rec.Code, "%s %s should not be routed", ep.method, ep.path)
		require.NotEqual(t, http.StatusCreated, rec.Code, "%s %s should not be routed", ep.method, ep.path)
	}
}

// --- Error mapping ---

func TestAR_ErrorMappings(t *testing.T) {
	require.Equal(t, http.StatusConflict, errorStatus(arinfer.ErrNotReady))
	require.Equal(t, http.StatusNotFound, errorStatus(arinfer.ErrModelMissing))
	require.Equal(t, http.StatusBadRequest, errorStatus(arinfer.ErrPromptFailed))
	require.Equal(t, http.StatusNotImplemented, errorStatus(arinfer.ErrDisabled))
}

// =============================================================================
// Camera Tracking Tests
// =============================================================================

// mockARTrackingManager is a test double for ARTrackingManager.
type mockARTrackingManager struct {
	sources      map[string][]tracking.TrackingSourceInfo
	poses        map[string]*tracking.CameraPose
	calibrations map[string]*tracking.CameraIntrinsics
	addErr       error // injected error for AddFreeDSource
	removeErr    error // injected error for RemoveSource
	calSetErr    error // injected error for SetCalibration
}

func newMockARTrackingManager() *mockARTrackingManager {
	return &mockARTrackingManager{
		sources:      make(map[string][]tracking.TrackingSourceInfo),
		poses:        make(map[string]*tracking.CameraPose),
		calibrations: make(map[string]*tracking.CameraIntrinsics),
	}
}

func (m *mockARTrackingManager) AddFreeDSource(sourceKey, udpAddr string, cameraID byte) error {
	if m.addErr != nil {
		return m.addErr
	}
	m.sources[sourceKey] = append(m.sources[sourceKey], tracking.TrackingSourceInfo{
		Name:    "freed",
		Type:    "freed",
		Running: true,
	})
	return nil
}

func (m *mockARTrackingManager) RemoveSource(sourceKey, trackerName string) error {
	if m.removeErr != nil {
		return m.removeErr
	}
	infos, ok := m.sources[sourceKey]
	if !ok {
		return tracking.ErrSourceNotFound
	}
	for i, info := range infos {
		if info.Name == trackerName {
			m.sources[sourceKey] = append(infos[:i], infos[i+1:]...)
			if len(m.sources[sourceKey]) == 0 {
				delete(m.sources, sourceKey)
			}
			return nil
		}
	}
	return tracking.ErrSourceNotFound
}

func (m *mockARTrackingManager) LatestPose(sourceKey string) *tracking.CameraPose {
	return m.poses[sourceKey]
}

func (m *mockARTrackingManager) Sources() map[string][]tracking.TrackingSourceInfo {
	return m.sources
}

func (m *mockARTrackingManager) SetCalibration(sourceKey string, cal tracking.CameraIntrinsics) error {
	if m.calSetErr != nil {
		return m.calSetErr
	}
	c := cal
	m.calibrations[sourceKey] = &c
	return nil
}

func (m *mockARTrackingManager) GetCalibration(sourceKey string) *tracking.CameraIntrinsics {
	return m.calibrations[sourceKey]
}

func (m *mockARTrackingManager) SetArucoPositions(positions map[int][3]float64) error {
	return nil
}

func (m *mockARTrackingManager) StudioMapStatus() (ready bool, dir string) {
	return false, ""
}

func (m *mockARTrackingManager) Close() error { return nil }

// setupTrackingAPI builds a test API with a tracking manager attached.
func setupTrackingAPI(t *testing.T) (*API, *mockARTrackingManager) {
	t.Helper()
	programRelay := distribution.NewRelay()
	sw := switcher.NewTestSwitcher(programRelay)
	mgr := newMockARTrackingManager()
	api := NewAPI(sw, WithARTracking(mgr))
	return api, mgr
}

// --- List sources ---

func TestARTracking_Sources_Empty(t *testing.T) {
	api, _ := setupTrackingAPI(t)

	req := httptest.NewRequest("GET", "/api/ar/tracking/sources", nil)
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)

	var sources map[string][]tracking.TrackingSourceInfo
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&sources))
	require.Empty(t, sources)
}

func TestARTracking_Sources_WithSources(t *testing.T) {
	api, mgr := setupTrackingAPI(t)
	mgr.sources["cam1"] = []tracking.TrackingSourceInfo{
		{Name: "freed", Type: "freed", Running: true, Confidence: 0.95},
	}

	req := httptest.NewRequest("GET", "/api/ar/tracking/sources", nil)
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)

	var sources map[string][]tracking.TrackingSourceInfo
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&sources))
	require.Len(t, sources, 1)
	require.Len(t, sources["cam1"], 1)
	require.Equal(t, "freed", sources["cam1"][0].Name)
	require.InDelta(t, 0.95, float64(sources["cam1"][0].Confidence), 0.01)
}

// --- Add Free-D source ---

func TestARTracking_AddFreeD(t *testing.T) {
	api, mgr := setupTrackingAPI(t)

	body := `{"addr":"192.168.1.100:40000","cameraID":1}`
	req := httptest.NewRequest("POST", "/api/ar/tracking/sources/cam1/freed", strings.NewReader(body))
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)
	require.Equal(t, http.StatusCreated, rec.Code)

	require.Len(t, mgr.sources["cam1"], 1)
	require.Equal(t, "freed", mgr.sources["cam1"][0].Name)
	require.True(t, mgr.sources["cam1"][0].Running)
}

func TestARTracking_AddFreeD_InvalidJSON(t *testing.T) {
	api, _ := setupTrackingAPI(t)

	req := httptest.NewRequest("POST", "/api/ar/tracking/sources/cam1/freed", strings.NewReader("not json"))
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)
	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestARTracking_AddFreeD_MissingAddr(t *testing.T) {
	api, _ := setupTrackingAPI(t)

	body := `{"cameraID":1}`
	req := httptest.NewRequest("POST", "/api/ar/tracking/sources/cam1/freed", strings.NewReader(body))
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)
	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestARTracking_AddFreeD_Duplicate(t *testing.T) {
	api, mgr := setupTrackingAPI(t)
	mgr.addErr = tracking.ErrSourceExists

	body := `{"addr":"192.168.1.100:40000","cameraID":1}`
	req := httptest.NewRequest("POST", "/api/ar/tracking/sources/cam1/freed", strings.NewReader(body))
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)
	require.Equal(t, http.StatusConflict, rec.Code)
}

// --- Remove source ---

func TestARTracking_Remove(t *testing.T) {
	api, mgr := setupTrackingAPI(t)
	mgr.sources["cam1"] = []tracking.TrackingSourceInfo{
		{Name: "freed", Type: "freed", Running: true},
	}

	req := httptest.NewRequest("DELETE", "/api/ar/tracking/sources/cam1/freed", nil)
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)
	require.Equal(t, http.StatusNoContent, rec.Code)

	require.Empty(t, mgr.sources)
}

func TestARTracking_Remove_NotFound(t *testing.T) {
	api, _ := setupTrackingAPI(t)

	req := httptest.NewRequest("DELETE", "/api/ar/tracking/sources/cam1/freed", nil)
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)
	require.Equal(t, http.StatusNotFound, rec.Code)
}

// --- Get pose ---

func TestARTracking_Pose(t *testing.T) {
	api, mgr := setupTrackingAPI(t)
	mgr.poses["cam1"] = &tracking.CameraPose{
		Position:    [3]float64{1.0, 2.0, 3.0},
		Orientation: [3]float64{10.0, 20.0, 30.0},
		FocalLength: 50.0,
		Confidence:  0.99,
		Source:      "freed",
	}

	req := httptest.NewRequest("GET", "/api/ar/tracking/sources/cam1/pose", nil)
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)

	var pose tracking.CameraPose
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&pose))
	require.InDelta(t, 1.0, pose.Position[0], 0.001)
	require.InDelta(t, 2.0, pose.Position[1], 0.001)
	require.InDelta(t, 3.0, pose.Position[2], 0.001)
	require.InDelta(t, 50.0, pose.FocalLength, 0.001)
	require.InDelta(t, 0.99, float64(pose.Confidence), 0.01)
}

func TestARTracking_Pose_NotFound(t *testing.T) {
	api, _ := setupTrackingAPI(t)

	req := httptest.NewRequest("GET", "/api/ar/tracking/sources/cam1/pose", nil)
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)
	require.Equal(t, http.StatusNotFound, rec.Code)
}

// --- Calibration ---

func TestARTracking_SetCalibration(t *testing.T) {
	api, mgr := setupTrackingAPI(t)
	// Source must exist for SetCalibration.
	mgr.sources["cam1"] = []tracking.TrackingSourceInfo{
		{Name: "freed", Type: "freed", Running: true},
	}

	body := `{"principalPoint":[0.5,0.5],"distortion":[0.1,-0.2,0,0,0.05],"sensorWidth":23.1}`
	req := httptest.NewRequest("PUT", "/api/ar/tracking/calibration/cam1", strings.NewReader(body))
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)

	require.NotNil(t, mgr.calibrations["cam1"])
	require.InDelta(t, 23.1, mgr.calibrations["cam1"].SensorWidth, 0.001)
}

func TestARTracking_SetCalibration_InvalidJSON(t *testing.T) {
	api, _ := setupTrackingAPI(t)

	req := httptest.NewRequest("PUT", "/api/ar/tracking/calibration/cam1", strings.NewReader("bad"))
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)
	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestARTracking_SetCalibration_SourceNotFound(t *testing.T) {
	api, mgr := setupTrackingAPI(t)
	mgr.calSetErr = tracking.ErrSourceNotFound

	body := `{"principalPoint":[0.5,0.5]}`
	req := httptest.NewRequest("PUT", "/api/ar/tracking/calibration/cam1", strings.NewReader(body))
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)
	require.Equal(t, http.StatusNotFound, rec.Code)
}

func TestARTracking_GetCalibration(t *testing.T) {
	api, mgr := setupTrackingAPI(t)
	mgr.calibrations["cam1"] = &tracking.CameraIntrinsics{
		PrincipalPoint: [2]float64{0.5, 0.5},
		SensorWidth:    23.1,
	}

	req := httptest.NewRequest("GET", "/api/ar/tracking/calibration/cam1", nil)
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)

	var cal tracking.CameraIntrinsics
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&cal))
	require.InDelta(t, 23.1, cal.SensorWidth, 0.001)
}

func TestARTracking_GetCalibration_NotFound(t *testing.T) {
	api, _ := setupTrackingAPI(t)

	req := httptest.NewRequest("GET", "/api/ar/tracking/calibration/cam1", nil)
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, req)
	require.Equal(t, http.StatusNotFound, rec.Code)
}

// --- Nil tracking manager (routes not registered) ---

func TestARTracking_NilManager_RoutesNotRegistered(t *testing.T) {
	programRelay := distribution.NewRelay()
	sw := switcher.NewTestSwitcher(programRelay)
	api := NewAPI(sw) // no WithARTracking

	endpoints := []struct {
		method string
		path   string
	}{
		{"GET", "/api/ar/tracking/sources"},
		{"POST", "/api/ar/tracking/sources/cam1/freed"},
		{"DELETE", "/api/ar/tracking/sources/cam1/freed"},
		{"GET", "/api/ar/tracking/sources/cam1/pose"},
		{"PUT", "/api/ar/tracking/calibration/cam1"},
		{"GET", "/api/ar/tracking/calibration/cam1"},
	}

	for _, ep := range endpoints {
		req := httptest.NewRequest(ep.method, ep.path, nil)
		rec := httptest.NewRecorder()
		api.Mux().ServeHTTP(rec, req)
		require.NotEqual(t, http.StatusOK, rec.Code, "%s %s should not be routed", ep.method, ep.path)
		require.NotEqual(t, http.StatusCreated, rec.Code, "%s %s should not be routed", ep.method, ep.path)
	}
}

// --- Tracking error mappings ---

func TestARTracking_ErrorMappings(t *testing.T) {
	require.Equal(t, http.StatusNotFound, errorStatus(tracking.ErrNoTracking))
	require.Equal(t, http.StatusBadRequest, errorStatus(tracking.ErrInvalidConfig))
	require.Equal(t, http.StatusConflict, errorStatus(tracking.ErrSourceExists))
	require.Equal(t, http.StatusNotFound, errorStatus(tracking.ErrSourceNotFound))
	require.Equal(t, http.StatusConflict, errorStatus(tracking.ErrMapNotReady))
}
