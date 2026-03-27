package control

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/zsiec/switchframe/server/arinfer"
	"github.com/zsiec/switchframe/server/arrender"
	"github.com/zsiec/switchframe/server/control/httperr"
	"github.com/zsiec/switchframe/server/tracking"
)

// registerARRoutes registers AR occlusion, tracking, scene, and asset API routes
// on the given mux. Each group of routes requires its corresponding dependency.
func (a *API) registerARRoutes(mux *http.ServeMux) {
	if a.arInference != nil {
		mux.HandleFunc("POST /api/ar/occlusion/enable", a.handleAROcclusionEnable)
		mux.HandleFunc("POST /api/ar/occlusion/disable", a.handleAROcclusionDisable)
		mux.HandleFunc("POST /api/ar/occlusion/prompt", a.handleARPromptAdd)
		mux.HandleFunc("GET /api/ar/occlusion/prompts", a.handleARPromptList)
		mux.HandleFunc("DELETE /api/ar/occlusion/prompts/{id}", a.handleARPromptDelete)
		mux.HandleFunc("GET /api/ar/occlusion/status", a.handleAROcclusionStatus)
	}

	if a.arTracking != nil {
		mux.HandleFunc("GET /api/ar/tracking/sources", a.handleARTrackingSources)
		mux.HandleFunc("POST /api/ar/tracking/sources/{sourceKey}/freed", a.handleARTrackingAddFreeD)
		mux.HandleFunc("PUT /api/ar/tracking/sources/{sourceKey}/visual", a.handleARTrackingAddVisual)
		mux.HandleFunc("DELETE /api/ar/tracking/sources/{sourceKey}/{trackerName}", a.handleARTrackingRemove)
		mux.HandleFunc("GET /api/ar/tracking/sources/{sourceKey}/pose", a.handleARTrackingPose)
		mux.HandleFunc("PUT /api/ar/tracking/calibration/{sourceKey}", a.handleARTrackingSetCalibration)
		mux.HandleFunc("GET /api/ar/tracking/calibration/{sourceKey}", a.handleARTrackingGetCalibration)
		mux.HandleFunc("PUT /api/ar/tracking/aruco", a.handleARTrackingSetAruco)
		mux.HandleFunc("POST /api/ar/tracking/studio-map", a.handleARTrackingStudioMapUpload)
		mux.HandleFunc("GET /api/ar/tracking/studio-map", a.handleARTrackingStudioMapStatus)
	}

	// Scene routes require an ARRenderer (scene is always available, renderer may be nil).
	if a.arRenderer != nil {
		mux.HandleFunc("GET /api/ar/scene", a.handleARSceneGet)
		mux.HandleFunc("POST /api/ar/scene/objects", a.handleARSceneAddObject)
		mux.HandleFunc("PUT /api/ar/scene/objects/{id}", a.handleARSceneUpdateObject)
		mux.HandleFunc("DELETE /api/ar/scene/objects/{id}", a.handleARSceneDeleteObject)
		mux.HandleFunc("POST /api/ar/scene/lights", a.handleARSceneAddLight)
		mux.HandleFunc("PUT /api/ar/scene/lights/{id}", a.handleARSceneUpdateLight)
		mux.HandleFunc("DELETE /api/ar/scene/lights/{id}", a.handleARSceneDeleteLight)
		mux.HandleFunc("GET /api/ar/render/settings", a.handleARRenderSettingsGet)
		mux.HandleFunc("PUT /api/ar/render/settings", a.handleARRenderSettingsSet)
		mux.HandleFunc("POST /api/ar/scene/load", a.handleARSceneLoad)
		mux.HandleFunc("PUT /api/ar/scene/environment", a.handleARSceneEnvironment)
	}

	// Asset routes require an ARAssetStore.
	if a.arAssetStore != nil {
		mux.HandleFunc("POST /api/ar/assets", a.handleARAssetUpload)
		mux.HandleFunc("GET /api/ar/assets", a.handleARAssetList)
		mux.HandleFunc("GET /api/ar/assets/{id}", a.handleARAssetGet)
		mux.HandleFunc("DELETE /api/ar/assets/{id}", a.handleARAssetDelete)
	}
}

