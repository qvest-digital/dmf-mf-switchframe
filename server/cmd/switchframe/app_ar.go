package main

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unsafe"

	"github.com/zsiec/switchframe/server/arrender"
	"github.com/zsiec/switchframe/server/control"
	"github.com/zsiec/switchframe/server/gpu"
	"github.com/zsiec/switchframe/server/tracking"
)

// initAR initializes the AR inference engine. The engine starts dormant —
// depth estimation and SAM segmentation are enabled on demand via the REST API.
// Always succeeds; the engine is lightweight until models are loaded.
func (a *App) initAR() {
	// The AR inference engine is now exclusively in the AI sidecar process.
	// If initAISidecar already set arInferEngine to an InferenceProxy, keep it.
	// Otherwise (no sidecar configured), arInferEngine stays nil and AR
	// inference is unavailable. All callers nil-check arInferEngine.
	if a.arInferEngine != nil {
		slog.Info("AR inference engine already configured via sidecar")
		return
	}
	slog.Info("AR inference engine not available (no AI sidecar configured)")
}

// initTracking initializes the camera tracking manager. The manager starts
// empty — tracking sources are added at runtime via the REST API.
// Starts a ~30Hz polling goroutine that reads pose data from all trackers.
func (a *App) initTracking() {
	a.trackingMgr = tracking.NewManager()
	a.trackingMgr.StartPolling(33 * time.Millisecond) // ~30Hz
	slog.Info("camera tracking manager initialized (polling at ~30Hz)")
}

// initARRenderer initializes the 3D scene renderer. The renderer may fail
// to create (no OptiX) — that's fine, it stays nil. The scene and asset
// store always work independently of the renderer.
func (a *App) initARRenderer() {
	settings := arrender.DefaultRenderSettings()
	var gpuCtx *gpu.Context
	if a.gpuState != nil {
		gpuCtx = a.gpuState.ctx
	}
	renderer, err := arrender.NewRenderer(gpuCtx, settings)
	if err != nil {
		slog.Info("AR renderer not available (scene management still active)", "err", err)
		return
	}
	a.arRenderer = renderer

	// Share the app's scene graph with the renderer so the REST API
	// and the render pipeline operate on the same scene instance.
	if a.arScene != nil {
		a.arRenderer.SetScene(a.arScene)
	}
	slog.Info("AR renderer initialized")

	// Load PTX shaders and initialize the OptiX pipeline.
	ptx, err := loadPTXShaders()
	if err != nil {
		slog.Info("AR renderer: PTX shaders not found, 3D rendering disabled", "err", err)
		return
	}
	if err := a.arRenderer.InitPipeline(ptx); err != nil {
		slog.Warn("AR renderer: pipeline init failed", "err", err)
		return
	}
	slog.Info("AR renderer: OptiX pipeline initialized", "ptxBytes", len(ptx))
}

// loadPTXShaders searches for the compiled OptiX PTX shader file in
// well-known locations. Returns the PTX source bytes or an error if
// not found.
func loadPTXShaders() ([]byte, error) {
	paths := []string{
		"server/arrender/shaders/ar_shaders.ptx",
		"/opt/switchframe/server/arrender/shaders/ar_shaders.ptx",
		"/data/switchframe/ar_shaders.ptx",
	}
	for _, p := range paths {
		data, err := os.ReadFile(p)
		if err == nil && len(data) > 0 {
			return data, nil
		}
	}
	return nil, fmt.Errorf("ar_shaders.ptx not found in any search path")
}

// initARAssetStore initializes the 3D asset store. Returns an error if
// the store directory cannot be created. The store is always available
// regardless of renderer status.
func (a *App) initARAssetStore() error {
	store, err := arrender.NewAssetStore(
		filepath.Join(a.statePath("ar_assets")),
		64, // max assets
	)
	if err != nil {
		return fmt.Errorf("ar asset store: %w", err)
	}
	a.arAssetStore = store

	if err := store.LoadBundledAssets(); err != nil {
		slog.Warn("failed to load bundled AR assets", "err", err)
	} else {
		slog.Info("AR asset store initialized", "dir", a.statePath("ar_assets"), "assets", len(store.List()))
	}
	return nil
}

// trackingManagerAdapter bridges tracking.Manager to control.ARTrackingManager.
type trackingManagerAdapter struct {
	mgr *tracking.Manager
	app *App
}

func (t *trackingManagerAdapter) AddFreeDSource(sourceKey, udpAddr string, cameraID byte) error {
	recv := tracking.NewFreeDReceiver(udpAddr, cameraID)
	if err := t.mgr.AddSource(sourceKey, recv); err != nil {
		return err
	}
	if err := recv.Start(); err != nil {
		t.mgr.RemoveSource(sourceKey, "freed")
		return err
	}
	return nil
}

