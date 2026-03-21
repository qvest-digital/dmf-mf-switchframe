#include <metal_stdlib>
using namespace metal;

struct STMapParams {
    uint width;
    uint height;
    uint dstPitch;
    uint srcPitch;
};

struct STMapUVParams {
    uint lumaW;
    uint lumaH;
    uint chromaW;
    uint chromaH;
    uint dstPitch;
    uint srcPitch;
};

// Y plane warp: remap each output pixel using normalized (S,T) coordinates.
// Uses bilinear interpolation from global memory.
kernel void stmap_warp_y(
    device const uint8_t* src     [[buffer(0)]],
    device const float* stmapS    [[buffer(1)]],
    device const float* stmapT    [[buffer(2)]],
    device uint8_t* dst           [[buffer(3)]],
    constant STMapParams& params  [[buffer(4)]],
    uint2 gid [[thread_position_in_grid]])
{
    if (gid.x >= params.width || gid.y >= params.height) return;

    uint stIdx = gid.y * params.width + gid.x;
    float srcXf = stmapS[stIdx] * float(params.width - 1);
    float srcYf = stmapT[stIdx] * float(params.height - 1);

    // Clamp to valid range
    srcXf = max(0.0f, min(srcXf, float(params.width - 1)));
    srcYf = max(0.0f, min(srcYf, float(params.height - 1)));

    int sx = int(srcXf);
    int sy = int(srcYf);
    float fx = srcXf - float(sx);
    float fy = srcYf - float(sy);

    uint sx1 = min(uint(sx + 1), params.width - 1);
    uint sy1 = min(uint(sy + 1), params.height - 1);

    float v00 = float(src[uint(sy)  * params.srcPitch + uint(sx)]);
    float v10 = float(src[uint(sy)  * params.srcPitch + sx1]);
    float v01 = float(src[sy1 * params.srcPitch + uint(sx)]);
    float v11 = float(src[sy1 * params.srcPitch + sx1]);

    float top = v00 + (v10 - v00) * fx;
    float bot = v01 + (v11 - v01) * fx;
    float val = top + (bot - top) * fy;

    dst[gid.y * params.dstPitch + gid.x] = uint8_t(val + 0.5f);
}

// UV plane warp (NV12 interleaved, half resolution)
// Averages ST coords from 2x2 luma block for chroma position.
kernel void stmap_warp_uv(
    device const uint8_t* srcUV   [[buffer(0)]],
    device const float* stmapS    [[buffer(1)]],
    device const float* stmapT    [[buffer(2)]],
    device uint8_t* dstUV         [[buffer(3)]],
    constant STMapUVParams& params [[buffer(4)]],
    uint2 gid [[thread_position_in_grid]])
{
    if (gid.x >= params.chromaW || gid.y >= params.chromaH) return;

    // Average ST coords from the 2x2 luma block
    uint lx = gid.x * 2;
    uint ly = gid.y * 2;
    uint lx1 = min(lx + 1, params.lumaW - 1);
    uint ly1 = min(ly + 1, params.lumaH - 1);

    float s00 = stmapS[ly  * params.lumaW + lx];
    float s10 = stmapS[ly  * params.lumaW + lx1];
    float s01 = stmapS[ly1 * params.lumaW + lx];
    float s11 = stmapS[ly1 * params.lumaW + lx1];
    float t00 = stmapT[ly  * params.lumaW + lx];
    float t10 = stmapT[ly  * params.lumaW + lx1];
    float t01 = stmapT[ly1 * params.lumaW + lx];
    float t11 = stmapT[ly1 * params.lumaW + lx1];

    float avgS = (s00 + s10 + s01 + s11) * 0.25f;
    float avgT = (t00 + t10 + t01 + t11) * 0.25f;

    float srcXf = avgS * float(params.chromaW - 1);
    float srcYf = avgT * float(params.chromaH - 1);

    srcXf = max(0.0f, min(srcXf, float(params.chromaW - 1)));
    srcYf = max(0.0f, min(srcYf, float(params.chromaH - 1)));

    int sx = int(srcXf);
    int sy = int(srcYf);
    float fx = srcXf - float(sx);
    float fy = srcYf - float(sy);

    uint sx1 = min(uint(sx + 1), params.chromaW - 1);
    uint sy1 = min(uint(sy + 1), params.chromaH - 1);

    // Bilinear for U channel
    float u00 = float(srcUV[uint(sy)  * params.srcPitch + uint(sx)  * 2]);
    float u10 = float(srcUV[uint(sy)  * params.srcPitch + sx1 * 2]);
    float u01 = float(srcUV[sy1 * params.srcPitch + uint(sx)  * 2]);
    float u11 = float(srcUV[sy1 * params.srcPitch + sx1 * 2]);
    float uTop = u00 + (u10 - u00) * fx;
    float uBot = u01 + (u11 - u01) * fx;
    float uVal = uTop + (uBot - uTop) * fy;

    // Bilinear for V channel
    float v00 = float(srcUV[uint(sy)  * params.srcPitch + uint(sx)  * 2 + 1]);
    float v10 = float(srcUV[uint(sy)  * params.srcPitch + sx1 * 2 + 1]);
    float v01 = float(srcUV[sy1 * params.srcPitch + uint(sx)  * 2 + 1]);
    float v11 = float(srcUV[sy1 * params.srcPitch + sx1 * 2 + 1]);
    float vTop = v00 + (v10 - v00) * fx;
    float vBot = v01 + (v11 - v01) * fx;
    float vVal = vTop + (vBot - vTop) * fy;

    uint idx = gid.y * params.dstPitch + gid.x * 2;
    dstUV[idx]     = uint8_t(uVal + 0.5f);
    dstUV[idx + 1] = uint8_t(vVal + 0.5f);
}
