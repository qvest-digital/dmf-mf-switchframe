// optix_wrapper.cpp — C-linkage wrappers around the OptiX C++ API.
// Compiled separately and linked into the Go binary via cgo LDFLAGS.
// Build: g++ -O2 -fPIC -std=c++17 -c optix_wrapper.cpp -I/usr/local/cuda/include -DHAS_OPTIX
//
// This file compiles in two modes:
//   1. Without OptiX SDK (#ifndef HAS_OPTIX): all functions return failure with
//      "OptiX SDK not installed" error. This lets us build and test the Go
//      bindings/stubs without the SDK.
//   2. With OptiX SDK (#ifdef HAS_OPTIX): full implementation using OptiX API.
//
// C-linkage API is declared in optix_wrapper.h.

#include <cstring>
#include <cstdio>
#include <cstdarg>
#include <cstdlib>
#include <stdint.h>

#include "optix_wrapper.h"

#ifdef HAS_OPTIX
#include <optix.h>
#include <optix_stubs.h>
#include <optix_function_table_definition.h>
#include <cuda_runtime.h>
#include <cuda.h>
#include <vector>
#include <memory>
#endif

// Thread-local error message buffer.
static thread_local char optix_error_buf[1024] = {0};

static void set_error(const char* fmt, ...) {
    va_list args;
    va_start(args, fmt);
    vsnprintf(optix_error_buf, sizeof(optix_error_buf), fmt, args);
    va_end(args);
}

#ifdef HAS_OPTIX

// ---------- OptiX logging callback ----------

static void optix_log_callback(unsigned int level, const char* tag, const char* message, void* /*cbdata*/) {
    // Level: 1=fatal, 2=error, 3=warning, 4=print
    if (level <= 3) {
        fprintf(stderr, "[OptiX %s] %s\n", tag, message);
    }
}

// ---------- Internal wrapper structs ----------

struct OptixContextWrapper {
    OptixDeviceContext context;
    CUcontext cuda_context;
};

struct OptixModuleWrapper {
    OptixModule module;
    OptixDeviceContext context;
};

struct OptixPipelineWrapper {
    OptixPipeline pipeline;
    OptixShaderBindingTable sbt;
    OptixDeviceContext context;

    // Program groups (owned — destroyed in optix_destroy_pipeline).
    OptixProgramGroup raygen_pg;
    OptixProgramGroup miss_pg;
    OptixProgramGroup shadow_miss_pg;
    OptixProgramGroup hitgroup_pg;

    // SBT device buffers (owned)
    CUdeviceptr d_raygen_record;
    CUdeviceptr d_miss_record;     // array of 2 miss records (primary + shadow)
    CUdeviceptr d_hitgroup_record;
    int hitgroup_record_count;     // number of allocated hitgroup records
    int hitgroup_record_capacity;  // allocated capacity

    // Persistent launch params device buffer (I3 fix: allocated once, reused).
    CUdeviceptr d_launch_params;
    size_t launch_params_size;
};

struct OptixGASWrapper {
    OptixTraversableHandle handle;
    CUdeviceptr d_gas_output;
    size_t gas_output_size;
    OptixDeviceContext context;
    CUdeviceptr d_vertices;   // retained device vertex buffer for SBT hitgroup data
    CUdeviceptr d_indices;    // retained device index buffer for SBT hitgroup data
    int num_vertices;
    int num_indices;
};

struct OptixIASWrapper {
    OptixTraversableHandle handle;
    CUdeviceptr d_ias_output;
    size_t ias_output_size;
    CUdeviceptr d_instances;
    size_t instances_size;
    CUdeviceptr d_temp;
    size_t temp_size;
    OptixDeviceContext context;
};

struct OptixDenoiserWrapper {
    OptixDenoiser denoiser;
    CUdeviceptr d_state;
    size_t state_size;
    CUdeviceptr d_scratch;
    size_t scratch_size;
    int width;
    int height;
    int temporal;
    OptixDeviceContext context;
};

// Helper: SBT record header size (OPTIX_SBT_RECORD_HEADER_SIZE = 32 bytes).
struct alignas(OPTIX_SBT_RECORD_ALIGNMENT) SBTRecord {
    char header[OPTIX_SBT_RECORD_HEADER_SIZE];
};

// SBT record with HitGroupData appended after the 32-byte header.
// Must match HitGroupData in arrender/shaders/common.cuh:
//   float3* vertices, float3* normals, float2* texcoords, uint3* indices, Material
struct alignas(OPTIX_SBT_RECORD_ALIGNMENT) HitGroupSBTRecord {
    char header[OPTIX_SBT_RECORD_HEADER_SIZE];
    // HitGroupData fields (must match common.cuh HitGroupData layout):
    void* vertices;     // float3* device pointer
    void* normals;      // float3* device pointer (nullable)
    void* texcoords;    // float2* device pointer (nullable)
    void* indices;      // uint3* device pointer
    // Material struct (3 floats base_color, float metallic, float roughness, 3 floats emission):
    float base_color[3];
    float metallic;
    float roughness;
    float emission[3];
};