func (t *trackingManagerAdapter) AddVisualSource(sourceKey string) error {
	if t.app.aiSidecarHost == nil || t.app.aiSidecarPool == nil {
		return fmt.Errorf("AI sidecar not configured (use --ai-sidecar flag)")
	}

	var framePool *gpu.FramePool
	if t.app.gpuState != nil {
		framePool = t.app.gpuState.pool
	}

	proxy := tracking.NewVisualTrackingProxy(
		t.app.aiSidecarHost,
		t.app.aiSidecarPool,
		sourceKey,
		framePool,
	)

	// Register with tracking manager.
	if err := t.mgr.AddSource(sourceKey, proxy); err != nil {
		proxy.Close()
		return err
	}

	// Store for OnMessage dispatch.
	t.app.aiTrackingProxiesMu.Lock()
	t.app.aiTrackingProxies[sourceKey] = proxy
	t.app.aiTrackingProxiesMu.Unlock()

	// Start tracking.
	if err := proxy.Start(); err != nil {
		t.mgr.RemoveSource(sourceKey, proxy.Name())
		t.app.aiTrackingProxiesMu.Lock()
		delete(t.app.aiTrackingProxies, sourceKey)
		t.app.aiTrackingProxiesMu.Unlock()
		return fmt.Errorf("start visual tracker: %w", err)
	}

	return nil
}

func (t *trackingManagerAdapter) RemoveSource(sourceKey, trackerName string) error {
	// Clean up tracking proxy from OnMessage dispatch map if it's a visual tracker.
	t.app.aiTrackingProxiesMu.Lock()
	if _, ok := t.app.aiTrackingProxies[sourceKey]; ok && (trackerName == "" || strings.HasPrefix(trackerName, "visual-sidecar-")) {
		delete(t.app.aiTrackingProxies, sourceKey)
	}
	t.app.aiTrackingProxiesMu.Unlock()

	return t.mgr.RemoveSource(sourceKey, trackerName)
}

func (t *trackingManagerAdapter) LatestPose(sourceKey string) *tracking.CameraPose {
	return t.mgr.LatestPose(sourceKey)
}

func (t *trackingManagerAdapter) Sources() map[string][]tracking.TrackingSourceInfo {
	return t.mgr.Sources()
}

func (t *trackingManagerAdapter) SetCalibration(sourceKey string, cal tracking.CameraIntrinsics) error {
	return t.mgr.SetCalibration(sourceKey, cal)
}

func (t *trackingManagerAdapter) GetCalibration(sourceKey string) *tracking.CameraIntrinsics {
	return t.mgr.GetCalibration(sourceKey)
}

func (t *trackingManagerAdapter) SetArucoPositions(positions map[int][3]float64) error {
	// Find all ArUco detectors across all source entries and set positions.
	t.mgr.SetArucoPositions(positions)
	return nil
}

func (t *trackingManagerAdapter) StudioMapStatus() (ready bool, dir string) {
	return false, ""
}

func (t *trackingManagerAdapter) Close() error {
	return t.mgr.Close()
}

// arCompositorStateAdapter bridges arinfer.Engine and arrender.Renderer to
// gpu.ARCompositorState. It holds a pointer to the App struct and lazily
// resolves the engine/renderer fields at call time. This allows the GPU
// pipeline to be wired before initAR/initARRenderer run — the adapter is
// always non-nil, but its underlying fields populate later.
type arCompositorStateAdapter struct {
	app *App
}

// Ensure interface compliance at compile time.
var _ gpu.ARCompositorState = (*arCompositorStateAdapter)(nil)

func (a *arCompositorStateAdapter) HasActiveAR() bool {
	if a.app.arInferEngine != nil && a.app.arInferEngine.IsActive() {
		return true
	}
	if a.app.arRenderer != nil && a.app.arRenderer.RenderedFrame() != 0 {
		return true
	}
	return false
}

func (a *arCompositorStateAdapter) OcclusionMap() *gpu.ARCompositorOcclusion {
	if a.app.arInferEngine == nil {
		return nil
	}
	occ := a.app.arInferEngine.LastOcclusion()
	if occ == nil {
		return nil
	}
	return &gpu.ARCompositorOcclusion{
		MaskPtr:  occ.MaskPtr,
		Width:    occ.Width,
		Height:   occ.Height,
		DepthPtr: occ.DepthPtr,
		DepthW:   occ.DepthW,
		DepthH:   occ.DepthH,
	}
}

func (a *arCompositorStateAdapter) ProcessFrame(framePtr uintptr, w, h, pitch int) {
	if a.app.arInferEngine == nil {
		return
	}
	a.app.arInferEngine.ProcessFrame(framePtr, w, h, pitch)
}

func (a *arCompositorStateAdapter) RenderedFrame() *gpu.ARRenderedFrame {
	if a.app.arRenderer == nil {
		return nil
	}
	ptr := a.app.arRenderer.RenderedFrame()
	if ptr == 0 {
		return nil
	}
	return &gpu.ARRenderedFrame{
		DevPtr: ptr,
		Width:  a.app.arRenderer.Settings().Width,
		Height: a.app.arRenderer.Settings().Height,
	}
}

