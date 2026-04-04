#include <metal_stdlib>
using namespace metal;

// ============================================================================
// DVE (Digital Video Effect) composite shader
//
// Single-kernel-per-slot compositing with:
//   - Inverse 3x3 projective transform (output→source mapping)
//   - Bilinear texture sampling
//   - SDF-based rounded-corner border rendering with softness
//   - Analytical drop shadow with Gaussian-like falloff
//   - Crop with soft edges
//   - Opacity compositing (fixed-point alpha)
// ============================================================================

struct DVECompositeYParams {
    uint dstW;
    uint dstH;
    uint dstPitch;     // bytes per row of destination Y plane

    uint srcW;
    uint srcH;
    uint srcPitch;     // bytes per row of source Y plane

    // Inverse transform matrix (row-major 3x3, maps output→source)
    float m00, m01, m02;
    float m10, m11, m12;
    float m20, m21, m22;

    // Axis-aligned bounding box for early-out (output pixels)
    int slotMinX, slotMinY, slotMaxX, slotMaxY;

    // Opacity: 0-256 fixed-point
    int alpha256;

    // Crop in source pixels (applied after inverse transform)
    float cropTop, cropBottom, cropLeft, cropRight;
    float cropSoftness;

    // Border
    float borderWidth;
    uint borderColorY;
    float borderCornerRadius;
    float borderSoftness;

    // Shadow
    int shadowEnabled;
    float shadowOffsetX, shadowOffsetY;   // in source pixels
    float shadowBlur;
    int shadowAlpha256;
    uint shadowColorY;

    // Mirror / mosaic
    int mirrorH;
    int mirrorV;
    int mosaicSize;

    // Color effects (Y plane: brightness + contrast)
    float brightness;   // -1..1, 0 = no change
    float contrast;     // 0..3, 1 = no change
};

struct DVECompositeUVParams {
    uint dstW;         // full luma width (for transform)
    uint dstChromaH;   // half height
    uint dstPitch;     // UV plane pitch

    uint srcW;         // full luma width
    uint srcChromaH;
    uint srcPitch;

    // Same inverse transform (operates in luma space)
    float m00, m01, m02;
    float m10, m11, m12;
    float m20, m21, m22;

    // AABB (in chroma coordinates)
    int slotMinCX, slotMinCY, slotMaxCX, slotMaxCY;

    int alpha256;

    float cropTop, cropBottom, cropLeft, cropRight;
    float cropSoftness;

    float borderWidth;
    uint borderColorCb;
    uint borderColorCr;
    float borderCornerRadius;
    float borderSoftness;

    int shadowEnabled;
    float shadowOffsetX, shadowOffsetY;
    float shadowBlur;
    int shadowAlpha256;
    uint shadowColorCb;
    uint shadowColorCr;

    // Mirror / mosaic
    int mirrorH;
    int mirrorV;
    int mosaicSize;

    // Color effects (UV plane: saturation)
    float saturation;   // 0..2, 1 = no change
};

// ============================================================================
// Shared SDF helpers
// ============================================================================

// Compute signed distance to a rounded rectangle.
// p is relative to rectangle center; half is the half-extents; cr is corner radius.
// Returns negative inside, positive outside.
inline float roundedRectSDF(float2 p, float2 half_ext, float cr) {
    float2 d = abs(p) - (half_ext - float2(cr));
    return length(max(d, float2(0.0f))) + min(max(d.x, d.y), 0.0f) - cr;
}

// Approximate Gaussian falloff for shadow: exp(-0.5 * (x/sigma)^2)
// Using a smooth approximation that's cheaper than exp().
inline float gaussianFalloff(float dist, float sigma) {
    if (sigma <= 0.0f) return 0.0f;
    float t = dist / sigma;
    float t2 = t * t;
    // Approximation: 1 / (1 + t2/2 + t2*t2/8) matches Gaussian closely for |t| < 3
    return 1.0f / (1.0f + 0.5f * t2 + 0.125f * t2 * t2);
}

