#include "common.cuh"

// ============================================================================
// DVE (Digital Video Effect) composite CUDA kernels
//
// Port of the Metal dve.metal shader. Single-kernel-per-slot compositing with:
//   - Inverse 3x3 projective transform (output->source mapping)
//   - Bilinear texture sampling
//   - SDF-based rounded-corner border rendering with softness
//   - Analytical drop shadow with Gaussian-like falloff
//   - Crop with soft edges
//   - Opacity compositing (fixed-point alpha)
// ============================================================================

struct DVECompositeYParams {
    int dstW, dstH, dstPitch;
    int srcW, srcH, srcPitch;
    float m00, m01, m02, m10, m11, m12, m20, m21, m22;
    int slotMinX, slotMinY, slotMaxX, slotMaxY;
    int alpha256;
    float cropTop, cropBottom, cropLeft, cropRight, cropSoftness;
    float borderWidth;
    int borderColorY;
    float borderCornerRadius, borderSoftness;
    int shadowEnabled;
    float shadowOffsetX, shadowOffsetY, shadowBlur;
    int shadowAlpha256;
    int shadowColorY;
    // Mirror / mosaic
    int mirrorH;
    int mirrorV;
    int mosaicSize;
    // Color effects (Y plane)
    float brightness;
    float contrast;
};

struct DVECompositeUVParams {
    int dstW, dstChromaH, dstPitch;
    int srcW, srcChromaH, srcPitch;
    float m00, m01, m02, m10, m11, m12, m20, m21, m22;
    int slotMinCX, slotMinCY, slotMaxCX, slotMaxCY;
    int alpha256;
    float cropTop, cropBottom, cropLeft, cropRight, cropSoftness;
    float borderWidth;
    int borderColorCb, borderColorCr;
    float borderCornerRadius, borderSoftness;
    int shadowEnabled;
    float shadowOffsetX, shadowOffsetY, shadowBlur;
    int shadowAlpha256;
    int shadowColorCb, shadowColorCr;
    // Mirror / mosaic
    int mirrorH;
    int mirrorV;
    int mosaicSize;
    // Color effects (UV plane)
    float saturation;
};

// ============================================================================
// Device helper functions
// ============================================================================

// Clamp float to [0, 1]
__device__ __forceinline__ float saturate_f(float x) {
    return fminf(fmaxf(x, 0.0f), 1.0f);
}

// Signed distance to a rounded rectangle.
// p is relative to rectangle center; halfW/halfH are half-extents; cr is corner radius.
// Returns negative inside, positive outside.
__device__ float roundedRectSDF(float px, float py, float halfW, float halfH, float cr) {
    float dx = fabsf(px) - (halfW - cr);
    float dy = fabsf(py) - (halfH - cr);
    float dxc = fmaxf(dx, 0.0f);
    float dyc = fmaxf(dy, 0.0f);
    return sqrtf(dxc * dxc + dyc * dyc) + fminf(fmaxf(dx, dy), 0.0f) - cr;
}

// Approximate Gaussian falloff: exp(-0.5*(x/sigma)^2)
// Using rational approximation: 1 / (1 + t^2/2 + t^4/8)
__device__ float gaussianFalloff(float dist, float sigma) {
    if (sigma <= 0.0f) return 0.0f;
    float t = dist / sigma;
    float t2 = t * t;
    return 1.0f / (1.0f + 0.5f * t2 + 0.125f * t2 * t2);
}

// Bilinear sample from Y plane (uint8)
__device__ float bilinearSampleY(const uint8_t* src, float srcX, float srcY,
                                  int srcW, int srcH, int srcPitch) {
    srcX = fmaxf(0.0f, fminf(srcX, (float)(srcW - 1)));
    srcY = fmaxf(0.0f, fminf(srcY, (float)(srcH - 1)));

    int sx = (int)srcX;
    int sy = (int)srcY;
    float fx = srcX - (float)sx;
    float fy = srcY - (float)sy;

    int sx1 = min(sx + 1, srcW - 1);
    int sy1 = min(sy + 1, srcH - 1);

    float v00 = (float)src[sy  * srcPitch + sx];
    float v10 = (float)src[sy  * srcPitch + sx1];
    float v01 = (float)src[sy1 * srcPitch + sx];
    float v11 = (float)src[sy1 * srcPitch + sx1];

    float top = v00 + (v10 - v00) * fx;
    float bot = v01 + (v11 - v01) * fx;
    return top + (bot - top) * fy;
}

