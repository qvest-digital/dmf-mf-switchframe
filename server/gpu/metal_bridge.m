#import <Metal/Metal.h>
#import <Foundation/Foundation.h>
#include "metal_bridge.h"
#include <string.h>

// ============================================================================
// Internal helpers
// ============================================================================

// Standard thread group size matching CUDA block dimensions
static MTLSize defaultGroupSize(void) {
    return MTLSizeMake(32, 8, 1);
}

// Compute grid size for a 2D dispatch
static MTLSize gridSize2D(uint32_t width, uint32_t height) {
    MTLSize gs = defaultGroupSize();
    return MTLSizeMake(
        (width  + gs.width  - 1) / gs.width,
        (height + gs.height - 1) / gs.height,
        1
    );
}

// Dispatch a compute kernel with params buffer and wait for completion
static MetalResult dispatch_2d(id<MTLCommandQueue> queue,
                               id<MTLComputePipelineState> pipeline,
                               id<MTLBuffer> buffers[], int nbuf,
                               const void* params, size_t paramsSize,
                               int paramsIndex,
                               uint32_t gridW, uint32_t gridH) {
    id<MTLCommandBuffer> cmdBuf = [queue commandBuffer];
    if (!cmdBuf) return METAL_ERROR_COMMIT;

    id<MTLComputeCommandEncoder> encoder = [cmdBuf computeCommandEncoder];
    if (!encoder) return METAL_ERROR_ENCODE;

    [encoder setComputePipelineState:pipeline];

    for (int i = 0; i < nbuf; i++) {
        [encoder setBuffer:buffers[i] offset:0 atIndex:i];
    }
    [encoder setBytes:params length:paramsSize atIndex:paramsIndex];

    MTLSize groupSize = defaultGroupSize();
    MTLSize threadgroups = gridSize2D(gridW, gridH);
    [encoder dispatchThreadgroups:threadgroups threadsPerThreadgroup:groupSize];
    [encoder endEncoding];

    [cmdBuf commit];
    [cmdBuf waitUntilCompleted];

    if (cmdBuf.error) return METAL_ERROR_COMMIT;
    return METAL_SUCCESS;
}

// ============================================================================
// Device lifecycle
// ============================================================================

MetalResult metal_init(MetalDeviceRef* device, MetalQueueRef* queue, MetalLibraryRef* library,
                       const char* metallib_path) {
    id<MTLDevice> dev = MTLCreateSystemDefaultDevice();
    if (!dev) return METAL_ERROR_NO_DEVICE;

    id<MTLCommandQueue> q = [dev newCommandQueue];
    if (!q) return METAL_ERROR_NO_QUEUE;

    NSError* error = nil;
    NSString* path = [NSString stringWithUTF8String:metallib_path];
    NSURL* url = [NSURL fileURLWithPath:path];
    id<MTLLibrary> lib = [dev newLibraryWithURL:url error:&error];
    if (!lib) {
        NSLog(@"metal_init: failed to load metallib at %@: %@", path, error);
        return METAL_ERROR_NO_LIBRARY;
    }

    *device  = (void*)dev;
    *queue   = (void*)q;
    *library = (void*)lib;
    return METAL_SUCCESS;
}

void metal_release(MetalDeviceRef device, MetalQueueRef queue, MetalLibraryRef library) {
    if (library) CFRelease(library);
    if (queue)   CFRelease(queue);
    if (device)  CFRelease(device);
}

const char* metal_device_name(MetalDeviceRef device) {
    id<MTLDevice> dev = (id<MTLDevice>)device;
    // Returns a pointer into the NSString's UTF8 buffer — valid for the device's lifetime.
    return [dev.name UTF8String];
}

uint64_t metal_device_memory(MetalDeviceRef device) {
    // Apple Silicon has unified memory; report recommended working set size.
    id<MTLDevice> dev = (id<MTLDevice>)device;
    return dev.recommendedMaxWorkingSetSize;
}

// ============================================================================
// Buffer management
// ============================================================================

