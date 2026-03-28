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

// ============================================================================
// P210 (10-bit 4:2:2) Gaussian blur kernels
// ============================================================================
// P210 layout: Y plane = uint16 samples, UV plane = interleaved uint16 CbCr.
// Both planes have full height (4:2:2). Pitch is in bytes; samples per row = pitch/2.
// The tmp buffer stores float values indexed by sample position.
// All values clamped to [0, 1023] (10-bit range).

struct BlurP210Params {
    uint width;       // luma width in pixels
    uint height;      // plane height in rows
    uint pitch;       // row stride in bytes
    uint radius;      // blur kernel radius
    uint samplesPerRow; // pitch / 2 (precomputed for convenience)
};

// Horizontal pass for P210 Y plane: reads uint16 src, writes float tmp.
kernel void gaussian_blur_p210_h(
    device float* tmp                [[buffer(0)]],
    device const uchar* src          [[buffer(1)]],
    device const float* weights      [[buffer(2)]],
    constant BlurP210Params& params  [[buffer(3)]],
    uint2 gid [[thread_position_in_grid]])
{
    if (gid.x >= params.width || gid.y >= params.height) return;

    device const ushort* srcRow = (device const ushort*)(src + gid.y * params.pitch);
    int r = int(params.radius);
    float acc = 0.0f;
    for (int k = -r; k <= r; ++k) {
        int sx = int(gid.x) + k;
        if (sx < 0) sx = 0;
        if (sx >= int(params.width)) sx = int(params.width) - 1;
        acc += weights[k + r] * float(srcRow[sx]);
    }
    tmp[gid.y * params.samplesPerRow + gid.x] = acc;
}

// Vertical pass for P210 Y plane: reads float tmp, writes uint16 dst.
kernel void gaussian_blur_p210_v(
    device uchar* dst                [[buffer(0)]],
    device const float* tmp          [[buffer(1)]],
    device const float* weights      [[buffer(2)]],
    constant BlurP210Params& params  [[buffer(3)]],
    uint2 gid [[thread_position_in_grid]])
{
    if (gid.x >= params.width || gid.y >= params.height) return;

    int r = int(params.radius);
    float acc = 0.0f;
    for (int k = -r; k <= r; ++k) {
        int sy = int(gid.y) + k;
        if (sy < 0) sy = 0;
        if (sy >= int(params.height)) sy = int(params.height) - 1;
        acc += weights[k + r] * tmp[uint(sy) * params.samplesPerRow + gid.x];
    }
    // Clamp to [0, 1023]
    if (acc < 0.0f)    acc = 0.0f;
    if (acc > 1023.0f) acc = 1023.0f;
    device ushort* dstRow = (device ushort*)(dst + gid.y * params.pitch);
    dstRow[gid.x] = ushort(acc + 0.5f);
}

// Horizontal pass for P210 UV plane: reads interleaved uint16 CbCr, writes float tmp.
// gid.x is chroma sample index (0..width/2-1), gid.y is row (0..height-1).
// Each chroma sample is a CbCr pair (2 uint16 values).
kernel void gaussian_blur_p210_uv_h(
    device float* tmp                [[buffer(0)]],
    device const uchar* src          [[buffer(1)]],
    device const float* weights      [[buffer(2)]],
    constant BlurP210Params& params  [[buffer(3)]],
    uint2 gid [[thread_position_in_grid]])
{
    uint chromaW = params.width / 2;
    if (gid.x >= chromaW || gid.y >= params.height) return;

    device const ushort* srcRow = (device const ushort*)(src + gid.y * params.pitch);
    int r = int(params.radius);
    float accCb = 0.0f;
    float accCr = 0.0f;
    for (int k = -r; k <= r; ++k) {
        int sx = int(gid.x) + k;
        if (sx < 0) sx = 0;
        if (sx >= int(chromaW)) sx = int(chromaW) - 1;
        accCb += weights[k + r] * float(srcRow[sx * 2]);
        accCr += weights[k + r] * float(srcRow[sx * 2 + 1]);
    }
    uint outIdx = gid.y * params.samplesPerRow + gid.x * 2;
    tmp[outIdx]     = accCb;
    tmp[outIdx + 1] = accCr;
}

// Vertical pass for P210 UV plane: reads float tmp, writes interleaved uint16 CbCr.
// gid.x is chroma sample index (0..width/2-1), gid.y is row (0..height-1).
kernel void gaussian_blur_p210_uv_v(
    device uchar* dst                [[buffer(0)]],
    device const float* tmp          [[buffer(1)]],
    device const float* weights      [[buffer(2)]],
    constant BlurP210Params& params  [[buffer(3)]],
    uint2 gid [[thread_position_in_grid]])
{
    uint chromaW = params.width / 2;
    if (gid.x >= chromaW || gid.y >= params.height) return;

    int r = int(params.radius);
    float accCb = 0.0f;
    float accCr = 0.0f;
    for (int k = -r; k <= r; ++k) {
        int sy = int(gid.y) + k;
        if (sy < 0) sy = 0;
        if (sy >= int(params.height)) sy = int(params.height) - 1;
        uint inIdx = uint(sy) * params.samplesPerRow + gid.x * 2;
        accCb += weights[k + r] * tmp[inIdx];
        accCr += weights[k + r] * tmp[inIdx + 1];
    }
    // Clamp to [0, 1023]
    if (accCb < 0.0f)    accCb = 0.0f;
    if (accCb > 1023.0f) accCb = 1023.0f;
    if (accCr < 0.0f)    accCr = 0.0f;
    if (accCr > 1023.0f) accCr = 1023.0f;
    device ushort* dstRow = (device ushort*)(dst + gid.y * params.pitch);
    dstRow[gid.x * 2]     = ushort(accCb + 0.5f);
    dstRow[gid.x * 2 + 1] = ushort(accCr + 0.5f);
}