// arOcclusionEnableRequest is the JSON body for POST /api/ar/occlusion/enable.
type arOcclusionEnableRequest struct {
	Mode string `json:"mode"` // "depth", "sam", or "both"
}

// handleAROcclusionEnable enables depth estimation and/or SAM segmentation.
//
// POST /api/ar/occlusion/enable
func (a *API) handleAROcclusionEnable(w http.ResponseWriter, r *http.Request) {
	a.setLastOperator(r)

	var req arOcclusionEnableRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httperr.Write(w, http.StatusBadRequest, "invalid json")
		return
	}

	if req.Mode != "depth" && req.Mode != "sam" && req.Mode != "both" {
		httperr.Write(w, http.StatusBadRequest, "mode must be \"depth\", \"sam\", or \"both\"")
		return
	}

	if req.Mode == "depth" || req.Mode == "both" {
		if err := a.arInference.EnableDepth(""); err != nil {
			httperr.WriteErr(w, errorStatus(err), err)
			return
		}
	}

	if req.Mode == "sam" || req.Mode == "both" {
		if err := a.arInference.EnableSAM("", ""); err != nil {
			httperr.WriteErr(w, errorStatus(err), err)
			return
		}
	}

	a.broadcast()

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(a.arInference.Status())
}

// handleAROcclusionDisable disables depth estimation and SAM segmentation.
//
// POST /api/ar/occlusion/disable
func (a *API) handleAROcclusionDisable(w http.ResponseWriter, r *http.Request) {
	a.setLastOperator(r)

	a.arInference.DisableDepth()
	a.arInference.DisableSAM()

	a.broadcast()

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(a.arInference.Status())
}

// handleARPromptAdd registers a new SAM segmentation prompt.
//
// POST /api/ar/occlusion/prompt
func (a *API) handleARPromptAdd(w http.ResponseWriter, r *http.Request) {
	a.setLastOperator(r)

	var prompt arinfer.SAMPrompt
	if err := json.NewDecoder(r.Body).Decode(&prompt); err != nil {
		httperr.Write(w, http.StatusBadRequest, "invalid json")
		return
	}

	if prompt.ID == "" {
		httperr.Write(w, http.StatusBadRequest, "id is required")
		return
	}
	if prompt.Type != "point" && prompt.Type != "box" {
		httperr.Write(w, http.StatusBadRequest, "type must be \"point\" or \"box\"")
		return
	}

	if err := a.arInference.AddPrompt(prompt); err != nil {
		httperr.WriteErr(w, errorStatus(err), err)
		return
	}

	a.broadcast()

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(prompt)
}