// Bilinear sample from Y plane (uint8)
inline float bilinearSampleY(device const uint8_t* src, float srcX, float srcY,
                              uint srcW, uint srcH, uint srcPitch) {
    // Clamp to valid source range
    srcX = max(0.0f, min(srcX, float(srcW - 1)));
    srcY = max(0.0f, min(srcY, float(srcH - 1)));

    int sx = int(srcX);
    int sy = int(srcY);
    float fx = srcX - float(sx);
    float fy = srcY - float(sy);

    uint sx1 = min(uint(sx + 1), srcW - 1);
    uint sy1 = min(uint(sy + 1), srcH - 1);

    float v00 = float(src[uint(sy)  * srcPitch + uint(sx)]);
    float v10 = float(src[uint(sy)  * srcPitch + sx1]);
    float v01 = float(src[sy1 * srcPitch + uint(sx)]);
    float v11 = float(src[sy1 * srcPitch + sx1]);

    float top = v00 + (v10 - v00) * fx;
    float bot = v01 + (v11 - v01) * fx;
    return top + (bot - top) * fy;
}

// Bilinear sample a single byte from interleaved NV12 UV plane at given chroma offset
inline float bilinearSampleUVChannel(device const uint8_t* srcUV, float srcCX, float srcCY,
                                      uint srcChromaW, uint srcChromaH, uint srcPitch,
                                      uint channelOffset) {
    srcCX = max(0.0f, min(srcCX, float(srcChromaW - 1)));
    srcCY = max(0.0f, min(srcCY, float(srcChromaH - 1)));

    int sx = int(srcCX);
    int sy = int(srcCY);
    float fx = srcCX - float(sx);
    float fy = srcCY - float(sy);

    uint sx1 = min(uint(sx + 1), srcChromaW - 1);
    uint sy1 = min(uint(sy + 1), srcChromaH - 1);

    float v00 = float(srcUV[uint(sy)  * srcPitch + uint(sx)  * 2 + channelOffset]);
    float v10 = float(srcUV[uint(sy)  * srcPitch + sx1 * 2 + channelOffset]);
    float v01 = float(srcUV[sy1 * srcPitch + uint(sx)  * 2 + channelOffset]);
    float v11 = float(srcUV[sy1 * srcPitch + sx1 * 2 + channelOffset]);

    float top = v00 + (v10 - v00) * fx;
    float bot = v01 + (v11 - v01) * fx;
    return top + (bot - top) * fy;
}

// ============================================================================
// Shared DVE logic: compute the color/alpha contribution for a single pixel
// given its source-space coordinate from the inverse transform.
//
// Returns: .x = value (Y or Cb or Cr), .y = alpha (0.0-1.0)
// ============================================================================
struct DVEPixelResult {
    float value;
    float alpha;
};