extern "C" {

// ---------- Context lifecycle ----------

optix_ctx_t optix_create_context(void* cuda_context) {
    // Initialize OptiX function table (must be called once per process).
    static bool initialized = false;
    if (!initialized) {
        OptixResult res = optixInit();
        if (res != OPTIX_SUCCESS) {
            set_error("optixInit failed: %d", (int)res);
            return nullptr;
        }
        initialized = true;
    }

    CUcontext cu_ctx = static_cast<CUcontext>(cuda_context);
    if (!cu_ctx) {
        // OptiX 9.0+ requires an explicit CUDA context (NULL no longer works).
        // Ensure CUDA Runtime API primary context is active on this thread,
        // then retrieve it via the Driver API.
        cudaSetDevice(0);
        cudaFree(0); // force primary context initialization
        CUresult cu_err = cuCtxGetCurrent(&cu_ctx);
        if (cu_err != CUDA_SUCCESS || !cu_ctx) {
            set_error("no current CUDA context after cudaSetDevice (cuCtxGetCurrent: %d)", (int)cu_err);
            return nullptr;
        }
    }

    OptixDeviceContextOptions options = {};
    options.logCallbackFunction = optix_log_callback;
    options.logCallbackLevel = 3; // warnings and above

    OptixDeviceContext opt_ctx = nullptr;
    OptixResult res = optixDeviceContextCreate(cu_ctx, &options, &opt_ctx);
    if (res != OPTIX_SUCCESS) {
        set_error("optixDeviceContextCreate failed: %d", (int)res);
        return nullptr;
    }

    auto* wrapper = new OptixContextWrapper();
    wrapper->context = opt_ctx;
    wrapper->cuda_context = cu_ctx;
    return static_cast<void*>(wrapper);
}

void optix_destroy_context(optix_ctx_t ctx) {
    if (!ctx) return;
    auto* wrapper = static_cast<OptixContextWrapper*>(ctx);
    if (wrapper->context) {
        optixDeviceContextDestroy(wrapper->context);
    }
    delete wrapper;
}

// ---------- Module ----------

optix_module_t optix_create_module(optix_ctx_t ctx, const char* ptx_source, int ptx_size) {
    if (!ctx || !ptx_source || ptx_size <= 0) {
        set_error("optix_create_module: invalid arguments");
        return nullptr;
    }

    auto* ctx_wrapper = static_cast<OptixContextWrapper*>(ctx);

    OptixModuleCompileOptions module_options = {};
    module_options.maxRegisterCount = OPTIX_COMPILE_DEFAULT_MAX_REGISTER_COUNT;
    module_options.optLevel = OPTIX_COMPILE_OPTIMIZATION_DEFAULT;
    module_options.debugLevel = OPTIX_COMPILE_DEBUG_LEVEL_NONE;

    OptixPipelineCompileOptions pipeline_options = {};
    pipeline_options.usesMotionBlur = 0;
    pipeline_options.traversableGraphFlags = OPTIX_TRAVERSABLE_GRAPH_FLAG_ALLOW_SINGLE_LEVEL_INSTANCING;
    pipeline_options.numPayloadValues = 2; // pointer low/high (primary), occluded flag (shadow)
    pipeline_options.numAttributeValues = 2; // barycentrics
    pipeline_options.exceptionFlags = OPTIX_EXCEPTION_FLAG_NONE;
    pipeline_options.pipelineLaunchParamsVariableName = "params";

    char log[2048];
    size_t log_size = sizeof(log);

    OptixModule module = nullptr;
    OptixResult res = optixModuleCreate(
        ctx_wrapper->context,
        &module_options,
        &pipeline_options,
        ptx_source,
        static_cast<size_t>(ptx_size),
        log, &log_size,
        &module);

    if (res != OPTIX_SUCCESS) {
        set_error("optixModuleCreate failed: %d, log: %s", (int)res, log);
        return nullptr;
    }

    auto* wrapper = new OptixModuleWrapper();
    wrapper->module = module;
    wrapper->context = ctx_wrapper->context;
    return static_cast<void*>(wrapper);
}

void optix_destroy_module(optix_module_t mod) {
    if (!mod) return;
    auto* wrapper = static_cast<OptixModuleWrapper*>(mod);
    if (wrapper->module) {
        optixModuleDestroy(wrapper->module);
    }
    delete wrapper;
}

// ---------- Pipeline ----------

optix_pipeline_t optix_create_pipeline(optix_ctx_t ctx, optix_pipeline_config_t* config) {
    if (!ctx || !config || !config->module) {
        set_error("optix_create_pipeline: invalid arguments");
        return nullptr;
    }

    auto* ctx_wrapper = static_cast<OptixContextWrapper*>(ctx);
    auto* mod_wrapper = static_cast<OptixModuleWrapper*>(config->module);

    // Create program groups: raygen, miss (primary + shadow), hitgroup.
    OptixProgramGroupOptions pg_options = {};
    char log[2048];
    size_t log_size;

    // Raygen program group.
    OptixProgramGroupDesc raygen_desc = {};
    raygen_desc.kind = OPTIX_PROGRAM_GROUP_KIND_RAYGEN;
    raygen_desc.raygen.module = mod_wrapper->module;
    raygen_desc.raygen.entryFunctionName = config->raygen_entry;

    OptixProgramGroup raygen_pg = nullptr;
    log_size = sizeof(log);
    OptixResult res = optixProgramGroupCreate(
        ctx_wrapper->context, &raygen_desc, 1, &pg_options, log, &log_size, &raygen_pg);
    if (res != OPTIX_SUCCESS) {
        set_error("optixProgramGroupCreate(raygen) failed: %d", (int)res);
        return nullptr;
    }

    // Primary miss program group.
    OptixProgramGroupDesc miss_desc = {};
    miss_desc.kind = OPTIX_PROGRAM_GROUP_KIND_MISS;
    miss_desc.miss.module = mod_wrapper->module;
    miss_desc.miss.entryFunctionName = config->miss_entry;

    OptixProgramGroup miss_pg = nullptr;
    log_size = sizeof(log);
    res = optixProgramGroupCreate(
        ctx_wrapper->context, &miss_desc, 1, &pg_options, log, &log_size, &miss_pg);
    if (res != OPTIX_SUCCESS) {
        set_error("optixProgramGroupCreate(miss) failed: %d", (int)res);
        optixProgramGroupDestroy(raygen_pg);
        return nullptr;
    }

    // Shadow miss program group (I9: separate miss program for shadow rays).
    // If no shadow_miss_entry is provided, reuse the primary miss entry.
    const char* shadow_miss_name = config->shadow_miss_entry
        ? config->shadow_miss_entry
        : config->miss_entry;

    OptixProgramGroupDesc shadow_miss_desc = {};
    shadow_miss_desc.kind = OPTIX_PROGRAM_GROUP_KIND_MISS;
    shadow_miss_desc.miss.module = mod_wrapper->module;
    shadow_miss_desc.miss.entryFunctionName = shadow_miss_name;

    OptixProgramGroup shadow_miss_pg = nullptr;
    log_size = sizeof(log);
    res = optixProgramGroupCreate(
        ctx_wrapper->context, &shadow_miss_desc, 1, &pg_options, log, &log_size, &shadow_miss_pg);
    if (res != OPTIX_SUCCESS) {
        set_error("optixProgramGroupCreate(shadow_miss) failed: %d", (int)res);
        optixProgramGroupDestroy(raygen_pg);
        optixProgramGroupDestroy(miss_pg);
        return nullptr;
    }

    // Hitgroup program group (closest-hit + optional any-hit).
    OptixProgramGroupDesc hitgroup_desc = {};
    hitgroup_desc.kind = OPTIX_PROGRAM_GROUP_KIND_HITGROUP;
    hitgroup_desc.hitgroup.moduleCH = mod_wrapper->module;
    hitgroup_desc.hitgroup.entryFunctionNameCH = config->closesthit_entry;
    if (config->anyhit_entry) {
        hitgroup_desc.hitgroup.moduleAH = mod_wrapper->module;
        hitgroup_desc.hitgroup.entryFunctionNameAH = config->anyhit_entry;
    }

    OptixProgramGroup hitgroup_pg = nullptr;
    log_size = sizeof(log);
    res = optixProgramGroupCreate(
        ctx_wrapper->context, &hitgroup_desc, 1, &pg_options, log, &log_size, &hitgroup_pg);
    if (res != OPTIX_SUCCESS) {
        set_error("optixProgramGroupCreate(hitgroup) failed: %d", (int)res);
        optixProgramGroupDestroy(raygen_pg);
        optixProgramGroupDestroy(miss_pg);
        optixProgramGroupDestroy(shadow_miss_pg);
        return nullptr;
    }

    // Link pipeline — include all 4 program groups.
    OptixProgramGroup program_groups[] = { raygen_pg, miss_pg, shadow_miss_pg, hitgroup_pg };

    OptixPipelineCompileOptions pipeline_compile_options = {};
    pipeline_compile_options.usesMotionBlur = 0;
    pipeline_compile_options.traversableGraphFlags = OPTIX_TRAVERSABLE_GRAPH_FLAG_ALLOW_SINGLE_LEVEL_INSTANCING;
    pipeline_compile_options.numPayloadValues = 2; // pointer low/high (primary), occluded flag (shadow)
    pipeline_compile_options.numAttributeValues = 2;
    pipeline_compile_options.exceptionFlags = OPTIX_EXCEPTION_FLAG_NONE;
    pipeline_compile_options.pipelineLaunchParamsVariableName = "params";

    OptixPipelineLinkOptions link_options = {};
    link_options.maxTraceDepth = config->max_trace_depth > 0 ? config->max_trace_depth : 2;

    OptixPipeline pipeline = nullptr;
    log_size = sizeof(log);
    res = optixPipelineCreate(
        ctx_wrapper->context,
        &pipeline_compile_options,
        &link_options,
        program_groups, 4,
        log, &log_size,
        &pipeline);
    if (res != OPTIX_SUCCESS) {
        set_error("optixPipelineCreate failed: %d", (int)res);
        optixProgramGroupDestroy(raygen_pg);
        optixProgramGroupDestroy(miss_pg);
        optixProgramGroupDestroy(shadow_miss_pg);
        optixProgramGroupDestroy(hitgroup_pg);
        return nullptr;
    }

    // Set pipeline stack sizes.
    optixPipelineSetStackSize(pipeline,
        2 * 1024,   // direct callable stack size from traversal
        2 * 1024,   // direct callable stack size from state
        2 * 1024,   // continuation stack size
        2);         // max traversable depth (IAS->GAS = 2 levels)

    // Build shader binding table (SBT).
    auto* pw = new OptixPipelineWrapper();
    pw->pipeline = pipeline;
    pw->context = ctx_wrapper->context;

    // I5: Store program groups — destroyed in optix_destroy_pipeline.
    pw->raygen_pg = raygen_pg;
    pw->miss_pg = miss_pg;
    pw->shadow_miss_pg = shadow_miss_pg;
    pw->hitgroup_pg = hitgroup_pg;

    // Raygen SBT record.
    SBTRecord raygen_record = {};
    optixSbtRecordPackHeader(raygen_pg, &raygen_record);
    cudaMalloc(reinterpret_cast<void**>(&pw->d_raygen_record), sizeof(SBTRecord));
    cudaMemcpy(reinterpret_cast<void*>(pw->d_raygen_record), &raygen_record,
               sizeof(SBTRecord), cudaMemcpyHostToDevice);

    // I9: Two miss SBT records — primary (index 0) and shadow (index 1).
    SBTRecord miss_records[2] = {};
    optixSbtRecordPackHeader(miss_pg, &miss_records[0]);
    optixSbtRecordPackHeader(shadow_miss_pg, &miss_records[1]);
    cudaMalloc(reinterpret_cast<void**>(&pw->d_miss_record), sizeof(SBTRecord) * 2);
    cudaMemcpy(reinterpret_cast<void*>(pw->d_miss_record), miss_records,
               sizeof(SBTRecord) * 2, cudaMemcpyHostToDevice);

    // I4: Hitgroup SBT record with HitGroupData (initially one record).
    HitGroupSBTRecord hitgroup_record = {};
    memset(&hitgroup_record, 0, sizeof(hitgroup_record));
    optixSbtRecordPackHeader(hitgroup_pg, &hitgroup_record);
    // Default material: gray dielectric.
    hitgroup_record.base_color[0] = 0.8f;
    hitgroup_record.base_color[1] = 0.8f;
    hitgroup_record.base_color[2] = 0.8f;
    hitgroup_record.roughness = 0.5f;

    cudaMalloc(reinterpret_cast<void**>(&pw->d_hitgroup_record), sizeof(HitGroupSBTRecord));
    cudaMemcpy(reinterpret_cast<void*>(pw->d_hitgroup_record), &hitgroup_record,
               sizeof(HitGroupSBTRecord), cudaMemcpyHostToDevice);
    pw->hitgroup_record_count = 1;
    pw->hitgroup_record_capacity = 1;

    // Populate SBT.
    pw->sbt = {};
    pw->sbt.raygenRecord = pw->d_raygen_record;
    pw->sbt.missRecordBase = pw->d_miss_record;
    pw->sbt.missRecordStrideInBytes = sizeof(SBTRecord);
    pw->sbt.missRecordCount = 2; // I9: primary + shadow miss programs
    pw->sbt.hitgroupRecordBase = pw->d_hitgroup_record;
    pw->sbt.hitgroupRecordStrideInBytes = sizeof(HitGroupSBTRecord);
    pw->sbt.hitgroupRecordCount = 1;

    // I3: Allocate persistent launch params buffer (reused every frame).
    pw->launch_params_size = sizeof(optix_launch_params_t);
    cudaMalloc(reinterpret_cast<void**>(&pw->d_launch_params), pw->launch_params_size);

    return static_cast<void*>(pw);
}

void optix_destroy_pipeline(optix_pipeline_t pipeline) {
    if (!pipeline) return;
    auto* pw = static_cast<OptixPipelineWrapper*>(pipeline);
    if (pw->d_raygen_record) cudaFree(reinterpret_cast<void*>(pw->d_raygen_record));
    if (pw->d_miss_record) cudaFree(reinterpret_cast<void*>(pw->d_miss_record));
    if (pw->d_hitgroup_record) cudaFree(reinterpret_cast<void*>(pw->d_hitgroup_record));
    // I3: Free persistent launch params buffer.
    if (pw->d_launch_params) cudaFree(reinterpret_cast<void*>(pw->d_launch_params));
    if (pw->pipeline) optixPipelineDestroy(pw->pipeline);
    // I5: Destroy program groups (owned by the wrapper, not destroyed at creation).
    if (pw->raygen_pg) optixProgramGroupDestroy(pw->raygen_pg);
    if (pw->miss_pg) optixProgramGroupDestroy(pw->miss_pg);
    if (pw->shadow_miss_pg) optixProgramGroupDestroy(pw->shadow_miss_pg);
    if (pw->hitgroup_pg) optixProgramGroupDestroy(pw->hitgroup_pg);
    delete pw;
}

// ---------- Hit group data ----------

int optix_set_hitgroup_data(optix_pipeline_t pipeline,
    int instance_index,
    optix_hitgroup_data_t* data,
    void* cuda_stream) {

    if (!pipeline || !data || instance_index < 0) {
        set_error("optix_set_hitgroup_data: invalid arguments");
        return -1;
    }

    auto* pw = static_cast<OptixPipelineWrapper*>(pipeline);
    cudaStream_t stream = static_cast<cudaStream_t>(cuda_stream);

    // Grow the hitgroup SBT array if needed.
    int required = instance_index + 1;
    if (required > pw->hitgroup_record_capacity) {
        int new_cap = required * 2;
        CUdeviceptr new_buf = 0;
        size_t new_size = sizeof(HitGroupSBTRecord) * new_cap;
        cudaError_t err = cudaMalloc(reinterpret_cast<void**>(&new_buf), new_size);
        if (err != cudaSuccess) {
            set_error("optix_set_hitgroup_data: cudaMalloc(%zu) failed: %d", new_size, (int)err);
            return -1;
        }

        // Copy existing records.
        if (pw->d_hitgroup_record && pw->hitgroup_record_count > 0) {
            err = cudaMemcpy(reinterpret_cast<void*>(new_buf),
                       reinterpret_cast<void*>(pw->d_hitgroup_record),
                       sizeof(HitGroupSBTRecord) * pw->hitgroup_record_count,
                       cudaMemcpyDeviceToDevice);
            if (err != cudaSuccess) {
                set_error("optix_set_hitgroup_data: cudaMemcpy(D2D) failed: %d", (int)err);
                cudaFree(reinterpret_cast<void*>(new_buf));
                return -1;
            }
            cudaFree(reinterpret_cast<void*>(pw->d_hitgroup_record));
        }

        // Initialize new records with the hitgroup program header.
        for (int i = pw->hitgroup_record_count; i < new_cap; i++) {
            HitGroupSBTRecord empty = {};
            optixSbtRecordPackHeader(pw->hitgroup_pg, &empty);
            empty.base_color[0] = 0.8f;
            empty.base_color[1] = 0.8f;
            empty.base_color[2] = 0.8f;
            empty.roughness = 0.5f;
            err = cudaMemcpy(
                reinterpret_cast<void*>(new_buf + sizeof(HitGroupSBTRecord) * i),
                &empty, sizeof(HitGroupSBTRecord), cudaMemcpyHostToDevice);
            if (err != cudaSuccess) {
                set_error("optix_set_hitgroup_data: cudaMemcpy(init record %d) failed: %d", i, (int)err);
                cudaFree(reinterpret_cast<void*>(new_buf));
                return -1;
            }
        }

        pw->d_hitgroup_record = new_buf;
        pw->hitgroup_record_capacity = new_cap;
    }

    if (required > pw->hitgroup_record_count) {
        pw->hitgroup_record_count = required;
    }

    // Build the hitgroup record on the host.
    HitGroupSBTRecord record = {};
    optixSbtRecordPackHeader(pw->hitgroup_pg, &record);
    record.vertices = data->vertices;
    record.normals = data->normals;
    record.texcoords = data->texcoords;
    record.indices = data->indices;
    fprintf(stderr, "[optix_set_hitgroup_data] idx=%d verts=%p normals=%p texcoords=%p indices=%p base_color=(%.2f,%.2f,%.2f) metallic=%.2f roughness=%.2f sizeof(record)=%zu\n",
        instance_index, data->vertices, data->normals, data->texcoords, data->indices,
        data->base_color[0], data->base_color[1], data->base_color[2],
        data->metallic, data->roughness, sizeof(record));
    record.base_color[0] = data->base_color[0];
    record.base_color[1] = data->base_color[1];
    record.base_color[2] = data->base_color[2];
    record.metallic = data->metallic;
    record.roughness = data->roughness;
    record.emission[0] = data->emission[0];
    record.emission[1] = data->emission[1];
    record.emission[2] = data->emission[2];

    // Upload to the correct slot.
    CUdeviceptr dest = pw->d_hitgroup_record + sizeof(HitGroupSBTRecord) * instance_index;
    cudaMemcpyAsync(reinterpret_cast<void*>(dest), &record,
                    sizeof(HitGroupSBTRecord),
                    cudaMemcpyHostToDevice, stream);

    // Update SBT.
    pw->sbt.hitgroupRecordBase = pw->d_hitgroup_record;
    pw->sbt.hitgroupRecordStrideInBytes = sizeof(HitGroupSBTRecord);
    pw->sbt.hitgroupRecordCount = pw->hitgroup_record_count;

    return 0;
}

// ---------- Geometry Acceleration Structure ----------

optix_gas_t optix_build_triangle_gas(optix_ctx_t ctx,
    const float* vertices, int num_vertices,
    const uint32_t* indices, int num_indices,
    void* cuda_stream) {

    if (!ctx || !vertices || num_vertices <= 0) {
        set_error("optix_build_triangle_gas: invalid arguments");
        return nullptr;
    }

    auto* ctx_wrapper = static_cast<OptixContextWrapper*>(ctx);
    cudaStream_t stream = static_cast<cudaStream_t>(cuda_stream);

    // Upload vertex data to device.
    size_t vertex_bytes = static_cast<size_t>(num_vertices) * 3 * sizeof(float);
    CUdeviceptr d_vertices = 0;
    cudaMalloc(reinterpret_cast<void**>(&d_vertices), vertex_bytes);
    cudaMemcpyAsync(reinterpret_cast<void*>(d_vertices), vertices,
                    vertex_bytes, cudaMemcpyHostToDevice, stream);

    // Upload index data if provided.
    CUdeviceptr d_indices = 0;
    if (indices && num_indices > 0) {
        size_t index_bytes = static_cast<size_t>(num_indices) * sizeof(uint32_t);
        cudaMalloc(reinterpret_cast<void**>(&d_indices), index_bytes);
        cudaMemcpyAsync(reinterpret_cast<void*>(d_indices), indices,
                        index_bytes, cudaMemcpyHostToDevice, stream);
    }

    // Build input descriptor.
    OptixBuildInput build_input = {};
    build_input.type = OPTIX_BUILD_INPUT_TYPE_TRIANGLES;

    build_input.triangleArray.vertexFormat = OPTIX_VERTEX_FORMAT_FLOAT3;
    build_input.triangleArray.vertexStrideInBytes = 3 * sizeof(float);
    build_input.triangleArray.numVertices = static_cast<unsigned int>(num_vertices);
    build_input.triangleArray.vertexBuffers = &d_vertices;

    if (d_indices) {
        build_input.triangleArray.indexFormat = OPTIX_INDICES_FORMAT_UNSIGNED_INT3;
        build_input.triangleArray.indexStrideInBytes = 3 * sizeof(uint32_t);
        build_input.triangleArray.numIndexTriplets = static_cast<unsigned int>(num_indices / 3);
        build_input.triangleArray.indexBuffer = d_indices;
    }

    uint32_t flags = OPTIX_GEOMETRY_FLAG_NONE;
    build_input.triangleArray.flags = &flags;
    build_input.triangleArray.numSbtRecords = 1;

    // Compute memory requirements.
    OptixAccelBuildOptions accel_options = {};
    accel_options.buildFlags = OPTIX_BUILD_FLAG_ALLOW_COMPACTION | OPTIX_BUILD_FLAG_PREFER_FAST_TRACE;
    accel_options.operation = OPTIX_BUILD_OPERATION_BUILD;

    OptixAccelBufferSizes gas_sizes;
    OptixResult res = optixAccelComputeMemoryUsage(
        ctx_wrapper->context, &accel_options, &build_input, 1, &gas_sizes);
    if (res != OPTIX_SUCCESS) {
        set_error("optixAccelComputeMemoryUsage failed: %d", (int)res);
        cudaFree(reinterpret_cast<void*>(d_vertices));
        if (d_indices) cudaFree(reinterpret_cast<void*>(d_indices));
        return nullptr;
    }

    // Allocate temp and output buffers.
    CUdeviceptr d_temp = 0;
    cudaMalloc(reinterpret_cast<void**>(&d_temp), gas_sizes.tempSizeInBytes);

    CUdeviceptr d_output = 0;
    cudaMalloc(reinterpret_cast<void**>(&d_output), gas_sizes.outputSizeInBytes);

    OptixTraversableHandle handle = 0;
    res = optixAccelBuild(
        ctx_wrapper->context, stream,
        &accel_options, &build_input, 1,
        d_temp, gas_sizes.tempSizeInBytes,
        d_output, gas_sizes.outputSizeInBytes,
        &handle, nullptr, 0);

    cudaFree(reinterpret_cast<void*>(d_temp));

    if (res != OPTIX_SUCCESS) {
        set_error("optixAccelBuild(GAS) failed: %d", (int)res);
        cudaFree(reinterpret_cast<void*>(d_output));
        cudaFree(reinterpret_cast<void*>(d_vertices));
        if (d_indices) cudaFree(reinterpret_cast<void*>(d_indices));
        return nullptr;
    }

    auto* gw = new OptixGASWrapper();
    gw->handle = handle;
    gw->d_gas_output = d_output;
    gw->gas_output_size = gas_sizes.outputSizeInBytes;
    gw->context = ctx_wrapper->context;
    // Retain vertex/index device pointers for SBT hitgroup data access.
    gw->d_vertices = d_vertices;
    gw->d_indices = d_indices;
    gw->num_vertices = num_vertices;
    gw->num_indices = num_indices;
    return static_cast<void*>(gw);
}

void optix_destroy_gas(optix_gas_t gas) {
    if (!gas) return;
    auto* gw = static_cast<OptixGASWrapper*>(gas);
    if (gw->d_gas_output) cudaFree(reinterpret_cast<void*>(gw->d_gas_output));
    if (gw->d_vertices) cudaFree(reinterpret_cast<void*>(gw->d_vertices));
    if (gw->d_indices) cudaFree(reinterpret_cast<void*>(gw->d_indices));
    delete gw;
}

void optix_gas_get_device_ptrs(optix_gas_t gas,
    void** out_vertices, int* out_num_vertices,
    void** out_indices, int* out_num_indices) {
    if (!gas) {
        if (out_vertices) *out_vertices = nullptr;
        if (out_indices) *out_indices = nullptr;
        if (out_num_vertices) *out_num_vertices = 0;
        if (out_num_indices) *out_num_indices = 0;
        return;
    }
    auto* gw = static_cast<OptixGASWrapper*>(gas);
    if (out_vertices) *out_vertices = reinterpret_cast<void*>(gw->d_vertices);
    if (out_num_vertices) *out_num_vertices = gw->num_vertices;
    if (out_indices) *out_indices = reinterpret_cast<void*>(gw->d_indices);
    if (out_num_indices) *out_num_indices = gw->num_indices;
}

// ---------- Instance Acceleration Structure ----------

optix_ias_t optix_build_ias(optix_ctx_t ctx,
    optix_instance_t* instances, int num_instances,
    void* cuda_stream) {

    if (!ctx || !instances || num_instances <= 0) {
        set_error("optix_build_ias: invalid arguments");
        return nullptr;
    }

    auto* ctx_wrapper = static_cast<OptixContextWrapper*>(ctx);
    cudaStream_t stream = static_cast<cudaStream_t>(cuda_stream);

    // Convert our instance descriptors to OptixInstance structs.
    std::vector<OptixInstance> opt_instances(num_instances);
    for (int i = 0; i < num_instances; i++) {
        auto* gas_wrapper = static_cast<OptixGASWrapper*>(instances[i].gas);
        if (!gas_wrapper) {
            set_error("optix_build_ias: null GAS at index %d", i);
            return nullptr;
        }

        memset(&opt_instances[i], 0, sizeof(OptixInstance));
        memcpy(opt_instances[i].transform, instances[i].transform, sizeof(float) * 12);
        opt_instances[i].instanceId = static_cast<unsigned int>(i);
        opt_instances[i].sbtOffset = instances[i].sbt_offset;
        opt_instances[i].visibilityMask = instances[i].visibility_mask;
        opt_instances[i].flags = OPTIX_INSTANCE_FLAG_NONE;
        opt_instances[i].traversableHandle = gas_wrapper->handle;
    }

    // Upload instances to device.
    size_t instance_bytes = sizeof(OptixInstance) * num_instances;
    CUdeviceptr d_instances = 0;
    cudaMalloc(reinterpret_cast<void**>(&d_instances), instance_bytes);
    cudaMemcpyAsync(reinterpret_cast<void*>(d_instances), opt_instances.data(),
                    instance_bytes, cudaMemcpyHostToDevice, stream);

    // Build input.
    OptixBuildInput build_input = {};
    build_input.type = OPTIX_BUILD_INPUT_TYPE_INSTANCES;
    build_input.instanceArray.instances = d_instances;
    build_input.instanceArray.numInstances = static_cast<unsigned int>(num_instances);

    OptixAccelBuildOptions accel_options = {};
    accel_options.buildFlags = OPTIX_BUILD_FLAG_ALLOW_UPDATE | OPTIX_BUILD_FLAG_PREFER_FAST_TRACE;
    accel_options.operation = OPTIX_BUILD_OPERATION_BUILD;

    OptixAccelBufferSizes ias_sizes;
    OptixResult res = optixAccelComputeMemoryUsage(
        ctx_wrapper->context, &accel_options, &build_input, 1, &ias_sizes);
    if (res != OPTIX_SUCCESS) {
        set_error("optixAccelComputeMemoryUsage(IAS) failed: %d", (int)res);
        cudaFree(reinterpret_cast<void*>(d_instances));
        return nullptr;
    }

    CUdeviceptr d_temp = 0;
    cudaMalloc(reinterpret_cast<void**>(&d_temp), ias_sizes.tempSizeInBytes);

    CUdeviceptr d_output = 0;
    cudaMalloc(reinterpret_cast<void**>(&d_output), ias_sizes.outputSizeInBytes);

    OptixTraversableHandle handle = 0;
    res = optixAccelBuild(
        ctx_wrapper->context, stream,
        &accel_options, &build_input, 1,
        d_temp, ias_sizes.tempSizeInBytes,
        d_output, ias_sizes.outputSizeInBytes,
        &handle, nullptr, 0);

    if (res != OPTIX_SUCCESS) {
        set_error("optixAccelBuild(IAS) failed: %d", (int)res);
        cudaFree(reinterpret_cast<void*>(d_temp));
        cudaFree(reinterpret_cast<void*>(d_instances));
        cudaFree(reinterpret_cast<void*>(d_output));
        return nullptr;
    }

    auto* iw = new OptixIASWrapper();
    iw->handle = handle;
    iw->d_ias_output = d_output;
    iw->ias_output_size = ias_sizes.outputSizeInBytes;
    iw->d_instances = d_instances;
    iw->instances_size = instance_bytes;
    iw->d_temp = d_temp;
    iw->temp_size = ias_sizes.tempSizeInBytes;
    iw->context = ctx_wrapper->context;
    return static_cast<void*>(iw);
}

int optix_update_ias(optix_ias_t ias,
    optix_instance_t* instances, int num_instances,
    void* cuda_stream) {

    if (!ias || !instances || num_instances <= 0) {
        set_error("optix_update_ias: invalid arguments");
        return -1;
    }

    auto* iw = static_cast<OptixIASWrapper*>(ias);
    cudaStream_t stream = static_cast<cudaStream_t>(cuda_stream);

    // Convert instance descriptors.
    std::vector<OptixInstance> opt_instances(num_instances);
    for (int i = 0; i < num_instances; i++) {
        auto* gas_wrapper = static_cast<OptixGASWrapper*>(instances[i].gas);
        if (!gas_wrapper) {
            set_error("optix_update_ias: null GAS at index %d", i);
            return -1;
        }

        memset(&opt_instances[i], 0, sizeof(OptixInstance));
        memcpy(opt_instances[i].transform, instances[i].transform, sizeof(float) * 12);
        opt_instances[i].instanceId = static_cast<unsigned int>(i);
        opt_instances[i].sbtOffset = instances[i].sbt_offset;
        opt_instances[i].visibilityMask = instances[i].visibility_mask;
        opt_instances[i].flags = OPTIX_INSTANCE_FLAG_NONE;
        opt_instances[i].traversableHandle = gas_wrapper->handle;
    }

    // Re-upload instances.
    size_t instance_bytes = sizeof(OptixInstance) * num_instances;
    if (instance_bytes > iw->instances_size) {
        cudaFree(reinterpret_cast<void*>(iw->d_instances));
        cudaMalloc(reinterpret_cast<void**>(&iw->d_instances), instance_bytes);
        iw->instances_size = instance_bytes;
    }
    cudaMemcpyAsync(reinterpret_cast<void*>(iw->d_instances), opt_instances.data(),
                    instance_bytes, cudaMemcpyHostToDevice, stream);

    // Build input.
    OptixBuildInput build_input = {};
    build_input.type = OPTIX_BUILD_INPUT_TYPE_INSTANCES;
    build_input.instanceArray.instances = iw->d_instances;
    build_input.instanceArray.numInstances = static_cast<unsigned int>(num_instances);

    OptixAccelBuildOptions accel_options = {};
    accel_options.buildFlags = OPTIX_BUILD_FLAG_ALLOW_UPDATE | OPTIX_BUILD_FLAG_PREFER_FAST_TRACE;
    accel_options.operation = OPTIX_BUILD_OPERATION_UPDATE;

    OptixResult res = optixAccelBuild(
        iw->context, stream,
        &accel_options, &build_input, 1,
        iw->d_temp, iw->temp_size,
        iw->d_ias_output, iw->ias_output_size,
        &iw->handle, nullptr, 0);

    if (res != OPTIX_SUCCESS) {
        set_error("optixAccelBuild(IAS update) failed: %d", (int)res);
        return -1;
    }

    return 0;
}

void optix_destroy_ias(optix_ias_t ias) {
    if (!ias) return;
    auto* iw = static_cast<OptixIASWrapper*>(ias);
    if (iw->d_ias_output) cudaFree(reinterpret_cast<void*>(iw->d_ias_output));
    if (iw->d_instances) cudaFree(reinterpret_cast<void*>(iw->d_instances));
    if (iw->d_temp) cudaFree(reinterpret_cast<void*>(iw->d_temp));
    delete iw;
}

uint64_t optix_ias_get_handle(optix_ias_t ias) {
    if (!ias) return 0;
    auto* iw = static_cast<OptixIASWrapper*>(ias);
    return static_cast<uint64_t>(iw->handle);
}

// ---------- Ray launch ----------

int optix_launch(optix_pipeline_t pipeline,
    optix_launch_params_t* params,
    void* cuda_stream) {

    if (!pipeline || !params) {
        set_error("optix_launch: invalid arguments");
        return -1;
    }

    auto* pw = static_cast<OptixPipelineWrapper*>(pipeline);
    cudaStream_t stream = static_cast<cudaStream_t>(cuda_stream);

    // I3: Upload launch params to the persistent device buffer (no per-frame alloc/free).
    cudaMemcpyAsync(reinterpret_cast<void*>(pw->d_launch_params), params,
                    sizeof(optix_launch_params_t), cudaMemcpyHostToDevice, stream);

    OptixResult res = optixLaunch(
        pw->pipeline, stream,
        pw->d_launch_params, sizeof(optix_launch_params_t),
        &pw->sbt,
        static_cast<unsigned int>(params->width),
        static_cast<unsigned int>(params->height),
        1); // depth = 1 for 2D launch

    if (res != OPTIX_SUCCESS) {
        set_error("optixLaunch failed: %d", (int)res);
        return -1;
    }

    return 0;
}

// ---------- Denoiser ----------

optix_denoiser_t optix_create_denoiser(optix_ctx_t ctx,
    int width, int height, int temporal) {

    if (!ctx || width <= 0 || height <= 0) {
        set_error("optix_create_denoiser: invalid arguments");
        return nullptr;
    }

    auto* ctx_wrapper = static_cast<OptixContextWrapper*>(ctx);

    OptixDenoiserOptions options = {};
    options.guideAlbedo = 0;
    options.guideNormal = 0;
    // Temporal denoising uses motion vectors for temporal stability.
    options.denoiseAlpha = OPTIX_DENOISER_ALPHA_MODE_COPY;

    OptixDenoiserModelKind model = temporal
        ? OPTIX_DENOISER_MODEL_KIND_TEMPORAL
        : OPTIX_DENOISER_MODEL_KIND_HDR;

    OptixDenoiser denoiser = nullptr;
    OptixResult res = optixDenoiserCreate(
        ctx_wrapper->context, model, &options, &denoiser);
    if (res != OPTIX_SUCCESS) {
        set_error("optixDenoiserCreate failed: %d", (int)res);
        return nullptr;
    }

    // Compute memory requirements.
    OptixDenoiserSizes sizes;
    res = optixDenoiserComputeMemoryResources(denoiser, width, height, &sizes);
    if (res != OPTIX_SUCCESS) {
        set_error("optixDenoiserComputeMemoryResources failed: %d", (int)res);
        optixDenoiserDestroy(denoiser);
        return nullptr;
    }

    // Allocate state and scratch.
    CUdeviceptr d_state = 0;
    cudaMalloc(reinterpret_cast<void**>(&d_state), sizes.stateSizeInBytes);

    size_t scratch_size = sizes.withoutOverlapScratchSizeInBytes;
    CUdeviceptr d_scratch = 0;
    cudaMalloc(reinterpret_cast<void**>(&d_scratch), scratch_size);

    // Setup denoiser.
    res = optixDenoiserSetup(
        denoiser, nullptr,
        width, height,
        d_state, sizes.stateSizeInBytes,
        d_scratch, scratch_size);
    if (res != OPTIX_SUCCESS) {
        set_error("optixDenoiserSetup failed: %d", (int)res);
        cudaFree(reinterpret_cast<void*>(d_state));
        cudaFree(reinterpret_cast<void*>(d_scratch));
        optixDenoiserDestroy(denoiser);
        return nullptr;
    }

    auto* dw = new OptixDenoiserWrapper();
    dw->denoiser = denoiser;
    dw->d_state = d_state;
    dw->state_size = sizes.stateSizeInBytes;
    dw->d_scratch = d_scratch;
    dw->scratch_size = scratch_size;
    dw->width = width;
    dw->height = height;
    dw->temporal = temporal;
    dw->context = ctx_wrapper->context;
    return static_cast<void*>(dw);
}

int optix_denoise(optix_denoiser_t denoiser,
    void* noisy_input, void* clean_output,
    void* flow_buffer,
    int width, int height,
    void* cuda_stream) {

    if (!denoiser || !noisy_input || !clean_output) {
        set_error("optix_denoise: invalid arguments");
        return -1;
    }

    auto* dw = static_cast<OptixDenoiserWrapper*>(denoiser);
    cudaStream_t stream = static_cast<cudaStream_t>(cuda_stream);

    // Setup input/output image descriptors.
    OptixImage2D input_image;
    input_image.data = reinterpret_cast<CUdeviceptr>(noisy_input);
    input_image.width = static_cast<unsigned int>(width);
    input_image.height = static_cast<unsigned int>(height);
    input_image.rowStrideInBytes = width * 4 * sizeof(float);
    input_image.pixelStrideInBytes = 4 * sizeof(float);
    input_image.format = OPTIX_PIXEL_FORMAT_FLOAT4;

    OptixImage2D output_image;
    output_image.data = reinterpret_cast<CUdeviceptr>(clean_output);
    output_image.width = static_cast<unsigned int>(width);
    output_image.height = static_cast<unsigned int>(height);
    output_image.rowStrideInBytes = width * 4 * sizeof(float);
    output_image.pixelStrideInBytes = 4 * sizeof(float);
    output_image.format = OPTIX_PIXEL_FORMAT_FLOAT4;

    // Flow image for temporal denoising.
    OptixImage2D flow_image = {};
    if (dw->temporal && flow_buffer) {
        flow_image.data = reinterpret_cast<CUdeviceptr>(flow_buffer);
        flow_image.width = static_cast<unsigned int>(width);
        flow_image.height = static_cast<unsigned int>(height);
        flow_image.rowStrideInBytes = width * 2 * sizeof(float);
        flow_image.pixelStrideInBytes = 2 * sizeof(float);
        flow_image.format = OPTIX_PIXEL_FORMAT_FLOAT2;
    }

    OptixDenoiserGuideLayer guide = {};
    if (dw->temporal && flow_buffer) {
        guide.flow = flow_image;
    }

    OptixDenoiserLayer layer = {};
    layer.input = input_image;
    layer.output = output_image;

    OptixDenoiserParams params = {};
    params.blendFactor = 0.0f;

    OptixResult res = optixDenoiserInvoke(
        dw->denoiser, stream,
        &params,
        dw->d_state, dw->state_size,
        &guide, &layer, 1,
        0, 0, // input offset x, y
        dw->d_scratch, dw->scratch_size);

    if (res != OPTIX_SUCCESS) {
        set_error("optixDenoiserInvoke failed: %d", (int)res);
        return -1;
    }

    return 0;
}

void optix_destroy_denoiser(optix_denoiser_t denoiser) {
    if (!denoiser) return;
    auto* dw = static_cast<OptixDenoiserWrapper*>(denoiser);
    if (dw->d_state) cudaFree(reinterpret_cast<void*>(dw->d_state));
    if (dw->d_scratch) cudaFree(reinterpret_cast<void*>(dw->d_scratch));
    if (dw->denoiser) optixDenoiserDestroy(dw->denoiser);
    delete dw;
}

// ---------- Error handling ----------

const char* optix_get_last_error(void) {
    return optix_error_buf;
}

} // extern "C"