// Bilinear sample a single byte from interleaved NV12 UV plane at given chroma offset
// channel: 0=Cb, 1=Cr
__device__ float bilinearSampleUVChannel(const uint8_t* srcUV, float srcCX, float srcCY,
                                          int srcChromaW, int srcChromaH, int srcPitch,
                                          int channel) {
    srcCX = fmaxf(0.0f, fminf(srcCX, (float)(srcChromaW - 1)));
    srcCY = fmaxf(0.0f, fminf(srcCY, (float)(srcChromaH - 1)));

    int sx = (int)srcCX;
    int sy = (int)srcCY;
    float fx = srcCX - (float)sx;
    float fy = srcCY - (float)sy;

    int sx1 = min(sx + 1, srcChromaW - 1);
    int sy1 = min(sy + 1, srcChromaH - 1);

    float v00 = (float)srcUV[sy  * srcPitch + sx  * 2 + channel];
    float v10 = (float)srcUV[sy  * srcPitch + sx1 * 2 + channel];
    float v01 = (float)srcUV[sy1 * srcPitch + sx  * 2 + channel];
    float v11 = (float)srcUV[sy1 * srcPitch + sx1 * 2 + channel];

    float top = v00 + (v10 - v00) * fx;
    float bot = v01 + (v11 - v01) * fx;
    return top + (bot - top) * fy;
}

// ============================================================================
// Shared DVE logic: compute the color/alpha contribution for a single pixel
// given its source-space coordinate from the inverse transform.
// ============================================================================

struct DVEPixelResult {
    float value;
    float alpha;
};

__device__ DVEPixelResult computeDVEPixel(
    float srcX, float srcY,
    float srcW, float srcH,
    float cropTop, float cropBottom, float cropLeft, float cropRight,
    float cropSoftness,
    float borderWidth, float borderColor, float borderCornerRadius, float borderSoftness,
    int shadowEnabled, float shadowOffsetX, float shadowOffsetY,
    float shadowBlur, float shadowAlpha, float shadowColor,
    float sampledValue,
    float slotAlpha)
{
    DVEPixelResult result;
    result.value = 0.0f;
    result.alpha = 0.0f;

    // Effective visible rectangle after crop
    float visLeft   = cropLeft;
    float visRight  = srcW - cropRight;
    float visTop    = cropTop;
    float visBottom = srcH - cropBottom;

    float halfW = (visRight - visLeft) * 0.5f;
    float halfH = (visBottom - visTop) * 0.5f;

    if (halfW <= 0.0f || halfH <= 0.0f) return result;

    float centerX = visLeft + halfW;
    float centerY = visTop + halfH;

    float cr = fminf(borderCornerRadius, fminf(halfW, halfH));

    float px = srcX - centerX;
    float py = srcY - centerY;
    float dist = roundedRectSDF(px, py, halfW, halfH, cr);

    // --- Shadow ---
    if (shadowEnabled && dist > 0.0f) {
        float shadowPx = srcX - shadowOffsetX - centerX;
        float shadowPy = srcY - shadowOffsetY - centerY;
        float shadowDist = roundedRectSDF(shadowPx, shadowPy, halfW, halfH, cr);

        if (shadowDist > 0.0f) {
            float sigma = fmaxf(shadowBlur * 0.5f, 0.5f);
            float falloff = gaussianFalloff(shadowDist, sigma);
            result.value = shadowColor;
            result.alpha = shadowAlpha * falloff * slotAlpha;
            return result;
        } else {
            result.value = shadowColor;
            result.alpha = shadowAlpha * slotAlpha;
            return result;
        }
    }

    // --- Border ---
    if (borderWidth > 0.0f && dist > -borderWidth && dist <= 0.0f) {
        float borderAlpha = 1.0f;

        if (borderSoftness > 0.0f) {
            borderAlpha = saturate_f(-dist / fmaxf(borderSoftness, 0.001f));
            borderAlpha *= saturate_f((dist + borderWidth) / fmaxf(borderSoftness, 0.001f));
        }

        result.value = borderColor;
        result.alpha = borderAlpha * slotAlpha;
        return result;
    }

    // Outside slot entirely (and no shadow)
    if (dist > 0.0f) {
        if (borderSoftness > 0.0f) {
            float edgeAlpha = saturate_f(-dist / fmaxf(borderSoftness, 0.001f));
            if (edgeAlpha > 0.001f) {
                result.value = sampledValue;
                result.alpha = edgeAlpha * slotAlpha;
                return result;
            }
        }
        return result;
    }

    // --- Inside slot: sample video ---
    float videoAlpha = 1.0f;

    // Crop softness: fade near crop edges
    if (cropSoftness > 0.0f) {
        float fadeLeft   = saturate_f((srcX - visLeft)   / cropSoftness);
        float fadeRight  = saturate_f((visRight - srcX)  / cropSoftness);
        float fadeTop    = saturate_f((srcY - visTop)    / cropSoftness);
        float fadeBottom = saturate_f((visBottom - srcY) / cropSoftness);
        videoAlpha = fminf(fminf(fadeLeft, fadeRight), fminf(fadeTop, fadeBottom));
    }

    // Border inner-edge softness transition
    if (borderWidth > 0.0f && borderSoftness > 0.0f && dist > -(borderWidth + borderSoftness)) {
        float innerFade = saturate_f((-dist - borderWidth) / fmaxf(borderSoftness, 0.001f));
        videoAlpha *= innerFade;
    }

    result.value = sampledValue;
    result.alpha = videoAlpha * slotAlpha;
    return result;
}