inline DVEPixelResult computeDVEPixel(
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

    float cr = min(borderCornerRadius, min(halfW, halfH));

    float2 p = float2(srcX - centerX, srcY - centerY);
    float dist = roundedRectSDF(p, float2(halfW, halfH), cr);

    // --- Shadow ---
    if (shadowEnabled && dist > 0.0f) {
        // Shadow SDF: same shape, but offset
        float2 shadowP = float2(srcX - shadowOffsetX - centerX,
                                srcY - shadowOffsetY - centerY);
        float shadowDist = roundedRectSDF(shadowP, float2(halfW, halfH), cr);

        if (shadowDist > 0.0f) {
            // Outside shadow shape: apply falloff
            float sigma = max(shadowBlur * 0.5f, 0.5f);
            float falloff = gaussianFalloff(shadowDist, sigma);
            result.value = shadowColor;
            result.alpha = shadowAlpha * falloff * slotAlpha;
            return result;
        } else {
            // Inside shadow shape but outside slot: shadow at full shadow alpha
            result.value = shadowColor;
            result.alpha = shadowAlpha * slotAlpha;
            return result;
        }
    }

    // --- Border ---
    if (borderWidth > 0.0f && dist > -borderWidth && dist <= 0.0f) {
        // In border zone: [-borderWidth, 0]
        float borderAlpha = 1.0f;

        // Outer edge softness (anti-aliasing at dist ≈ 0)
        if (borderSoftness > 0.0f) {
            borderAlpha *= saturate(-dist / borderSoftness);
        }
        // Inner edge softness (smooth transition to video)
        if (borderSoftness > 0.0f) {
            float innerEdge = saturate((dist + borderWidth) / borderSoftness);
            borderAlpha *= (1.0f - innerEdge) + innerEdge * 0.0f;
            // Actually, we want the border to be solid in its zone.
            // Softness at outer edge: fade from 1→0 as dist → 0
            // Softness at inner edge: fade from 1→0 as dist → -borderWidth
            // Recalculate:
            borderAlpha = saturate(-dist / max(borderSoftness, 0.001f));
            borderAlpha *= saturate((dist + borderWidth) / max(borderSoftness, 0.001f));
        }

        result.value = borderColor;
        result.alpha = borderAlpha * slotAlpha;
        return result;
    }

    // Outside slot entirely (and no shadow)
    if (dist > 0.0f) {
        // Anti-alias the outer edge if borderSoftness > 0
        if (borderSoftness > 0.0f) {
            // This handles anti-aliasing when there's no border
            float edgeAlpha = saturate(-dist / max(borderSoftness, 0.001f));
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
        float fadeLeft   = saturate((srcX - visLeft)   / cropSoftness);
        float fadeRight  = saturate((visRight - srcX)  / cropSoftness);
        float fadeTop    = saturate((srcY - visTop)    / cropSoftness);
        float fadeBottom = saturate((visBottom - srcY) / cropSoftness);
        videoAlpha = min(min(fadeLeft, fadeRight), min(fadeTop, fadeBottom));
    }

    // Border inner-edge softness transition (video fades in from border)
    if (borderWidth > 0.0f && borderSoftness > 0.0f && dist > -(borderWidth + borderSoftness)) {
        float innerFade = saturate((-dist - borderWidth) / max(borderSoftness, 0.001f));
        videoAlpha *= innerFade;
    }

    result.value = sampledValue;
    result.alpha = videoAlpha * slotAlpha;
    return result;
}

// ============================================================================
// dve_composite_y — Y plane kernel
// ============================================================================
kernel void dve_composite_y(
    device uint8_t* dst           [[buffer(0)]],
    device const uint8_t* src     [[buffer(1)]],
    constant DVECompositeYParams& params [[buffer(2)]],
    uint2 gid [[thread_position_in_grid]])
{
    int ox = int(gid.x);
    int oy = int(gid.y);

    // Bounds check against destination
    if (uint(ox) >= params.dstW || uint(oy) >= params.dstH) return;

    // Early-out: expand AABB by shadow + border extent
    float expand = params.borderWidth + params.borderSoftness;
    if (params.shadowEnabled) {
        expand = max(expand, abs(params.shadowOffsetX) + params.shadowBlur * 2.0f);
        expand = max(expand, abs(params.shadowOffsetY) + params.shadowBlur * 2.0f);
    }
    // The AABB is in output pixels; we need to expand it.
    // A conservative expand in output space based on the transform scale would be complex,
    // so we use a fixed pixel expansion that covers typical cases.
    int expandPx = int(expand + 1.0f);
    if (ox < params.slotMinX - expandPx || ox > params.slotMaxX + expandPx ||
        oy < params.slotMinY - expandPx || oy > params.slotMaxY + expandPx) {
        return;
    }

    // Inverse transform: output pixel → source coordinate
    float w = params.m20 * float(ox) + params.m21 * float(oy) + params.m22;
    // Perspective divide (guard against near-zero w)
    float invW = (abs(w) > 1e-6f) ? (1.0f / w) : 0.0f;
    float srcX = (params.m00 * float(ox) + params.m01 * float(oy) + params.m02) * invW;
    float srcY = (params.m10 * float(ox) + params.m11 * float(oy) + params.m12) * invW;

    // Mirror
    if (params.mirrorH) srcX = float(params.srcW) - 1.0f - srcX;
    if (params.mirrorV) srcY = float(params.srcH) - 1.0f - srcY;

    // Mosaic (quantize to block grid)
    if (params.mosaicSize > 0) {
        float ms = float(params.mosaicSize);
        srcX = floor(srcX / ms) * ms + ms * 0.5f;
        srcY = floor(srcY / ms) * ms + ms * 0.5f;
    }

    // Bilinear sample from source Y plane (used if pixel is inside the slot)
    float sampledY = bilinearSampleY(src, srcX, srcY,
                                      params.srcW, params.srcH, params.srcPitch);

    // Brightness + Contrast (Y plane)
    if (params.contrast != 1.0f || params.brightness != 0.0f) {
        float val = sampledY / 255.0f;
        val = (val - 0.5f) * params.contrast + 0.5f + params.brightness;
        val = clamp(val, 0.0f, 1.0f);
        sampledY = val * 255.0f;
    }

    // Compute DVE pixel
    float slotAlpha = float(params.alpha256) / 256.0f;
    float shadowAlpha = float(params.shadowAlpha256) / 256.0f;

    DVEPixelResult px = computeDVEPixel(
        srcX, srcY,
        float(params.srcW), float(params.srcH),
        params.cropTop, params.cropBottom, params.cropLeft, params.cropRight,
        params.cropSoftness,
        params.borderWidth, float(params.borderColorY), params.borderCornerRadius, params.borderSoftness,
        params.shadowEnabled, params.shadowOffsetX, params.shadowOffsetY,
        params.shadowBlur, shadowAlpha, float(params.shadowColorY),
        sampledY, slotAlpha);

    if (px.alpha < 0.002f) return;

    // Alpha-blend onto destination
    uint dstIdx = uint(oy) * params.dstPitch + uint(ox);
    int dstVal = int(dst[dstIdx]);
    int srcVal = int(px.value + 0.5f);
    int alpha256 = int(px.alpha * 256.0f + 0.5f);
    alpha256 = min(alpha256, 256);
    int inv = 256 - alpha256;
    dst[dstIdx] = uint8_t(clamp((dstVal * inv + srcVal * alpha256 + 128) >> 8, 0, 255));
}

// ============================================================================
// dve_composite_uv — NV12 interleaved chroma plane kernel
// ============================================================================
kernel void dve_composite_uv(
    device uint8_t* dstUV         [[buffer(0)]],
    device const uint8_t* srcUV   [[buffer(1)]],
    constant DVECompositeUVParams& params [[buffer(2)]],
    uint2 gid [[thread_position_in_grid]])
{
    int cx = int(gid.x);
    int cy = int(gid.y);

    // Bounds check against destination chroma dimensions
    uint dstChromaW = params.dstW / 2;
    if (uint(cx) >= dstChromaW || uint(cy) >= params.dstChromaH) return;

    // Early-out using chroma-space AABB
    float expand = params.borderWidth + params.borderSoftness;
    if (params.shadowEnabled) {
        expand = max(expand, abs(params.shadowOffsetX) + params.shadowBlur * 2.0f);
        expand = max(expand, abs(params.shadowOffsetY) + params.shadowBlur * 2.0f);
    }
    // Convert expansion from luma pixels to chroma pixels
    int expandCPx = int(expand * 0.5f + 1.0f);
    if (cx < params.slotMinCX - expandCPx || cx > params.slotMaxCX + expandCPx ||
        cy < params.slotMinCY - expandCPx || cy > params.slotMaxCY + expandCPx) {
        return;
    }

    // Map chroma grid position back to luma coordinates for the 2x2 block.
    // Average inverse transform from the 4 luma pixels in this chroma cell.
    float lumaX0 = float(cx * 2);
    float lumaY0 = float(cy * 2);

    // Compute inverse transform for all 4 corners of the 2x2 luma block
    float srcX = 0.0f;
    float srcY = 0.0f;

    for (int j = 0; j < 2; j++) {
        for (int i = 0; i < 2; i++) {
            float lx = lumaX0 + float(i);
            float ly = lumaY0 + float(j);
            float w = params.m20 * lx + params.m21 * ly + params.m22;
            float invW = (abs(w) > 1e-6f) ? (1.0f / w) : 0.0f;
            srcX += (params.m00 * lx + params.m01 * ly + params.m02) * invW;
            srcY += (params.m10 * lx + params.m11 * ly + params.m12) * invW;
        }
    }
    srcX *= 0.25f;
    srcY *= 0.25f;

    // Mirror (in luma coordinate space before chroma downscale)
    if (params.mirrorH) srcX = float(params.srcW) - 1.0f - srcX;
    if (params.mirrorV) srcY = float(params.srcChromaH * 2) - 1.0f - srcY;

    // Mosaic (in luma coordinate space before chroma downscale)
    if (params.mosaicSize > 0) {
        float ms = float(params.mosaicSize);
        srcX = floor(srcX / ms) * ms + ms * 0.5f;
        srcY = floor(srcY / ms) * ms + ms * 0.5f;
    }

    // Source chroma coordinates (half resolution)
    uint srcChromaW = params.srcW / 2;
    uint srcChromaH = params.srcChromaH;
    float srcCX = srcX * 0.5f;
    float srcCY = srcY * 0.5f;

    // Sample Cb and Cr via bilinear interpolation
    float sampledCb = bilinearSampleUVChannel(srcUV, srcCX, srcCY,
                                               srcChromaW, srcChromaH, params.srcPitch, 0);
    float sampledCr = bilinearSampleUVChannel(srcUV, srcCX, srcCY,
                                               srcChromaW, srcChromaH, params.srcPitch, 1);

    // Saturation (UV plane): scale Cb and Cr toward 128)
    if (params.saturation != 1.0f) {
        sampledCb = 128.0f + (sampledCb - 128.0f) * params.saturation;
        sampledCr = 128.0f + (sampledCr - 128.0f) * params.saturation;
        sampledCb = clamp(sampledCb, 0.0f, 255.0f);
        sampledCr = clamp(sampledCr, 0.0f, 255.0f);
    }

    // Compute DVE pixel for Cb channel
    float slotAlpha = float(params.alpha256) / 256.0f;
    float shadowAlpha = float(params.shadowAlpha256) / 256.0f;

    DVEPixelResult pxCb = computeDVEPixel(
        srcX, srcY,
        float(params.srcW), float(params.srcChromaH * 2),
        params.cropTop, params.cropBottom, params.cropLeft, params.cropRight,
        params.cropSoftness,
        params.borderWidth, float(params.borderColorCb), params.borderCornerRadius, params.borderSoftness,
        params.shadowEnabled, params.shadowOffsetX, params.shadowOffsetY,
        params.shadowBlur, shadowAlpha, float(params.shadowColorCb),
        sampledCb, slotAlpha);

    DVEPixelResult pxCr = computeDVEPixel(
        srcX, srcY,
        float(params.srcW), float(params.srcChromaH * 2),
        params.cropTop, params.cropBottom, params.cropLeft, params.cropRight,
        params.cropSoftness,
        params.borderWidth, float(params.borderColorCr), params.borderCornerRadius, params.borderSoftness,
        params.shadowEnabled, params.shadowOffsetX, params.shadowOffsetY,
        params.shadowBlur, shadowAlpha, float(params.shadowColorCr),
        sampledCr, slotAlpha);

    if (pxCb.alpha < 0.002f && pxCr.alpha < 0.002f) return;

    // Alpha-blend onto destination UV
    uint dstIdx = uint(cy) * params.dstPitch + uint(cx) * 2;
    if (dstIdx + 1 >= params.dstPitch * params.dstChromaH) return;

    // Cb
    {
        int alpha256 = min(int(pxCb.alpha * 256.0f + 0.5f), 256);
        int inv = 256 - alpha256;
        int dstVal = int(dstUV[dstIdx]);
        int srcVal = int(pxCb.value + 0.5f);
        dstUV[dstIdx] = uint8_t(clamp((dstVal * inv + srcVal * alpha256 + 128) >> 8, 0, 255));
    }

    // Cr
    {
        int alpha256 = min(int(pxCr.alpha * 256.0f + 0.5f), 256);
        int inv = 256 - alpha256;
        int dstVal = int(dstUV[dstIdx + 1]);
        int srcVal = int(pxCr.value + 0.5f);
        dstUV[dstIdx + 1] = uint8_t(clamp((dstVal * inv + srcVal * alpha256 + 128) >> 8, 0, 255));
    }
}
