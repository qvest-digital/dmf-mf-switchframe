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

struct Lanczos3HParams {
    uint dstW;
    uint srcW;
    uint srcH;
    uint srcPitch;
};

struct Lanczos3VParams {
    uint dstW;
    uint dstH;
    uint dstPitch;
    uint srcH;
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

// Bilinear scale for NV12 UV plane (interleaved CbCr pairs)
// One thread per OUTPUT chroma sample (CbCr pair). Interpolates Cb and Cr
// independently to prevent cross-channel mixing.
// dstW/srcW are in CHROMA SAMPLES (width/2), not bytes.
kernel void scale_bilinear_uv(
    device uint8_t* dst           [[buffer(0)]],
    device const uint8_t* src     [[buffer(1)]],
    constant ScaleParams& params  [[buffer(2)]],
    uint2 gid [[thread_position_in_grid]])
{
    // params.dstW = chroma width (luma width / 2)
    // params.srcW = chroma width (luma width / 2)
    if (gid.x >= params.dstW || gid.y >= params.dstH) return;

    float srcXf = float(gid.x) * float(params.srcW - 1) / float(max(params.dstW - 1, 1u));
    float srcYf = float(gid.y) * float(params.srcH - 1) / float(max(params.dstH - 1, 1u));
    int sx = int(srcXf);
    int sy = int(srcYf);
    int fx = int((srcXf - float(sx)) * 65536.0f);
    int fy = int((srcYf - float(sy)) * 65536.0f);

    uint sx1 = min(uint(sx + 1), params.srcW - 1);
    uint sy1 = min(uint(sy + 1), params.srcH - 1);

    // Read CbCr pairs (2 bytes each) — index by chroma sample, offset by 2
    uint srcByteX0 = uint(sx) * 2;
    uint srcByteX1 = sx1 * 2;

    // Cb channel
    int cb00 = int(src[uint(sy)  * params.srcPitch + srcByteX0]);
    int cb10 = int(src[uint(sy)  * params.srcPitch + srcByteX1]);
    int cb01 = int(src[sy1 * params.srcPitch + srcByteX0]);
    int cb11 = int(src[sy1 * params.srcPitch + srcByteX1]);

    int cbTop = cb00 + ((cb10 - cb00) * fx >> 16);
    int cbBot = cb01 + ((cb11 - cb01) * fx >> 16);
    int cb = cbTop + ((cbBot - cbTop) * fy >> 16);

    // Cr channel (offset +1 from Cb)
    int cr00 = int(src[uint(sy)  * params.srcPitch + srcByteX0 + 1]);
    int cr10 = int(src[uint(sy)  * params.srcPitch + srcByteX1 + 1]);
    int cr01 = int(src[sy1 * params.srcPitch + srcByteX0 + 1]);
    int cr11 = int(src[sy1 * params.srcPitch + srcByteX1 + 1]);

    int crTop = cr00 + ((cr10 - cr00) * fx >> 16);
    int crBot = cr01 + ((cr11 - cr01) * fx >> 16);
    int cr = crTop + ((crBot - crTop) * fy >> 16);

    uint dstByte = gid.y * params.dstPitch + gid.x * 2;
    dst[dstByte]     = uint8_t(cb);
    dst[dstByte + 1] = uint8_t(cr);
}

// ---------------------------------------------------------------------------
// Lanczos-3 separable two-pass scaler
// ---------------------------------------------------------------------------
// L(x) = sinc(x)*sinc(x/3) for |x| < 3, else 0
// sinc(x) = sin(pi*x)/(pi*x), sinc(0) = 1
inline float lanczos3(float x)
{
    if (x == 0.0f) return 1.0f;
    if (x < -3.0f || x > 3.0f) return 0.0f;
    float pix  = 3.14159265358979323846f * x;
    float pix3 = pix / 3.0f;
    return (sin(pix) / pix) * (sin(pix3) / pix3);
}

// Pass 1 (horizontal): src uint8 -> tmpBuf float
// tmpBuf layout: row-major [srcH][dstW], row stride = dstW
kernel void scale_lanczos3_h(
    device float* tmpBuf           [[buffer(0)]],
    device const uint8_t* src      [[buffer(1)]],
    constant Lanczos3HParams& params [[buffer(2)]],
    uint2 gid [[thread_position_in_grid]])
{
    uint dx = gid.x;
    uint dy = gid.y;
    if (dx >= params.dstW || dy >= params.srcH) return;

    float srcXf = float(dx) * float(params.srcW - 1) / float(max(params.dstW - 1, 1u));
    int center = int(srcXf);

    float acc  = 0.0f;
    float wsum = 0.0f;
    // 6 taps: floor(srcX) - 2 ... floor(srcX) + 3
    for (int k = -2; k <= 3; ++k) {
        int sx = center + k;
        if (sx < 0) sx = 0;
        if (sx >= int(params.srcW)) sx = int(params.srcW) - 1;
        float w = lanczos3(srcXf - float(sx));
        acc  += w * float(src[dy * params.srcPitch + uint(sx)]);
        wsum += w;
    }

    tmpBuf[dy * params.dstW + dx] = (wsum != 0.0f) ? (acc / wsum) : 0.0f;
}

// Pass 2 (vertical): tmpBuf float -> dst uint8
kernel void scale_lanczos3_v(
    device uint8_t* dst             [[buffer(0)]],
    device const float* tmpBuf      [[buffer(1)]],
    constant Lanczos3VParams& params [[buffer(2)]],
    uint2 gid [[thread_position_in_grid]])
{
    uint dx = gid.x;
    uint dy = gid.y;
    if (dx >= params.dstW || dy >= params.dstH) return;

    float srcYf = float(dy) * float(params.srcH - 1) / float(max(params.dstH - 1, 1u));
    int center = int(srcYf);

    float acc  = 0.0f;
    float wsum = 0.0f;
    for (int k = -2; k <= 3; ++k) {
        int sy = center + k;
        if (sy < 0) sy = 0;
        if (sy >= int(params.srcH)) sy = int(params.srcH) - 1;
        float w = lanczos3(srcYf - float(sy));
        acc  += w * tmpBuf[uint(sy) * params.dstW + dx];
        wsum += w;
    }

    float val = (wsum != 0.0f) ? (acc / wsum) : 0.0f;
    // clamp to [0, 255]
    if (val < 0.0f)   val = 0.0f;
    if (val > 255.0f) val = 255.0f;
    dst[dy * params.dstPitch + dx] = uint8_t(val + 0.5f);
}