// ============================================================================
// dve_composite_y — Y plane kernel
// ============================================================================
__global__ void dve_composite_y_kernel(
    uint8_t* __restrict__ dst,
    const uint8_t* __restrict__ src,
    DVECompositeYParams p)
{
    int ox = blockIdx.x * blockDim.x + threadIdx.x;
    int oy = blockIdx.y * blockDim.y + threadIdx.y;

    // Bounds check against destination
    if (ox >= p.dstW || oy >= p.dstH) return;

    // Early-out: expand AABB by shadow + border extent
    float expand = p.borderWidth + p.borderSoftness;
    if (p.shadowEnabled) {
        expand = fmaxf(expand, fabsf(p.shadowOffsetX) + p.shadowBlur * 2.0f);
        expand = fmaxf(expand, fabsf(p.shadowOffsetY) + p.shadowBlur * 2.0f);
    }
    int expandPx = (int)(expand + 1.0f);
    if (ox < p.slotMinX - expandPx || ox > p.slotMaxX + expandPx ||
        oy < p.slotMinY - expandPx || oy > p.slotMaxY + expandPx) {
        return;
    }

    // Inverse transform: output pixel -> source coordinate
    float w = p.m20 * (float)ox + p.m21 * (float)oy + p.m22;
    float invW = (fabsf(w) > 1e-6f) ? (1.0f / w) : 0.0f;
    float srcX = (p.m00 * (float)ox + p.m01 * (float)oy + p.m02) * invW;
    float srcY = (p.m10 * (float)ox + p.m11 * (float)oy + p.m12) * invW;

    // Mirror
    if (p.mirrorH) srcX = (float)(p.srcW) - 1.0f - srcX;
    if (p.mirrorV) srcY = (float)(p.srcH) - 1.0f - srcY;

    // Mosaic (quantize to block grid)
    if (p.mosaicSize > 0) {
        float ms = (float)p.mosaicSize;
        srcX = floorf(srcX / ms) * ms + ms * 0.5f;
        srcY = floorf(srcY / ms) * ms + ms * 0.5f;
    }

    // Bilinear sample from source Y plane
    float sampledY = bilinearSampleY(src, srcX, srcY, p.srcW, p.srcH, p.srcPitch);

    // Brightness + Contrast (Y plane)
    if (p.contrast != 1.0f || p.brightness != 0.0f) {
        float val = sampledY / 255.0f;
        val = (val - 0.5f) * p.contrast + 0.5f + p.brightness;
        val = fminf(fmaxf(val, 0.0f), 1.0f);
        sampledY = val * 255.0f;
    }

    // Compute DVE pixel
    float slotAlpha = (float)p.alpha256 / 256.0f;
    float shadowAlpha = (float)p.shadowAlpha256 / 256.0f;

    DVEPixelResult px = computeDVEPixel(
        srcX, srcY,
        (float)p.srcW, (float)p.srcH,
        p.cropTop, p.cropBottom, p.cropLeft, p.cropRight,
        p.cropSoftness,
        p.borderWidth, (float)p.borderColorY, p.borderCornerRadius, p.borderSoftness,
        p.shadowEnabled, p.shadowOffsetX, p.shadowOffsetY,
        p.shadowBlur, shadowAlpha, (float)p.shadowColorY,
        sampledY, slotAlpha);

    if (px.alpha < 0.002f) return;

    // Alpha-blend onto destination
    int dstIdx = oy * p.dstPitch + ox;
    int dstVal = (int)dst[dstIdx];
    int srcVal = (int)(px.value + 0.5f);
    int alpha256 = (int)(px.alpha * 256.0f + 0.5f);
    alpha256 = min(alpha256, 256);
    int inv = 256 - alpha256;
    int blended = (dstVal * inv + srcVal * alpha256 + 128) >> 8;
    dst[dstIdx] = (uint8_t)min(max(blended, 0), 255);
}

