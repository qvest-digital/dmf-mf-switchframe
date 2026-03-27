//go:build cgo && cuda && optix

package gpu

/*
#cgo CFLAGS: -I/usr/local/cuda/include
#cgo LDFLAGS: -L${SRCDIR}/cuda -lswitchframe_optix -L/usr/local/cuda/lib64 -lcuda -lcudart -lstdc++

#include <stdlib.h>
#include <stdint.h>

// OptiX is a C++ API, so we use extern "C" wrapper functions compiled
// separately in cuda/optix_wrapper.cpp -> libswitchframe_optix.a.
// See cuda/optix_wrapper.h for the full C-linkage API declaration.

typedef void* optix_ctx_t;
typedef void* optix_module_t;
typedef void* optix_pipeline_t;
typedef void* optix_gas_t;
typedef void* optix_ias_t;
typedef void* optix_denoiser_t;

// Context lifecycle.
extern optix_ctx_t optix_create_context(void* cuda_context);
extern void optix_destroy_context(optix_ctx_t ctx);

// Module.
extern optix_module_t optix_create_module(optix_ctx_t ctx, const char* ptx_source, int ptx_size);
extern void optix_destroy_module(optix_module_t mod);

// Pipeline configuration.
typedef struct {
    optix_module_t module;
    const char* raygen_entry;
    const char* miss_entry;
    const char* shadow_miss_entry;
    const char* closesthit_entry;
    const char* anyhit_entry;
    int max_trace_depth;
} optix_pipeline_config_t;

extern optix_pipeline_t optix_create_pipeline(optix_ctx_t ctx, optix_pipeline_config_t* config);
extern void optix_destroy_pipeline(optix_pipeline_t pipeline);

// Hit group data.
typedef struct {
    void* vertices;
    void* normals;
    void* texcoords;
    void* indices;
    float base_color[3];
    float metallic;
    float roughness;
    float emission[3];
    uint64_t base_color_tex;
} optix_hitgroup_data_t;

// CUDA texture objects.
typedef void* cuda_texture_t;
extern cuda_texture_t cuda_create_texture(const uint8_t* rgba, int width, int height);
extern void cuda_destroy_texture(cuda_texture_t tex);
extern uint64_t cuda_texture_get_handle(cuda_texture_t tex);

extern int optix_set_hitgroup_data(optix_pipeline_t pipeline,
    int instance_index,
    optix_hitgroup_data_t* data,
    void* cuda_stream);

// GAS.
extern optix_gas_t optix_build_triangle_gas(optix_ctx_t ctx,
    const float* vertices, int num_vertices,
    const uint32_t* indices, int num_indices,
    void* cuda_stream);
extern void optix_destroy_gas(optix_gas_t gas);
extern void optix_gas_get_device_ptrs(optix_gas_t gas,
    void** out_vertices, int* out_num_vertices,
    void** out_indices, int* out_num_indices);

// Instance descriptor.
typedef struct {
    optix_gas_t gas;
    float transform[12];
    uint32_t sbt_offset;
    uint32_t visibility_mask;
} optix_instance_t;

// IAS.
extern optix_ias_t optix_build_ias(optix_ctx_t ctx,
    optix_instance_t* instances, int num_instances,
    void* cuda_stream);
extern int optix_update_ias(optix_ias_t ias,
    optix_instance_t* instances, int num_instances,
    void* cuda_stream);
extern void optix_destroy_ias(optix_ias_t ias);
extern uint64_t optix_ias_get_handle(optix_ias_t ias);

// Launch parameters.
typedef struct {
    void* output_buffer;
    int width;
    int height;
    void* ias_handle;
    float camera_pos[3];
    float camera_u[3];
    float camera_v[3];
    float camera_w[3];
    int samples_per_pixel;
    int frame_number;
    void* lights;
    int num_lights;
} optix_launch_params_t;

// Launch.
extern int optix_launch(optix_pipeline_t pipeline,
    optix_launch_params_t* params,
    void* cuda_stream);

// Denoiser.
extern optix_denoiser_t optix_create_denoiser(optix_ctx_t ctx,
    int width, int height, int temporal);
extern int optix_denoise(optix_denoiser_t denoiser,
    void* noisy_input, void* clean_output, void* flow_buffer,
    int width, int height,
    void* cuda_stream);
extern void optix_destroy_denoiser(optix_denoiser_t denoiser);

// Error handling.
extern const char* optix_get_last_error(void);
*/
import "C"

