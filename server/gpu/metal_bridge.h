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
MetalQueueRef metal_create_queue(MetalDeviceRef device);
void metal_queue_release(MetalQueueRef queue);
const char* metal_device_name(MetalDeviceRef device);
uint64_t metal_device_memory(MetalDeviceRef device);

// --- Buffer management (unified memory) ---
MetalBufferRef metal_buffer_alloc(MetalDeviceRef device, size_t size);
MetalBufferRef metal_buffer_alloc_aligned(MetalDeviceRef device, size_t size, size_t alignment);
MetalBufferRef metal_buffer_alloc_default(size_t size);
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

typedef struct {
    uint32_t width;
    uint32_t height;
    uint32_t pitch;
    uint16_t yVal;
    uint16_t cbVal;
    uint16_t crVal;
} MetalFillP210Params;

MetalResult metal_p210_fill(MetalQueueRef queue, MetalPipelineRef pipeline,
    MetalBufferRef p210, const MetalFillP210Params* params);

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

// --- Gradient map wipe blend ---
typedef struct {
    uint32_t width;
    uint32_t height;
    uint32_t pitch;
    uint32_t gradPitch;
    float position;
    float softEdge;
    float borderWidth;
    uint8_t borderColorY;
    uint8_t borderColorCb;
    uint8_t borderColorCr;
    int32_t reverse;
    int32_t multiply;
} MetalWipeMapBlendParams;

MetalResult metal_wipe_map_blend_y(MetalQueueRef queue, MetalPipelineRef pipeline,
    MetalBufferRef srcA, MetalBufferRef srcB, MetalBufferRef gradMap, MetalBufferRef dst,
    const MetalWipeMapBlendParams* params);

MetalResult metal_wipe_map_blend_uv(MetalQueueRef queue, MetalPipelineRef pipeline,
    MetalBufferRef srcAUV, MetalBufferRef srcBUV, MetalBufferRef gradMap, MetalBufferRef dstUV,
    const MetalWipeMapBlendParams* params);

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

// --- P210 (10-bit 4:2:2) key kernels ---
typedef struct {
    uint32_t width;
    uint32_t height;
    uint32_t pitch;
    uint32_t maskPitch;
    uint32_t uvOffsetU16;     // start of UV plane in uint16 indices (= pitch * height / 2)
    uint16_t keyCb;           // 10-bit
    uint16_t keyCr;           // 10-bit
    int32_t simDistSq;
    int32_t totalDistSq;
    float spillSuppress;
    uint16_t spillReplaceCb;  // 10-bit
    uint16_t spillReplaceCr;  // 10-bit
} MetalChromaKeyP210Params;

typedef struct {
    uint32_t width;
    uint32_t height;
    uint32_t pitch;
    uint32_t maskPitch;
} MetalLumaKeyP210Params;

MetalResult metal_chroma_key_p210(MetalQueueRef queue, MetalPipelineRef pipeline,
    MetalBufferRef p210, MetalBufferRef mask, const MetalChromaKeyP210Params* params);

MetalResult metal_luma_key_p210(MetalQueueRef queue, MetalPipelineRef pipeline,
    MetalBufferRef p210, MetalBufferRef mask, MetalBufferRef lut,
    const MetalLumaKeyP210Params* params);

// --- Composite kernels ---
typedef struct {
    uint32_t dstW; uint32_t dstH; uint32_t dstPitch;
    uint32_t srcW; uint32_t srcH; uint32_t srcPitch;
    int32_t rectX; int32_t rectY; int32_t rectW; int32_t rectH;
    int32_t alpha256;
    int32_t cropX; int32_t cropY; int32_t cropW; int32_t cropH; // source crop rect (0,0,0,0 = full source)
} MetalPIPCompositeYParams;

