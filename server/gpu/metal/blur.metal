#include <metal_stdlib>
using namespace metal;

struct BlurParams {
    uint width;
    uint height;
    uint pitch;
    uint radius;
};

// Horizontal pass: reads uint8 src, writes float tmp.
// tmp layout: row-major [height][pitch] floats (same layout as src).
kernel void gaussian_blur_h(
    device float* tmp                [[buffer(0)]],
    device const uint8_t* src        [[buffer(1)]],
    device const float* weights      [[buffer(2)]],
    constant BlurParams& params      [[buffer(3)]],
    uint2 gid [[thread_position_in_grid]])
{
    if (gid.x >= params.width || gid.y >= params.height) return;

    int r = int(params.radius);
    float acc = 0.0f;
    for (int k = -r; k <= r; ++k) {
        int sx = int(gid.x) + k;
        // Clamp to frame boundaries
        if (sx < 0) sx = 0;
        if (sx >= int(params.width)) sx = int(params.width) - 1;
        acc += weights[k + r] * float(src[gid.y * params.pitch + uint(sx)]);
    }
    tmp[gid.y * params.pitch + gid.x] = acc;
}

// Vertical pass: reads float tmp, writes uint8 dst.
kernel void gaussian_blur_v(
    device uint8_t* dst              [[buffer(0)]],
    device const float* tmp          [[buffer(1)]],
    device const float* weights      [[buffer(2)]],
    constant BlurParams& params      [[buffer(3)]],
    uint2 gid [[thread_position_in_grid]])
{
    if (gid.x >= params.width || gid.y >= params.height) return;

    int r = int(params.radius);
    float acc = 0.0f;
    for (int k = -r; k <= r; ++k) {
        int sy = int(gid.y) + k;
        // Clamp to frame boundaries
        if (sy < 0) sy = 0;
        if (sy >= int(params.height)) sy = int(params.height) - 1;
        acc += weights[k + r] * tmp[uint(sy) * params.pitch + gid.x];
    }
    // Clamp to [0, 255]
    if (acc < 0.0f)   acc = 0.0f;
    if (acc > 255.0f) acc = 255.0f;
    dst[gid.y * params.pitch + gid.x] = uint8_t(acc + 0.5f);
}

// Horizontal pass for UV plane: reads uint8 src, writes float tmp.
// UV is interleaved CbCr pairs. Each Cb and Cr channel is blurred
// independently. width is the full byte width (= luma width).
kernel void gaussian_blur_uv_h(
    device float* tmp                [[buffer(0)]],
    device const uint8_t* src        [[buffer(1)]],
    device const float* weights      [[buffer(2)]],
    constant BlurParams& params      [[buffer(3)]],
    uint2 gid [[thread_position_in_grid]])
{
    // gid.x is in chroma samples (0..width/2-1), gid.y is UV row (0..height/2-1)
    uint chromaW = params.width / 2;
    if (gid.x >= chromaW || gid.y >= params.height) return;

    int r = int(params.radius);
    float accCb = 0.0f;
    float accCr = 0.0f;
    for (int k = -r; k <= r; ++k) {
        int sx = int(gid.x) + k;
        if (sx < 0) sx = 0;
        if (sx >= int(chromaW)) sx = int(chromaW) - 1;
        uint byteIdx = gid.y * params.pitch + uint(sx) * 2;
        accCb += weights[k + r] * float(src[byteIdx]);
        accCr += weights[k + r] * float(src[byteIdx + 1]);
    }
    uint outIdx = gid.y * params.pitch + gid.x * 2;
    tmp[outIdx]     = accCb;
    tmp[outIdx + 1] = accCr;
}

// Vertical pass for UV plane: reads float tmp, writes uint8 dst.
kernel void gaussian_blur_uv_v(
    device uint8_t* dst              [[buffer(0)]],
    device const float* tmp          [[buffer(1)]],
    device const float* weights      [[buffer(2)]],
    constant BlurParams& params      [[buffer(3)]],
    uint2 gid [[thread_position_in_grid]])
{
    // gid.x is in chroma samples (0..width/2-1), gid.y is UV row (0..height/2-1)
    uint chromaW = params.width / 2;
    if (gid.x >= chromaW || gid.y >= params.height) return;

    int r = int(params.radius);
    float accCb = 0.0f;
    float accCr = 0.0f;
    for (int k = -r; k <= r; ++k) {
        int sy = int(gid.y) + k;
        if (sy < 0) sy = 0;
        if (sy >= int(params.height)) sy = int(params.height) - 1;
        uint inIdx = uint(sy) * params.pitch + gid.x * 2;
        accCb += weights[k + r] * tmp[inIdx];
        accCr += weights[k + r] * tmp[inIdx + 1];
    }
    // Clamp to [0, 255]
    if (accCb < 0.0f)   accCb = 0.0f;
    if (accCb > 255.0f) accCb = 255.0f;
    if (accCr < 0.0f)   accCr = 0.0f;
    if (accCr > 255.0f) accCr = 255.0f;
    uint outIdx = gid.y * params.pitch + gid.x * 2;
    dst[outIdx]     = uint8_t(accCb + 0.5f);
    dst[outIdx + 1] = uint8_t(accCr + 0.5f);
}
