#ifndef SWITCHFRAME_METAL_BRIDGE_H
#define SWITCHFRAME_METAL_BRIDGE_H

#include <stdint.h>
#include <stddef.h>

// Opaque handles for Metal objects
typedef void* MetalDeviceRef;
typedef void* MetalQueueRef;
typedef void* MetalLibraryRef;
typedef void* MetalPipelineRef;
typedef void* MetalBufferRef;

// Result code (0 = success, non-zero = error)
typedef int MetalResult;

#define METAL_SUCCESS 0
#define METAL_ERROR_NO_DEVICE 1
#define METAL_ERROR_NO_QUEUE 2
#define METAL_ERROR_NO_LIBRARY 3
#define METAL_ERROR_NO_FUNCTION 4
#define METAL_ERROR_PIPELINE 5
#define METAL_ERROR_BUFFER 6
#define METAL_ERROR_ENCODE 7
#define METAL_ERROR_COMMIT 8

// --- Device lifecycle ---
MetalResult metal_init(MetalDeviceRef* device, MetalQueueRef* queue, MetalLibraryRef* library,
                       const char* metallib_path);
void metal_release(MetalDeviceRef device, MetalQueueRef queue, MetalLibraryRef library);
const char* metal_device_name(MetalDeviceRef device);
uint64_t metal_device_memory(MetalDeviceRef device);

// --- Buffer management (unified memory) ---
MetalBufferRef metal_buffer_alloc(MetalDeviceRef device, size_t size);
MetalBufferRef metal_buffer_alloc_aligned(MetalDeviceRef device, size_t size, size_t alignment);
void metal_buffer_free(MetalBufferRef buffer);
void* metal_buffer_contents(MetalBufferRef buffer);
size_t metal_buffer_length(MetalBufferRef buffer);

// --- Pipeline state ---
MetalPipelineRef metal_pipeline_create(MetalDeviceRef device, MetalLibraryRef library,
                                       const char* function_name);
void metal_pipeline_free(MetalPipelineRef pipeline);

// --- Synchronization ---
MetalResult metal_sync(MetalQueueRef queue);

// --- Convert kernels ---
typedef struct {
    uint32_t width;
    uint32_t height;
    uint32_t nv12Pitch;
    uint32_t srcStride;
} MetalConvertParams;

typedef struct {
    uint32_t width;
    uint32_t height;
    uint32_t pitch;
    uint8_t yVal;
    uint8_t cbVal;
    uint8_t crVal;
} MetalFillParams;

MetalResult metal_yuv420p_to_nv12(MetalQueueRef queue, MetalPipelineRef pipeline,
    MetalBufferRef y_src, MetalBufferRef cb_src, MetalBufferRef cr_src,
    MetalBufferRef nv12, const MetalConvertParams* params);

MetalResult metal_nv12_to_yuv420p(MetalQueueRef queue, MetalPipelineRef pipeline,
    MetalBufferRef y_dst, MetalBufferRef cb_dst, MetalBufferRef cr_dst,
    MetalBufferRef nv12, const MetalConvertParams* params);

MetalResult metal_nv12_fill(MetalQueueRef queue, MetalPipelineRef pipeline,
    MetalBufferRef nv12, const MetalFillParams* params);

// --- Offset-aware dispatch ---
// dispatch_2d_offset binds each buffer at a caller-specified byte offset.
// This is critical for NV12 UV-plane operations where UV data starts at
// pitch * height within the same buffer.
MetalResult metal_dispatch_2d_offset(MetalQueueRef queue, MetalPipelineRef pipeline,
    MetalBufferRef buffers[], int64_t offsets[], int nbuf,
    const void* params, size_t paramsSize, int paramsIndex,
    uint32_t gridW, uint32_t gridH);

// --- Blend kernels ---
typedef struct {
    uint32_t width;
    uint32_t height;
    uint32_t pitch;
    int32_t pos256;
} MetalBlendUniformParams;

typedef struct {
    uint32_t width;
    uint32_t height;
    uint32_t pitch;
    int32_t pos256;
    uint8_t constY;
    uint8_t constUV;
} MetalBlendFadeConstParams;

typedef struct {
    uint32_t width;
    uint32_t height;
    uint32_t pitch;
    uint32_t alphaPitch;
} MetalBlendAlphaParams;

typedef struct {
    uint32_t width;
    uint32_t height;
    uint32_t pitch;
    float position;
    int32_t direction;
    int32_t softEdge;
} MetalWipeMaskParams;

typedef struct {
    uint32_t chromaW;
    uint32_t chromaH;
    uint32_t srcPitch;
    uint32_t dstPitch;
} MetalDownsampleAlphaParams;