import (
	"errors"
	"fmt"
	"unsafe"
)

// ErrOptiXNotAvailable indicates OptiX is not available in the current build.
var ErrOptiXNotAvailable = errors.New("gpu: OptiX not available (build without optix tag)")

// OptixContext wraps an OptiX device context.
type OptixContext struct {
	handle C.optix_ctx_t
}

// OptixModule wraps a compiled OptiX module (from PTX source).
type OptixModule struct {
	handle C.optix_module_t
}

// OptixPipeline wraps an OptiX ray tracing pipeline with its SBT.
type OptixPipeline struct {
	handle C.optix_pipeline_t
}

// OptixGAS wraps a geometry acceleration structure (bottom-level BVH).
type OptixGAS struct {
	handle C.optix_gas_t
}

// OptixIAS wraps an instance acceleration structure (top-level BVH).
type OptixIAS struct {
	handle C.optix_ias_t
}

// OptixDenoiser wraps an OptiX AI denoiser.
type OptixDenoiser struct {
	handle   C.optix_denoiser_t
	width    int
	height   int
	temporal bool
}

// OptixPipelineConfig specifies shader entry points for pipeline creation.
type OptixPipelineConfig struct {
	Module          *OptixModule
	RaygenEntry     string // e.g., "__raygen__render"
	MissEntry       string // e.g., "__miss__transparent"
	ShadowMissEntry string // e.g., "__miss__shadow" (empty = use MissEntry)
	ClosestHitEntry string // e.g., "__closesthit__pbr"
	AnyHitEntry     string // e.g., "__anyhit__shadow" (empty = none)
	MaxTraceDepth   int    // typically 2 (primary + shadow)
}

// OptixInstance describes a GAS instance in the scene with its transform.
type OptixInstance struct {
	GAS            *OptixGAS
	Transform      [12]float32 // 3x4 row-major affine transform
	SBTOffset      uint32      // shader binding table offset (material index)
	VisibilityMask uint32      // ray visibility mask (0xFF = visible to all)
}

// OptixLaunchParams specifies parameters for a ray tracing launch.
type OptixLaunchParams struct {
	OutputBuffer    unsafe.Pointer // float4 RGBA device pointer
	Width           int
	Height          int
	IASHandle       unsafe.Pointer // traversable handle (device pointer)
	CameraPos       [3]float32
	CameraU         [3]float32 // camera right vector
	CameraV         [3]float32 // camera up vector
	CameraW         [3]float32 // camera forward vector
	SamplesPerPixel int
	FrameNumber     int
	Lights          unsafe.Pointer // device pointer to light buffer (nullable)
	NumLights       int            // number of lights in buffer (0 = use shader fallback)
}

// NewOptixContext creates an OptiX device context on the given CUDA context.
// cudaCtx is a CUcontext (unsafe.Pointer).
func NewOptixContext(cudaCtx unsafe.Pointer) (*OptixContext, error) {
	handle := C.optix_create_context(cudaCtx)
	if handle == nil {
		errMsg := C.GoString(C.optix_get_last_error())
		return nil, fmt.Errorf("gpu: optix: create context failed: %s", errMsg)
	}
	return &OptixContext{handle: handle}, nil
}

// Close releases the OptiX device context.
func (c *OptixContext) Close() {
	if c == nil || c.handle == nil {
		return
	}
	C.optix_destroy_context(c.handle)
	c.handle = nil
}

