package main

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"github.com/zsiec/switchframe/server/arinfer"
	"github.com/zsiec/switchframe/server/arrender"
	"github.com/zsiec/switchframe/server/control"
	"github.com/zsiec/switchframe/server/gpu"
	"github.com/zsiec/switchframe/server/tracking"
)

// initAR initializes the AR inference engine. The engine starts dormant —
// depth estimation and SAM segmentation are enabled on demand via the REST API.
// Always succeeds; the engine is lightweight until models are loaded.
func (a *App) initAR() {
	// Look for models in the per-session state dir first, then fall back
	// to a shared system-wide location. This allows a single set of TRT
	// engine files to serve all sessions on the machine.
	modelsDir := a.statePath("ar_models")
	if _, err := os.Stat(filepath.Join(modelsDir, "depth_anything_v2_vits.plan")); err != nil {
		for _, fallback := range []string{
			"/data/switchframe/ar_models",
			"/opt/switchframe/ar_models",
		} {
			if _, err := os.Stat(filepath.Join(fallback, "depth_anything_v2_vits.plan")); err == nil {
				modelsDir = fallback
				break
			}
		}
	}

	cfg := arinfer.Config{
		ModelsDir:      modelsDir,
		MaxInferenceMs: 20,
	}
	a.arInferEngine = arinfer.New(cfg)
	slog.Info("AR inference engine initialized (dormant)", "modelsDir", modelsDir)
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
	slog.Info("AR renderer initialized")
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

func (t *trackingManagerAdapter) RemoveSource(sourceKey, trackerName string) error {
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