MetalBufferRef metal_buffer_alloc(MetalDeviceRef device, size_t size) {
    id<MTLDevice> dev = (id<MTLDevice>)device;
    // MTLResourceStorageModeShared = unified memory (CPU + GPU see same data)
    id<MTLBuffer> buf = [dev newBufferWithLength:size options:MTLResourceStorageModeShared];
    if (!buf) return NULL;
    return (void*)buf;
}

MetalBufferRef metal_buffer_alloc_aligned(MetalDeviceRef device, size_t size, size_t alignment) {
    // Metal buffers are already page-aligned; we just round up the size.
    size_t aligned = (size + alignment - 1) & ~(alignment - 1);
    return metal_buffer_alloc(device, aligned);
}

void metal_buffer_free(MetalBufferRef buffer) {
    if (buffer) CFRelease(buffer);
}

void* metal_buffer_contents(MetalBufferRef buffer) {
    id<MTLBuffer> buf = (id<MTLBuffer>)buffer;
    return [buf contents];
}

size_t metal_buffer_length(MetalBufferRef buffer) {
    id<MTLBuffer> buf = (id<MTLBuffer>)buffer;
    return [buf length];
}

// ============================================================================
// Pipeline state
// ============================================================================

MetalPipelineRef metal_pipeline_create(MetalDeviceRef device, MetalLibraryRef library,
                                       const char* function_name) {
    id<MTLDevice> dev = (id<MTLDevice>)device;
    id<MTLLibrary> lib = (id<MTLLibrary>)library;

    NSString* name = [NSString stringWithUTF8String:function_name];
    id<MTLFunction> func = [lib newFunctionWithName:name];
    if (!func) return NULL;

    NSError* error = nil;
    id<MTLComputePipelineState> pso = [dev newComputePipelineStateWithFunction:func error:&error];
    if (!pso) {
        NSLog(@"metal_pipeline_create: %@ failed: %@", name, error);
        return NULL;
    }

    return (void*)pso;
}

void metal_pipeline_free(MetalPipelineRef pipeline) {
    if (pipeline) CFRelease(pipeline);
}

// ============================================================================
// Synchronization
// ============================================================================

MetalResult metal_sync(MetalQueueRef queue) {
    id<MTLCommandQueue> q = (id<MTLCommandQueue>)queue;
    id<MTLCommandBuffer> cmdBuf = [q commandBuffer];
    if (!cmdBuf) return METAL_ERROR_COMMIT;
    [cmdBuf commit];
    [cmdBuf waitUntilCompleted];
    return METAL_SUCCESS;
}

// ============================================================================
// Convert kernels
// ============================================================================

MetalResult metal_yuv420p_to_nv12(MetalQueueRef queue, MetalPipelineRef pipeline,
    MetalBufferRef y_src, MetalBufferRef cb_src, MetalBufferRef cr_src,
    MetalBufferRef nv12, const MetalConvertParams* params) {
    id<MTLBuffer> bufs[] = {
        (id<MTLBuffer>)y_src,
        (id<MTLBuffer>)cb_src,
        (id<MTLBuffer>)cr_src,
        (id<MTLBuffer>)nv12,
    };
    return dispatch_2d((id<MTLCommandQueue>)queue,
                       (id<MTLComputePipelineState>)pipeline,
                       bufs, 4, params, sizeof(*params), 4,
                       params->width, params->height);
}

MetalResult metal_nv12_to_yuv420p(MetalQueueRef queue, MetalPipelineRef pipeline,
    MetalBufferRef y_dst, MetalBufferRef cb_dst, MetalBufferRef cr_dst,
    MetalBufferRef nv12, const MetalConvertParams* params) {
    id<MTLBuffer> bufs[] = {
        (id<MTLBuffer>)y_dst,
        (id<MTLBuffer>)cb_dst,
        (id<MTLBuffer>)cr_dst,
        (id<MTLBuffer>)nv12,
    };
    return dispatch_2d((id<MTLCommandQueue>)queue,
                       (id<MTLComputePipelineState>)pipeline,
                       bufs, 4, params, sizeof(*params), 4,
                       params->width, params->height);
}

MetalResult metal_nv12_fill(MetalQueueRef queue, MetalPipelineRef pipeline,
    MetalBufferRef nv12, const MetalFillParams* params) {
    id<MTLBuffer> bufs[] = { (id<MTLBuffer>)nv12 };
    return dispatch_2d((id<MTLCommandQueue>)queue,
                       (id<MTLComputePipelineState>)pipeline,
                       bufs, 1, params, sizeof(*params), 1,
                       params->width, params->height);
}