// ============================================================================
// dve_composite_uv — NV12 interleaved chroma plane kernel
// ============================================================================
__global__ void dve_composite_uv_kernel(
    uint8_t* __restrict__ dstUV,
    const uint8_t* __restrict__ srcUV,
    DVECompositeUVParams p)
{
    int cx = blockIdx.x * blockDim.x + threadIdx.x;
    int cy = blockIdx.y * blockDim.y + threadIdx.y;

    // Bounds check against destination chroma dimensions
    int dstChromaW = p.dstW / 2;
    if (cx >= dstChromaW || cy >= p.dstChromaH) return;

    // Early-out using chroma-space AABB
    float expand = p.borderWidth + p.borderSoftness;
    if (p.shadowEnabled) {
        expand = fmaxf(expand, fabsf(p.shadowOffsetX) + p.shadowBlur * 2.0f);
        expand = fmaxf(expand, fabsf(p.shadowOffsetY) + p.shadowBlur * 2.0f);
    }
    int expandCPx = (int)(expand * 0.5f + 1.0f);
    if (cx < p.slotMinCX - expandCPx || cx > p.slotMaxCX + expandCPx ||
        cy < p.slotMinCY - expandCPx || cy > p.slotMaxCY + expandCPx) {
        return;
    }

    // Map chroma grid position back to luma coordinates for the 2x2 block.
    // Average inverse transform from the 4 luma pixels in this chroma cell.
    float lumaX0 = (float)(cx * 2);
    float lumaY0 = (float)(cy * 2);

    float srcX = 0.0f;
    float srcY = 0.0f;

    for (int j = 0; j < 2; j++) {
        for (int i = 0; i < 2; i++) {
            float lx = lumaX0 + (float)i;
            float ly = lumaY0 + (float)j;
            float w = p.m20 * lx + p.m21 * ly + p.m22;
            float invW = (fabsf(w) > 1e-6f) ? (1.0f / w) : 0.0f;
            srcX += (p.m00 * lx + p.m01 * ly + p.m02) * invW;
            srcY += (p.m10 * lx + p.m11 * ly + p.m12) * invW;
        }
    }
    srcX *= 0.25f;
    srcY *= 0.25f;

    // Mirror (in luma coordinate space before chroma downscale)
    if (p.mirrorH) srcX = (float)(p.srcW) - 1.0f - srcX;
    if (p.mirrorV) srcY = (float)(p.srcChromaH * 2) - 1.0f - srcY;

    // Mosaic (in luma coordinate space before chroma downscale)
    if (p.mosaicSize > 0) {
        float ms = (float)p.mosaicSize;
        srcX = floorf(srcX / ms) * ms + ms * 0.5f;
        srcY = floorf(srcY / ms) * ms + ms * 0.5f;
    }

    // Source chroma coordinates (half resolution)
    int srcChromaW = p.srcW / 2;
    int srcChromaH = p.srcChromaH;
    float srcCX = srcX * 0.5f;
    float srcCY = srcY * 0.5f;

    // Sample Cb and Cr via bilinear interpolation
    float sampledCb = bilinearSampleUVChannel(srcUV, srcCX, srcCY,
                                               srcChromaW, srcChromaH, p.srcPitch, 0);
    float sampledCr = bilinearSampleUVChannel(srcUV, srcCX, srcCY,
                                               srcChromaW, srcChromaH, p.srcPitch, 1);

    // Saturation (UV plane): scale Cb and Cr toward 128
    if (p.saturation != 1.0f) {
        sampledCb = 128.0f + (sampledCb - 128.0f) * p.saturation;
        sampledCr = 128.0f + (sampledCr - 128.0f) * p.saturation;
        sampledCb = fminf(fmaxf(sampledCb, 0.0f), 255.0f);
        sampledCr = fminf(fmaxf(sampledCr, 0.0f), 255.0f);
    }

    // Compute DVE pixel for Cb channel
    float slotAlpha = (float)p.alpha256 / 256.0f;
    float shadowAlpha = (float)p.shadowAlpha256 / 256.0f;

    DVEPixelResult pxCb = computeDVEPixel(
        srcX, srcY,
        (float)p.srcW, (float)(p.srcChromaH * 2),
        p.cropTop, p.cropBottom, p.cropLeft, p.cropRight,
        p.cropSoftness,
        p.borderWidth, (float)p.borderColorCb, p.borderCornerRadius, p.borderSoftness,
        p.shadowEnabled, p.shadowOffsetX, p.shadowOffsetY,
        p.shadowBlur, shadowAlpha, (float)p.shadowColorCb,
        sampledCb, slotAlpha);

    DVEPixelResult pxCr = computeDVEPixel(
        srcX, srcY,
        (float)p.srcW, (float)(p.srcChromaH * 2),
        p.cropTop, p.cropBottom, p.cropLeft, p.cropRight,
        p.cropSoftness,
        p.borderWidth, (float)p.borderColorCr, p.borderCornerRadius, p.borderSoftness,
        p.shadowEnabled, p.shadowOffsetX, p.shadowOffsetY,
        p.shadowBlur, shadowAlpha, (float)p.shadowColorCr,
        sampledCr, slotAlpha);

    if (pxCb.alpha < 0.002f && pxCr.alpha < 0.002f) return;

    // Alpha-blend onto destination UV
    int dstIdx = cy * p.dstPitch + cx * 2;
    if (dstIdx + 1 >= p.dstPitch * p.dstChromaH) return;

    // Cb
    {
        int alpha256 = min((int)(pxCb.alpha * 256.0f + 0.5f), 256);
        int inv = 256 - alpha256;
        int dstVal = (int)dstUV[dstIdx];
        int srcVal = (int)(pxCb.value + 0.5f);
        int blended = (dstVal * inv + srcVal * alpha256 + 128) >> 8;
        dstUV[dstIdx] = (uint8_t)min(max(blended, 0), 255);
    }

    // Cr
    {
        int alpha256 = min((int)(pxCr.alpha * 256.0f + 0.5f), 256);
        int inv = 256 - alpha256;
        int dstVal = (int)dstUV[dstIdx + 1];
        int srcVal = (int)(pxCr.value + 0.5f);
        int blended = (dstVal * inv + srcVal * alpha256 + 128) >> 8;
        dstUV[dstIdx + 1] = (uint8_t)min(max(blended, 0), 255);
    }
}