typedef struct {
    uint32_t dstW; uint32_t dstChromaH; uint32_t dstPitch;
    uint32_t srcW; uint32_t srcChromaH; uint32_t srcPitch;
    int32_t rectX; int32_t rectY; int32_t rectCW; int32_t rectCH;
    int32_t alpha256;
    int32_t cropX; int32_t cropY; int32_t cropCW; int32_t cropCH; // chroma crop rect (0,0,0,0 = full source)
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

// --- P210 (10-bit) Border / FillRect kernels ---
typedef struct {
    uint32_t dstW; uint32_t dstH; uint32_t dstPitch;
    int32_t rectX; int32_t rectY; int32_t rectW; int32_t rectH;
    uint16_t colorY; uint16_t colorCb; uint16_t colorCr;
} MetalFillRectP210Params;

typedef struct {
    uint32_t dstW; uint32_t dstH; uint32_t dstPitch;
    int32_t rectX; int32_t rectY; int32_t rectW; int32_t rectH;
    int32_t outerX; int32_t outerY; int32_t outerW; int32_t outerH;
    int32_t thickness;
    uint16_t colorY; uint16_t colorCb; uint16_t colorCr;
} MetalBorderP210Params;

MetalResult metal_fill_rect_p210(MetalQueueRef queue, MetalPipelineRef pipeline,
    MetalBufferRef dst, const MetalFillRectP210Params* params);

MetalResult metal_draw_border_p210(MetalQueueRef queue, MetalPipelineRef pipeline,
    MetalBufferRef dst, const MetalBorderP210Params* params);

// --- VideoToolbox direct encode from NV12 unified memory ---
typedef void* VTEncoderRef;

VTEncoderRef metal_vt_encoder_create(int width, int height, int fps_num, int fps_den, int bitrate, int gop_frames, int preview);
VTEncoderRef metal_vt_encoder_create_v2(int width, int height, int fps_num, int fps_den,
                                         int bitrate, int gop_frames, int preview,
                                         int codec_type, int bit_depth);
void metal_vt_encoder_destroy(VTEncoderRef enc);

// Encode an NV12 frame from unified memory. Returns Annex B H.264 data.
// out_buf/out_len receive the encoded data (caller must free with free()).
// out_is_idr is set to 1 for keyframes.
int metal_vt_encode(VTEncoderRef enc, void* nv12_ptr, int pitch, int width, int height,
                    int64_t pts, int force_idr,
                    uint8_t** out_buf, int* out_len, int* out_is_idr);

// --- Direct CPU YUV420p → NV12 conversion (Apple Silicon unified memory) ---
// Writes directly into the destination buffer — no staging needed.
// Each frame has its own memory, so concurrent uploads are race-free.
void metal_yuv420p_to_nv12_cpu(void* dst, int dstPitch,
                                const void* y, const void* cb, const void* cr,
                                int width, int height);

// Direct CPU NV12 → YUV420p conversion for Apple Silicon unified memory.
// Reads directly from the source NV12 buffer — no staging needed.
void metal_nv12_to_yuv420p_cpu(const void* src, int srcPitch,
                                void* y, void* cb, void* cr,
                                int width, int height);

// --- P210 (10-bit 4:2:2) CPU conversion (Apple Silicon unified memory) ---
// YUV422P10LE (planar) → P210 (semi-planar): interleave Cb/Cr into UV plane.
void metal_yuv422p10_to_p210_cpu(void* dst, int dstPitch,
                                  const void* src, int width, int height);

// P210 (semi-planar) → YUV422P10LE (planar): deinterleave UV to Cb/Cr planes.
void metal_p210_to_yuv422p10_cpu(const void* src, int srcPitch,
                                  void* dst, int width, int height);

// YUV420P (8-bit planar 4:2:0) → P210 (10-bit semi-planar 4:2:2).
// Bit-promotes each sample (<<2) and upsamples chroma from half to full height
// by duplicating rows. Writes directly to unified memory.
void metal_yuv420p_to_p210_cpu(void* dst, int dstPitch,
                                const void* y, const void* cb, const void* cr,
                                int width, int height);

// --- P210 (10-bit 4:2:2) blend kernels ---
typedef struct {
    uint32_t width;
    uint32_t height;
    uint32_t pitch;
    int32_t pos256;
} MetalBlendUniformP210Params;

typedef struct {
    uint32_t width;
    uint32_t height;
    uint32_t pitch;
    int32_t pos256;
    uint16_t constVal;
} MetalBlendFadeConstP210Params;

MetalResult metal_blend_uniform_p210(MetalQueueRef queue, MetalPipelineRef pipeline,
    MetalBufferRef dst, MetalBufferRef a, MetalBufferRef b,
    const MetalBlendUniformP210Params* params);

MetalResult metal_blend_fade_const_p210(MetalQueueRef queue, MetalPipelineRef pipeline,
    MetalBufferRef dst, MetalBufferRef src,
    const MetalBlendFadeConstP210Params* params);

// P210 alpha-blend (wipes, stingers)
typedef struct {
    uint32_t width;
    uint32_t height;
    uint32_t pitch;
    uint32_t alphaPitch;
} MetalBlendAlphaP210Params;

// P210 downsample alpha (luma resolution -> UV resolution for 4:2:2)
typedef struct {
    uint32_t chromaW;
    uint32_t chromaH;
    uint32_t srcPitch;
    uint32_t dstPitch;
} MetalDownsampleAlphaP210Params;

MetalResult metal_blend_alpha_p210(MetalQueueRef queue, MetalPipelineRef pipeline,
    MetalBufferRef dst, MetalBufferRef a, MetalBufferRef b, MetalBufferRef alpha,
    const MetalBlendAlphaP210Params* params);

MetalResult metal_downsample_alpha_to_p210_uv(MetalQueueRef queue, MetalPipelineRef pipeline,
    MetalBufferRef dst, MetalBufferRef src,
    const MetalDownsampleAlphaP210Params* params);

// --- P210 scale kernels ---
typedef struct {
    uint32_t srcW;
    uint32_t srcH;
    uint32_t srcPitch;
    uint32_t dstW;
    uint32_t dstH;
    uint32_t dstPitch;
} MetalScaleP210Params;

MetalResult metal_scale_bilinear_p210_y(MetalQueueRef queue, MetalPipelineRef pipeline,
    MetalBufferRef dst, MetalBufferRef src, const MetalScaleP210Params* params);

// --- P210→NV12 cross-format scale ---
typedef struct {
    uint32_t srcW; uint32_t srcH; uint32_t srcPitch;
    uint32_t dstW; uint32_t dstH; uint32_t dstPitch;
} MetalScaleP210ToNV12Params;

MetalResult metal_scale_p210_to_nv12_y(MetalQueueRef queue, MetalPipelineRef pipeline,
    MetalBufferRef dst, MetalBufferRef src, const MetalScaleP210ToNV12Params* params);

// --- P210 composite kernels ---
typedef struct {
    uint32_t dstW; uint32_t dstH; uint32_t dstPitch;
    uint32_t srcW; uint32_t srcH; uint32_t srcPitch;
    int32_t rectX; int32_t rectY; int32_t rectW; int32_t rectH;
    int32_t alpha256;
    int32_t cropX; int32_t cropY; int32_t cropW; int32_t cropH;
} MetalPIPCompositeP210YParams;

typedef struct {
    uint32_t dstW; uint32_t dstChromaH; uint32_t dstPitch;
    uint32_t srcW; uint32_t srcChromaH; uint32_t srcPitch;
    int32_t rectX; int32_t rectY; int32_t rectCW; int32_t rectCH;
    int32_t alpha256;
    int32_t cropX; int32_t cropY; int32_t cropCW; int32_t cropCH;
} MetalPIPCompositeP210UVParams;

MetalResult metal_pip_composite_p210_y(MetalQueueRef queue, MetalPipelineRef pipeline,
    MetalBufferRef dst, MetalBufferRef src, const MetalPIPCompositeP210YParams* params);

MetalResult metal_pip_composite_p210_uv(MetalQueueRef queue, MetalPipelineRef pipeline,
    MetalBufferRef dstUV, MetalBufferRef srcUV, const MetalPIPCompositeP210UVParams* params);

// --- P210 V210 lossless conversion kernels ---
typedef struct {
    uint32_t width; uint32_t height;
    uint32_t p210Pitch; uint32_t v210Stride32;
} MetalV210P210Params;

MetalResult metal_v210_to_p210(MetalQueueRef queue, MetalPipelineRef pipeline,
    MetalBufferRef p210, MetalBufferRef v210, const MetalV210P210Params* params);

MetalResult metal_p210_to_v210(MetalQueueRef queue, MetalPipelineRef pipeline,
    MetalBufferRef v210, MetalBufferRef p210, const MetalV210P210Params* params);

// --- P210 DSK overlay kernels ---
typedef struct {
    uint32_t width; uint32_t height;
    uint32_t p210Pitch; uint32_t rgbaPitch;
    int32_t alphaScale256;
} MetalDSKP210FullFrameParams;

MetalResult metal_dsk_overlay_p210(MetalQueueRef queue, MetalPipelineRef pipeline,
    MetalBufferRef p210, MetalBufferRef rgba, const MetalDSKP210FullFrameParams* params);

// --- P210 STMap kernels ---
typedef struct {
    uint32_t width; uint32_t height;
    uint32_t dstPitch; uint32_t srcPitch;
} MetalSTMapP210Params;

typedef struct {
    uint32_t lumaW; uint32_t lumaH;
    uint32_t chromaW; uint32_t chromaH;
    uint32_t dstPitch; uint32_t srcPitch;
} MetalSTMapP210UVParams;

MetalResult metal_stmap_warp_p210_y(MetalQueueRef queue, MetalPipelineRef pipeline,
    MetalBufferRef src, MetalBufferRef stmapS, MetalBufferRef stmapT,
    MetalBufferRef dst, const MetalSTMapP210Params* params);

MetalResult metal_stmap_warp_p210_uv(MetalQueueRef queue, MetalPipelineRef pipeline,
    MetalBufferRef srcUV, MetalBufferRef stmapS, MetalBufferRef stmapT,
    MetalBufferRef dstUV, const MetalSTMapP210UVParams* params);

// --- DVE composite kernels ---
typedef struct {
    uint32_t dstW, dstH, dstPitch;
    uint32_t srcW, srcH, srcPitch;
    float m00, m01, m02, m10, m11, m12, m20, m21, m22;
    int32_t slotMinX, slotMinY, slotMaxX, slotMaxY;
    int32_t alpha256;
    float cropTop, cropBottom, cropLeft, cropRight, cropSoftness;
    float borderWidth;
    uint32_t borderColorY;
    float borderCornerRadius, borderSoftness;
    int32_t shadowEnabled;
    float shadowOffsetX, shadowOffsetY, shadowBlur;
    int32_t shadowAlpha256;
    uint32_t shadowColorY;
    // Mirror / mosaic
    int32_t mirrorH;
    int32_t mirrorV;
    int32_t mosaicSize;
    // Color effects (Y plane)
    float brightness;
    float contrast;
} MetalDVECompositeYParams;

typedef struct {
    uint32_t dstW, dstChromaH, dstPitch;
    uint32_t srcW, srcChromaH, srcPitch;
    float m00, m01, m02, m10, m11, m12, m20, m21, m22;
    int32_t slotMinCX, slotMinCY, slotMaxCX, slotMaxCY;
    int32_t alpha256;
    float cropTop, cropBottom, cropLeft, cropRight, cropSoftness;
    float borderWidth;
    uint32_t borderColorCb, borderColorCr;
    float borderCornerRadius, borderSoftness;
    int32_t shadowEnabled;
    float shadowOffsetX, shadowOffsetY, shadowBlur;
    int32_t shadowAlpha256;
    uint32_t shadowColorCb, shadowColorCr;
    // Mirror / mosaic
    int32_t mirrorH;
    int32_t mirrorV;
    int32_t mosaicSize;
    // Color effects (UV plane)
    float saturation;
} MetalDVECompositeUVParams;

MetalResult metal_dve_composite_y(MetalQueueRef queue, MetalPipelineRef pipeline,
    MetalBufferRef dst, MetalBufferRef src, const MetalDVECompositeYParams* params);

MetalResult metal_dve_composite_uv(MetalQueueRef queue, MetalPipelineRef pipeline,
    MetalBufferRef dstUV, MetalBufferRef srcUV, const MetalDVECompositeUVParams* params);

// --- 3D DVE composite kernels ---
typedef struct {
    uint32_t dstW, dstH, dstPitch;
    uint32_t srcW, srcH, srcPitch;
    float invMVP[16];   // row-major 4x4 inverse MVP matrix
    int32_t slotMinX, slotMinY, slotMaxX, slotMaxY;
    int32_t alpha256;
    int32_t shape;      // 0=plane, 1=cube, 2=page_turn
    int32_t lightEnabled;
    float lightX, lightY, lightZ;
    float ambient, specular;
    float srcAspect;
    int32_t reflectionEnabled;
    float reflectionOpacity, reflectionFade;
    // Page turn (shape=2)
    float curlProgress;
    float curlRadius;
} MetalDVE3DYParams;

typedef struct {
    uint32_t dstW, dstChromaH, dstPitch;
    uint32_t srcW, srcChromaH, srcPitch;
    float invMVP[16];
    int32_t slotMinCX, slotMinCY, slotMaxCX, slotMaxCY;
    int32_t alpha256;
    int32_t shape;
    int32_t lightEnabled;
    float lightX, lightY, lightZ;
    float ambient, specular;
    float srcAspect;
    int32_t reflectionEnabled;
    float reflectionOpacity, reflectionFade;
    // Page turn (shape=2)
    float curlProgress;
    float curlRadius;
} MetalDVE3DUVParams;

MetalResult metal_dve3d_composite_y(MetalQueueRef queue, MetalPipelineRef pipeline,
    MetalBufferRef dst, MetalBufferRef src, const MetalDVE3DYParams* params);

MetalResult metal_dve3d_composite_uv(MetalQueueRef queue, MetalPipelineRef pipeline,
    MetalBufferRef dstUV, MetalBufferRef srcUV, const MetalDVE3DUVParams* params);

// --- Gaussian blur kernels ---
typedef struct {
    uint32_t width;
    uint32_t height;
    uint32_t pitch;
    uint32_t radius;
} MetalBlurParams;

MetalResult metal_gaussian_blur_h(MetalQueueRef queue, MetalPipelineRef pipeline,
    MetalBufferRef tmpBuf, MetalBufferRef src, MetalBufferRef weights,
    const MetalBlurParams* params);

MetalResult metal_gaussian_blur_v(MetalQueueRef queue, MetalPipelineRef pipeline,
    MetalBufferRef dst, MetalBufferRef tmpBuf, MetalBufferRef weights,
    const MetalBlurParams* params);

// --- P210 (10-bit) Gaussian blur kernels ---
typedef struct {
    uint32_t width;
    uint32_t height;
    uint32_t pitch;
    uint32_t radius;
    uint32_t samplesPerRow;
} MetalBlurP210Params;

MetalResult metal_gaussian_blur_p210_h(MetalQueueRef queue, MetalPipelineRef pipeline,
    MetalBufferRef tmpBuf, MetalBufferRef src, MetalBufferRef weights,
    const MetalBlurP210Params* params);

MetalResult metal_gaussian_blur_p210_v(MetalQueueRef queue, MetalPipelineRef pipeline,
    MetalBufferRef dst, MetalBufferRef tmpBuf, MetalBufferRef weights,
    const MetalBlurP210Params* params);

#endif // SWITCHFRAME_METAL_BRIDGE_H