// ============================================================================
// Blend kernels
// ============================================================================

MetalResult metal_blend_uniform(MetalQueueRef queue, MetalPipelineRef pipeline,
    MetalBufferRef dst, MetalBufferRef a, MetalBufferRef b,
    const MetalBlendUniformParams* params) {
    id<MTLBuffer> bufs[] = {
        (id<MTLBuffer>)dst,
        (id<MTLBuffer>)a,
        (id<MTLBuffer>)b,
    };
    return dispatch_2d((id<MTLCommandQueue>)queue,
                       (id<MTLComputePipelineState>)pipeline,
                       bufs, 3, params, sizeof(*params), 3,
                       params->width, params->height);
}

MetalResult metal_blend_fade_const(MetalQueueRef queue, MetalPipelineRef pipeline,
    MetalBufferRef dst, MetalBufferRef src,
    const MetalBlendFadeConstParams* params) {
    id<MTLBuffer> bufs[] = {
        (id<MTLBuffer>)dst,
        (id<MTLBuffer>)src,
    };
    return dispatch_2d((id<MTLCommandQueue>)queue,
                       (id<MTLComputePipelineState>)pipeline,
                       bufs, 2, params, sizeof(*params), 2,
                       params->width, params->height);
}

MetalResult metal_blend_alpha(MetalQueueRef queue, MetalPipelineRef pipeline,
    MetalBufferRef dst, MetalBufferRef a, MetalBufferRef b, MetalBufferRef alpha,
    const MetalBlendAlphaParams* params) {
    id<MTLBuffer> bufs[] = {
        (id<MTLBuffer>)dst,
        (id<MTLBuffer>)a,
        (id<MTLBuffer>)b,
        (id<MTLBuffer>)alpha,
    };
    return dispatch_2d((id<MTLCommandQueue>)queue,
                       (id<MTLComputePipelineState>)pipeline,
                       bufs, 4, params, sizeof(*params), 4,
                       params->width, params->height);
}

MetalResult metal_wipe_mask_generate(MetalQueueRef queue, MetalPipelineRef pipeline,
    MetalBufferRef mask, const MetalWipeMaskParams* params) {
    id<MTLBuffer> bufs[] = { (id<MTLBuffer>)mask };
    return dispatch_2d((id<MTLCommandQueue>)queue,
                       (id<MTLComputePipelineState>)pipeline,
                       bufs, 1, params, sizeof(*params), 1,
                       params->width, params->height);
}

MetalResult metal_downsample_alpha_to_nv12_uv(MetalQueueRef queue, MetalPipelineRef pipeline,
    MetalBufferRef dst, MetalBufferRef src,
    const MetalDownsampleAlphaParams* params) {
    id<MTLBuffer> bufs[] = {
        (id<MTLBuffer>)dst,
        (id<MTLBuffer>)src,
    };
    return dispatch_2d((id<MTLCommandQueue>)queue,
                       (id<MTLComputePipelineState>)pipeline,
                       bufs, 2, params, sizeof(*params), 2,
                       params->chromaW, params->chromaH);
}

// ============================================================================
// Scale kernels
// ============================================================================

MetalResult metal_scale_bilinear(MetalQueueRef queue, MetalPipelineRef pipeline,
    MetalBufferRef dst, MetalBufferRef src, const MetalScaleParams* params) {
    id<MTLBuffer> bufs[] = {
        (id<MTLBuffer>)dst,
        (id<MTLBuffer>)src,
    };
    return dispatch_2d((id<MTLCommandQueue>)queue,
                       (id<MTLComputePipelineState>)pipeline,
                       bufs, 2, params, sizeof(*params), 2,
                       params->dstW, params->dstH);
}

// ============================================================================
// Key kernels
// ============================================================================

