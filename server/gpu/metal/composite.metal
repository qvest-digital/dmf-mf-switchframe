#include <metal_stdlib>
using namespace metal;

struct PIPCompositeYParams {
    uint dstW;
    uint dstH;
    uint dstPitch;
    uint srcW;
    uint srcH;
    uint srcPitch;
    int rectX;
    int rectY;
    int rectW;
    int rectH;
    int alpha256;
    int cropX;
    int cropY;
    int cropW;
    int cropH;
};

struct PIPCompositeUVParams {
    uint dstW;
    uint dstChromaH;
    uint dstPitch;
    uint srcW;
    uint srcChromaH;
    uint srcPitch;
    int rectX;
    int rectY;
    int rectCW;
    int rectCH;
    int alpha256;
    int cropX;
    int cropY;
    int cropCW;
    int cropCH;
};

struct BorderParams {
    uint dstW;
    uint dstH;
    uint dstPitch;
    int rectX;
    int rectY;
    int rectW;
    int rectH;
    int outerX;
    int outerY;
    int outerW;
    int outerH;
    int thickness;
    uint8_t colorY;
    uint8_t colorCb;
    uint8_t colorCr;
};

struct FillRectParams {
    uint dstW;
    uint dstH;
    uint dstPitch;
    int rectX;
    int rectY;
    int rectW;
    int rectH;
    uint8_t colorY;
    uint8_t colorCb;
    uint8_t colorCr;
};

// PIP composite Y plane: scale source (or source crop region) and place into destination region
kernel void pip_composite_y(
    device uint8_t* dst           [[buffer(0)]],
    device const uint8_t* src     [[buffer(1)]],
    constant PIPCompositeYParams& params [[buffer(2)]],
    uint2 gid [[thread_position_in_grid]])
{
    if (gid.x >= uint(params.rectW) || gid.y >= uint(params.rectH)) return;

    int dx = params.rectX + int(gid.x);
    int dy = params.rectY + int(gid.y);
    if (dx >= int(params.dstW) || dy >= int(params.dstH) || dx < 0 || dy < 0) return;

    // Determine source region: use crop rect if specified, otherwise full source.
    int srcRegionX = (params.cropW > 0) ? params.cropX : 0;
    int srcRegionY = (params.cropW > 0) ? params.cropY : 0;
    int srcRegionW = (params.cropW > 0) ? params.cropW : int(params.srcW);
    int srcRegionH = (params.cropW > 0) ? params.cropH : int(params.srcH);

    // Map local rect coords to source crop region coords
    float srcXf = float(srcRegionX) + float(gid.x) * float(srcRegionW - 1) / float(max(params.rectW - 1, 1));
    float srcYf = float(srcRegionY) + float(gid.y) * float(srcRegionH - 1) / float(max(params.rectH - 1, 1));
    int sx = int(srcXf);
    int sy = int(srcYf);
    float fx = srcXf - float(sx);
    float fy = srcYf - float(sy);

    uint sx1 = min(uint(sx + 1), params.srcW - 1);
    uint sy1 = min(uint(sy + 1), params.srcH - 1);

    int v00 = int(src[uint(sy) * params.srcPitch + uint(sx)]);
    int v10 = int(src[uint(sy) * params.srcPitch + sx1]);
    int v01 = int(src[sy1 * params.srcPitch + uint(sx)]);
    int v11 = int(src[sy1 * params.srcPitch + sx1]);

    float top = float(v00) + float(v10 - v00) * fx;
    float bot = float(v01) + float(v11 - v01) * fx;
    int val = int(top + (bot - top) * fy + 0.5f);

    uint dstIdx = uint(dy) * params.dstPitch + uint(dx);
    if (params.alpha256 >= 256) {
        dst[dstIdx] = uint8_t(val);
    } else {
        int inv = 256 - params.alpha256;
        dst[dstIdx] = uint8_t((int(dst[dstIdx]) * inv + val * params.alpha256 + 128) >> 8);
    }
}