MetalResult metal_blend_uniform(MetalQueueRef queue, MetalPipelineRef pipeline,
    MetalBufferRef dst, MetalBufferRef a, MetalBufferRef b,
    const MetalBlendUniformParams* params);

MetalResult metal_blend_fade_const(MetalQueueRef queue, MetalPipelineRef pipeline,
    MetalBufferRef dst, MetalBufferRef src,
    const MetalBlendFadeConstParams* params);

MetalResult metal_blend_alpha(MetalQueueRef queue, MetalPipelineRef pipeline,
    MetalBufferRef dst, MetalBufferRef a, MetalBufferRef b, MetalBufferRef alpha,
    const MetalBlendAlphaParams* params);

MetalResult metal_wipe_mask_generate(MetalQueueRef queue, MetalPipelineRef pipeline,
    MetalBufferRef mask, const MetalWipeMaskParams* params);

MetalResult metal_downsample_alpha_to_nv12_uv(MetalQueueRef queue, MetalPipelineRef pipeline,
    MetalBufferRef dst, MetalBufferRef src,
    const MetalDownsampleAlphaParams* params);

// --- Scale kernels ---
typedef struct {
    uint32_t srcW;
    uint32_t srcH;
    uint32_t srcPitch;
    uint32_t dstW;
    uint32_t dstH;
    uint32_t dstPitch;
} MetalScaleParams;

typedef struct {
    uint32_t dstW;
    uint32_t srcW;
    uint32_t srcH;
    uint32_t srcPitch;
} MetalLanczos3HParams;

typedef struct {
    uint32_t dstW;
    uint32_t dstH;
    uint32_t dstPitch;
    uint32_t srcH;
} MetalLanczos3VParams;

MetalResult metal_scale_bilinear(MetalQueueRef queue, MetalPipelineRef pipeline,
    MetalBufferRef dst, MetalBufferRef src, const MetalScaleParams* params);

MetalResult metal_scale_lanczos3_h(MetalQueueRef queue, MetalPipelineRef pipeline,
    MetalBufferRef tmpBuf, MetalBufferRef src, const MetalLanczos3HParams* params);

MetalResult metal_scale_lanczos3_v(MetalQueueRef queue, MetalPipelineRef pipeline,
    MetalBufferRef dst, MetalBufferRef tmpBuf, const MetalLanczos3VParams* params);

// --- Key kernels ---
typedef struct {
    uint32_t width;
    uint32_t height;
    uint32_t pitch;
    uint8_t keyCb;
    uint8_t keyCr;
    int32_t simDistSq;
    int32_t totalDistSq;
    float spillSuppress;
    uint8_t spillReplaceCb;
    uint8_t spillReplaceCr;
} MetalChromaKeyParams;

typedef struct {
    uint32_t width;
    uint32_t height;
    uint32_t pitch;
} MetalLumaKeyParams;

MetalResult metal_chroma_key(MetalQueueRef queue, MetalPipelineRef pipeline,
    MetalBufferRef nv12, MetalBufferRef mask, const MetalChromaKeyParams* params);

MetalResult metal_luma_key(MetalQueueRef queue, MetalPipelineRef pipeline,
    MetalBufferRef nv12, MetalBufferRef mask, MetalBufferRef lut,
    const MetalLumaKeyParams* params);

// --- Composite kernels ---
typedef struct {
    uint32_t dstW; uint32_t dstH; uint32_t dstPitch;
    uint32_t srcW; uint32_t srcH; uint32_t srcPitch;
    int32_t rectX; int32_t rectY; int32_t rectW; int32_t rectH;
    int32_t alpha256;
} MetalPIPCompositeYParams;

typedef struct {
    uint32_t dstW; uint32_t dstChromaH; uint32_t dstPitch;
    uint32_t srcW; uint32_t srcChromaH; uint32_t srcPitch;
    int32_t rectX; int32_t rectY; int32_t rectCW; int32_t rectCH;
    int32_t alpha256;
} MetalPIPCompositeUVParams;

MetalResult metal_pip_composite_y(MetalQueueRef queue, MetalPipelineRef pipeline,
    MetalBufferRef dst, MetalBufferRef src, const MetalPIPCompositeYParams* params);

MetalResult metal_pip_composite_uv(MetalQueueRef queue, MetalPipelineRef pipeline,
    MetalBufferRef dstUV, MetalBufferRef srcUV, const MetalPIPCompositeUVParams* params);

// --- DSK kernels ---
typedef struct {
    uint32_t width; uint32_t height;
    uint32_t nv12Pitch; uint32_t rgbaPitch;
    int32_t alphaScale256;
} MetalDSKFullFrameParams;