#else // !HAS_OPTIX — stub implementations

extern "C" {

optix_ctx_t optix_create_context(void* /*cuda_context*/) {
    set_error("OptiX SDK not installed");
    return nullptr;
}

void optix_destroy_context(optix_ctx_t /*ctx*/) {}

optix_module_t optix_create_module(optix_ctx_t /*ctx*/, const char* /*ptx_source*/, int /*ptx_size*/) {
    set_error("OptiX SDK not installed");
    return nullptr;
}

void optix_destroy_module(optix_module_t /*mod*/) {}

optix_pipeline_t optix_create_pipeline(optix_ctx_t /*ctx*/, optix_pipeline_config_t* /*config*/) {
    set_error("OptiX SDK not installed");
    return nullptr;
}

void optix_destroy_pipeline(optix_pipeline_t /*pipeline*/) {}

int optix_set_hitgroup_data(optix_pipeline_t /*pipeline*/,
    int /*instance_index*/,
    optix_hitgroup_data_t* /*data*/,
    void* /*cuda_stream*/) {
    set_error("OptiX SDK not installed");
    return -1;
}

optix_gas_t optix_build_triangle_gas(optix_ctx_t /*ctx*/,
    const float* /*vertices*/, int /*num_vertices*/,
    const uint32_t* /*indices*/, int /*num_indices*/,
    void* /*cuda_stream*/) {
    set_error("OptiX SDK not installed");
    return nullptr;
}

void optix_destroy_gas(optix_gas_t /*gas*/) {}

void optix_gas_get_device_ptrs(optix_gas_t /*gas*/,
    void** out_vertices, int* out_num_vertices,
    void** out_indices, int* out_num_indices) {
    if (out_vertices) *out_vertices = nullptr;
    if (out_indices) *out_indices = nullptr;
    if (out_num_vertices) *out_num_vertices = 0;
    if (out_num_indices) *out_num_indices = 0;
}

optix_ias_t optix_build_ias(optix_ctx_t /*ctx*/,
    optix_instance_t* /*instances*/, int /*num_instances*/,
    void* /*cuda_stream*/) {
    set_error("OptiX SDK not installed");
    return nullptr;
}

int optix_update_ias(optix_ias_t /*ias*/,
    optix_instance_t* /*instances*/, int /*num_instances*/,
    void* /*cuda_stream*/) {
    set_error("OptiX SDK not installed");
    return -1;
}

void optix_destroy_ias(optix_ias_t /*ias*/) {}

uint64_t optix_ias_get_handle(optix_ias_t /*ias*/) { return 0; }

int optix_launch(optix_pipeline_t /*pipeline*/,
    optix_launch_params_t* /*params*/,
    void* /*cuda_stream*/) {
    set_error("OptiX SDK not installed");
    return -1;
}

optix_denoiser_t optix_create_denoiser(optix_ctx_t /*ctx*/,
    int /*width*/, int /*height*/, int /*temporal*/) {
    set_error("OptiX SDK not installed");
    return nullptr;
}

int optix_denoise(optix_denoiser_t /*denoiser*/,
    void* /*noisy_input*/, void* /*clean_output*/,
    void* /*flow_buffer*/,
    int /*width*/, int /*height*/,
    void* /*cuda_stream*/) {
    set_error("OptiX SDK not installed");
    return -1;
}

void optix_destroy_denoiser(optix_denoiser_t /*denoiser*/) {}

const char* optix_get_last_error(void) {
    return optix_error_buf;
}

} // extern "C"

#endif // HAS_OPTIX