// handleARPromptList returns all active SAM prompts.
//
// GET /api/ar/occlusion/prompts
func (a *API) handleARPromptList(w http.ResponseWriter, r *http.Request) {
	prompts := a.arInference.Prompts()
	if prompts == nil {
		prompts = []arinfer.SAMPrompt{}
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(prompts)
}

// handleARPromptDelete removes a SAM prompt by ID.
//
// DELETE /api/ar/occlusion/prompts/{id}
func (a *API) handleARPromptDelete(w http.ResponseWriter, r *http.Request) {
	a.setLastOperator(r)

	id := r.PathValue("id")
	if id == "" {
		httperr.Write(w, http.StatusBadRequest, "prompt id is required")
		return
	}

	if err := a.arInference.RemovePrompt(id); err != nil {
		httperr.WriteErr(w, errorStatus(err), err)
		return
	}

	a.broadcast()

	w.WriteHeader(http.StatusNoContent)
}

// handleAROcclusionStatus returns the current inference engine status.
//
// GET /api/ar/occlusion/status
func (a *API) handleAROcclusionStatus(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(a.arInference.Status())
}

// --- Camera Tracking ---

// handleARTrackingSources returns all registered tracking sources grouped by source key.
//
// GET /api/ar/tracking/sources
func (a *API) handleARTrackingSources(w http.ResponseWriter, r *http.Request) {
	sources := a.arTracking.Sources()
	if sources == nil {
		sources = make(map[string][]tracking.TrackingSourceInfo)
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(sources)
}

// arTrackingAddFreeDRequest is the JSON body for POST /api/ar/tracking/sources/{sourceKey}/freed.
type arTrackingAddFreeDRequest struct {
	Addr     string `json:"addr"`
	CameraID byte   `json:"cameraID"`
}

// handleARTrackingAddFreeD creates a Free-D tracking source for a camera.
//
// POST /api/ar/tracking/sources/{sourceKey}/freed
func (a *API) handleARTrackingAddFreeD(w http.ResponseWriter, r *http.Request) {
	a.setLastOperator(r)

	sourceKey := r.PathValue("sourceKey")
	if sourceKey == "" {
		httperr.Write(w, http.StatusBadRequest, "sourceKey is required")
		return
	}

	var req arTrackingAddFreeDRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httperr.Write(w, http.StatusBadRequest, "invalid json")
		return
	}

	if req.Addr == "" {
		httperr.Write(w, http.StatusBadRequest, "addr is required")
		return
	}

	if err := a.arTracking.AddFreeDSource(sourceKey, req.Addr, req.CameraID); err != nil {
		httperr.WriteErr(w, errorStatus(err), err)
		return
	}

	a.broadcast()

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(a.arTracking.Sources())
}

// handleARTrackingRemove removes a tracking source from a camera.
//
// DELETE /api/ar/tracking/sources/{sourceKey}/{trackerName}
func (a *API) handleARTrackingRemove(w http.ResponseWriter, r *http.Request) {
	a.setLastOperator(r)

	sourceKey := r.PathValue("sourceKey")
	trackerName := r.PathValue("trackerName")
	if sourceKey == "" || trackerName == "" {
		httperr.Write(w, http.StatusBadRequest, "sourceKey and trackerName are required")
		return
	}

	if err := a.arTracking.RemoveSource(sourceKey, trackerName); err != nil {
		httperr.WriteErr(w, errorStatus(err), err)
		return
	}

	a.broadcast()

	w.WriteHeader(http.StatusNoContent)
}

// handleARTrackingPose returns the latest filtered pose for a source.
//
// GET /api/ar/tracking/sources/{sourceKey}/pose
func (a *API) handleARTrackingPose(w http.ResponseWriter, r *http.Request) {
	sourceKey := r.PathValue("sourceKey")
	if sourceKey == "" {
		httperr.Write(w, http.StatusBadRequest, "sourceKey is required")
		return
	}

	pose := a.arTracking.LatestPose(sourceKey)
	if pose == nil {
		httperr.WriteErr(w, http.StatusNotFound, tracking.ErrNoTracking)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(pose)
}

// handleARTrackingSetCalibration sets the lens calibration intrinsics for a source.
//
// PUT /api/ar/tracking/calibration/{sourceKey}
func (a *API) handleARTrackingSetCalibration(w http.ResponseWriter, r *http.Request) {
	a.setLastOperator(r)

	sourceKey := r.PathValue("sourceKey")
	if sourceKey == "" {
		httperr.Write(w, http.StatusBadRequest, "sourceKey is required")
		return
	}

	var cal tracking.CameraIntrinsics
	if err := json.NewDecoder(r.Body).Decode(&cal); err != nil {
		httperr.Write(w, http.StatusBadRequest, "invalid json")
		return
	}

	if err := a.arTracking.SetCalibration(sourceKey, cal); err != nil {
		httperr.WriteErr(w, errorStatus(err), err)
		return
	}

	a.broadcast()

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(cal)
}

// handleARTrackingGetCalibration returns the lens calibration intrinsics for a source.
//
// GET /api/ar/tracking/calibration/{sourceKey}
func (a *API) handleARTrackingGetCalibration(w http.ResponseWriter, r *http.Request) {
	sourceKey := r.PathValue("sourceKey")
	if sourceKey == "" {
		httperr.Write(w, http.StatusBadRequest, "sourceKey is required")
		return
	}

	cal := a.arTracking.GetCalibration(sourceKey)
	if cal == nil {
		httperr.WriteErr(w, http.StatusNotFound, tracking.ErrSourceNotFound)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(cal)
}

// =============================================================================
// 3D Scene Management
// =============================================================================

// handleARSceneGet returns the full scene state (objects, lights, settings).
//
// GET /api/ar/scene
func (a *API) handleARSceneGet(w http.ResponseWriter, r *http.Request) {
	state := a.arRenderer.SceneState()
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(state)
}

// handleARSceneAddObject adds a new object to the scene.
//
// POST /api/ar/scene/objects
func (a *API) handleARSceneAddObject(w http.ResponseWriter, r *http.Request) {
	a.setLastOperator(r)

	var obj arrender.SceneObject
	if err := json.NewDecoder(r.Body).Decode(&obj); err != nil {
		httperr.Write(w, http.StatusBadRequest, "invalid json")
		return
	}

	if obj.ID == "" {
		httperr.Write(w, http.StatusBadRequest, "id is required")
		return
	}

	scene := a.arRenderer.Scene()
	if scene == nil {
		httperr.WriteErr(w, http.StatusServiceUnavailable, arrender.ErrRendererNotReady)
		return
	}

	if err := scene.AddObject(obj); err != nil {
		httperr.WriteErr(w, errorStatus(err), err)
		return
	}

	a.broadcast()

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(obj)
}

// handleARSceneUpdateObject updates an existing object in the scene.
//
// PUT /api/ar/scene/objects/{id}
func (a *API) handleARSceneUpdateObject(w http.ResponseWriter, r *http.Request) {
	a.setLastOperator(r)

	id := r.PathValue("id")
	if id == "" {
		httperr.Write(w, http.StatusBadRequest, "object id is required")
		return
	}

	var obj arrender.SceneObject
	if err := json.NewDecoder(r.Body).Decode(&obj); err != nil {
		httperr.Write(w, http.StatusBadRequest, "invalid json")
		return
	}

	scene := a.arRenderer.Scene()
	if scene == nil {
		httperr.WriteErr(w, http.StatusServiceUnavailable, arrender.ErrRendererNotReady)
		return
	}

	if err := scene.UpdateObject(id, obj); err != nil {
		httperr.WriteErr(w, errorStatus(err), err)
		return
	}

	a.broadcast()

	updated, _ := scene.GetObject(id)
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(updated)
}

// handleARSceneDeleteObject removes an object from the scene.
//
// DELETE /api/ar/scene/objects/{id}
func (a *API) handleARSceneDeleteObject(w http.ResponseWriter, r *http.Request) {
	a.setLastOperator(r)

	id := r.PathValue("id")
	if id == "" {
		httperr.Write(w, http.StatusBadRequest, "object id is required")
		return
	}

	scene := a.arRenderer.Scene()
	if scene == nil {
		httperr.WriteErr(w, http.StatusServiceUnavailable, arrender.ErrRendererNotReady)
		return
	}

	if err := scene.RemoveObject(id); err != nil {
		httperr.WriteErr(w, errorStatus(err), err)
		return
	}

	a.broadcast()

	w.WriteHeader(http.StatusNoContent)
}

// handleARSceneAddLight adds a new light to the scene.
//
// POST /api/ar/scene/lights
func (a *API) handleARSceneAddLight(w http.ResponseWriter, r *http.Request) {
	a.setLastOperator(r)

	var light arrender.Light
	if err := json.NewDecoder(r.Body).Decode(&light); err != nil {
		httperr.Write(w, http.StatusBadRequest, "invalid json")
		return
	}

	if light.ID == "" {
		httperr.Write(w, http.StatusBadRequest, "id is required")
		return
	}

	scene := a.arRenderer.Scene()
	if scene == nil {
		httperr.WriteErr(w, http.StatusServiceUnavailable, arrender.ErrRendererNotReady)
		return
	}

	if err := scene.AddLight(light); err != nil {
		httperr.WriteErr(w, errorStatus(err), err)
		return
	}

	a.broadcast()

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(light)
}

// handleARSceneUpdateLight updates an existing light in the scene.
//
// PUT /api/ar/scene/lights/{id}
func (a *API) handleARSceneUpdateLight(w http.ResponseWriter, r *http.Request) {
	a.setLastOperator(r)

	id := r.PathValue("id")
	if id == "" {
		httperr.Write(w, http.StatusBadRequest, "light id is required")
		return
	}

	var light arrender.Light
	if err := json.NewDecoder(r.Body).Decode(&light); err != nil {
		httperr.Write(w, http.StatusBadRequest, "invalid json")
		return
	}

	scene := a.arRenderer.Scene()
	if scene == nil {
		httperr.WriteErr(w, http.StatusServiceUnavailable, arrender.ErrRendererNotReady)
		return
	}

	if err := scene.UpdateLight(id, light); err != nil {
		httperr.WriteErr(w, errorStatus(err), err)
		return
	}

	a.broadcast()

	// Return the light with the canonical ID (preserved from the URL).
	light.ID = id
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(light)
}

// handleARSceneDeleteLight removes a light from the scene.
//
// DELETE /api/ar/scene/lights/{id}
func (a *API) handleARSceneDeleteLight(w http.ResponseWriter, r *http.Request) {
	a.setLastOperator(r)

	id := r.PathValue("id")
	if id == "" {
		httperr.Write(w, http.StatusBadRequest, "light id is required")
		return
	}

	scene := a.arRenderer.Scene()
	if scene == nil {
		httperr.WriteErr(w, http.StatusServiceUnavailable, arrender.ErrRendererNotReady)
		return
	}

	if err := scene.RemoveLight(id); err != nil {
		httperr.WriteErr(w, errorStatus(err), err)
		return
	}

	a.broadcast()

	w.WriteHeader(http.StatusNoContent)
}

// handleARRenderSettingsGet returns the current render settings.
//
// GET /api/ar/render/settings
func (a *API) handleARRenderSettingsGet(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(a.arRenderer.Settings())
}

// handleARRenderSettingsSet updates the render settings.
//
// PUT /api/ar/render/settings
func (a *API) handleARRenderSettingsSet(w http.ResponseWriter, r *http.Request) {
	a.setLastOperator(r)

	var settings arrender.RenderSettings
	if err := json.NewDecoder(r.Body).Decode(&settings); err != nil {
		httperr.Write(w, http.StatusBadRequest, "invalid json")
		return
	}

	if err := a.arRenderer.SetSettings(settings); err != nil {
		httperr.WriteErr(w, errorStatus(err), err)
		return
	}

	a.broadcast()

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(a.arRenderer.Settings())
}

// =============================================================================
// 3D Asset Management
// =============================================================================

// handleARAssetUpload accepts a multipart file upload of a 3D asset.
// Max upload size: 256MB.
//
// POST /api/ar/assets
func (a *API) handleARAssetUpload(w http.ResponseWriter, r *http.Request) {
	a.setLastOperator(r)

	// Limit to 256MB.
	r.Body = http.MaxBytesReader(w, r.Body, 256<<20)

	// Parse multipart form (32MB in memory, rest to disk).
	if err := r.ParseMultipartForm(32 << 20); err != nil {
		httperr.Write(w, http.StatusBadRequest, "invalid multipart form or file too large")
		return
	}

	file, header, err := r.FormFile("asset")
	if err != nil {
		httperr.Write(w, http.StatusBadRequest, "asset field required")
		return
	}
	defer func() { _ = file.Close() }()

	name := r.FormValue("name")
	if name == "" {
		name = header.Filename
	}

	data, err := io.ReadAll(file)
	if err != nil {
		httperr.Write(w, http.StatusBadRequest, "failed to read asset data")
		return
	}

	info, err := a.arAssetStore.Upload(name, header.Filename, data)
	if err != nil {
		httperr.WriteErr(w, errorStatus(err), err)
		return
	}

	a.broadcast()

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(info)
}

// handleARAssetList returns all stored assets.
//
// GET /api/ar/assets
func (a *API) handleARAssetList(w http.ResponseWriter, r *http.Request) {
	assets := a.arAssetStore.List()
	if assets == nil {
		assets = []*arrender.AssetInfo{}
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(assets)
}

// handleARAssetGet returns a single asset's metadata.
//
// GET /api/ar/assets/{id}
func (a *API) handleARAssetGet(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		httperr.Write(w, http.StatusBadRequest, "asset id is required")
		return
	}

	info, err := a.arAssetStore.Get(id)
	if err != nil {
		httperr.WriteErr(w, errorStatus(err), err)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(info)
}

// handleARAssetDelete removes an asset from the store.
//
// DELETE /api/ar/assets/{id}
func (a *API) handleARAssetDelete(w http.ResponseWriter, r *http.Request) {
	a.setLastOperator(r)

	id := r.PathValue("id")
	if id == "" {
		httperr.Write(w, http.StatusBadRequest, "asset id is required")
		return
	}

	if err := a.arAssetStore.Delete(id); err != nil {
		httperr.WriteErr(w, errorStatus(err), err)
		return
	}

	a.broadcast()

	w.WriteHeader(http.StatusNoContent)
}

// =============================================================================
// Visual Tracking (501 — requires TensorRT models)
// =============================================================================

// handleARTrackingAddVisual enables visual tracking for a source.
// This requires TensorRT models that are not yet available.
//
// PUT /api/ar/tracking/sources/{sourceKey}/visual
func (a *API) handleARTrackingAddVisual(w http.ResponseWriter, r *http.Request) {
	httperr.Write(w, http.StatusNotImplemented, "visual tracking requires TensorRT models (not yet available)")
}

// =============================================================================
// Studio Map (501 — requires COLMAP)
// =============================================================================

// handleARTrackingStudioMapUpload uploads studio map images for 3D reconstruction.
// Studio map building requires COLMAP, which is not yet integrated.
//
// POST /api/ar/tracking/studio-map
func (a *API) handleARTrackingStudioMapUpload(w http.ResponseWriter, r *http.Request) {
	httperr.Write(w, http.StatusNotImplemented, "studio map building requires COLMAP (not yet available)")
}

// handleARTrackingStudioMapStatus returns the studio map status.
//
// GET /api/ar/tracking/studio-map
func (a *API) handleARTrackingStudioMapStatus(w http.ResponseWriter, r *http.Request) {
	ready, dir := a.arTracking.StudioMapStatus()
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"ready": ready,
		"dir":   dir,
	})
}

// =============================================================================
// ArUco Marker Configuration
// =============================================================================

// arArucoPositionsRequest is the JSON body for PUT /api/ar/tracking/aruco.
type arArucoPositionsRequest struct {
	Markers map[string][3]float64 `json:"markers"` // markerID (string) → [x, y, z]
}

// handleARTrackingSetAruco configures the known 3D positions of ArUco markers
// in the studio coordinate system.
//
// PUT /api/ar/tracking/aruco
func (a *API) handleARTrackingSetAruco(w http.ResponseWriter, r *http.Request) {
	a.setLastOperator(r)

	var req arArucoPositionsRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httperr.Write(w, http.StatusBadRequest, "invalid json")
		return
	}

	if len(req.Markers) == 0 {
		httperr.Write(w, http.StatusBadRequest, "markers map is required")
		return
	}

	// Convert string keys to int keys.
	positions := make(map[int][3]float64, len(req.Markers))
	for key, pos := range req.Markers {
		var id int
		if _, err := fmt.Sscanf(key, "%d", &id); err != nil {
			httperr.Write(w, http.StatusBadRequest, fmt.Sprintf("invalid marker ID %q: must be an integer", key))
			return
		}
		positions[id] = pos
	}

	if err := a.arTracking.SetArucoPositions(positions); err != nil {
		httperr.WriteErr(w, errorStatus(err), err)
		return
	}

	a.broadcast()

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"markers": len(positions),
	})
}

