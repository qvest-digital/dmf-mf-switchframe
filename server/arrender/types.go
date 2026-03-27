// Package arrender provides scene graph management, renderer lifecycle,
// and asset storage for AR graphics ray tracing.
package arrender

import "errors"

// Sentinel errors for the arrender subsystem.
var (
	// ErrObjectNotFound indicates a scene object with the given ID does not exist.
	ErrObjectNotFound = errors.New("arrender: object not found")

	// ErrLightNotFound indicates a light with the given ID does not exist.
	ErrLightNotFound = errors.New("arrender: light not found")

	// ErrAssetNotFound indicates an asset with the given ID does not exist.
	ErrAssetNotFound = errors.New("arrender: asset not found")

	// ErrInvalidAsset indicates an uploaded asset has an invalid name or format.
	ErrInvalidAsset = errors.New("arrender: invalid asset")

	// ErrTooManyAssets indicates the asset store has reached its capacity limit.
	ErrTooManyAssets = errors.New("arrender: too many assets")

	// ErrRendererNotReady indicates the renderer is not available
	// (missing GPU, OptiX not compiled in, etc.).
	ErrRendererNotReady = errors.New("arrender: renderer not ready")

	// ErrDuplicateID indicates an object or light with the given ID already exists.
	ErrDuplicateID = errors.New("arrender: duplicate ID")
)

// SceneObject represents a 3D object placed in the scene.
type SceneObject struct {
	ID       string     `json:"id"`
	AssetID  string     `json:"assetId"`
	Position [3]float64 `json:"position"` // meters
	Rotation [3]float64 `json:"rotation"` // degrees (Euler XYZ)
	Scale    [3]float64 `json:"scale"`
	Visible  bool       `json:"visible"`
}

// Light represents a light source in the scene.
type Light struct {
	ID        string     `json:"id"`
	Type      string     `json:"type"` // "point", "directional", "area"
	Position  [3]float64 `json:"position"`
	Direction [3]float64 `json:"direction"`
	Color     [3]float64 `json:"color"` // linear RGB, 0-1
	Intensity float64    `json:"intensity"`
}

// RenderSettings controls the ray tracing render parameters.
type RenderSettings struct {
	Width   int  `json:"width"`   // render resolution (default 1280)
	Height  int  `json:"height"`  // default 720
	SPP     int  `json:"spp"`     // samples per pixel (default 2)
	Denoise bool `json:"denoise"` // OptiX AI denoiser (default true)
}

// DefaultRenderSettings returns production-ready default render settings.
func DefaultRenderSettings() RenderSettings {
	return RenderSettings{Width: 1280, Height: 720, SPP: 2, Denoise: true}
}

// SceneState is the JSON-serializable snapshot for REST API and state broadcast.
type SceneState struct {
	Objects   []SceneObject  `json:"objects"`
	Lights    []Light        `json:"lights"`
	Settings  RenderSettings `json:"settings"`
	HasEnvMap bool           `json:"hasEnvMap"`
}
