#include <metal_stdlib>
using namespace metal;

struct DSKFullFrameParams {
    uint width;
    uint height;
    uint nv12Pitch;
    uint rgbaPitch;
    int alphaScale256;
};

struct DSKRectParams {
    uint frameW;
    uint frameH;
    uint nv12Pitch;
    uint overlayW;
    uint overlayH;
    uint rgbaPitch;
    int rectX;
    int rectY;
    int rectW;
    int rectH;
    int alphaScale256;
};

// Alpha blend RGBA overlay onto NV12 frame (full-frame)
// BT.709 limited-range conversion
kernel void dsk_overlay_nv12(
    device uint8_t* nv12          [[buffer(0)]],
    device const uint8_t* rgba    [[buffer(1)]],
    constant DSKFullFrameParams& params [[buffer(2)]],
    uint2 gid [[thread_position_in_grid]])
{
    if (gid.x >= params.width || gid.y >= params.height) return;

    uint rgbaIdx = gid.y * params.rgbaPitch + gid.x * 4;
    int r = int(rgba[rgbaIdx]);
    int g = int(rgba[rgbaIdx + 1]);
    int b = int(rgba[rgbaIdx + 2]);
    int a = int(rgba[rgbaIdx + 3]);

    // Apply global alpha scale
    a = (a * params.alphaScale256 + 128) >> 8;
    if (a == 0) return;

    int inv = 256 - a;

    // BT.709 limited-range conversion (integer, matching CPU coefficients)
    int overlayY  = 16  + ((47 * r + 157 * g + 16 * b + 128) >> 8);
    int overlayCb = 128 + ((-26 * r - 86 * g + 112 * b + 128) >> 8);
    int overlayCr = 128 + ((112 * r - 102 * g - 10 * b + 128) >> 8);

    overlayY  = min(max(overlayY, 16), 235);
    overlayCb = min(max(overlayCb, 16), 240);
    overlayCr = min(max(overlayCr, 16), 240);

    // Blend Y plane
    uint yIdx = gid.y * params.nv12Pitch + gid.x;
    nv12[yIdx] = uint8_t((int(nv12[yIdx]) * inv + overlayY * a + 128) >> 8);

    // Blend UV plane (once per 2x2 block)
    if ((gid.x & 1) == 0 && (gid.y & 1) == 0) {
        uint uvOffset = params.nv12Pitch * params.height;
        uint uvIdx = (gid.y / 2) * params.nv12Pitch + gid.x;
        int curCb = int(nv12[uvOffset + uvIdx]);
        int curCr = int(nv12[uvOffset + uvIdx + 1]);
        nv12[uvOffset + uvIdx]     = uint8_t((curCb * inv + overlayCb * a + 128) >> 8);
        nv12[uvOffset + uvIdx + 1] = uint8_t((curCr * inv + overlayCr * a + 128) >> 8);
    }
}

// Rectangular RGBA overlay with nearest-neighbor scaling onto NV12
kernel void dsk_overlay_rect_nv12(
    device uint8_t* nv12          [[buffer(0)]],
    device const uint8_t* rgba    [[buffer(1)]],
    constant DSKRectParams& params [[buffer(2)]],
    uint2 gid [[thread_position_in_grid]])
{
    if (gid.x >= uint(params.rectW) || gid.y >= uint(params.rectH)) return;

    int dx = params.rectX + int(gid.x);
    int dy = params.rectY + int(gid.y);
    if (dx >= int(params.frameW) || dy >= int(params.frameH) || dx < 0 || dy < 0) return;

    // Nearest-neighbor sample from overlay
    int sx = int(gid.x) * int(params.overlayW) / max(params.rectW, 1);
    int sy = int(gid.y) * int(params.overlayH) / max(params.rectH, 1);
    sx = min(sx, int(params.overlayW) - 1);
    sy = min(sy, int(params.overlayH) - 1);
    uint rgbaIdx = uint(sy) * params.rgbaPitch + uint(sx) * 4;

    int r = int(rgba[rgbaIdx]);
    int g = int(rgba[rgbaIdx + 1]);
    int b = int(rgba[rgbaIdx + 2]);
    int a = int(rgba[rgbaIdx + 3]);

    a = (a * params.alphaScale256 + 128) >> 8;
    if (a == 0) return;

    int inv = 256 - a;

    int overlayY  = 16  + ((47 * r + 157 * g + 16 * b + 128) >> 8);
    int overlayCb = 128 + ((-26 * r - 86 * g + 112 * b + 128) >> 8);
    int overlayCr = 128 + ((112 * r - 102 * g - 10 * b + 128) >> 8);

    overlayY  = min(max(overlayY, 16), 235);
    overlayCb = min(max(overlayCb, 16), 240);
    overlayCr = min(max(overlayCr, 16), 240);

    uint yIdx = uint(dy) * params.nv12Pitch + uint(dx);
    nv12[yIdx] = uint8_t((int(nv12[yIdx]) * inv + overlayY * a + 128) >> 8);

    if ((dx & 1) == 0 && (dy & 1) == 0) {
        uint uvOffset = params.nv12Pitch * params.frameH;
        uint uvIdx = uint(dy / 2) * params.nv12Pitch + uint(dx);
        int curCb = int(nv12[uvOffset + uvIdx]);
        int curCr = int(nv12[uvOffset + uvIdx + 1]);
        nv12[uvOffset + uvIdx]     = uint8_t((curCb * inv + overlayCb * a + 128) >> 8);
        nv12[uvOffset + uvIdx + 1] = uint8_t((curCr * inv + overlayCr * a + 128) >> 8);
    }
}