// ============================================================================
// P210 (10-bit 4:2:2) DVE composite kernels
// ============================================================================

// Bilinear sample from Y plane (uint16, 10-bit range [0, 1023])
__device__ float bilinearSampleY_p210(const uint16_t* src, float srcX, float srcY,
                                       int srcW, int srcH, int pitch16) {
    srcX = fmaxf(0.0f, fminf(srcX, (float)(srcW - 1)));
    srcY = fmaxf(0.0f, fminf(srcY, (float)(srcH - 1)));

    int sx = (int)srcX;
    int sy = (int)srcY;
    float fx = srcX - (float)sx;
    float fy = srcY - (float)sy;

    int sx1 = min(sx + 1, srcW - 1);
    int sy1 = min(sy + 1, srcH - 1);

    float v00 = (float)src[sy  * pitch16 + sx];
    float v10 = (float)src[sy  * pitch16 + sx1];
    float v01 = (float)src[sy1 * pitch16 + sx];
    float v11 = (float)src[sy1 * pitch16 + sx1];

    float top = v00 + (v10 - v00) * fx;
    float bot = v01 + (v11 - v01) * fx;
    return top + (bot - top) * fy;
}

// Bilinear sample a single channel from interleaved P210 UV plane (uint16)
// channel: 0=Cb, 1=Cr
__device__ float bilinearSampleUVChannel_p210(const uint16_t* srcUV, float srcCX, float srcCY,
                                               int srcChromaW, int srcChromaH, int pitch16,
                                               int channel) {
    srcCX = fmaxf(0.0f, fminf(srcCX, (float)(srcChromaW - 1)));
    srcCY = fmaxf(0.0f, fminf(srcCY, (float)(srcChromaH - 1)));

    int sx = (int)srcCX;
    int sy = (int)srcCY;
    float fx = srcCX - (float)sx;
    float fy = srcCY - (float)sy;

    int sx1 = min(sx + 1, srcChromaW - 1);
    int sy1 = min(sy + 1, srcChromaH - 1);

    float v00 = (float)srcUV[sy  * pitch16 + sx  * 2 + channel];
    float v10 = (float)srcUV[sy  * pitch16 + sx1 * 2 + channel];
    float v01 = (float)srcUV[sy1 * pitch16 + sx  * 2 + channel];
    float v11 = (float)srcUV[sy1 * pitch16 + sx1 * 2 + channel];

    float top = v00 + (v10 - v00) * fx;
    float bot = v01 + (v11 - v01) * fx;
    return top + (bot - top) * fy;
}