typedef struct {
    uint32_t frameW; uint32_t frameH;
    uint32_t nv12Pitch;
    uint32_t overlayW; uint32_t overlayH; uint32_t rgbaPitch;
    int32_t rectX; int32_t rectY; int32_t rectW; int32_t rectH;
    int32_t alphaScale256;
} MetalDSKRectParams;

MetalResult metal_dsk_overlay_full(MetalQueueRef queue, MetalPipelineRef pipeline,
    MetalBufferRef nv12, MetalBufferRef rgba, const MetalDSKFullFrameParams* params);

MetalResult metal_dsk_overlay_rect(MetalQueueRef queue, MetalPipelineRef pipeline,
    MetalBufferRef nv12, MetalBufferRef rgba, const MetalDSKRectParams* params);

// --- STMap kernels ---
typedef struct {
    uint32_t width; uint32_t height;
    uint32_t dstPitch; uint32_t srcPitch;
} MetalSTMapParams;

typedef struct {
    uint32_t lumaW; uint32_t lumaH;
    uint32_t chromaW; uint32_t chromaH;
    uint32_t dstPitch; uint32_t srcPitch;
} MetalSTMapUVParams;

MetalResult metal_stmap_warp_y(MetalQueueRef queue, MetalPipelineRef pipeline,
    MetalBufferRef src, MetalBufferRef stmapS, MetalBufferRef stmapT,
    MetalBufferRef dst, const MetalSTMapParams* params);

MetalResult metal_stmap_warp_uv(MetalQueueRef queue, MetalPipelineRef pipeline,
    MetalBufferRef srcUV, MetalBufferRef stmapS, MetalBufferRef stmapT,
    MetalBufferRef dstUV, const MetalSTMapUVParams* params);

// --- FRUC kernels ---
typedef struct {
    uint32_t width; uint32_t height;
    uint32_t dstPitch; uint32_t prevPitch; uint32_t currPitch;
    float alpha;
} MetalFRUCBlendParams;

MetalResult metal_fruc_blend(MetalQueueRef queue, MetalPipelineRef pipeline,
    MetalBufferRef dst, MetalBufferRef prev, MetalBufferRef curr,
    const MetalFRUCBlendParams* params);

// --- V210 kernels ---
typedef struct {
    uint32_t width; uint32_t height;
    uint32_t nv12Pitch; uint32_t v210Stride32;
} MetalV210Params;

MetalResult metal_v210_to_nv12(MetalQueueRef queue, MetalPipelineRef pipeline,
    MetalBufferRef nv12, MetalBufferRef v210, const MetalV210Params* params);

MetalResult metal_nv12_to_v210(MetalQueueRef queue, MetalPipelineRef pipeline,
    MetalBufferRef v210, MetalBufferRef nv12, const MetalV210Params* params);

// --- Border / FillRect kernels ---
typedef struct {
    uint32_t dstW; uint32_t dstH; uint32_t dstPitch;
    int32_t rectX; int32_t rectY; int32_t rectW; int32_t rectH;
    int32_t outerX; int32_t outerY; int32_t outerW; int32_t outerH;
    int32_t thickness;
    uint8_t colorY; uint8_t colorCb; uint8_t colorCr;
} MetalBorderParams;

typedef struct {
    uint32_t dstW; uint32_t dstH; uint32_t dstPitch;
    int32_t rectX; int32_t rectY; int32_t rectW; int32_t rectH;
    uint8_t colorY; uint8_t colorCb; uint8_t colorCr;
} MetalFillRectParams;

MetalResult metal_draw_border(MetalQueueRef queue, MetalPipelineRef pipeline,
    MetalBufferRef dst, const MetalBorderParams* params);

MetalResult metal_fill_rect(MetalQueueRef queue, MetalPipelineRef pipeline,
    MetalBufferRef dst, const MetalFillRectParams* params);

// --- VideoToolbox direct encode from NV12 unified memory ---
typedef void* VTEncoderRef;

VTEncoderRef metal_vt_encoder_create(int width, int height, int fps_num, int fps_den, int bitrate, int gop_frames);
void metal_vt_encoder_destroy(VTEncoderRef enc);

// Encode an NV12 frame from unified memory. Returns Annex B H.264 data.
// out_buf/out_len receive the encoded data (caller must free with free()).
// out_is_idr is set to 1 for keyframes.
int metal_vt_encode(VTEncoderRef enc, void* nv12_ptr, int pitch, int width, int height,
                    int64_t pts, int force_idr,
                    uint8_t** out_buf, int* out_len, int* out_is_idr);

#endif // SWITCHFRAME_METAL_BRIDGE_H
