#include <metal_stdlib>
using namespace metal;

struct ScaleParams {
    uint srcW;
    uint srcH;
    uint srcPitch;
    uint dstW;
    uint dstH;
    uint dstPitch;
};

// Bilinear scale for a single NV12 plane (Y or UV)
// One thread per output pixel. Uses float for sub-pixel accuracy.
kernel void scale_bilinear(
    device uint8_t* dst           [[buffer(0)]],
    device const uint8_t* src     [[buffer(1)]],
    constant ScaleParams& params  [[buffer(2)]],
    uint2 gid [[thread_position_in_grid]])
{
    if (gid.x >= params.dstW || gid.y >= params.dstH) return;

    float srcXf = float(gid.x) * float(params.srcW - 1) / float(max(params.dstW - 1, 1u));
    float srcYf = float(gid.y) * float(params.srcH - 1) / float(max(params.dstH - 1, 1u));
    int sx = int(srcXf);
    int sy = int(srcYf);
    int fx = int((srcXf - float(sx)) * 65536.0f); // 16-bit fractional part
    int fy = int((srcYf - float(sy)) * 65536.0f);

    uint sx1 = min(uint(sx + 1), params.srcW - 1);
    uint sy1 = min(uint(sy + 1), params.srcH - 1);

    int v00 = int(src[uint(sy)  * params.srcPitch + uint(sx)]);
    int v10 = int(src[uint(sy)  * params.srcPitch + sx1]);
    int v01 = int(src[sy1 * params.srcPitch + uint(sx)]);
    int v11 = int(src[sy1 * params.srcPitch + sx1]);

    int top = v00 + ((v10 - v00) * fx >> 16);
    int bot = v01 + ((v11 - v01) * fx >> 16);
    int val = top + ((bot - top) * fy >> 16);

    dst[gid.y * params.dstPitch + gid.x] = uint8_t(val);
}