// PIP composite UV plane (NV12 interleaved, half resolution)
kernel void pip_composite_uv(
    device uint8_t* dstUV         [[buffer(0)]],
    device const uint8_t* srcUV   [[buffer(1)]],
    constant PIPCompositeUVParams& params [[buffer(2)]],
    uint2 gid [[thread_position_in_grid]])
{
    if (gid.x >= uint(params.rectCW) || gid.y >= uint(params.rectCH)) return;

    int dx = params.rectX + int(gid.x) * 2;
    int dy = params.rectY / 2 + int(gid.y);
    if (dx + 1 >= int(params.dstW) || dy >= int(params.dstChromaH) || dx < 0 || dy < 0) return;

    // Determine source chroma region: use crop rect if specified, otherwise full source.
    int srcChromaX = (params.cropCW > 0) ? params.cropX : 0;
    int srcChromaY = (params.cropCW > 0) ? params.cropY : 0;
    int srcChromaW = (params.cropCW > 0) ? params.cropCW : int(params.srcW / 2);
    int srcChromaH = (params.cropCW > 0) ? params.cropCH : int(params.srcChromaH);

    float srcXf = float(srcChromaX) + float(gid.x) * float(srcChromaW - 1) / float(max(params.rectCW - 1, 1));
    float srcYf = float(srcChromaY) + float(gid.y) * float(srcChromaH - 1) / float(max(params.rectCH - 1, 1));
    int sx = min(int(srcXf), int(params.srcW / 2 - 1));
    int sy = min(int(srcYf), int(params.srcChromaH - 1));

    uint srcIdx = uint(sy) * params.srcPitch + uint(sx) * 2;
    uint dstIdx = uint(dy) * params.dstPitch + uint(params.rectX / 2) * 2 + gid.x * 2;
    if (dstIdx + 1 >= params.dstPitch * params.dstChromaH) return;

    if (params.alpha256 >= 256) {
        dstUV[dstIdx]     = srcUV[srcIdx];
        dstUV[dstIdx + 1] = srcUV[srcIdx + 1];
    } else {
        int inv = 256 - params.alpha256;
        dstUV[dstIdx]     = uint8_t((int(dstUV[dstIdx])     * inv + int(srcUV[srcIdx])     * params.alpha256 + 128) >> 8);
        dstUV[dstIdx + 1] = uint8_t((int(dstUV[dstIdx + 1]) * inv + int(srcUV[srcIdx + 1]) * params.alpha256 + 128) >> 8);
    }
}

// Draw border around a rectangle
// Thread indices are relative to the outer bounding box origin (outerX, outerY).
kernel void draw_border_nv12(
    device uint8_t* dst           [[buffer(0)]],
    constant BorderParams& params [[buffer(1)]],
    uint2 gid [[thread_position_in_grid]])
{
    int lx = int(gid.x);
    int ly = int(gid.y);

    // Convert local (outer-box-relative) coords to absolute frame coords
    int x = params.outerX + lx;
    int y = params.outerY + ly;

    if (lx >= params.outerW || ly >= params.outerH) return;
    if (x >= int(params.dstW) || y >= int(params.dstH) || x < 0 || y < 0) return;

    // Inside the rect itself? Skip (not border)
    if (x >= params.rectX && x < params.rectX + params.rectW && y >= params.rectY && y < params.rectY + params.rectH) return;

    dst[uint(y) * params.dstPitch + uint(x)] = params.colorY;

    if ((x & 1) == 0 && (y & 1) == 0) {
        uint uvOffset = params.dstPitch * params.dstH;
        uint uvIdx = uint(y / 2) * params.dstPitch + uint(x);
        dst[uvOffset + uvIdx]     = params.colorCb;
        dst[uvOffset + uvIdx + 1] = params.colorCr;
    }
}

// Fill rectangle with constant NV12 color
kernel void fill_rect_nv12(
    device uint8_t* dst           [[buffer(0)]],
    constant FillRectParams& params [[buffer(1)]],
    uint2 gid [[thread_position_in_grid]])
{
    if (gid.x >= uint(params.rectW) || gid.y >= uint(params.rectH)) return;

    int x = params.rectX + int(gid.x);
    int y = params.rectY + int(gid.y);
    if (x >= int(params.dstW) || y >= int(params.dstH) || x < 0 || y < 0) return;

    dst[uint(y) * params.dstPitch + uint(x)] = params.colorY;

    if ((gid.x & 1) == 0 && (gid.y & 1) == 0) {
        uint uvOffset = params.dstPitch * params.dstH;
        uint uvIdx = uint(y / 2) * params.dstPitch + uint(x);
        dst[uvOffset + uvIdx]     = params.colorCb;
        dst[uvOffset + uvIdx + 1] = params.colorCr;
    }
}