// CreateModule compiles PTX source into an OptiX module.
func (c *OptixContext) CreateModule(ptxSource []byte) (*OptixModule, error) {
	if c == nil || c.handle == nil {
		return nil, fmt.Errorf("gpu: optix: nil context")
	}
	if len(ptxSource) == 0 {
		return nil, fmt.Errorf("gpu: optix: empty PTX source")
	}

	cPTX := (*C.char)(unsafe.Pointer(&ptxSource[0]))
	handle := C.optix_create_module(c.handle, cPTX, C.int(len(ptxSource)))
	if handle == nil {
		errMsg := C.GoString(C.optix_get_last_error())
		return nil, fmt.Errorf("gpu: optix: create module failed: %s", errMsg)
	}
	return &OptixModule{handle: handle}, nil
}

// Close releases the OptiX module.
func (m *OptixModule) Close() {
	if m == nil || m.handle == nil {
		return
	}
	C.optix_destroy_module(m.handle)
	m.handle = nil
}

// CreatePipeline builds a ray tracing pipeline from the given configuration.
func (c *OptixContext) CreatePipeline(config OptixPipelineConfig) (*OptixPipeline, error) {
	if c == nil || c.handle == nil {
		return nil, fmt.Errorf("gpu: optix: nil context")
	}
	if config.Module == nil || config.Module.handle == nil {
		return nil, fmt.Errorf("gpu: optix: nil module in pipeline config")
	}

	cRaygen := C.CString(config.RaygenEntry)
	defer C.free(unsafe.Pointer(cRaygen))

	cMiss := C.CString(config.MissEntry)
	defer C.free(unsafe.Pointer(cMiss))

	cClosestHit := C.CString(config.ClosestHitEntry)
	defer C.free(unsafe.Pointer(cClosestHit))

	var cShadowMiss *C.char
	if config.ShadowMissEntry != "" {
		cShadowMiss = C.CString(config.ShadowMissEntry)
		defer C.free(unsafe.Pointer(cShadowMiss))
	}

	var cAnyHit *C.char
	if config.AnyHitEntry != "" {
		cAnyHit = C.CString(config.AnyHitEntry)
		defer C.free(unsafe.Pointer(cAnyHit))
	}

	maxDepth := config.MaxTraceDepth
	if maxDepth <= 0 {
		maxDepth = 2
	}

	cConfig := C.optix_pipeline_config_t{
		module:            config.Module.handle,
		raygen_entry:      cRaygen,
		miss_entry:        cMiss,
		shadow_miss_entry: cShadowMiss,
		closesthit_entry:  cClosestHit,
		anyhit_entry:      cAnyHit,
		max_trace_depth:   C.int(maxDepth),
	}

	handle := C.optix_create_pipeline(c.handle, &cConfig)
	if handle == nil {
		errMsg := C.GoString(C.optix_get_last_error())
		return nil, fmt.Errorf("gpu: optix: create pipeline failed: %s", errMsg)
	}
	return &OptixPipeline{handle: handle}, nil
}

// Close releases the OptiX pipeline.
func (p *OptixPipeline) Close() {
	if p == nil || p.handle == nil {
		return
	}
	C.optix_destroy_pipeline(p.handle)
	p.handle = nil
}

// BuildTriangleGAS builds a bottom-level acceleration structure from triangle mesh data.
// vertices is a flat float32 slice with x,y,z interleaved (len must be num_vertices*3).
// indices is a flat uint32 slice of triangle indices (len must be divisible by 3, or nil for non-indexed).
func (c *OptixContext) BuildTriangleGAS(vertices []float32, indices []uint32, stream unsafe.Pointer) (*OptixGAS, error) {
	if c == nil || c.handle == nil {
		return nil, fmt.Errorf("gpu: optix: nil context")
	}
	if len(vertices) == 0 || len(vertices)%3 != 0 {
		return nil, fmt.Errorf("gpu: optix: vertices must be non-empty and divisible by 3")
	}

	numVertices := len(vertices) / 3
	var cIndices *C.uint32_t
	numIndices := 0
	if len(indices) > 0 {
		cIndices = (*C.uint32_t)(unsafe.Pointer(&indices[0]))
		numIndices = len(indices)
	}

	handle := C.optix_build_triangle_gas(c.handle,
		(*C.float)(unsafe.Pointer(&vertices[0])), C.int(numVertices),
		cIndices, C.int(numIndices),
		stream)
	if handle == nil {
		errMsg := C.GoString(C.optix_get_last_error())
		return nil, fmt.Errorf("gpu: optix: build GAS failed: %s", errMsg)
	}
	return &OptixGAS{handle: handle}, nil
}