MetalResult metal_chroma_key(MetalQueueRef queue, MetalPipelineRef pipeline,
    MetalBufferRef nv12, MetalBufferRef mask, const MetalChromaKeyParams* params) {
    id<MTLBuffer> bufs[] = {
        (id<MTLBuffer>)nv12,
        (id<MTLBuffer>)mask,
    };
    return dispatch_2d((id<MTLCommandQueue>)queue,
                       (id<MTLComputePipelineState>)pipeline,
                       bufs, 2, params, sizeof(*params), 2,
                       params->width, params->height);
}

MetalResult metal_luma_key(MetalQueueRef queue, MetalPipelineRef pipeline,
    MetalBufferRef nv12, MetalBufferRef mask, MetalBufferRef lut,
    const MetalLumaKeyParams* params) {
    id<MTLBuffer> bufs[] = {
        (id<MTLBuffer>)nv12,
        (id<MTLBuffer>)mask,
        (id<MTLBuffer>)lut,
    };
    return dispatch_2d((id<MTLCommandQueue>)queue,
                       (id<MTLComputePipelineState>)pipeline,
                       bufs, 3, params, sizeof(*params), 3,
                       params->width, params->height);
}

// ============================================================================
// Composite kernels
// ============================================================================

MetalResult metal_pip_composite_y(MetalQueueRef queue, MetalPipelineRef pipeline,
    MetalBufferRef dst, MetalBufferRef src, const MetalPIPCompositeYParams* params) {
    id<MTLBuffer> bufs[] = {
        (id<MTLBuffer>)dst,
        (id<MTLBuffer>)src,
    };
    return dispatch_2d((id<MTLCommandQueue>)queue,
                       (id<MTLComputePipelineState>)pipeline,
                       bufs, 2, params, sizeof(*params), 2,
                       (uint32_t)params->rectW, (uint32_t)params->rectH);
}

MetalResult metal_pip_composite_uv(MetalQueueRef queue, MetalPipelineRef pipeline,
    MetalBufferRef dstUV, MetalBufferRef srcUV, const MetalPIPCompositeUVParams* params) {
    id<MTLBuffer> bufs[] = {
        (id<MTLBuffer>)dstUV,
        (id<MTLBuffer>)srcUV,
    };
    return dispatch_2d((id<MTLCommandQueue>)queue,
                       (id<MTLComputePipelineState>)pipeline,
                       bufs, 2, params, sizeof(*params), 2,
                       (uint32_t)params->rectCW, (uint32_t)params->rectCH);
}

// ============================================================================
// DSK kernels
// ============================================================================

MetalResult metal_dsk_overlay_full(MetalQueueRef queue, MetalPipelineRef pipeline,
    MetalBufferRef nv12, MetalBufferRef rgba, const MetalDSKFullFrameParams* params) {
    id<MTLBuffer> bufs[] = {
        (id<MTLBuffer>)nv12,
        (id<MTLBuffer>)rgba,
    };
    return dispatch_2d((id<MTLCommandQueue>)queue,
                       (id<MTLComputePipelineState>)pipeline,
                       bufs, 2, params, sizeof(*params), 2,
                       params->width, params->height);
}

MetalResult metal_dsk_overlay_rect(MetalQueueRef queue, MetalPipelineRef pipeline,
    MetalBufferRef nv12, MetalBufferRef rgba, const MetalDSKRectParams* params) {
    id<MTLBuffer> bufs[] = {
        (id<MTLBuffer>)nv12,
        (id<MTLBuffer>)rgba,
    };
    return dispatch_2d((id<MTLCommandQueue>)queue,
                       (id<MTLComputePipelineState>)pipeline,
                       bufs, 2, params, sizeof(*params), 2,
                       (uint32_t)params->rectW, (uint32_t)params->rectH);
}

// ============================================================================
// STMap kernels
// ============================================================================

MetalResult metal_stmap_warp_y(MetalQueueRef queue, MetalPipelineRef pipeline,
    MetalBufferRef src, MetalBufferRef stmapS, MetalBufferRef stmapT,
    MetalBufferRef dst, const MetalSTMapParams* params) {
    id<MTLBuffer> bufs[] = {
        (id<MTLBuffer>)src,
        (id<MTLBuffer>)stmapS,
        (id<MTLBuffer>)stmapT,
        (id<MTLBuffer>)dst,
    };
    return dispatch_2d((id<MTLCommandQueue>)queue,
                       (id<MTLComputePipelineState>)pipeline,
                       bufs, 4, params, sizeof(*params), 4,
                       params->width, params->height);
}