// ============================================================================
// dve_composite_y_p210 — P210 Y plane kernel (10-bit uint16)
// ============================================================================
__global__ void dve_composite_y_p210_kernel(
    uint16_t* __restrict__ dst,
    const uint16_t* __restrict__ src,
    DVECompositeYParams p)
{
    int ox = blockIdx.x * blockDim.x + threadIdx.x;
    int oy = blockIdx.y * blockDim.y + threadIdx.y;

    if (ox >= p.dstW || oy >= p.dstH) return;

    // pitch16 = pitch in bytes / 2 (stride in uint16 elements)
    int dstPitch16 = p.dstPitch / 2;
    int srcPitch16 = p.srcPitch / 2;

    // Early-out: expand AABB by shadow + border extent
    float expand = p.borderWidth + p.borderSoftness;
    if (p.shadowEnabled) {
        expand = fmaxf(expand, fabsf(p.shadowOffsetX) + p.shadowBlur * 2.0f);
        expand = fmaxf(expand, fabsf(p.shadowOffsetY) + p.shadowBlur * 2.0f);
    }
    int expandPx = (int)(expand + 1.0f);
    if (ox < p.slotMinX - expandPx || ox > p.slotMaxX + expandPx ||
        oy < p.slotMinY - expandPx || oy > p.slotMaxY + expandPx) {
        return;
    }

    // Inverse transform: output pixel -> source coordinate
    float w = p.m20 * (float)ox + p.m21 * (float)oy + p.m22;
    float invW = (fabsf(w) > 1e-6f) ? (1.0f / w) : 0.0f;
    float srcX = (p.m00 * (float)ox + p.m01 * (float)oy + p.m02) * invW;
    float srcY = (p.m10 * (float)ox + p.m11 * (float)oy + p.m12) * invW;

    // Mirror
    if (p.mirrorH) srcX = (float)(p.srcW) - 1.0f - srcX;
    if (p.mirrorV) srcY = (float)(p.srcH) - 1.0f - srcY;

    // Mosaic
    if (p.mosaicSize > 0) {
        float ms = (float)p.mosaicSize;
        srcX = floorf(srcX / ms) * ms + ms * 0.5f;
        srcY = floorf(srcY / ms) * ms + ms * 0.5f;
    }

    // Bilinear sample from source Y plane (10-bit)
    float sampledY = bilinearSampleY_p210(src, srcX, srcY, p.srcW, p.srcH, srcPitch16);

    // Brightness + Contrast: normalize to [0, 1023]
    if (p.contrast != 1.0f || p.brightness != 0.0f) {
        float val = sampledY / 1023.0f;
        val = (val - 0.5f) * p.contrast + 0.5f + p.brightness;
        val = fminf(fmaxf(val, 0.0f), 1.0f);
        sampledY = val * 1023.0f;
    }

    // Compute DVE pixel
    float slotAlpha = (float)p.alpha256 / 256.0f;
    float shadowAlpha = (float)p.shadowAlpha256 / 256.0f;

    DVEPixelResult px = computeDVEPixel(
        srcX, srcY,
        (float)p.srcW, (float)p.srcH,
        p.cropTop, p.cropBottom, p.cropLeft, p.cropRight,
        p.cropSoftness,
        p.borderWidth, (float)p.borderColorY, p.borderCornerRadius, p.borderSoftness,
        p.shadowEnabled, p.shadowOffsetX, p.shadowOffsetY,
        p.shadowBlur, shadowAlpha, (float)p.shadowColorY,
        sampledY, slotAlpha);

    if (px.alpha < 0.002f) return;

    // Alpha-blend onto destination (10-bit)
    int dstIdx = oy * dstPitch16 + ox;
    int dstVal = (int)dst[dstIdx];
    int srcVal = (int)(px.value + 0.5f);
    int alpha256 = (int)(px.alpha * 256.0f + 0.5f);
    alpha256 = min(alpha256, 256);
    int inv = 256 - alpha256;
    int blended = (dstVal * inv + srcVal * alpha256 + 128) >> 8;
    dst[dstIdx] = (uint16_t)min(max(blended, 0), 1023);
}

