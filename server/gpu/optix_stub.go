//go:build !cgo || !cuda || !optix

package gpu

import (
	"errors"
	"unsafe"
)

// ErrOptiXNotAvailable indicates OptiX is not available in the current build.
var ErrOptiXNotAvailable = errors.New("gpu: OptiX not available (build without optix tag)")

// OptixContext is a stub for non-OptiX builds.
type OptixContext struct{}

// OptixModule is a stub for non-OptiX builds.
type OptixModule struct{}

// OptixPipeline is a stub for non-OptiX builds.
type OptixPipeline struct{}

// OptixGAS is a stub for non-OptiX builds.
type OptixGAS struct{}

// OptixIAS is a stub for non-OptiX builds.
type OptixIAS struct{}

// OptixDenoiser is a stub for non-OptiX builds.
type OptixDenoiser struct{}

// OptixPipelineConfig specifies shader entry points for pipeline creation.
type OptixPipelineConfig struct {
	Module          *OptixModule
	RaygenEntry     string
	MissEntry       string
	ShadowMissEntry string
	ClosestHitEntry string
	AnyHitEntry     string
	MaxTraceDepth   int
}

// OptixInstance describes a GAS instance in the scene with its transform.
type OptixInstance struct {
	GAS            *OptixGAS
	Transform      [12]float32
	SBTOffset      uint32
	VisibilityMask uint32
}

// OptixLaunchParams specifies parameters for a ray tracing launch.
type OptixLaunchParams struct {
	OutputBuffer    unsafe.Pointer
	Width           int
	Height          int
	IASHandle       unsafe.Pointer
	CameraPos       [3]float32
	CameraU         [3]float32
	CameraV         [3]float32
	CameraW         [3]float32
	SamplesPerPixel int
	FrameNumber     int
	Lights          unsafe.Pointer
	NumLights       int
}

// NewOptixContext returns ErrOptiXNotAvailable on non-OptiX builds.
func NewOptixContext(cudaCtx unsafe.Pointer) (*OptixContext, error) {
	return nil, ErrOptiXNotAvailable
}

// Close is a no-op on non-OptiX builds.
func (c *OptixContext) Close() {}

// CreateModule returns ErrOptiXNotAvailable on non-OptiX builds.
func (c *OptixContext) CreateModule(ptxSource []byte) (*OptixModule, error) {
	return nil, ErrOptiXNotAvailable
}

// CreatePipeline returns ErrOptiXNotAvailable on non-OptiX builds.
func (c *OptixContext) CreatePipeline(config OptixPipelineConfig) (*OptixPipeline, error) {
	return nil, ErrOptiXNotAvailable
}

// BuildTriangleGAS returns ErrOptiXNotAvailable on non-OptiX builds.
func (c *OptixContext) BuildTriangleGAS(vertices []float32, indices []uint32, stream unsafe.Pointer) (*OptixGAS, error) {
	return nil, ErrOptiXNotAvailable
}

// BuildIAS returns ErrOptiXNotAvailable on non-OptiX builds.
func (c *OptixContext) BuildIAS(instances []OptixInstance, stream unsafe.Pointer) (*OptixIAS, error) {
	return nil, ErrOptiXNotAvailable
}

// Close is a no-op on non-OptiX builds.
func (m *OptixModule) Close() {}

// Close is a no-op on non-OptiX builds.
func (p *OptixPipeline) Close() {}

// Launch returns ErrOptiXNotAvailable on non-OptiX builds.
func (p *OptixPipeline) Launch(params OptixLaunchParams, stream unsafe.Pointer) error {
	return ErrOptiXNotAvailable
}

// Close is a no-op on non-OptiX builds.
func (g *OptixGAS) Close() {}

// DeviceVertices returns (0, 0) on non-OptiX builds.
func (g *OptixGAS) DeviceVertices() (uintptr, int) { return 0, 0 }

// DeviceIndices returns (0, 0) on non-OptiX builds.
func (g *OptixGAS) DeviceIndices() (uintptr, int) { return 0, 0 }

// SetHitGroupData returns ErrOptiXNotAvailable on non-OptiX builds.
func (p *OptixPipeline) SetHitGroupData(index int, vertices, normals, texcoords, indices unsafe.Pointer,
	mat [3]float32, metallic, roughness float32, emission [3]float32,
	baseColorTex uint64, stream unsafe.Pointer) error {
	return ErrOptiXNotAvailable
}

// CUDATexture is a stub for non-OptiX builds.
type CUDATexture struct{}

// CreateCUDATexture returns ErrOptiXNotAvailable on non-OptiX builds.
func CreateCUDATexture(rgba []byte, width, height int) (*CUDATexture, error) {
	return nil, ErrOptiXNotAvailable
}

// Handle returns 0 on non-OptiX builds.
func (t *CUDATexture) Handle() uint64 { return 0 }

// Close is a no-op on non-OptiX builds.
func (t *CUDATexture) Close() {}

// Update returns ErrOptiXNotAvailable on non-OptiX builds.
func (ias *OptixIAS) Update(instances []OptixInstance, stream unsafe.Pointer) error {
	return ErrOptiXNotAvailable
}

// Close is a no-op on non-OptiX builds.
func (ias *OptixIAS) Close() {}

// TraversableHandle returns 0 on non-OptiX builds.
func (ias *OptixIAS) TraversableHandle() uintptr { return 0 }

// NewOptixDenoiser returns ErrOptiXNotAvailable on non-OptiX builds.
func NewOptixDenoiser(ctx *OptixContext, width, height int, temporal bool) (*OptixDenoiser, error) {
	return nil, ErrOptiXNotAvailable
}

// Denoise returns ErrOptiXNotAvailable on non-OptiX builds.
func (d *OptixDenoiser) Denoise(noisyInput, cleanOutput, flowBuffer unsafe.Pointer,
	width, height int, stream unsafe.Pointer) error {
	return ErrOptiXNotAvailable
}

// Close is a no-op on non-OptiX builds.
func (d *OptixDenoiser) Close() {}

// Width returns 0 on non-OptiX builds.
func (d *OptixDenoiser) Width() int { return 0 }

// Height returns 0 on non-OptiX builds.
func (d *OptixDenoiser) Height() int { return 0 }

// Temporal returns false on non-OptiX builds.
func (d *OptixDenoiser) Temporal() bool { return false }