// Close releases the GAS and its device memory.
func (g *OptixGAS) Close() {
	if g == nil || g.handle == nil {
		return
	}
	C.optix_destroy_gas(g.handle)
	g.handle = nil
}

// DeviceVertices returns the retained device pointer to vertex data (float3)
// and the number of vertices. Used for SBT hitgroup data population.
func (g *OptixGAS) DeviceVertices() (uintptr, int) {
	if g == nil || g.handle == nil {
		return 0, 0
	}
	var verts unsafe.Pointer
	var numVerts C.int
	C.optix_gas_get_device_ptrs(g.handle, &verts, &numVerts, nil, nil)
	return uintptr(verts), int(numVerts)
}

// DeviceIndices returns the retained device pointer to index data (uint3)
// and the number of indices. Used for SBT hitgroup data population.
func (g *OptixGAS) DeviceIndices() (uintptr, int) {
	if g == nil || g.handle == nil {
		return 0, 0
	}
	var indices unsafe.Pointer
	var numIndices C.int
	C.optix_gas_get_device_ptrs(g.handle, nil, nil, &indices, &numIndices)
	return uintptr(indices), int(numIndices)
}

// SetHitGroupData updates the hitgroup SBT record for a given instance.
// This populates per-instance vertex/index/normal/material data so the
// closest-hit shader can access geometry and material properties.
// baseColorTex is the cudaTextureObject_t handle (0 = no texture).
func (p *OptixPipeline) SetHitGroupData(index int, vertices, normals, texcoords, indices unsafe.Pointer,
	mat [3]float32, metallic, roughness float32, emission [3]float32,
	baseColorTex uint64, stream unsafe.Pointer) error {
	if p == nil || p.handle == nil {
		return fmt.Errorf("gpu: optix: nil pipeline")
	}

	data := C.optix_hitgroup_data_t{
		vertices:       vertices,
		normals:        normals,
		texcoords:      texcoords,
		indices:        indices,
		metallic:       C.float(metallic),
		roughness:      C.float(roughness),
		base_color_tex: C.uint64_t(baseColorTex),
	}
	data.base_color[0] = C.float(mat[0])
	data.base_color[1] = C.float(mat[1])
	data.base_color[2] = C.float(mat[2])
	data.emission[0] = C.float(emission[0])
	data.emission[1] = C.float(emission[1])
	data.emission[2] = C.float(emission[2])

	rc := C.optix_set_hitgroup_data(p.handle, C.int(index), &data, stream)
	if rc != 0 {
		errMsg := C.GoString(C.optix_get_last_error())
		return fmt.Errorf("gpu: optix: set hitgroup data failed: %s", errMsg)
	}
	return nil
}

// CUDATexture wraps a CUDA texture object (cudaArray + cudaTextureObject_t).
type CUDATexture struct {
	handle C.cuda_texture_t
}

// CreateCUDATexture uploads RGBA pixel data to GPU memory and creates a
// CUDA texture object with bilinear filtering. The returned texture must
// be freed with Close() when no longer needed.
func CreateCUDATexture(rgba []byte, width, height int) (*CUDATexture, error) {
	if len(rgba) == 0 || width <= 0 || height <= 0 {
		return nil, fmt.Errorf("gpu: cuda texture: invalid arguments (len=%d, %dx%d)", len(rgba), width, height)
	}
	expected := width * height * 4
	if len(rgba) < expected {
		return nil, fmt.Errorf("gpu: cuda texture: rgba buffer too small (%d < %d)", len(rgba), expected)
	}

	handle := C.cuda_create_texture((*C.uint8_t)(unsafe.Pointer(&rgba[0])), C.int(width), C.int(height))
	if handle == nil {
		errMsg := C.GoString(C.optix_get_last_error())
		return nil, fmt.Errorf("gpu: cuda texture: create failed: %s", errMsg)
	}
	return &CUDATexture{handle: handle}, nil
}

