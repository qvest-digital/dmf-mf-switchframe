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

// ============================================================================
// P210 (10-bit 4:2:2) scale kernels
// ============================================================================

struct ScaleP210Params {
    uint srcW;      // width in luma samples
    uint srcH;
    uint srcPitch;  // bytes per row
    uint dstW;
    uint dstH;
    uint dstPitch;
};

// Bilinear scale for P210 Y plane (uint16 samples).
// One thread per output luma sample.
kernel void scale_bilinear_p210_y(
    device uchar* dst            [[buffer(0)]],
    device const uchar* src      [[buffer(1)]],
    constant ScaleP210Params& params [[buffer(2)]],
    uint2 gid [[thread_position_in_grid]])
{
    if (gid.x >= params.dstW || gid.y >= params.dstH) return;

    float srcXf = float(gid.x) * float(params.srcW - 1) / float(max(params.dstW - 1, 1u));
    float srcYf = float(gid.y) * float(params.srcH - 1) / float(max(params.dstH - 1, 1u));
    int sx = int(srcXf);
    int sy = int(srcYf);
    int fx = int((srcXf - float(sx)) * 65536.0f);
    int fy = int((srcYf - float(sy)) * 65536.0f);

    uint sx1 = min(uint(sx + 1), params.srcW - 1);
    uint sy1 = min(uint(sy + 1), params.srcH - 1);

    // Access as uint16 — offset in samples: row * (pitch/2) + col
    uint srcSamplesPerRow = params.srcPitch / 2;
    device const ushort* srcU16 = (device const ushort*)src;

    int v00 = int(srcU16[uint(sy)  * srcSamplesPerRow + uint(sx)]);
    int v10 = int(srcU16[uint(sy)  * srcSamplesPerRow + sx1]);
    int v01 = int(srcU16[sy1 * srcSamplesPerRow + uint(sx)]);
    int v11 = int(srcU16[sy1 * srcSamplesPerRow + sx1]);

    int top = v00 + ((v10 - v00) * fx >> 16);
    int bot = v01 + ((v11 - v01) * fx >> 16);
    int val = top + ((bot - top) * fy >> 16);

    uint dstSamplesPerRow = params.dstPitch / 2;
    ((device ushort*)dst)[gid.y * dstSamplesPerRow + gid.x] = ushort(clamp(val, 0, 1023));
}

