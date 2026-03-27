//go:build !cgo || !cuda || !optix

package arrender

import (
	"github.com/zsiec/switchframe/server/gpu"
	"github.com/zsiec/switchframe/server/tracking"
)

// Renderer is a stub for builds without OptiX support.
type Renderer struct {
	scene    *Scene
	settings RenderSettings
}

// NewRenderer returns ErrRendererNotReady on non-OptiX builds.
func NewRenderer(_ *gpu.Context, _ RenderSettings) (*Renderer, error) {
	return nil, ErrRendererNotReady
}

// SetIntrinsics is a no-op on stub builds.
func (r *Renderer) SetIntrinsics(_ *tracking.CameraIntrinsics) {}

// InitPipeline returns ErrRendererNotReady on stub builds.
func (r *Renderer) InitPipeline(_ []byte) error {
	return ErrRendererNotReady
}

// Scene returns nil on stub builds.
func (r *Renderer) Scene() *Scene {
	if r == nil {
		return nil
	}
	return r.scene
}

// Settings returns default settings on stub builds.
func (r *Renderer) Settings() RenderSettings {
	if r == nil {
		return DefaultRenderSettings()
	}
	return r.settings
}

// SetSettings returns ErrRendererNotReady on stub builds.
func (r *Renderer) SetSettings(_ RenderSettings) error {
	return ErrRendererNotReady
}

// Render returns ErrRendererNotReady on stub builds.
func (r *Renderer) Render(_ *tracking.CameraPose) error {
	return ErrRendererNotReady
}

// RenderedFrame returns 0 on stub builds.
func (r *Renderer) RenderedFrame() uintptr {
	return 0
}

// SceneState returns an empty state on stub builds.
func (r *Renderer) SceneState() SceneState {
	return SceneState{}
}

// defaultCameraPose returns a static camera suitable for rendering objects
// placed via depth-based placement (no tracking required).
func defaultCameraPose() *tracking.CameraPose {
	return &tracking.CameraPose{
		Position:    [3]float64{0, 0, 0},
		Orientation: [3]float64{0, 0, 0},
		FocalLength: 50,
		Confidence:  1.0,
		Source:      "default",
	}
}

// DefaultCameraPose returns a static default camera pose. Exported for
// use by the compositor state adapter.
func DefaultCameraPose() *tracking.CameraPose {
	return defaultCameraPose()
}

// MeshesForAsset returns nil, false on stub builds.
func (r *Renderer) MeshesForAsset(_ string) ([]GLTFMesh, bool) {
	return nil, false
}

// LoadAssetMeshes is a no-op on stub builds.
func (r *Renderer) LoadAssetMeshes(_ string, _ []GLTFMesh) {}

// PipelineReady returns false on stub builds.
func (r *Renderer) PipelineReady() bool {
	return false
}

// Close is a no-op on stub builds.
func (r *Renderer) Close() error {
	return nil
}