// ============================================================================
// dve_composite_uv_p210 — P210 interleaved chroma plane kernel (10-bit, 4:2:2)
// ============================================================================
__global__ void dve_composite_uv_p210_kernel(
    uint16_t* __restrict__ dstUV,
    const uint16_t* __restrict__ srcUV,
    DVECompositeUVParams p)
{
    int cx = blockIdx.x * blockDim.x + threadIdx.x;
    int cy = blockIdx.y * blockDim.y + threadIdx.y;

    // Bounds check against destination chroma dimensions
    int dstChromaW = p.dstW / 2;
    if (cx >= dstChromaW || cy >= p.dstChromaH) return;

    // pitch16 = pitch in bytes / 2 (stride in uint16 elements)
    int dstPitch16 = p.dstPitch / 2;
    int srcPitch16 = p.srcPitch / 2;

    // Early-out using chroma-space AABB
    float expand = p.borderWidth + p.borderSoftness;
    if (p.shadowEnabled) {
        expand = fmaxf(expand, fabsf(p.shadowOffsetX) + p.shadowBlur * 2.0f);
        expand = fmaxf(expand, fabsf(p.shadowOffsetY) + p.shadowBlur * 2.0f);
    }
    int expandCPx = (int)(expand * 0.5f + 1.0f);
    if (cx < p.slotMinCX - expandCPx || cx > p.slotMaxCX + expandCPx ||
        cy < p.slotMinCY - expandCPx || cy > p.slotMaxCY + expandCPx) {
        return;
    }

    // P210 4:2:2: chroma has full vertical resolution.
    // Map chroma grid position to luma coordinates.
    // Horizontally: each chroma sample covers 2 luma columns.
    // Vertically: 1:1 mapping (no subsampling).
    float lumaX0 = (float)(cx * 2);
    float lumaY = (float)cy;

    // Average 2 horizontal luma pixels for chroma position
    float srcX = 0.0f;
    float srcY = 0.0f;

    for (int i = 0; i < 2; i++) {
        float lx = lumaX0 + (float)i;
        float w = p.m20 * lx + p.m21 * lumaY + p.m22;
        float invW = (fabsf(w) > 1e-6f) ? (1.0f / w) : 0.0f;
        srcX += (p.m00 * lx + p.m01 * lumaY + p.m02) * invW;
        srcY += (p.m10 * lx + p.m11 * lumaY + p.m12) * invW;
    }
    srcX *= 0.5f;
    srcY *= 0.5f;

    // Mirror (in luma coordinate space)
    if (p.mirrorH) srcX = (float)(p.srcW) - 1.0f - srcX;
    if (p.mirrorV) srcY = (float)(p.srcChromaH) - 1.0f - srcY;

    // Mosaic (in luma coordinate space)
    if (p.mosaicSize > 0) {
        float ms = (float)p.mosaicSize;
        srcX = floorf(srcX / ms) * ms + ms * 0.5f;
        srcY = floorf(srcY / ms) * ms + ms * 0.5f;
    }

    // Source chroma coordinates: half horizontal, full vertical
    int srcChromaW = p.srcW / 2;
    int srcChromaH = p.srcChromaH;
    float srcCX = srcX * 0.5f;
    float srcCY = srcY;  // 4:2:2: no vertical subsampling

    // Sample Cb and Cr via bilinear interpolation (10-bit)
    float sampledCb = bilinearSampleUVChannel_p210(srcUV, srcCX, srcCY,
                                                    srcChromaW, srcChromaH, srcPitch16, 0);
    float sampledCr = bilinearSampleUVChannel_p210(srcUV, srcCX, srcCY,
                                                    srcChromaW, srcChromaH, srcPitch16, 1);

    // Saturation: neutral chroma is 512 for 10-bit
    if (p.saturation != 1.0f) {
        sampledCb = 512.0f + (sampledCb - 512.0f) * p.saturation;
        sampledCr = 512.0f + (sampledCr - 512.0f) * p.saturation;
        sampledCb = fminf(fmaxf(sampledCb, 0.0f), 1023.0f);
        sampledCr = fminf(fmaxf(sampledCr, 0.0f), 1023.0f);
    }

    // Compute DVE pixel for Cb channel
    // For P210 4:2:2: srcH in computeDVEPixel = srcChromaH (full height)
    float slotAlpha = (float)p.alpha256 / 256.0f;
    float shadowAlpha = (float)p.shadowAlpha256 / 256.0f;

    DVEPixelResult pxCb = computeDVEPixel(
        srcX, srcY,
        (float)p.srcW, (float)p.srcChromaH,
        p.cropTop, p.cropBottom, p.cropLeft, p.cropRight,
        p.cropSoftness,
        p.borderWidth, (float)p.borderColorCb, p.borderCornerRadius, p.borderSoftness,
        p.shadowEnabled, p.shadowOffsetX, p.shadowOffsetY,
        p.shadowBlur, shadowAlpha, (float)p.shadowColorCb,
        sampledCb, slotAlpha);

    DVEPixelResult pxCr = computeDVEPixel(
        srcX, srcY,
        (float)p.srcW, (float)p.srcChromaH,
        p.cropTop, p.cropBottom, p.cropLeft, p.cropRight,
        p.cropSoftness,
        p.borderWidth, (float)p.borderColorCr, p.borderCornerRadius, p.borderSoftness,
        p.shadowEnabled, p.shadowOffsetX, p.shadowOffsetY,
        p.shadowBlur, shadowAlpha, (float)p.shadowColorCr,
        sampledCr, slotAlpha);

    if (pxCb.alpha < 0.002f && pxCr.alpha < 0.002f) return;

    // Alpha-blend onto destination UV (10-bit)
    int dstIdx = cy * dstPitch16 + cx * 2;
    if (dstIdx + 1 >= dstPitch16 * p.dstChromaH) return;

    // Cb
    {
        int alpha256 = min((int)(pxCb.alpha * 256.0f + 0.5f), 256);
        int inv = 256 - alpha256;
        int dstVal = (int)dstUV[dstIdx];
        int srcVal = (int)(pxCb.value + 0.5f);
        int blended = (dstVal * inv + srcVal * alpha256 + 128) >> 8;
        dstUV[dstIdx] = (uint16_t)min(max(blended, 0), 1023);
    }

    // Cr
    {
        int alpha256 = min((int)(pxCr.alpha * 256.0f + 0.5f), 256);
        int inv = 256 - alpha256;
        int dstVal = (int)dstUV[dstIdx + 1];
        int srcVal = (int)(pxCr.value + 0.5f);
        int blended = (dstVal * inv + srcVal * alpha256 + 128) >> 8;
        dstUV[dstIdx + 1] = (uint16_t)min(max(blended, 0), 1023);
    }
}