// Handle returns the raw cudaTextureObject_t as a uint64 for use in SBT data.
// Returns 0 if the texture is nil.
func (t *CUDATexture) Handle() uint64 {
	if t == nil || t.handle == nil {
		return 0
	}
	return uint64(C.cuda_texture_get_handle(t.handle))
}

// Close releases the CUDA array and texture object.
func (t *CUDATexture) Close() {
	if t == nil || t.handle == nil {
		return
	}
	C.cuda_destroy_texture(t.handle)
	t.handle = nil
}

// BuildIAS builds a top-level acceleration structure from instances.
func (c *OptixContext) BuildIAS(instances []OptixInstance, stream unsafe.Pointer) (*OptixIAS, error) {
	if c == nil || c.handle == nil {
		return nil, fmt.Errorf("gpu: optix: nil context")
	}
	if len(instances) == 0 {
		return nil, fmt.Errorf("gpu: optix: no instances provided")
	}

	// Convert Go instances to C structs.
	cInstances := make([]C.optix_instance_t, len(instances))
	for i, inst := range instances {
		if inst.GAS == nil || inst.GAS.handle == nil {
			return nil, fmt.Errorf("gpu: optix: nil GAS at instance %d", i)
		}
		cInstances[i].gas = inst.GAS.handle
		for j := 0; j < 12; j++ {
			cInstances[i].transform[j] = C.float(inst.Transform[j])
		}
		cInstances[i].sbt_offset = C.uint32_t(inst.SBTOffset)
		cInstances[i].visibility_mask = C.uint32_t(inst.VisibilityMask)
	}

	handle := C.optix_build_ias(c.handle,
		&cInstances[0], C.int(len(cInstances)),
		stream)
	if handle == nil {
		errMsg := C.GoString(C.optix_get_last_error())
		return nil, fmt.Errorf("gpu: optix: build IAS failed: %s", errMsg)
	}
	return &OptixIAS{handle: handle}, nil
}

// Update performs an in-place refit of the IAS with new instance transforms.
func (ias *OptixIAS) Update(instances []OptixInstance, stream unsafe.Pointer) error {
	if ias == nil || ias.handle == nil {
		return fmt.Errorf("gpu: optix: nil IAS")
	}
	if len(instances) == 0 {
		return fmt.Errorf("gpu: optix: no instances provided")
	}

	cInstances := make([]C.optix_instance_t, len(instances))
	for i, inst := range instances {
		if inst.GAS == nil || inst.GAS.handle == nil {
			return fmt.Errorf("gpu: optix: nil GAS at instance %d", i)
		}
		cInstances[i].gas = inst.GAS.handle
		for j := 0; j < 12; j++ {
			cInstances[i].transform[j] = C.float(inst.Transform[j])
		}
		cInstances[i].sbt_offset = C.uint32_t(inst.SBTOffset)
		cInstances[i].visibility_mask = C.uint32_t(inst.VisibilityMask)
	}

	rc := C.optix_update_ias(ias.handle,
		&cInstances[0], C.int(len(cInstances)),
		stream)
	if rc != 0 {
		errMsg := C.GoString(C.optix_get_last_error())
		return fmt.Errorf("gpu: optix: update IAS failed: %s", errMsg)
	}
	return nil
}

// Close releases the IAS and its device memory.
func (ias *OptixIAS) Close() {
	if ias == nil || ias.handle == nil {
		return
	}
	C.optix_destroy_ias(ias.handle)
	ias.handle = nil
}

// TraversableHandle returns the OptiX traversable handle for use in launch params.
// Cast the returned uintptr to unsafe.Pointer when setting OptixLaunchParams.IASHandle.
func (ias *OptixIAS) TraversableHandle() uintptr {
	if ias == nil || ias.handle == nil {
		return 0
	}
	return uintptr(C.optix_ias_get_handle(ias.handle))
}

