#import <Metal/Metal.h>
#import <Foundation/Foundation.h>
#include "metal_bridge.h"
#include <string.h>

// ============================================================================
// Internal helpers
// ============================================================================

// Standard thread group size matching CUDA block dimensions.
// 32x8 = 256 threads per group, optimal for Apple Silicon GPU.
static MTLSize defaultGroupSize(void) {
    return MTLSizeMake(32, 8, 1);
}

// NOTE: We use dispatchThreadgroups (not dispatchThreads) because it works on
// all Metal GPUs including older Intel/AMD Macs and Apple Silicon. Each kernel
// includes bounds checks (if gid.x >= width) to handle non-aligned grids.
// dispatchThreads requires Metal GPU Family Apple 4+.

// Compute grid size for a 2D dispatch
static MTLSize gridSize2D(uint32_t width, uint32_t height) {
    MTLSize gs = defaultGroupSize();
    return MTLSizeMake(
        (width  + gs.width  - 1) / gs.width,
        (height + gs.height - 1) / gs.height,
        1
    );
}

// Dispatch a compute kernel with params buffer and wait for completion.
// Wrapped in @autoreleasepool for safety on cgo threads.
static MetalResult dispatch_2d(id<MTLCommandQueue> queue,
                               id<MTLComputePipelineState> pipeline,
                               id<MTLBuffer> buffers[], int nbuf,
                               const void* params, size_t paramsSize,
                               int paramsIndex,
                               uint32_t gridW, uint32_t gridH) {
    @autoreleasepool {
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
}

// Dispatch a compute kernel with per-buffer offsets and wait for completion.
// Each buffer is bound at the corresponding offset (in bytes).
static MetalResult dispatch_2d_offset(id<MTLCommandQueue> queue,
                                       id<MTLComputePipelineState> pipeline,
                                       id<MTLBuffer> buffers[], int64_t offsets[], int nbuf,
                                       const void* params, size_t paramsSize,
                                       int paramsIndex,
                                       uint32_t gridW, uint32_t gridH) {
    @autoreleasepool {
        id<MTLCommandBuffer> cmdBuf = [queue commandBuffer];
        if (!cmdBuf) return METAL_ERROR_COMMIT;

        id<MTLComputeCommandEncoder> encoder = [cmdBuf computeCommandEncoder];
        if (!encoder) return METAL_ERROR_ENCODE;

        [encoder setComputePipelineState:pipeline];

        for (int i = 0; i < nbuf; i++) {
            [encoder setBuffer:buffers[i] offset:(NSUInteger)offsets[i] atIndex:i];
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
}

// Public C wrapper for dispatch_2d_offset (used from Go via cgo).
MetalResult metal_dispatch_2d_offset(MetalQueueRef queue, MetalPipelineRef pipeline,
    MetalBufferRef buffers[], int64_t offsets[], int nbuf,
    const void* params, size_t paramsSize, int paramsIndex,
    uint32_t gridW, uint32_t gridH) {
    return dispatch_2d_offset((id<MTLCommandQueue>)queue,
                              (id<MTLComputePipelineState>)pipeline,
                              (id<MTLBuffer>*)buffers, offsets, nbuf,
                              params, paramsSize, paramsIndex,
                              gridW, gridH);
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

MetalQueueRef metal_create_queue(MetalDeviceRef device) {
    id<MTLDevice> dev = (id<MTLDevice>)device;
    id<MTLCommandQueue> q = [dev newCommandQueue];
    return (MetalQueueRef)q;
}

void metal_queue_release(MetalQueueRef queue) {
    if (queue) CFRelease(queue);
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

MetalResult metal_scale_lanczos3_h(MetalQueueRef queue, MetalPipelineRef pipeline,
    MetalBufferRef tmpBuf, MetalBufferRef src, const MetalLanczos3HParams* params) {
    id<MTLBuffer> bufs[] = {
        (id<MTLBuffer>)tmpBuf,
        (id<MTLBuffer>)src,
    };
    return dispatch_2d((id<MTLCommandQueue>)queue,
                       (id<MTLComputePipelineState>)pipeline,
                       bufs, 2, params, sizeof(*params), 2,
                       params->dstW, params->srcH);
}

MetalResult metal_scale_lanczos3_v(MetalQueueRef queue, MetalPipelineRef pipeline,
    MetalBufferRef dst, MetalBufferRef tmpBuf, const MetalLanczos3VParams* params) {
    id<MTLBuffer> bufs[] = {
        (id<MTLBuffer>)dst,
        (id<MTLBuffer>)tmpBuf,
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
    return dispatch_2d((id<MTLCommandQueue>)queue,
                       (id<MTLComputePipelineState>)pipeline,
                       bufs, 1, params, sizeof(*params), 1,
                       (uint32_t)params->outerW, (uint32_t)params->outerH);
}

MetalResult metal_fill_rect(MetalQueueRef queue, MetalPipelineRef pipeline,
    MetalBufferRef dst, const MetalFillRectParams* params) {
    id<MTLBuffer> bufs[] = { (id<MTLBuffer>)dst };
    return dispatch_2d((id<MTLCommandQueue>)queue,
                       (id<MTLComputePipelineState>)pipeline,
                       bufs, 1, params, sizeof(*params), 1,
                       (uint32_t)params->rectW, (uint32_t)params->rectH);
}

// ============================================================================
// P210 (10-bit 4:2:2) blend kernels
// ============================================================================

MetalResult metal_blend_uniform_p210(MetalQueueRef queue, MetalPipelineRef pipeline,
    MetalBufferRef dst, MetalBufferRef a, MetalBufferRef b,
    const MetalBlendUniformP210Params* params) {
    id<MTLBuffer> bufs[] = {
        (id<MTLBuffer>)dst,
        (id<MTLBuffer>)a,
        (id<MTLBuffer>)b,
    };
    // Grid: samples per row x total height (Y + UV planes)
    uint32_t samplesPerRow = params->pitch / 2;
    return dispatch_2d((id<MTLCommandQueue>)queue,
                       (id<MTLComputePipelineState>)pipeline,
                       bufs, 3, params, sizeof(*params), 3,
                       samplesPerRow, params->height);
}

MetalResult metal_blend_fade_const_p210(MetalQueueRef queue, MetalPipelineRef pipeline,
    MetalBufferRef dst, MetalBufferRef src,
    const MetalBlendFadeConstP210Params* params) {
    id<MTLBuffer> bufs[] = {
        (id<MTLBuffer>)dst,
        (id<MTLBuffer>)src,
    };
    uint32_t samplesPerRow = params->pitch / 2;
    return dispatch_2d((id<MTLCommandQueue>)queue,
                       (id<MTLComputePipelineState>)pipeline,
                       bufs, 2, params, sizeof(*params), 2,
                       samplesPerRow, params->height);
}

// ============================================================================
// P210 scale kernels
// ============================================================================

MetalResult metal_scale_bilinear_p210_y(MetalQueueRef queue, MetalPipelineRef pipeline,
    MetalBufferRef dst, MetalBufferRef src, const MetalScaleP210Params* params) {
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
// P210→NV12 cross-format scale kernels
// ============================================================================

MetalResult metal_scale_p210_to_nv12_y(MetalQueueRef queue, MetalPipelineRef pipeline,
    MetalBufferRef dst, MetalBufferRef src, const MetalScaleP210ToNV12Params* params) {
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
// P210 composite kernels
// ============================================================================

MetalResult metal_pip_composite_p210_y(MetalQueueRef queue, MetalPipelineRef pipeline,
    MetalBufferRef dst, MetalBufferRef src, const MetalPIPCompositeP210YParams* params) {
    id<MTLBuffer> bufs[] = {
        (id<MTLBuffer>)dst,
        (id<MTLBuffer>)src,
    };
    return dispatch_2d((id<MTLCommandQueue>)queue,
                       (id<MTLComputePipelineState>)pipeline,
                       bufs, 2, params, sizeof(*params), 2,
                       (uint32_t)params->rectW, (uint32_t)params->rectH);
}

MetalResult metal_pip_composite_p210_uv(MetalQueueRef queue, MetalPipelineRef pipeline,
    MetalBufferRef dstUV, MetalBufferRef srcUV, const MetalPIPCompositeP210UVParams* params) {
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
// P210 V210 lossless conversion kernels
// ============================================================================

MetalResult metal_v210_to_p210(MetalQueueRef queue, MetalPipelineRef pipeline,
    MetalBufferRef p210, MetalBufferRef v210, const MetalV210P210Params* params) {
    id<MTLBuffer> bufs[] = {
        (id<MTLBuffer>)p210,
        (id<MTLBuffer>)v210,
    };
    uint32_t groupsPerRow = params->width / 6;
    return dispatch_2d((id<MTLCommandQueue>)queue,
                       (id<MTLComputePipelineState>)pipeline,
                       bufs, 2, params, sizeof(*params), 2,
                       groupsPerRow, params->height);
}

MetalResult metal_p210_to_v210(MetalQueueRef queue, MetalPipelineRef pipeline,
    MetalBufferRef v210, MetalBufferRef p210, const MetalV210P210Params* params) {
    id<MTLBuffer> bufs[] = {
        (id<MTLBuffer>)v210,
        (id<MTLBuffer>)p210,
    };
    uint32_t groupsPerRow = params->width / 6;
    return dispatch_2d((id<MTLCommandQueue>)queue,
                       (id<MTLComputePipelineState>)pipeline,
                       bufs, 2, params, sizeof(*params), 2,
                       groupsPerRow, params->height);
}

// ============================================================================
// P210 DSK overlay kernels
// ============================================================================

MetalResult metal_dsk_overlay_p210(MetalQueueRef queue, MetalPipelineRef pipeline,
    MetalBufferRef p210, MetalBufferRef rgba, const MetalDSKP210FullFrameParams* params) {
    id<MTLBuffer> bufs[] = {
        (id<MTLBuffer>)p210,
        (id<MTLBuffer>)rgba,
    };
    return dispatch_2d((id<MTLCommandQueue>)queue,
                       (id<MTLComputePipelineState>)pipeline,
                       bufs, 2, params, sizeof(*params), 2,
                       params->width, params->height);
}

// ============================================================================
// P210 STMap kernels
// ============================================================================

MetalResult metal_stmap_warp_p210_y(MetalQueueRef queue, MetalPipelineRef pipeline,
    MetalBufferRef src, MetalBufferRef stmapS, MetalBufferRef stmapT,
    MetalBufferRef dst, const MetalSTMapP210Params* params) {
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

MetalResult metal_stmap_warp_p210_uv(MetalQueueRef queue, MetalPipelineRef pipeline,
    MetalBufferRef srcUV, MetalBufferRef stmapS, MetalBufferRef stmapT,
    MetalBufferRef dstUV, const MetalSTMapP210UVParams* params) {
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
// Gaussian blur kernels
// ============================================================================

MetalResult metal_gaussian_blur_h(MetalQueueRef queue, MetalPipelineRef pipeline,
    MetalBufferRef tmpBuf, MetalBufferRef src, MetalBufferRef weights,
    const MetalBlurParams* params) {
    id<MTLBuffer> bufs[] = {
        (id<MTLBuffer>)tmpBuf,
        (id<MTLBuffer>)src,
        (id<MTLBuffer>)weights,
    };
    return dispatch_2d((id<MTLCommandQueue>)queue,
                       (id<MTLComputePipelineState>)pipeline,
                       bufs, 3, params, sizeof(*params), 3,
                       params->width, params->height);
}

MetalResult metal_gaussian_blur_v(MetalQueueRef queue, MetalPipelineRef pipeline,
    MetalBufferRef dst, MetalBufferRef tmpBuf, MetalBufferRef weights,
    const MetalBlurParams* params) {
    id<MTLBuffer> bufs[] = {
        (id<MTLBuffer>)dst,
        (id<MTLBuffer>)tmpBuf,
        (id<MTLBuffer>)weights,
    };
    return dispatch_2d((id<MTLCommandQueue>)queue,
                       (id<MTLComputePipelineState>)pipeline,
                       bufs, 3, params, sizeof(*params), 3,
                       params->width, params->height);
}

// ============================================================================
// VideoToolbox direct encode from NV12 unified memory
// ============================================================================

#import <VideoToolbox/VideoToolbox.h>
#import <CoreVideo/CoreVideo.h>

typedef struct VTEncoderState {
    VTCompressionSessionRef session;
    CVPixelBufferPoolRef pixelBufferPool;  // reusable pixel buffer pool
    OSType pixelFormat;  // track pixel format for encode-time data copy
    int codecType;       // 0=H.264, 1=HEVC (for parameter set extraction)
    int width, height, pitch;
    // Output buffer (reused across frames, grows as needed)
    uint8_t *outputBuf;
    int outputLen;
    int outputCap;   // allocated capacity of outputBuf
    int outputIsIDR;
    dispatch_semaphore_t sem;  // synchronize async callback
} VTEncoderState;

static void vtOutputCallback(void *outputCallbackRefCon,
                              void *sourceFrameRefCon,
                              OSStatus status,
                              VTEncodeInfoFlags infoFlags,
                              CMSampleBufferRef sampleBuffer) {
    VTEncoderState *state = (VTEncoderState *)outputCallbackRefCon;
    if (status != noErr || sampleBuffer == NULL) {
        state->outputLen = 0;
        dispatch_semaphore_signal(state->sem);
        return;
    }

    // Check if keyframe
    CFArrayRef attachments = CMSampleBufferGetSampleAttachmentsArray(sampleBuffer, false);
    state->outputIsIDR = 0;
    if (attachments && CFArrayGetCount(attachments) > 0) {
        CFDictionaryRef dict = CFArrayGetValueAtIndex(attachments, 0);
        CFBooleanRef notSync;
        if (!CFDictionaryGetValueIfPresent(dict, kCMSampleAttachmentKey_NotSync, (const void**)&notSync) || !CFBooleanGetValue(notSync)) {
            state->outputIsIDR = 1;
        }
    }

    // Extract Annex B data from CMSampleBuffer (codec-aware: H.264 or HEVC)
    CMFormatDescriptionRef format = CMSampleBufferGetFormatDescription(sampleBuffer);
    CMBlockBufferRef blockBuf = CMSampleBufferGetDataBuffer(sampleBuffer);

    size_t paramSetsLen = 0;
    int isHEVC = state->codecType == 1;

    // Calculate parameter sets length for keyframes
    if (state->outputIsIDR && format) {
        size_t paramSetCount = 0;
        if (isHEVC) {
            CMVideoFormatDescriptionGetHEVCParameterSetAtIndex(format, 0, NULL, NULL, &paramSetCount, NULL);
        } else {
            CMVideoFormatDescriptionGetH264ParameterSetAtIndex(format, 0, NULL, NULL, &paramSetCount, NULL);
        }
        for (size_t i = 0; i < paramSetCount; i++) {
            const uint8_t *paramSet;
            size_t paramSetSize;
            if (isHEVC) {
                CMVideoFormatDescriptionGetHEVCParameterSetAtIndex(format, i, &paramSet, &paramSetSize, NULL, NULL);
            } else {
                CMVideoFormatDescriptionGetH264ParameterSetAtIndex(format, i, &paramSet, &paramSetSize, NULL, NULL);
            }
            paramSetsLen += 4 + paramSetSize; // start code + data
        }
    }

    // Get block buffer data
    size_t blockLen = CMBlockBufferGetDataLength(blockBuf);

    // Total size: parameter sets (for IDR) + block data (AVCC/hvCC → Annex B is same size: 4→4)
    size_t totalLen = paramSetsLen + blockLen;

    // Reuse output buffer — only reallocate when capacity is insufficient.
    if ((int)totalLen > state->outputCap) {
        free(state->outputBuf);
        state->outputBuf = (uint8_t *)malloc(totalLen);
        if (!state->outputBuf) {
            state->outputLen = 0;
            state->outputCap = 0;
            dispatch_semaphore_signal(state->sem);
            return;
        }
        state->outputCap = (int)totalLen;
    }

    size_t offset = 0;

    // Write parameter sets with Annex B start codes for keyframes
    if (state->outputIsIDR && format) {
        size_t paramSetCount = 0;
        if (isHEVC) {
            CMVideoFormatDescriptionGetHEVCParameterSetAtIndex(format, 0, NULL, NULL, &paramSetCount, NULL);
        } else {
            CMVideoFormatDescriptionGetH264ParameterSetAtIndex(format, 0, NULL, NULL, &paramSetCount, NULL);
        }
        for (size_t i = 0; i < paramSetCount; i++) {
            const uint8_t *paramSet;
            size_t paramSetSize;
            if (isHEVC) {
                CMVideoFormatDescriptionGetHEVCParameterSetAtIndex(format, i, &paramSet, &paramSetSize, NULL, NULL);
            } else {
                CMVideoFormatDescriptionGetH264ParameterSetAtIndex(format, i, &paramSet, &paramSetSize, NULL, NULL);
            }
            state->outputBuf[offset++] = 0;
            state->outputBuf[offset++] = 0;
            state->outputBuf[offset++] = 0;
            state->outputBuf[offset++] = 1;
            memcpy(state->outputBuf + offset, paramSet, paramSetSize);
            offset += paramSetSize;
        }
    }

    // Convert AVCC NALUs to Annex B (replace 4-byte length with 4-byte start code)
    char *blockData;
    CMBlockBufferGetDataPointer(blockBuf, 0, NULL, NULL, &blockData);
    size_t blockOffset = 0;
    while (blockOffset + 4 <= blockLen) {
        uint32_t naluLen = 0;
        memcpy(&naluLen, blockData + blockOffset, 4);
        naluLen = CFSwapInt32BigToHost(naluLen);
        blockOffset += 4;

        if (naluLen == 0 || blockOffset + naluLen > blockLen) {
            break;
        }

        state->outputBuf[offset++] = 0;
        state->outputBuf[offset++] = 0;
        state->outputBuf[offset++] = 0;
        state->outputBuf[offset++] = 1;
        memcpy(state->outputBuf + offset, blockData + blockOffset, naluLen);
        offset += naluLen;
        blockOffset += naluLen;
    }

    state->outputLen = (int)offset;
    dispatch_semaphore_signal(state->sem);
}

VTEncoderRef metal_vt_encoder_create(int width, int height, int fps_num, int fps_den, int bitrate, int gop_frames, int preview) {
    VTEncoderState *state = calloc(1, sizeof(VTEncoderState));
    if (!state) return NULL;

    state->width = width;
    state->height = height;
    state->pixelFormat = kCVPixelFormatType_420YpCbCr8BiPlanarVideoRange;
    state->codecType = 0; // H.264
    state->sem = dispatch_semaphore_create(0);

    NSDictionary *pixelBufferAttrs = @{
        (NSString *)kCVPixelBufferWidthKey: @(width),
        (NSString *)kCVPixelBufferHeightKey: @(height),
        (NSString *)kCVPixelBufferPixelFormatTypeKey: @(kCVPixelFormatType_420YpCbCr8BiPlanarVideoRange),
    };

    VTCompressionSessionRef session;
    OSStatus status = VTCompressionSessionCreate(
        NULL,                           // allocator
        width, height,
        kCMVideoCodecType_H264,
        NULL,                           // encoder specification
        (__bridge CFDictionaryRef)pixelBufferAttrs,
        NULL,                           // compressed data allocator
        vtOutputCallback,
        state,                          // callback ref
        &session
    );
    if (status != noErr) {
        dispatch_release(state->sem);
        free(state);
        return NULL;
    }

    // Configure encoder properties.
    // Preview mode: Baseline profile for tight bitrate control at low targets.
    // Program mode: High profile for broadcast quality.
    VTSessionSetProperty(session, kVTCompressionPropertyKey_RealTime, kCFBooleanTrue);
    if (preview) {
        VTSessionSetProperty(session, kVTCompressionPropertyKey_ProfileLevel, kVTProfileLevel_H264_Baseline_AutoLevel);
        VTSessionSetProperty(session, kVTCompressionPropertyKey_H264EntropyMode, kVTH264EntropyMode_CAVLC);
    } else {
        VTSessionSetProperty(session, kVTCompressionPropertyKey_ProfileLevel, kVTProfileLevel_H264_High_AutoLevel);
        VTSessionSetProperty(session, kVTCompressionPropertyKey_H264EntropyMode, kVTH264EntropyMode_CABAC);
    }
    VTSessionSetProperty(session, kVTCompressionPropertyKey_AllowFrameReordering, kCFBooleanFalse);

    // Force immediate output — no internal frame buffering for rate-control
    // lookahead. Without this, VT may hold frames and introduce variable
    // latency (7ms–238ms observed), stalling the GPU pipeline.
    int maxDelay = 0;
    CFNumberRef delayRef = CFNumberCreate(NULL, kCFNumberIntType, &maxDelay);
    VTSessionSetProperty(session, kVTCompressionPropertyKey_MaxFrameDelayCount, delayRef);
    CFRelease(delayRef);

    // BT.709 colorspace signaling
    VTSessionSetProperty(session, kVTCompressionPropertyKey_ColorPrimaries, kCVImageBufferColorPrimaries_ITU_R_709_2);
    VTSessionSetProperty(session, kVTCompressionPropertyKey_TransferFunction, kCVImageBufferTransferFunction_ITU_R_709_2);
    VTSessionSetProperty(session, kVTCompressionPropertyKey_YCbCrMatrix, kCVImageBufferYCbCrMatrix_ITU_R_709_2);

    // Set bitrate (constrained VBR: average + 1.2x peak)
    CFNumberRef bitrateRef = CFNumberCreate(NULL, kCFNumberIntType, &bitrate);
    VTSessionSetProperty(session, kVTCompressionPropertyKey_AverageBitRate, bitrateRef);
    CFRelease(bitrateRef);

    // Data rate limits: [bytes_per_second, period_in_seconds]
    // 1.2x peak over 1 second window
    int peakBytesPerSec = (bitrate + bitrate / 5) / 8;
    float periodSecs = 1.0f;
    NSArray *limits = @[@(peakBytesPerSec), @(periodSecs)];
    VTSessionSetProperty(session, kVTCompressionPropertyKey_DataRateLimits, (__bridge CFArrayRef)limits);

    // Set max keyframe interval
    if (gop_frames < 1) gop_frames = 60;
    CFNumberRef kfRef = CFNumberCreate(NULL, kCFNumberIntType, &gop_frames);
    VTSessionSetProperty(session, kVTCompressionPropertyKey_MaxKeyFrameInterval, kfRef);
    CFRelease(kfRef);

    // Set expected frame rate
    float fps = (float)fps_num / (float)fps_den;
    CFNumberRef fpsRef = CFNumberCreate(NULL, kCFNumberFloatType, &fps);
    VTSessionSetProperty(session, kVTCompressionPropertyKey_ExpectedFrameRate, fpsRef);
    CFRelease(fpsRef);

    // No B-frames already enforced by AllowFrameReordering = kCFBooleanFalse above.

    VTCompressionSessionPrepareToEncodeFrames(session);

    state->session = session;

    // Create a CVPixelBufferPool for reusable pixel buffers. This avoids
    // per-frame CVPixelBufferCreate overhead (~1-2ms per call) by recycling
    // previously allocated buffers. The pool attributes match the session.
    NSDictionary *poolAttrs = @{
        (NSString *)kCVPixelBufferWidthKey: @(width),
        (NSString *)kCVPixelBufferHeightKey: @(height),
        (NSString *)kCVPixelBufferPixelFormatTypeKey: @(kCVPixelFormatType_420YpCbCr8BiPlanarVideoRange),
    };
    CVPixelBufferPoolRef pool = NULL;
    CVReturn poolRet = CVPixelBufferPoolCreate(
        NULL,   // allocator
        NULL,   // pool attributes (min/max buffer count — NULL = auto)
        (__bridge CFDictionaryRef)poolAttrs,
        &pool
    );
    if (poolRet == kCVReturnSuccess && pool) {
        state->pixelBufferPool = pool;
    }
    // If pool creation fails, fall back to per-frame CVPixelBufferCreate (slower but works)

    return (VTEncoderRef)state;
}

VTEncoderRef metal_vt_encoder_create_v2(int width, int height, int fps_num, int fps_den,
                                         int bitrate, int gop_frames, int preview,
                                         int codec_type, int bit_depth) {
    VTEncoderState *state = calloc(1, sizeof(VTEncoderState));
    if (!state) return NULL;

    state->width = width;
    state->height = height;
    state->codecType = codec_type;
    state->sem = dispatch_semaphore_create(0);

    // Select codec
    CMVideoCodecType vtCodec;
    if (codec_type == 1) {
        vtCodec = kCMVideoCodecType_HEVC;
    } else {
        vtCodec = kCMVideoCodecType_H264;
    }

    // Select pixel format
    OSType pixelFormat;
    if (bit_depth == 10) {
        pixelFormat = kCVPixelFormatType_420YpCbCr10BiPlanarVideoRange;
    } else {
        pixelFormat = kCVPixelFormatType_420YpCbCr8BiPlanarVideoRange;
    }
    state->pixelFormat = pixelFormat;

    NSDictionary *pixelBufferAttrs = @{
        (NSString *)kCVPixelBufferWidthKey: @(width),
        (NSString *)kCVPixelBufferHeightKey: @(height),
        (NSString *)kCVPixelBufferPixelFormatTypeKey: @(pixelFormat),
    };

    VTCompressionSessionRef session;
    OSStatus status = VTCompressionSessionCreate(
        NULL,                           // allocator
        width, height,
        vtCodec,
        NULL,                           // encoder specification
        (__bridge CFDictionaryRef)pixelBufferAttrs,
        NULL,                           // compressed data allocator
        vtOutputCallback,
        state,                          // callback ref
        &session
    );
    if (status != noErr) {
        dispatch_release(state->sem);
        free(state);
        return NULL;
    }

    // Configure encoder properties
    VTSessionSetProperty(session, kVTCompressionPropertyKey_RealTime, kCFBooleanTrue);

    if (codec_type == 1) {
        // HEVC profile selection
        if (bit_depth == 10) {
            VTSessionSetProperty(session, kVTCompressionPropertyKey_ProfileLevel, kVTProfileLevel_HEVC_Main10_AutoLevel);
        } else {
            VTSessionSetProperty(session, kVTCompressionPropertyKey_ProfileLevel, kVTProfileLevel_HEVC_Main_AutoLevel);
        }
        // Note: H264EntropyMode is H.264-specific, not set for HEVC
    } else {
        // H.264 profile selection (same as v1)
        if (preview) {
            VTSessionSetProperty(session, kVTCompressionPropertyKey_ProfileLevel, kVTProfileLevel_H264_Baseline_AutoLevel);
            VTSessionSetProperty(session, kVTCompressionPropertyKey_H264EntropyMode, kVTH264EntropyMode_CAVLC);
        } else {
            VTSessionSetProperty(session, kVTCompressionPropertyKey_ProfileLevel, kVTProfileLevel_H264_High_AutoLevel);
            VTSessionSetProperty(session, kVTCompressionPropertyKey_H264EntropyMode, kVTH264EntropyMode_CABAC);
        }
    }

    VTSessionSetProperty(session, kVTCompressionPropertyKey_AllowFrameReordering, kCFBooleanFalse);

    // Force immediate output — no internal frame buffering
    int maxDelay = 0;
    CFNumberRef delayRef = CFNumberCreate(NULL, kCFNumberIntType, &maxDelay);
    VTSessionSetProperty(session, kVTCompressionPropertyKey_MaxFrameDelayCount, delayRef);
    CFRelease(delayRef);

    // BT.709 colorspace signaling
    VTSessionSetProperty(session, kVTCompressionPropertyKey_ColorPrimaries, kCVImageBufferColorPrimaries_ITU_R_709_2);
    VTSessionSetProperty(session, kVTCompressionPropertyKey_TransferFunction, kCVImageBufferTransferFunction_ITU_R_709_2);
    VTSessionSetProperty(session, kVTCompressionPropertyKey_YCbCrMatrix, kCVImageBufferYCbCrMatrix_ITU_R_709_2);

    // Set bitrate (constrained VBR: average + 1.2x peak)
    CFNumberRef bitrateRef = CFNumberCreate(NULL, kCFNumberIntType, &bitrate);
    VTSessionSetProperty(session, kVTCompressionPropertyKey_AverageBitRate, bitrateRef);
    CFRelease(bitrateRef);

    // Data rate limits: [bytes_per_second, period_in_seconds]
    int peakBytesPerSec = (bitrate + bitrate / 5) / 8;
    float periodSecs = 1.0f;
    NSArray *limits = @[@(peakBytesPerSec), @(periodSecs)];
    VTSessionSetProperty(session, kVTCompressionPropertyKey_DataRateLimits, (__bridge CFArrayRef)limits);

    // Set max keyframe interval
    if (gop_frames < 1) gop_frames = 60;
    CFNumberRef kfRef = CFNumberCreate(NULL, kCFNumberIntType, &gop_frames);
    VTSessionSetProperty(session, kVTCompressionPropertyKey_MaxKeyFrameInterval, kfRef);
    CFRelease(kfRef);

    // Set expected frame rate
    float fps = (float)fps_num / (float)fps_den;
    CFNumberRef fpsRef = CFNumberCreate(NULL, kCFNumberFloatType, &fps);
    VTSessionSetProperty(session, kVTCompressionPropertyKey_ExpectedFrameRate, fpsRef);
    CFRelease(fpsRef);

    VTCompressionSessionPrepareToEncodeFrames(session);

    state->session = session;

    // Create a CVPixelBufferPool for reusable pixel buffers
    NSDictionary *poolAttrs = @{
        (NSString *)kCVPixelBufferWidthKey: @(width),
        (NSString *)kCVPixelBufferHeightKey: @(height),
        (NSString *)kCVPixelBufferPixelFormatTypeKey: @(pixelFormat),
    };
    CVPixelBufferPoolRef pool = NULL;
    CVReturn poolRet = CVPixelBufferPoolCreate(
        NULL,   // allocator
        NULL,   // pool attributes
        (__bridge CFDictionaryRef)poolAttrs,
        &pool
    );
    if (poolRet == kCVReturnSuccess && pool) {
        state->pixelBufferPool = pool;
    }

    return (VTEncoderRef)state;
}

void metal_vt_encoder_destroy(VTEncoderRef enc) {
    if (!enc) return;
    VTEncoderState *state = (VTEncoderState *)enc;
    if (state->session) {
        VTCompressionSessionCompleteFrames(state->session, kCMTimeInvalid);
        VTCompressionSessionInvalidate(state->session);
        CFRelease(state->session);
    }
    if (state->pixelBufferPool) {
        CVPixelBufferPoolRelease(state->pixelBufferPool);
    }
    if (state->outputBuf) free(state->outputBuf);
    dispatch_release(state->sem);
    free(state);
}

int metal_vt_encode(VTEncoderRef enc, void* nv12_ptr, int pitch, int width, int height,
                    int64_t pts, int force_idr,
                    uint8_t** out_buf, int* out_len, int* out_is_idr) {
    VTEncoderState *state = (VTEncoderState *)enc;
    if (!state || !state->session) return -1;

    // Acquire a CVPixelBuffer from the pool (or create one if pool unavailable).
    // Pool buffers are recycled across frames, avoiding per-frame kernel memory
    // allocation (~1-2ms savings per frame).
    CVPixelBufferRef pixelBuffer = NULL;
    CVReturn cvRet;
    if (state->pixelBufferPool) {
        cvRet = CVPixelBufferPoolCreatePixelBuffer(NULL, state->pixelBufferPool, &pixelBuffer);
    } else {
        cvRet = CVPixelBufferCreate(
            NULL, width, height,
            state->pixelFormat,
            NULL, &pixelBuffer
        );
    }
    if (cvRet != kCVReturnSuccess || !pixelBuffer) {
        return -2;
    }

    // Copy frame data from Metal unified-memory buffer into the VT pixel buffer.
    // VT needs its own copy because it holds reference frames for P-frame prediction
    // beyond the lifetime of this call.
    //
    // Three source formats handled:
    //   NV12 (8-bit 4:2:0): direct copy, Y + UV(h/2)
    //   P010 → P010 (10-bit 4:2:0): direct copy, but our P210 stores LSB-aligned
    //     while Apple P010 is MSB-aligned (10 bits in high 10 of uint16, 6 zero LSBs).
    //   P210 → P010 (10-bit 4:2:2 → 4:2:0): left-shift Y samples by 6 to convert
    //     LSB→MSB alignment, and vertically downsample UV from full-height to half-height
    //     by averaging adjacent row pairs, also with the <<6 shift.
    int is10bit = (state->pixelFormat == kCVPixelFormatType_420YpCbCr10BiPlanarVideoRange);

    CVPixelBufferLockBaseAddress(pixelBuffer, 0);

    uint8_t *vtY = (uint8_t *)CVPixelBufferGetBaseAddressOfPlane(pixelBuffer, 0);
    size_t vtYStride = CVPixelBufferGetBytesPerRowOfPlane(pixelBuffer, 0);
    const uint8_t *srcY = (const uint8_t *)nv12_ptr;

    if (is10bit) {
        // P210 source (LSB-aligned uint16) → P010 destination (MSB-aligned uint16).
        // Left-shift each sample by 6 to convert LSB→MSB alignment.
        // Source UV is full-height (4:2:2); destination UV is half-height (4:2:0).
        int samplesPerRow = width;

        // Y plane: shift each uint16 sample left by 6
        for (int row = 0; row < height; row++) {
            const uint16_t *srcRow = (const uint16_t *)(srcY + row * pitch);
            uint16_t *dstRow = (uint16_t *)(vtY + row * vtYStride);
            for (int col = 0; col < samplesPerRow; col++) {
                dstRow[col] = srcRow[col] << 6;
            }
        }

        // UV plane: P210 has full-height UV (4:2:2), P010 expects half-height (4:2:0).
        // Average pairs of adjacent UV rows and shift LSB→MSB.
        uint8_t *vtUV = (uint8_t *)CVPixelBufferGetBaseAddressOfPlane(pixelBuffer, 1);
        size_t vtUVStride = CVPixelBufferGetBytesPerRowOfPlane(pixelBuffer, 1);
        const uint8_t *srcUV = (const uint8_t *)nv12_ptr + pitch * height;
        int chromaH = height / 2;
        int uvSamplesPerRow = width; // interleaved Cb/Cr = width uint16 values

        for (int row = 0; row < chromaH; row++) {
            const uint16_t *srcRow0 = (const uint16_t *)(srcUV + (row * 2) * pitch);
            const uint16_t *srcRow1 = (const uint16_t *)(srcUV + (row * 2 + 1) * pitch);
            uint16_t *dstRow = (uint16_t *)(vtUV + row * vtUVStride);
            for (int col = 0; col < uvSamplesPerRow; col++) {
                uint16_t avg = (uint16_t)(((uint32_t)srcRow0[col] + (uint32_t)srcRow1[col] + 1) >> 1);
                dstRow[col] = avg << 6;
            }
        }
    } else {
        // NV12 (8-bit): direct copy as before.
        if (vtYStride == (size_t)pitch) {
            memcpy(vtY, srcY, (size_t)pitch * height);
        } else {
            for (int row = 0; row < height; row++) {
                memcpy(vtY + row * vtYStride, srcY + row * pitch, width);
            }
        }

        uint8_t *vtUV = (uint8_t *)CVPixelBufferGetBaseAddressOfPlane(pixelBuffer, 1);
        size_t vtUVStride = CVPixelBufferGetBytesPerRowOfPlane(pixelBuffer, 1);
        const uint8_t *srcUV = (const uint8_t *)nv12_ptr + pitch * height;
        int chromaH = height / 2;
        if (vtUVStride == (size_t)pitch) {
            memcpy(vtUV, srcUV, (size_t)pitch * chromaH);
        } else {
            for (int row = 0; row < chromaH; row++) {
                memcpy(vtUV + row * vtUVStride, srcUV + row * pitch, width);
            }
        }
    }

    CVPixelBufferUnlockBaseAddress(pixelBuffer, 0);

    // Create presentation timestamp
    CMTime cmPTS = CMTimeMake(pts, 90000); // 90kHz timebase

    // Encode properties (force IDR if requested)
    NSDictionary *frameProps = nil;
    if (force_idr) {
        frameProps = @{
            (NSString *)kVTEncodeFrameOptionKey_ForceKeyFrame: @YES
        };
    }

    // Reset output length — buffer is reused (grown as needed in callback).
    state->outputLen = 0;

    OSStatus status = VTCompressionSessionEncodeFrame(
        state->session,
        pixelBuffer,
        cmPTS,
        kCMTimeInvalid,                 // duration
        (__bridge CFDictionaryRef)frameProps,
        NULL,                           // source frame ref
        NULL                            // info flags out
    );

    // Release our reference. VT retains the buffer internally during encode
    // and releases its own reference after the output callback fires.
    CVPixelBufferRelease(pixelBuffer);

    if (status != noErr) {
        return -3;
    }

    // Wait for the async callback to complete
    dispatch_semaphore_wait(state->sem, DISPATCH_TIME_FOREVER);

    *out_buf = state->outputBuf;
    *out_len = state->outputLen;
    *out_is_idr = state->outputIsIDR;

    // Buffer is owned by VTEncoderState and reused across frames.
    // Go side copies via C.GoBytes() — do NOT null the pointer.

    return 0;
}

// ============================================================================
// Direct CPU YUV420p → NV12 conversion (Apple Silicon unified memory)
// ============================================================================

void metal_yuv420p_to_nv12_cpu(void* dst, int dstPitch,
                                const void* y, const void* cb, const void* cr,
                                int width, int height) {
    uint8_t* dstBytes = (uint8_t*)dst;
    const uint8_t* yBytes = (const uint8_t*)y;
    const uint8_t* cbBytes = (const uint8_t*)cb;
    const uint8_t* crBytes = (const uint8_t*)cr;

    // Y plane: copy row-by-row (pitch may differ from width due to 256-byte alignment)
    for (int row = 0; row < height; row++) {
        memcpy(dstBytes + row * dstPitch, yBytes + row * width, width);
    }

    // UV plane: interleave Cb + Cr into NV12 format.
    // On ARM64, Clang auto-vectorizes this with NEON vst2 (zip interleave).
    int chromaW = width / 2;
    int chromaH = height / 2;
    uint8_t* uvDst = dstBytes + dstPitch * height;
    for (int row = 0; row < chromaH; row++) {
        const uint8_t* cbRow = cbBytes + row * chromaW;
        const uint8_t* crRow = crBytes + row * chromaW;
        uint8_t* dstRow = uvDst + row * dstPitch;
        for (int x = 0; x < chromaW; x++) {
            dstRow[x * 2]     = cbRow[x];
            dstRow[x * 2 + 1] = crRow[x];
        }
    }
}

// ============================================================================
// Direct CPU NV12 → YUV420p conversion (Apple Silicon unified memory)
// ============================================================================

void metal_nv12_to_yuv420p_cpu(const void* src, int srcPitch,
                                void* y, void* cb, void* cr,
                                int width, int height) {
    const uint8_t* srcBytes = (const uint8_t*)src;
    uint8_t* yBytes = (uint8_t*)y;
    uint8_t* cbBytes = (uint8_t*)cb;
    uint8_t* crBytes = (uint8_t*)cr;

    // Y plane: copy row-by-row (pitch may differ from width due to 256-byte alignment)
    for (int row = 0; row < height; row++) {
        memcpy(yBytes + row * width, srcBytes + row * srcPitch, width);
    }

    // UV plane: deinterleave NV12 CbCr pairs into planar Cb and Cr.
    // On ARM64, Clang auto-vectorizes this with NEON vld2 (deinterleave load).
    int chromaW = width / 2;
    int chromaH = height / 2;
    const uint8_t* uvSrc = srcBytes + srcPitch * height;
    for (int row = 0; row < chromaH; row++) {
        const uint8_t* uvRow = uvSrc + row * srcPitch;
        uint8_t* cbRow = cbBytes + row * chromaW;
        uint8_t* crRow = crBytes + row * chromaW;
        for (int x = 0; x < chromaW; x++) {
            cbRow[x] = uvRow[x * 2];
            crRow[x] = uvRow[x * 2 + 1];
        }
    }
}

// ============================================================================
// Direct CPU YUV422P10LE → P210 conversion (Apple Silicon unified memory)
// ============================================================================

void metal_yuv422p10_to_p210_cpu(void* dst, int dstPitch,
                                  const void* src, int width, int height) {
    uint8_t* dstBytes = (uint8_t*)dst;
    const uint8_t* srcBytes = (const uint8_t*)src;
    int bps = 2; // bytes per sample (uint16)

    // Y plane: copy row-by-row (pitch may differ from width*2 due to alignment)
    int yRowBytes = width * bps;
    for (int row = 0; row < height; row++) {
        memcpy(dstBytes + row * dstPitch, srcBytes + row * yRowBytes, yRowBytes);
    }

    // UV plane: interleave planar Cb + Cr into P210 semi-planar format.
    // Source layout: Y (w*h uint16) | Cb (w/2*h uint16) | Cr (w/2*h uint16)
    // P210 UV: interleaved Cb,Cr uint16 pairs at full height (4:2:2).
    int chromaW = width / 2;
    int yPlaneSize = width * height * bps;
    int cbPlaneSize = chromaW * height * bps;
    const uint16_t* cbSrc = (const uint16_t*)(srcBytes + yPlaneSize);
    const uint16_t* crSrc = (const uint16_t*)(srcBytes + yPlaneSize + cbPlaneSize);
    uint16_t* uvDst = (uint16_t*)(dstBytes + dstPitch * height);

    // dstPitch is in bytes; UV row stride in uint16 units is dstPitch/2
    int uvRowStride16 = dstPitch / bps;
    for (int row = 0; row < height; row++) {
        const uint16_t* cbRow = cbSrc + row * chromaW;
        const uint16_t* crRow = crSrc + row * chromaW;
        uint16_t* dstRow = uvDst + row * uvRowStride16;
        for (int x = 0; x < chromaW; x++) {
            dstRow[x * 2]     = cbRow[x];
            dstRow[x * 2 + 1] = crRow[x];
        }
    }
}

// ============================================================================
// Direct CPU YUV420P (8-bit) → P210 (10-bit 4:2:2) conversion
// ============================================================================

void metal_yuv420p_to_p210_cpu(void* dst, int dstPitch,
                                const void* y, const void* cb, const void* cr,
                                int width, int height) {
    uint8_t* dstBytes = (uint8_t*)dst;
    const uint8_t* yPlane = (const uint8_t*)y;
    const uint8_t* cbPlane = (const uint8_t*)cb;
    const uint8_t* crPlane = (const uint8_t*)cr;

    // Y plane: uint8 → uint16 with <<2 bit promotion.
    int dstStrideU16 = dstPitch / 2;
    uint16_t* dstY16 = (uint16_t*)dstBytes;
    for (int row = 0; row < height; row++) {
        const uint8_t* srcRow = yPlane + row * width;
        uint16_t* dstRow = dstY16 + row * dstStrideU16;
        for (int col = 0; col < width; col++) {
            dstRow[col] = (uint16_t)srcRow[col] << 2;
        }
    }

    // UV plane: 4:2:0 → 4:2:2 (duplicate rows) + interleave + <<2.
    int chromaW = width / 2;
    int chromaH = height / 2;
    uint16_t* dstUV = (uint16_t*)(dstBytes + dstPitch * height);

    for (int srcRow = 0; srcRow < chromaH; srcRow++) {
        const uint8_t* cbRow = cbPlane + srcRow * chromaW;
        const uint8_t* crRow = crPlane + srcRow * chromaW;
        uint16_t* dstRow0 = dstUV + (srcRow * 2) * dstStrideU16;
        uint16_t* dstRow1 = dstUV + (srcRow * 2 + 1) * dstStrideU16;
        for (int col = 0; col < chromaW; col++) {
            uint16_t cb16 = (uint16_t)cbRow[col] << 2;
            uint16_t cr16 = (uint16_t)crRow[col] << 2;
            dstRow0[col * 2]     = cb16;
            dstRow0[col * 2 + 1] = cr16;
            dstRow1[col * 2]     = cb16;
            dstRow1[col * 2 + 1] = cr16;
        }
    }
}

// ============================================================================
// Direct CPU P210 → YUV422P10LE conversion (Apple Silicon unified memory)
// ============================================================================

void metal_p210_to_yuv422p10_cpu(const void* src, int srcPitch,
                                  void* dst, int width, int height) {
    const uint8_t* srcBytes = (const uint8_t*)src;
    uint8_t* dstBytes = (uint8_t*)dst;
    int bps = 2; // bytes per sample (uint16)

    // Y plane: copy row-by-row
    int yRowBytes = width * bps;
    for (int row = 0; row < height; row++) {
        memcpy(dstBytes + row * yRowBytes, srcBytes + row * srcPitch, yRowBytes);
    }

    // UV plane: deinterleave P210 CbCr pairs into planar Cb and Cr.
    int chromaW = width / 2;
    int yPlaneSize = width * height * bps;
    int cbPlaneSize = chromaW * height * bps;
    uint16_t* cbDst = (uint16_t*)(dstBytes + yPlaneSize);
    uint16_t* crDst = (uint16_t*)(dstBytes + yPlaneSize + cbPlaneSize);
    const uint16_t* uvSrc = (const uint16_t*)(srcBytes + srcPitch * height);

    int uvRowStride16 = srcPitch / bps;
    for (int row = 0; row < height; row++) {
        const uint16_t* uvRow = uvSrc + row * uvRowStride16;
        uint16_t* cbRow = cbDst + row * chromaW;
        uint16_t* crRow = crDst + row * chromaW;
        for (int x = 0; x < chromaW; x++) {
            cbRow[x] = uvRow[x * 2];
            crRow[x] = uvRow[x * 2 + 1];
        }
    }
}
