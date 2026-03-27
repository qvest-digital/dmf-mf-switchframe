// optix_wrapper.h — C-linkage declarations for OptiX ray tracing wrapper functions.
// This header is consumed by:
//   - Go cgo preambles (optix_cgo.go)
//   - Other C/C++ code in the cuda/ directory that needs OptiX interop
//
// The implementation lives in optix_wrapper.cpp, compiled into
// libswitchframe_optix.a via the cuda/Makefile (only when HAS_OPTIX is defined).

#ifndef SWITCHFRAME_OPTIX_WRAPPER_H
#define SWITCHFRAME_OPTIX_WRAPPER_H

#include <stdint.h>

#ifdef __cplusplus
extern "C" {
#endif

// ---------- Opaque handles ----------

// Context handle: wraps OptixDeviceContext.
typedef void* optix_ctx_t;

// Module handle: wraps OptixModule (compiled PTX/OptiX IR).
typedef void* optix_module_t;

// Pipeline handle: wraps OptixPipeline + SBT.
typedef void* optix_pipeline_t;

// GAS handle: wraps geometry acceleration structure + device buffer.
typedef void* optix_gas_t;

// IAS handle: wraps instance acceleration structure + device buffer.
typedef void* optix_ias_t;

// Denoiser handle: wraps OptixDenoiser + scratch/state buffers.
typedef void* optix_denoiser_t;

// ---------- Context lifecycle ----------

// optix_create_context initializes OptiX on the given CUDA context.
// cuda_context is a CUcontext. Returns NULL on failure.
optix_ctx_t optix_create_context(void* cuda_context);

// optix_destroy_context releases the OptiX device context.
void optix_destroy_context(optix_ctx_t ctx);

// ---------- Module (compiled PTX -> OptiX module) ----------

// optix_create_module compiles PTX source into an OptiX module.
// Returns NULL on failure.
optix_module_t optix_create_module(optix_ctx_t ctx, const char* ptx_source, int ptx_size);

// optix_destroy_module releases the OptiX module.
void optix_destroy_module(optix_module_t mod);

// ---------- Pipeline (raygen + miss + closest-hit + any-hit programs) ----------

// Pipeline configuration: specifies shader entry points and trace depth.
typedef struct {
    optix_module_t module;
    const char* raygen_entry;       // e.g., "__raygen__render"
    const char* miss_entry;         // e.g., "__miss__transparent"
    const char* shadow_miss_entry;  // e.g., "__miss__shadow" (nullable — uses miss_entry if NULL)
    const char* closesthit_entry;   // e.g., "__closesthit__pbr"
    const char* anyhit_entry;       // e.g., "__anyhit__shadow" (nullable)
    int max_trace_depth;            // typically 2 (primary + shadow)
} optix_pipeline_config_t;

// optix_create_pipeline builds a ray tracing pipeline from the config.
// Returns NULL on failure.
optix_pipeline_t optix_create_pipeline(optix_ctx_t ctx, optix_pipeline_config_t* config);

// optix_destroy_pipeline releases the pipeline and its SBT.
void optix_destroy_pipeline(optix_pipeline_t pipeline);

// ---------- Hit group data ----------

// Per-primitive SBT data for closest-hit programs.
// Matches HitGroupData in arrender/shaders/common.cuh.
typedef struct {
    void* vertices;     // float3* device pointer to vertex positions
    void* normals;      // float3* device pointer to vertex normals (nullable)
    void* texcoords;    // float2* device pointer to UVs (nullable)
    void* indices;      // uint3* device pointer to triangle indices
    float base_color[3];
    float metallic;
    float roughness;
    float emission[3];
} optix_hitgroup_data_t;

// optix_set_hitgroup_data updates the hitgroup SBT record with per-instance data.
// instance_index selects which hitgroup record to update (for multi-material scenes).
// Returns 0 on success, non-zero on failure.
int optix_set_hitgroup_data(optix_pipeline_t pipeline,
    int instance_index,
    optix_hitgroup_data_t* data,
    void* cuda_stream);

// ---------- Geometry Acceleration Structure (per-mesh) ----------

// optix_build_triangle_gas builds a bottom-level acceleration structure
// from triangle mesh data. vertices is float3 (x,y,z) interleaved.
// Returns NULL on failure.
optix_gas_t optix_build_triangle_gas(optix_ctx_t ctx,
    const float* vertices, int num_vertices,
    const uint32_t* indices, int num_indices,
    void* cuda_stream);

// optix_destroy_gas releases the GAS and its device memory.
void optix_destroy_gas(optix_gas_t gas);

// optix_gas_get_device_ptrs returns the retained device vertex/index pointers
// from the GAS build. These are needed for SBT hitgroup data population.
void optix_gas_get_device_ptrs(optix_gas_t gas,
    void** out_vertices, int* out_num_vertices,
    void** out_indices, int* out_num_indices);

// ---------- Instance Acceleration Structure (scene-level) ----------

// Instance descriptor: references a GAS with a transform and material.
typedef struct {
    optix_gas_t gas;
    float transform[12];       // 3x4 row-major affine transform
    uint32_t sbt_offset;       // shader binding table offset (material index)
    uint32_t visibility_mask;  // ray visibility mask (0xFF = visible to all)
} optix_instance_t;

// optix_build_ias builds a top-level acceleration structure from instances.
// Returns NULL on failure.
optix_ias_t optix_build_ias(optix_ctx_t ctx,
    optix_instance_t* instances, int num_instances,
    void* cuda_stream);

// optix_update_ias performs an in-place refit of the IAS.
// Use this for animated transforms without full rebuild.
// Returns 0 on success, non-zero on failure.
int optix_update_ias(optix_ias_t ias,
    optix_instance_t* instances, int num_instances,
    void* cuda_stream);

// optix_destroy_ias releases the IAS and its device memory.
void optix_destroy_ias(optix_ias_t ias);

// optix_ias_get_handle returns the OptixTraversableHandle for use in launch params.
// The returned uint64 should be cast to void* and stored in optix_launch_params_t.ias_handle.
uint64_t optix_ias_get_handle(optix_ias_t ias);

// ---------- Light ----------

// Scene light for direct illumination. Matches Light struct in common.cuh.
typedef struct {
    int type;               // 0 = point, 1 = directional, 2 = area
    float position[3];      // world-space position (point/area)
    float direction[3];     // light direction (directional/area)
    float color[3];         // linear RGB color
    float intensity;        // radiance multiplier
} optix_light_t;

// ---------- Launch parameters ----------

// Parameters passed to all OptiX programs during ray launch.
// IMPORTANT: This struct layout must stay in sync with LaunchParams
// in arrender/shaders/common.cuh. If you add or reorder fields here,
// update the CUDA struct there (and vice-versa).
typedef struct {
    void* output_buffer;    // float4 RGBA, device pointer
    int width;
    int height;
    void* ias_handle;       // traversable handle (uint64_t internally)
    float camera_pos[3];    // camera world position
    float camera_u[3];      // camera right vector (screen-space X)
    float camera_v[3];      // camera up vector (screen-space Y)
    float camera_w[3];      // camera forward vector (screen-space -Z)
    int samples_per_pixel;  // number of rays per pixel
    int frame_number;       // for temporal accumulation / RNG seed
    void* lights;           // device pointer to optix_light_t array (nullable)
    int num_lights;         // number of lights in buffer (0 = use shader fallback)
} optix_launch_params_t;

// ---------- Ray launch ----------

// optix_launch launches ray tracing for the given pipeline and parameters.
// Returns 0 on success, non-zero on failure.
int optix_launch(optix_pipeline_t pipeline,
    optix_launch_params_t* params,
    void* cuda_stream);

// ---------- Denoiser ----------

// optix_create_denoiser creates an AI denoiser for the given resolution.
// temporal=1 enables temporal denoising (requires motion vectors).
// Returns NULL on failure.
optix_denoiser_t optix_create_denoiser(optix_ctx_t ctx,
    int width, int height, int temporal);

// optix_denoise runs the denoiser on a noisy input image.
// flow_buffer is float2 motion vectors (nullable for non-temporal mode).
// Returns 0 on success, non-zero on failure.
int optix_denoise(optix_denoiser_t denoiser,
    void* noisy_input,   // float4 RGBA device pointer
    void* clean_output,  // float4 RGBA device pointer
    void* flow_buffer,   // float2 motion vectors (nullable)
    int width, int height,
    void* cuda_stream);

// optix_destroy_denoiser releases the denoiser and its scratch buffers.
void optix_destroy_denoiser(optix_denoiser_t denoiser);

// ---------- Error handling ----------

// optix_get_last_error returns the last error message (thread-local buffer).
const char* optix_get_last_error(void);

#ifdef __cplusplus
}
#endif

#endif // SWITCHFRAME_OPTIX_WRAPPER_H