// Launch executes ray tracing for the given pipeline and parameters.
func (p *OptixPipeline) Launch(params OptixLaunchParams, stream unsafe.Pointer) error {
	if p == nil || p.handle == nil {
		return fmt.Errorf("gpu: optix: nil pipeline")
	}

	cParams := C.optix_launch_params_t{
		output_buffer:     params.OutputBuffer,
		width:             C.int(params.Width),
		height:            C.int(params.Height),
		ias_handle:        params.IASHandle,
		samples_per_pixel: C.int(params.SamplesPerPixel),
		frame_number:      C.int(params.FrameNumber),
		lights:            params.Lights,
		num_lights:        C.int(params.NumLights),
	}
	for i := 0; i < 3; i++ {
		cParams.camera_pos[i] = C.float(params.CameraPos[i])
		cParams.camera_u[i] = C.float(params.CameraU[i])
		cParams.camera_v[i] = C.float(params.CameraV[i])
		cParams.camera_w[i] = C.float(params.CameraW[i])
	}

	rc := C.optix_launch(p.handle, &cParams, stream)
	if rc != 0 {
		errMsg := C.GoString(C.optix_get_last_error())
		return fmt.Errorf("gpu: optix: launch failed: %s", errMsg)
	}
	return nil
}

// NewOptixDenoiser creates an AI denoiser for the given resolution.
// If temporal is true, the denoiser uses motion vectors for temporal stability.
func NewOptixDenoiser(ctx *OptixContext, width, height int, temporal bool) (*OptixDenoiser, error) {
	if ctx == nil || ctx.handle == nil {
		return nil, fmt.Errorf("gpu: optix: nil context")
	}
	if width <= 0 || height <= 0 {
		return nil, fmt.Errorf("gpu: optix: invalid denoiser dimensions: %dx%d", width, height)
	}

	temporalInt := C.int(0)
	if temporal {
		temporalInt = 1
	}

	handle := C.optix_create_denoiser(ctx.handle, C.int(width), C.int(height), temporalInt)
	if handle == nil {
		errMsg := C.GoString(C.optix_get_last_error())
		return nil, fmt.Errorf("gpu: optix: create denoiser failed: %s", errMsg)
	}
	return &OptixDenoiser{
		handle:   handle,
		width:    width,
		height:   height,
		temporal: temporal,
	}, nil
}

// Denoise runs the AI denoiser on a noisy input image.
// noisyInput and cleanOutput are float4 RGBA device pointers.
// flowBuffer is a float2 motion vector device pointer (nil for non-temporal mode).
func (d *OptixDenoiser) Denoise(noisyInput, cleanOutput, flowBuffer unsafe.Pointer,
	width, height int, stream unsafe.Pointer) error {
	if d == nil || d.handle == nil {
		return fmt.Errorf("gpu: optix: nil denoiser")
	}
	if noisyInput == nil || cleanOutput == nil {
		return fmt.Errorf("gpu: optix: nil input or output pointer")
	}

	rc := C.optix_denoise(d.handle,
		noisyInput, cleanOutput, flowBuffer,
		C.int(width), C.int(height),
		stream)
	if rc != 0 {
		errMsg := C.GoString(C.optix_get_last_error())
		return fmt.Errorf("gpu: optix: denoise failed: %s", errMsg)
	}
	return nil
}

// Close releases the denoiser and its scratch buffers.
func (d *OptixDenoiser) Close() {
	if d == nil || d.handle == nil {
		return
	}
	C.optix_destroy_denoiser(d.handle)
	d.handle = nil
}

// Width returns the denoiser's configured width.
func (d *OptixDenoiser) Width() int {
	if d == nil {
		return 0
	}
	return d.width
}

// Height returns the denoiser's configured height.
func (d *OptixDenoiser) Height() int {
	if d == nil {
		return 0
	}
	return d.height
}

// Temporal returns whether the denoiser uses temporal mode.
func (d *OptixDenoiser) Temporal() bool {
	if d == nil {
		return false
	}
	return d.temporal
}