MetalResult metal_stmap_warp_uv(MetalQueueRef queue, MetalPipelineRef pipeline,
    MetalBufferRef srcUV, MetalBufferRef stmapS, MetalBufferRef stmapT,
    MetalBufferRef dstUV, const MetalSTMapUVParams* params) {
    id<MTLBuffer> bufs[] = {
        (id<MTLBuffer>)srcUV,
        (id<MTLBuffer>)stmapS,
        (id<MTLBuffer>)stmapT,
        (id<MTLBuffer>)dstUV,
    };
    return dispatch_2d((id<MTLCommandQueue>)queue,
                       (id<MTLComputePipelineState>)pipeline,
                       bufs, 4, params, sizeof(*params), 4,
                       params->chromaW, params->chromaH);
}

// ============================================================================
// FRUC kernels
// ============================================================================

MetalResult metal_fruc_blend(MetalQueueRef queue, MetalPipelineRef pipeline,
    MetalBufferRef dst, MetalBufferRef prev, MetalBufferRef curr,
    const MetalFRUCBlendParams* params) {
    id<MTLBuffer> bufs[] = {
        (id<MTLBuffer>)dst,
        (id<MTLBuffer>)prev,
        (id<MTLBuffer>)curr,
    };
    return dispatch_2d((id<MTLCommandQueue>)queue,
                       (id<MTLComputePipelineState>)pipeline,
                       bufs, 3, params, sizeof(*params), 3,
                       params->width, params->height);
}

// ============================================================================
// V210 kernels
// ============================================================================

MetalResult metal_v210_to_nv12(MetalQueueRef queue, MetalPipelineRef pipeline,
    MetalBufferRef nv12, MetalBufferRef v210, const MetalV210Params* params) {
    id<MTLBuffer> bufs[] = {
        (id<MTLBuffer>)nv12,
        (id<MTLBuffer>)v210,
    };
    uint32_t groupsPerRow = params->width / 6;
    return dispatch_2d((id<MTLCommandQueue>)queue,
                       (id<MTLComputePipelineState>)pipeline,
                       bufs, 2, params, sizeof(*params), 2,
                       groupsPerRow, params->height);
}

MetalResult metal_nv12_to_v210(MetalQueueRef queue, MetalPipelineRef pipeline,
    MetalBufferRef v210, MetalBufferRef nv12, const MetalV210Params* params) {
    id<MTLBuffer> bufs[] = {
        (id<MTLBuffer>)v210,
        (id<MTLBuffer>)nv12,
    };
    uint32_t groupsPerRow = params->width / 6;
    return dispatch_2d((id<MTLCommandQueue>)queue,
                       (id<MTLComputePipelineState>)pipeline,
                       bufs, 2, params, sizeof(*params), 2,
                       groupsPerRow, params->height);
}

// ============================================================================
// Border / FillRect kernels
// ============================================================================

MetalResult metal_draw_border(MetalQueueRef queue, MetalPipelineRef pipeline,
    MetalBufferRef dst, const MetalBorderParams* params) {
    id<MTLBuffer> bufs[] = { (id<MTLBuffer>)dst };
    uint32_t outerW = (uint32_t)(params->rectW + params->thickness * 2);
    uint32_t outerH = (uint32_t)(params->rectH + params->thickness * 2);
    return dispatch_2d((id<MTLCommandQueue>)queue,
                       (id<MTLComputePipelineState>)pipeline,
                       bufs, 1, params, sizeof(*params), 1,
                       outerW, outerH);
}

MetalResult metal_fill_rect(MetalQueueRef queue, MetalPipelineRef pipeline,
    MetalBufferRef dst, const MetalFillRectParams* params) {
    id<MTLBuffer> bufs[] = { (id<MTLBuffer>)dst };
    return dispatch_2d((id<MTLCommandQueue>)queue,
                       (id<MTLComputePipelineState>)pipeline,
                       bufs, 1, params, sizeof(*params), 1,
                       (uint32_t)params->rectW, (uint32_t)params->rectH);
}