// TriggerRender is called every frame by the GPU pipeline. If the scene
// has changed (object placed/moved/removed), it triggers an OptiX render
// with a default camera pose (or real tracking data when available).
// When the scene is clean, this is a no-op (~1ns atomic load).
func (a *arCompositorStateAdapter) TriggerRender() {
	if a.app.arRenderer == nil || !a.app.arRenderer.PipelineReady() {
		return
	}
	// Use the app's shared scene (same instance the REST API writes to),
	// not the renderer's internal scene which is a separate instance.
	scene := a.app.arScene
	if scene == nil || !scene.IsDirty() {
		return
	}

	objects := scene.Objects()
	slog.Info("AR TriggerRender: scene dirty", "objects", len(objects))

	// Ensure mesh data is loaded for all scene objects.
	a.app.ensureAllMeshesLoaded()

	// Log mesh status for each object.
	for _, obj := range objects {
		_, hasMesh := a.app.arRenderer.MeshesForAsset(obj.AssetID)
		slog.Info("AR object mesh status", "id", obj.ID, "assetId", obj.AssetID, "hasMesh", hasMesh, "visible", obj.Visible, "pos", obj.Position)
	}

	// Use default camera if no tracking is available.
	pose := arrender.DefaultCameraPose()

	if err := a.app.arRenderer.Render(pose); err != nil {
		slog.Info("AR render failed", "err", err)
		return
	}
	// Diagnostic: sample a few pixels from the uint8 render output to verify non-zero
	framePtr := a.app.arRenderer.RenderedFrame()
	w := a.app.arRenderer.Settings().Width
	h := a.app.arRenderer.Settings().Height
	sample := make([]byte, 64) // first 16 pixels
	if framePtr != 0 {
		gpu.DownloadMaskU8(sample, unsafe.Pointer(framePtr), 64)
	}
	nonZero := 0
	for _, b := range sample {
		if b != 0 {
			nonZero++
		}
	}
	slog.Info("AR render complete", "renderedFrame", framePtr, "width", w, "height", h, "sampleNonZero", nonZero, "firstBytes", fmt.Sprintf("%v", sample[:16]))
}

// ensureAllMeshesLoaded checks all scene objects and loads any missing
// mesh data from the asset store into the renderer. This bridges the
// gap between the REST API (which adds SceneObjects to the scene graph)
// and the renderer (which needs parsed GLTFMesh data for GAS building).
func (a *App) ensureAllMeshesLoaded() {
	if a.arRenderer == nil || a.arAssetStore == nil {
		return
	}
	scene := a.arRenderer.Scene()
	if scene == nil {
		return
	}
	for _, obj := range scene.Objects() {
		if obj.AssetID == "" {
			continue
		}
		a.ensureAssetMeshesLoaded(obj.AssetID)
	}
}

// ensureAssetMeshesLoaded loads glTF mesh data for an asset into the
// renderer if it hasn't been loaded yet. This is called before rendering
// to ensure the renderer has geometry data for all scene objects.
func (a *App) ensureAssetMeshesLoaded(assetID string) {
	if a.arRenderer == nil || a.arAssetStore == nil {
		slog.Info("AR ensureAssetMeshesLoaded: nil renderer or store", "renderer", a.arRenderer != nil, "store", a.arAssetStore != nil)
		return
	}
	// Already loaded — skip.
	if _, ok := a.arRenderer.MeshesForAsset(assetID); ok {
		return
	}
	slog.Info("AR loading mesh for asset", "assetID", assetID)
	// Get file path from asset store.
	path, err := a.arAssetStore.FilePath(assetID)
	if err != nil {
		slog.Info("AR asset not found for mesh loading", "assetID", assetID, "err", err)
		return
	}
	// Parse the glTF/GLB file.
	meshes, err := arrender.ParseGLTF(path)
	if err != nil {
		slog.Info("AR asset mesh parse failed", "assetID", assetID, "path", path, "err", err)
		return
	}
	if len(meshes) == 0 {
		return
	}
	a.arRenderer.LoadAssetMeshes(assetID, meshes)
	slog.Debug("AR asset meshes loaded", "assetID", assetID, "meshCount", len(meshes))
}

// arRendererAdapter bridges arrender.Renderer to control.ARRenderer.
// It wraps the renderer (which may be nil on non-OptiX builds) and provides
// a Scene() that always returns a valid scene for REST API use.
type arRendererAdapter struct {
	renderer *arrender.Renderer
	scene    *arrender.Scene // always non-nil, even when renderer is nil
}

// Ensure compile-time compliance with the control.ARRenderer interface.
var _ control.ARRenderer = (*arRendererAdapter)(nil)

func (a *arRendererAdapter) Scene() *arrender.Scene {
	return a.scene
}

func (a *arRendererAdapter) Settings() arrender.RenderSettings {
	if a.renderer != nil {
		return a.renderer.Settings()
	}
	return arrender.DefaultRenderSettings()
}

func (a *arRendererAdapter) SetSettings(s arrender.RenderSettings) error {
	if a.renderer != nil {
		return a.renderer.SetSettings(s)
	}
	return arrender.ErrRendererNotReady
}

func (a *arRendererAdapter) SceneState() arrender.SceneState {
	state := a.scene.State()
	if a.renderer != nil {
		state.Settings = a.renderer.Settings()
	}
	return state
}