// =============================================================================
// Scene File Loading
// =============================================================================

// handleARSceneLoad loads a USD/glTF scene file via multipart upload and
// adds its contents to the scene graph.
//
// POST /api/ar/scene/load
func (a *API) handleARSceneLoad(w http.ResponseWriter, r *http.Request) {
	a.setLastOperator(r)

	// Limit to 256MB.
	r.Body = http.MaxBytesReader(w, r.Body, 256<<20)

	if err := r.ParseMultipartForm(32 << 20); err != nil {
		httperr.Write(w, http.StatusBadRequest, "invalid multipart form or file too large")
		return
	}

	file, header, err := r.FormFile("scene")
	if err != nil {
		httperr.Write(w, http.StatusBadRequest, "scene field required")
		return
	}
	defer func() { _ = file.Close() }()

	ext := strings.ToLower(filepath.Ext(header.Filename))
	isUSD := arrender.IsUSDExtension(ext)
	isGLTF := ext == ".glb" || ext == ".gltf"
	if !isUSD && !isGLTF {
		httperr.Write(w, http.StatusBadRequest, fmt.Sprintf("unsupported file format %q (supported: .usd, .usda, .usdc, .usdz, .glb, .gltf)", ext))
		return
	}

	scene := a.arRenderer.Scene()
	if scene == nil {
		httperr.WriteErr(w, http.StatusServiceUnavailable, arrender.ErrRendererNotReady)
		return
	}

	// Write uploaded file to temp dir for parser.
	tmpDir, err := os.MkdirTemp("", "switchframe-scene-*")
	if err != nil {
		httperr.Write(w, http.StatusInternalServerError, "failed to create temp directory")
		return
	}
	defer func() { _ = os.RemoveAll(tmpDir) }()

	tmpPath := filepath.Join(tmpDir, header.Filename)
	data, err := io.ReadAll(file)
	if err != nil {
		httperr.Write(w, http.StatusBadRequest, "failed to read scene file")
		return
	}
	if err := os.WriteFile(tmpPath, data, 0o644); err != nil {
		httperr.Write(w, http.StatusInternalServerError, "failed to write temp file")
		return
	}

	var ids []string
	if isGLTF {
		ids, err = arrender.LoadGLTF(tmpPath, scene)
	} else {
		ids, err = arrender.LoadUSD(tmpPath, scene)
	}
	if err != nil {
		httperr.WriteErr(w, errorStatus(err), err)
		return
	}

	a.broadcast()

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"objects": ids,
		"count":   len(ids),
	})
}

// =============================================================================
// Scene Environment Map
// =============================================================================

// handleARSceneEnvironment sets the HDR environment map for the scene.
// Accepts a multipart upload of an HDR/EXR image file.
// Currently returns 501 as environment map support requires the OptiX
// renderer to be configured with IBL (image-based lighting) support.
//
// PUT /api/ar/scene/environment
func (a *API) handleARSceneEnvironment(w http.ResponseWriter, r *http.Request) {
	httperr.Write(w, http.StatusNotImplemented, "HDR environment map requires OptiX IBL support (not yet available)")
}