// Bilinear scale for P210 UV plane (interleaved uint16 CbCr pairs, full height for 4:2:2).
// One thread per output chroma sample (CbCr pair).
// dstW/srcW are in chroma samples (width/2), not bytes.
kernel void scale_bilinear_p210_uv(
    device uchar* dst            [[buffer(0)]],
    device const uchar* src      [[buffer(1)]],
    constant ScaleP210Params& params [[buffer(2)]],
    uint2 gid [[thread_position_in_grid]])
{
    if (gid.x >= params.dstW || gid.y >= params.dstH) return;

    float srcXf = float(gid.x) * float(params.srcW - 1) / float(max(params.dstW - 1, 1u));
    float srcYf = float(gid.y) * float(params.srcH - 1) / float(max(params.dstH - 1, 1u));
    int sx = int(srcXf);
    int sy = int(srcYf);
    int fx = int((srcXf - float(sx)) * 65536.0f);
    int fy = int((srcYf - float(sy)) * 65536.0f);

    uint sx1 = min(uint(sx + 1), params.srcW - 1);
    uint sy1 = min(uint(sy + 1), params.srcH - 1);

    // UV plane has interleaved uint16 pairs. Each chroma sample occupies 4 bytes (Cb uint16 + Cr uint16).
    device const ushort* srcU16 = (device const ushort*)src;
    uint srcSamplesPerRow = params.srcPitch / 2; // ushort elements per row

    // Cb channel (even indices within the row of ushorts)
    int cb00 = int(srcU16[uint(sy)  * srcSamplesPerRow + uint(sx)  * 2]);
    int cb10 = int(srcU16[uint(sy)  * srcSamplesPerRow + sx1 * 2]);
    int cb01 = int(srcU16[sy1 * srcSamplesPerRow + uint(sx)  * 2]);
    int cb11 = int(srcU16[sy1 * srcSamplesPerRow + sx1 * 2]);

    int cbTop = cb00 + ((cb10 - cb00) * fx >> 16);
    int cbBot = cb01 + ((cb11 - cb01) * fx >> 16);
    int cb = cbTop + ((cbBot - cbTop) * fy >> 16);

    // Cr channel (odd indices)
    int cr00 = int(srcU16[uint(sy)  * srcSamplesPerRow + uint(sx)  * 2 + 1]);
    int cr10 = int(srcU16[uint(sy)  * srcSamplesPerRow + sx1 * 2 + 1]);
    int cr01 = int(srcU16[sy1 * srcSamplesPerRow + uint(sx)  * 2 + 1]);
    int cr11 = int(srcU16[sy1 * srcSamplesPerRow + sx1 * 2 + 1]);

    int crTop = cr00 + ((cr10 - cr00) * fx >> 16);
    int crBot = cr01 + ((cr11 - cr01) * fx >> 16);
    int cr = crTop + ((crBot - crTop) * fy >> 16);

    uint dstSamplesPerRow = params.dstPitch / 2;
    ((device ushort*)dst)[gid.y * dstSamplesPerRow + gid.x * 2]     = ushort(clamp(cb, 0, 1023));
    ((device ushort*)dst)[gid.y * dstSamplesPerRow + gid.x * 2 + 1] = ushort(clamp(cr, 0, 1023));
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

// ============================================================================
// Lanczos-3 separable two-pass scaler for P210 (10-bit 4:2:2)
// ============================================================================
// Y plane: horizontal reads uint16 luma, writes float. Vertical reads float,
//   writes uint16, clamps to [0, 1023].
// UV plane: dedicated kernels that handle interleaved CbCr uint16 pairs,
//   filtering Cb and Cr channels independently to prevent cross-channel mixing.
//   Temp buffer stores 2 floats per chroma sample (Cb, Cr interleaved).

// Y plane pass 1 (horizontal): P210 uint16 src -> tmpBuf float
// tmpBuf layout: row-major [srcH][dstW], row stride = dstW
kernel void scale_lanczos3_p210_h(
    device float* tmpBuf           [[buffer(0)]],
    device const uchar* src        [[buffer(1)]],
    constant Lanczos3HParams& params [[buffer(2)]],
    uint2 gid [[thread_position_in_grid]])
{
    uint dx = gid.x;
    uint dy = gid.y;
    if (dx >= params.dstW || dy >= params.srcH) return;

    float srcXf = float(dx) * float(params.srcW - 1) / float(max(params.dstW - 1, 1u));
    int center = int(srcXf);

    device const ushort* srcU16 = (device const ushort*)src;
    uint srcSamplesPerRow = params.srcPitch / 2;

    float acc  = 0.0f;
    float wsum = 0.0f;
    for (int k = -2; k <= 3; ++k) {
        int sx = center + k;
        if (sx < 0) sx = 0;
        if (sx >= int(params.srcW)) sx = int(params.srcW) - 1;
        float w = lanczos3(srcXf - float(sx));
        acc  += w * float(srcU16[dy * srcSamplesPerRow + uint(sx)]);
        wsum += w;
    }

    tmpBuf[dy * params.dstW + dx] = (wsum != 0.0f) ? (acc / wsum) : 0.0f;
}

// Y plane pass 2 (vertical): tmpBuf float -> P210 uint16 dst
kernel void scale_lanczos3_p210_v(
    device uchar* dst               [[buffer(0)]],
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
    // clamp to [0, 1023] for 10-bit
    if (val < 0.0f)    val = 0.0f;
    if (val > 1023.0f) val = 1023.0f;

    uint dstSamplesPerRow = params.dstPitch / 2;
    ((device ushort*)dst)[dy * dstSamplesPerRow + dx] = ushort(val + 0.5f);
}

// UV plane pass 1 (horizontal): P210 interleaved uint16 CbCr -> tmpBuf float
// One thread per output chroma SAMPLE (CbCr pair).
// srcW/dstW are in chroma samples (luma width / 2).
// tmpBuf layout: row-major [srcH][dstW * 2] floats (Cb, Cr interleaved).
kernel void scale_lanczos3_p210_uv_h(
    device float* tmpBuf           [[buffer(0)]],
    device const uchar* src        [[buffer(1)]],
    constant Lanczos3HParams& params [[buffer(2)]],
    uint2 gid [[thread_position_in_grid]])
{
    uint dx = gid.x;  // chroma sample index
    uint dy = gid.y;
    if (dx >= params.dstW || dy >= params.srcH) return;

    float srcXf = float(dx) * float(params.srcW - 1) / float(max(params.dstW - 1, 1u));
    int center = int(srcXf);

    device const ushort* srcU16 = (device const ushort*)src;
    uint srcSamplesPerRow = params.srcPitch / 2; // ushort elements per row

    float cbAcc = 0.0f, crAcc = 0.0f;
    float wsum = 0.0f;
    for (int k = -2; k <= 3; ++k) {
        int sx = center + k;
        if (sx < 0) sx = 0;
        if (sx >= int(params.srcW)) sx = int(params.srcW) - 1;
        float w = lanczos3(srcXf - float(sx));
        cbAcc += w * float(srcU16[dy * srcSamplesPerRow + uint(sx) * 2]);
        crAcc += w * float(srcU16[dy * srcSamplesPerRow + uint(sx) * 2 + 1]);
        wsum  += w;
    }

    uint tmpStride = params.dstW * 2; // 2 floats per chroma sample
    if (wsum != 0.0f) {
        tmpBuf[dy * tmpStride + dx * 2]     = cbAcc / wsum;
        tmpBuf[dy * tmpStride + dx * 2 + 1] = crAcc / wsum;
    } else {
        tmpBuf[dy * tmpStride + dx * 2]     = 0.0f;
        tmpBuf[dy * tmpStride + dx * 2 + 1] = 0.0f;
    }
}

// UV plane pass 2 (vertical): tmpBuf float -> P210 interleaved uint16 CbCr
// One thread per output chroma SAMPLE (CbCr pair).
// dstW is in chroma samples (luma width / 2).
// tmpBuf stride is dstW * 2 floats per row.
kernel void scale_lanczos3_p210_uv_v(
    device uchar* dst               [[buffer(0)]],
    device const float* tmpBuf      [[buffer(1)]],
    constant Lanczos3VParams& params [[buffer(2)]],
    uint2 gid [[thread_position_in_grid]])
{
    uint dx = gid.x;  // chroma sample index
    uint dy = gid.y;
    if (dx >= params.dstW || dy >= params.dstH) return;

    float srcYf = float(dy) * float(params.srcH - 1) / float(max(params.dstH - 1, 1u));
    int center = int(srcYf);

    uint tmpStride = params.dstW * 2; // 2 floats per chroma sample
    float cbAcc = 0.0f, crAcc = 0.0f;
    float wsum = 0.0f;
    for (int k = -2; k <= 3; ++k) {
        int sy = center + k;
        if (sy < 0) sy = 0;
        if (sy >= int(params.srcH)) sy = int(params.srcH) - 1;
        float w = lanczos3(srcYf - float(sy));
        cbAcc += w * tmpBuf[uint(sy) * tmpStride + dx * 2];
        crAcc += w * tmpBuf[uint(sy) * tmpStride + dx * 2 + 1];
        wsum  += w;
    }

    float cbVal = (wsum != 0.0f) ? (cbAcc / wsum) : 0.0f;
    float crVal = (wsum != 0.0f) ? (crAcc / wsum) : 0.0f;
    if (cbVal < 0.0f)    cbVal = 0.0f;
    if (cbVal > 1023.0f) cbVal = 1023.0f;
    if (crVal < 0.0f)    crVal = 0.0f;
    if (crVal > 1023.0f) crVal = 1023.0f;

    uint dstSamplesPerRow = params.dstPitch / 2;
    ((device ushort*)dst)[dy * dstSamplesPerRow + dx * 2]     = ushort(cbVal + 0.5f);
    ((device ushort*)dst)[dy * dstSamplesPerRow + dx * 2 + 1] = ushort(crVal + 0.5f);
}

// ============================================================================
// Cross-format scale: P210 (10-bit 4:2:2) → NV12 (8-bit 4:2:0)
// Reads uint16 samples from P210, bilinear interpolates, right-shifts to 8-bit.
// ============================================================================

struct ScaleP210ToNV12Params {
    uint srcW;      // source luma width in samples
    uint srcH;      // source luma height
    uint srcPitch;  // source P210 row pitch in bytes (uint16 samples)
    uint dstW;      // destination luma width in samples
    uint dstH;      // destination luma height
    uint dstPitch;  // destination NV12 row pitch in bytes (uint8 samples)
};

// Y plane: read P210 uint16 luma, bilinear scale, write NV12 uint8 luma.
kernel void scale_p210_to_nv12_y(
    device uint8_t* dst          [[buffer(0)]],
    device const uchar* src      [[buffer(1)]],
    constant ScaleP210ToNV12Params& params [[buffer(2)]],
    uint2 gid [[thread_position_in_grid]])
{
    if (gid.x >= params.dstW || gid.y >= params.dstH) return;

    float srcXf = float(gid.x) * float(params.srcW - 1) / float(max(params.dstW - 1, 1u));
    float srcYf = float(gid.y) * float(params.srcH - 1) / float(max(params.dstH - 1, 1u));
    int sx = int(srcXf);
    int sy = int(srcYf);
    int fx = int((srcXf - float(sx)) * 65536.0f);
    int fy = int((srcYf - float(sy)) * 65536.0f);

    uint sx1 = min(uint(sx + 1), params.srcW - 1);
    uint sy1 = min(uint(sy + 1), params.srcH - 1);

    // Read from P210: uint16 samples
    uint srcSamplesPerRow = params.srcPitch / 2;
    device const ushort* srcU16 = (device const ushort*)src;

    int v00 = int(srcU16[uint(sy)  * srcSamplesPerRow + uint(sx)]);
    int v10 = int(srcU16[uint(sy)  * srcSamplesPerRow + sx1]);
    int v01 = int(srcU16[sy1 * srcSamplesPerRow + uint(sx)]);
    int v11 = int(srcU16[sy1 * srcSamplesPerRow + sx1]);

    int top = v00 + ((v10 - v00) * fx >> 16);
    int bot = v01 + ((v11 - v01) * fx >> 16);
    int val = top + ((bot - top) * fy >> 16);

    // Write to NV12: 10-bit → 8-bit (right-shift 2)
    dst[gid.y * params.dstPitch + gid.x] = uint8_t(clamp(val >> 2, 0, 255));
}

// UV plane: read P210 uint16 CbCr (full-height 4:2:2), bilinear scale,
// write NV12 uint8 CbCr (half-height 4:2:0).
// srcW/dstW are in chroma samples (luma width / 2).
// srcH is full source chroma height (4:2:2 = luma height).
// dstH is half destination luma height (4:2:0 = luma height / 2).
kernel void scale_p210_to_nv12_uv(
    device uint8_t* dst          [[buffer(0)]],
    device const uchar* src      [[buffer(1)]],
    constant ScaleP210ToNV12Params& params [[buffer(2)]],
    uint2 gid [[thread_position_in_grid]])
{
    if (gid.x >= params.dstW || gid.y >= params.dstH) return;

    float srcXf = float(gid.x) * float(params.srcW - 1) / float(max(params.dstW - 1, 1u));
    float srcYf = float(gid.y) * float(params.srcH - 1) / float(max(params.dstH - 1, 1u));
    int sx = int(srcXf);
    int sy = int(srcYf);
    int fx = int((srcXf - float(sx)) * 65536.0f);
    int fy = int((srcYf - float(sy)) * 65536.0f);

    uint sx1 = min(uint(sx + 1), params.srcW - 1);
    uint sy1 = min(uint(sy + 1), params.srcH - 1);

    // Read P210 UV: interleaved uint16 CbCr pairs (4 bytes per chroma sample)
    device const ushort* srcU16 = (device const ushort*)src;
    uint srcSamplesPerRow = params.srcPitch / 2; // ushort elements per row

    // Cb channel (even indices)
    int cb00 = int(srcU16[uint(sy)  * srcSamplesPerRow + uint(sx)  * 2]);
    int cb10 = int(srcU16[uint(sy)  * srcSamplesPerRow + sx1 * 2]);
    int cb01 = int(srcU16[sy1 * srcSamplesPerRow + uint(sx)  * 2]);
    int cb11 = int(srcU16[sy1 * srcSamplesPerRow + sx1 * 2]);

    int cbTop = cb00 + ((cb10 - cb00) * fx >> 16);
    int cbBot = cb01 + ((cb11 - cb01) * fx >> 16);
    int cb = cbTop + ((cbBot - cbTop) * fy >> 16);

    // Cr channel (odd indices)
    int cr00 = int(srcU16[uint(sy)  * srcSamplesPerRow + uint(sx)  * 2 + 1]);
    int cr10 = int(srcU16[uint(sy)  * srcSamplesPerRow + sx1 * 2 + 1]);
    int cr01 = int(srcU16[sy1 * srcSamplesPerRow + uint(sx)  * 2 + 1]);
    int cr11 = int(srcU16[sy1 * srcSamplesPerRow + sx1 * 2 + 1]);

    int crTop = cr00 + ((cr10 - cr00) * fx >> 16);
    int crBot = cr01 + ((cr11 - cr01) * fx >> 16);
    int cr = crTop + ((crBot - crTop) * fy >> 16);

    // Write NV12 UV: interleaved uint8 CbCr pairs, 10-bit → 8-bit
    uint dstIdx = gid.y * params.dstPitch + gid.x * 2;
    dst[dstIdx]     = uint8_t(clamp(cb >> 2, 0, 255));
    dst[dstIdx + 1] = uint8_t(clamp(cr >> 2, 0, 255));
}