// ============================================================================
// Extern C wrappers
// ============================================================================

extern "C" {

cudaError_t dve_composite_y(
    uint8_t* dst, const uint8_t* src,
    DVECompositeYParams params, cudaStream_t stream)
{
    dim3 block(BLOCK_DIM_X, BLOCK_DIM_Y);
    dim3 grid((params.dstW + block.x - 1) / block.x,
              (params.dstH + block.y - 1) / block.y);
    dve_composite_y_kernel<<<grid, block, 0, stream>>>(dst, src, params);
    return cudaGetLastError();
}

cudaError_t dve_composite_uv(
    uint8_t* dstUV, const uint8_t* srcUV,
    DVECompositeUVParams params, cudaStream_t stream)
{
    int dstChromaW = params.dstW / 2;
    dim3 block(BLOCK_DIM_X, BLOCK_DIM_Y);
    dim3 grid((dstChromaW + block.x - 1) / block.x,
              (params.dstChromaH + block.y - 1) / block.y);
    dve_composite_uv_kernel<<<grid, block, 0, stream>>>(dstUV, srcUV, params);
    return cudaGetLastError();
}

cudaError_t dve_composite_y_p210(
    uint8_t* dst, const uint8_t* src,
    DVECompositeYParams params, cudaStream_t stream)
{
    dim3 block(BLOCK_DIM_X, BLOCK_DIM_Y);
    dim3 grid((params.dstW + block.x - 1) / block.x,
              (params.dstH + block.y - 1) / block.y);
    dve_composite_y_p210_kernel<<<grid, block, 0, stream>>>(
        (uint16_t*)dst, (const uint16_t*)src, params);
    return cudaGetLastError();
}

cudaError_t dve_composite_uv_p210(
    uint8_t* dstUV, const uint8_t* srcUV,
    DVECompositeUVParams params, cudaStream_t stream)
{
    int dstChromaW = params.dstW / 2;
    dim3 block(BLOCK_DIM_X, BLOCK_DIM_Y);
    dim3 grid((dstChromaW + block.x - 1) / block.x,
              (params.dstChromaH + block.y - 1) / block.y);
    dve_composite_uv_p210_kernel<<<grid, block, 0, stream>>>(
        (uint16_t*)dstUV, (const uint16_t*)srcUV, params);
    return cudaGetLastError();
}

} // extern "C"
