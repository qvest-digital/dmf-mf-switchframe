#include "common.cuh"

// Bilinear scale for Y plane (one byte per sample).
// One thread per output pixel. Uses 16.16 fixed-point for sub-pixel accuracy.
__global__ void scale_bilinear_kernel(
    uint8_t* __restrict__ dst, int dstW, int dstH, int dstPitch,
    const uint8_t* __restrict__ src, int srcW, int srcH, int srcPitch)
{
    int dx = blockIdx.x * blockDim.x + threadIdx.x;
    int dy = blockIdx.y * blockDim.y + threadIdx.y;
    if (dx >= dstW || dy >= dstH) return;

    // Map destination to source coordinates using float to avoid int32 overflow
    // (e.g., 1920 * 65536 overflows 32-bit int for coordinates > ~32)
    float srcXf = (float)dx * (float)(srcW - 1) / (float)max(dstW - 1, 1);
    float srcYf = (float)dy * (float)(srcH - 1) / (float)max(dstH - 1, 1);
    int sx = (int)srcXf;
    int sy = (int)srcYf;
    int fx = (int)((srcXf - sx) * 65536.0f); // 16-bit fractional part
    int fy = (int)((srcYf - sy) * 65536.0f);

    int sx1 = min(sx + 1, srcW - 1);
    int sy1 = min(sy + 1, srcH - 1);

    int v00 = src[sy  * srcPitch + sx];
    int v10 = src[sy  * srcPitch + sx1];
    int v01 = src[sy1 * srcPitch + sx];
    int v11 = src[sy1 * srcPitch + sx1];

    int top = v00 + ((v10 - v00) * fx >> 16);
    int bot = v01 + ((v11 - v01) * fx >> 16);
    int val = top + ((bot - top) * fy >> 16);

    dst[dy * dstPitch + dx] = (uint8_t)val;
}

// Bilinear scale for NV12 UV plane (interleaved CbCr pairs).
// One thread per OUTPUT chroma sample (CbCr pair). Interpolates Cb and Cr
// independently to prevent cross-channel mixing that causes color corruption.
// chromaDstW/chromaSrcW are in CHROMA SAMPLES (width/2), not bytes.
__global__ void scale_bilinear_uv_kernel(
    uint8_t* __restrict__ dst, int chromaDstW, int dstChromaH, int dstPitch,
    const uint8_t* __restrict__ src, int chromaSrcW, int srcChromaH, int srcPitch)
{
    int dx = blockIdx.x * blockDim.x + threadIdx.x;
    int dy = blockIdx.y * blockDim.y + threadIdx.y;
    if (dx >= chromaDstW || dy >= dstChromaH) return;

    float srcXf = (float)dx * (float)(chromaSrcW - 1) / (float)max(chromaDstW - 1, 1);
    float srcYf = (float)dy * (float)(srcChromaH - 1) / (float)max(dstChromaH - 1, 1);
    int sx = (int)srcXf;
    int sy = (int)srcYf;
    int fx = (int)((srcXf - sx) * 65536.0f);
    int fy = (int)((srcYf - sy) * 65536.0f);

    int sx1 = min(sx + 1, chromaSrcW - 1);
    int sy1 = min(sy + 1, srcChromaH - 1);

    // Read CbCr pairs (2 bytes each) — index by chroma sample, offset by 2
    int srcByteX0 = sx * 2;
    int srcByteX1 = sx1 * 2;

    // Cb channel
    int cb00 = src[sy  * srcPitch + srcByteX0];
    int cb10 = src[sy  * srcPitch + srcByteX1];
    int cb01 = src[sy1 * srcPitch + srcByteX0];
    int cb11 = src[sy1 * srcPitch + srcByteX1];

    int cbTop = cb00 + ((cb10 - cb00) * fx >> 16);
    int cbBot = cb01 + ((cb11 - cb01) * fx >> 16);
    int cb = cbTop + ((cbBot - cbTop) * fy >> 16);

    // Cr channel (offset +1 from Cb)
    int cr00 = src[sy  * srcPitch + srcByteX0 + 1];
    int cr10 = src[sy  * srcPitch + srcByteX1 + 1];
    int cr01 = src[sy1 * srcPitch + srcByteX0 + 1];
    int cr11 = src[sy1 * srcPitch + srcByteX1 + 1];

    int crTop = cr00 + ((cr10 - cr00) * fx >> 16);
    int crBot = cr01 + ((cr11 - cr01) * fx >> 16);
    int cr = crTop + ((crBot - crTop) * fy >> 16);

    int dstByte = dy * dstPitch + dx * 2;
    dst[dstByte]     = (uint8_t)cb;
    dst[dstByte + 1] = (uint8_t)cr;
}

// ---------------------------------------------------------------------------
// Lanczos-3 separable two-pass scaler
// ---------------------------------------------------------------------------
// L(x) = sinc(x)*sinc(x/3) for |x| < 3, else 0
// sinc(x) = sin(pi*x)/(pi*x), sinc(0) = 1
// NOTE: defined before any kernel that uses it (UV kernels below).
__device__ __forceinline__ float lanczos3(float x)
{
    if (x == 0.0f) return 1.0f;
    if (x < -3.0f || x > 3.0f) return 0.0f;
    float pix   = 3.14159265358979323846f * x;
    float pix3  = pix / 3.0f;
    return (__sinf(pix) / pix) * (__sinf(pix3) / pix3);
}

// Pass 1 (horizontal): src uint8 → tmpBuf float
// tmpBuf layout: row-major [srcH][dstW], row stride = dstW
__global__ void scale_lanczos3_h_kernel(
    float* __restrict__ tmpBuf, int dstW, int srcW, int srcH, int srcPitch,
    const uint8_t* __restrict__ src)
{
    int dx = blockIdx.x * blockDim.x + threadIdx.x;
    int dy = blockIdx.y * blockDim.y + threadIdx.y;
    if (dx >= dstW || dy >= srcH) return;

    float srcXf = (float)dx * (float)(srcW - 1) / (float)max(dstW - 1, 1);
    int   center = (int)srcXf;

    float acc    = 0.0f;
    float wsum   = 0.0f;
    // 6 taps: floor(srcX) - 2 … floor(srcX) + 3
    for (int k = -2; k <= 3; ++k) {
        int sx = center + k;
        if (sx < 0) sx = 0;
        if (sx >= srcW) sx = srcW - 1;
        float w = lanczos3(srcXf - (float)sx);
        acc  += w * (float)src[dy * srcPitch + sx];
        wsum += w;
    }

    tmpBuf[dy * dstW + dx] = (wsum != 0.0f) ? (acc / wsum) : 0.0f;
}

// Pass 2 (vertical): tmpBuf float → dst uint8
__global__ void scale_lanczos3_v_kernel(
    uint8_t* __restrict__ dst, int dstW, int dstH, int dstPitch,
    const float* __restrict__ tmpBuf, int srcH)
{
    int dx = blockIdx.x * blockDim.x + threadIdx.x;
    int dy = blockIdx.y * blockDim.y + threadIdx.y;
    if (dx >= dstW || dy >= dstH) return;

    float srcYf = (float)dy * (float)(srcH - 1) / (float)max(dstH - 1, 1);
    int   center = (int)srcYf;

    float acc    = 0.0f;
    float wsum   = 0.0f;
    for (int k = -2; k <= 3; ++k) {
        int sy = center + k;
        if (sy < 0) sy = 0;
        if (sy >= srcH) sy = srcH - 1;
        float w = lanczos3(srcYf - (float)sy);
        acc  += w * tmpBuf[sy * dstW + dx];
        wsum += w;
    }

    float val = (wsum != 0.0f) ? (acc / wsum) : 0.0f;
    // clamp to [0, 255]
    if (val < 0.0f)   val = 0.0f;
    if (val > 255.0f) val = 255.0f;
    dst[dy * dstPitch + dx] = (uint8_t)(val + 0.5f);
}

// Lanczos-3 horizontal pass for NV12 UV plane (interleaved CbCr pairs).
// One thread per output chroma sample. Writes TWO floats per sample (Cb, Cr)
// into tmpBuf layout: [srcChromaH][chromaDstW * 2], row stride = chromaDstW * 2.
__global__ void scale_lanczos3_h_uv_kernel(
    float* __restrict__ tmpBuf, int chromaDstW, int chromaSrcW, int srcChromaH,
    int srcPitch, const uint8_t* __restrict__ src)
{
    int dx = blockIdx.x * blockDim.x + threadIdx.x;
    int dy = blockIdx.y * blockDim.y + threadIdx.y;
    if (dx >= chromaDstW || dy >= srcChromaH) return;

    float srcXf = (float)dx * (float)(chromaSrcW - 1) / (float)max(chromaDstW - 1, 1);
    int center = (int)srcXf;

    float accCb = 0.0f, accCr = 0.0f;
    float wsum = 0.0f;
    for (int k = -2; k <= 3; ++k) {
        int sx = center + k;
        if (sx < 0) sx = 0;
        if (sx >= chromaSrcW) sx = chromaSrcW - 1;
        float w = lanczos3(srcXf - (float)sx);
        accCb += w * (float)src[dy * srcPitch + sx * 2];
        accCr += w * (float)src[dy * srcPitch + sx * 2 + 1];
        wsum += w;
    }

    int idx = dy * chromaDstW * 2 + dx * 2;
    if (wsum != 0.0f) {
        tmpBuf[idx]     = accCb / wsum;
        tmpBuf[idx + 1] = accCr / wsum;
    } else {
        tmpBuf[idx]     = 0.0f;
        tmpBuf[idx + 1] = 0.0f;
    }
}

// Lanczos-3 vertical pass for NV12 UV plane (interleaved CbCr pairs).
// Reads TWO floats per sample from tmpBuf, writes interleaved uint8 Cb/Cr to dst.
__global__ void scale_lanczos3_v_uv_kernel(
    uint8_t* __restrict__ dst, int chromaDstW, int dstChromaH, int dstPitch,
    const float* __restrict__ tmpBuf, int srcChromaH)
{
    int dx = blockIdx.x * blockDim.x + threadIdx.x;
    int dy = blockIdx.y * blockDim.y + threadIdx.y;
    if (dx >= chromaDstW || dy >= dstChromaH) return;

    float srcYf = (float)dy * (float)(srcChromaH - 1) / (float)max(dstChromaH - 1, 1);
    int center = (int)srcYf;

    float accCb = 0.0f, accCr = 0.0f;
    float wsum = 0.0f;
    for (int k = -2; k <= 3; ++k) {
        int sy = center + k;
        if (sy < 0) sy = 0;
        if (sy >= srcChromaH) sy = srcChromaH - 1;
        float w = lanczos3(srcYf - (float)sy);
        int idx = sy * chromaDstW * 2 + dx * 2;
        accCb += w * tmpBuf[idx];
        accCr += w * tmpBuf[idx + 1];
        wsum += w;
    }

    float valCb = (wsum != 0.0f) ? (accCb / wsum) : 0.0f;
    float valCr = (wsum != 0.0f) ? (accCr / wsum) : 0.0f;
    if (valCb < 0.0f) valCb = 0.0f; if (valCb > 255.0f) valCb = 255.0f;
    if (valCr < 0.0f) valCr = 0.0f; if (valCr > 255.0f) valCr = 255.0f;

    int dstByte = dy * dstPitch + dx * 2;
    dst[dstByte]     = (uint8_t)(valCb + 0.5f);
    dst[dstByte + 1] = (uint8_t)(valCr + 0.5f);
}

// ============================================================================
// P210 (10-bit 4:2:2) scale kernels
// ============================================================================

// Bilinear scale for P210 Y plane (uint16 per sample)
__global__ void scale_bilinear_p210_y_kernel(
    uint16_t* __restrict__ dst, int dstW, int dstH, int dstPitch16,
    const uint16_t* __restrict__ src, int srcW, int srcH, int srcPitch16)
{
    int dx = blockIdx.x * blockDim.x + threadIdx.x;
    int dy = blockIdx.y * blockDim.y + threadIdx.y;
    if (dx >= dstW || dy >= dstH) return;

    float srcXf = (float)dx * (float)(srcW - 1) / (float)max(dstW - 1, 1);
    float srcYf = (float)dy * (float)(srcH - 1) / (float)max(dstH - 1, 1);
    int sx = (int)srcXf;
    int sy = (int)srcYf;
    int fx = (int)((srcXf - sx) * 65536.0f);
    int fy = (int)((srcYf - sy) * 65536.0f);

    int sx1 = min(sx + 1, srcW - 1);
    int sy1 = min(sy + 1, srcH - 1);

    int v00 = src[sy  * srcPitch16 + sx];
    int v10 = src[sy  * srcPitch16 + sx1];
    int v01 = src[sy1 * srcPitch16 + sx];
    int v11 = src[sy1 * srcPitch16 + sx1];

    int top = v00 + ((v10 - v00) * fx >> 16);
    int bot = v01 + ((v11 - v01) * fx >> 16);
    int val = top + ((bot - top) * fy >> 16);

    dst[dy * dstPitch16 + dx] = (uint16_t)min(max(val, 0), 1023);
}

// Bilinear scale for P210 UV plane (interleaved uint16 CbCr pairs)
// chromaDstW/chromaSrcW are in CHROMA SAMPLES (width/2), not bytes.
// P210 chroma: full height (4:2:2), half width.
__global__ void scale_bilinear_p210_uv_kernel(
    uint16_t* __restrict__ dst, int chromaDstW, int dstH, int dstPitch16,
    const uint16_t* __restrict__ src, int chromaSrcW, int srcH, int srcPitch16)
{
    int dx = blockIdx.x * blockDim.x + threadIdx.x;
    int dy = blockIdx.y * blockDim.y + threadIdx.y;
    if (dx >= chromaDstW || dy >= dstH) return;

    float srcXf = (float)dx * (float)(chromaSrcW - 1) / (float)max(chromaDstW - 1, 1);
    float srcYf = (float)dy * (float)(srcH - 1) / (float)max(dstH - 1, 1);
    int sx = (int)srcXf;
    int sy = (int)srcYf;
    int fx = (int)((srcXf - sx) * 65536.0f);
    int fy = (int)((srcYf - sy) * 65536.0f);

    int sx1 = min(sx + 1, chromaSrcW - 1);
    int sy1 = min(sy + 1, srcH - 1);

    // Cb channel (even index in interleaved pairs)
    int srcByteX0 = sx * 2;
    int srcByteX1 = sx1 * 2;

    int cb00 = src[sy  * srcPitch16 + srcByteX0];
    int cb10 = src[sy  * srcPitch16 + srcByteX1];
    int cb01 = src[sy1 * srcPitch16 + srcByteX0];
    int cb11 = src[sy1 * srcPitch16 + srcByteX1];

    int cbTop = cb00 + ((cb10 - cb00) * fx >> 16);
    int cbBot = cb01 + ((cb11 - cb01) * fx >> 16);
    int cb = cbTop + ((cbBot - cbTop) * fy >> 16);

    // Cr channel (odd index)
    int cr00 = src[sy  * srcPitch16 + srcByteX0 + 1];
    int cr10 = src[sy  * srcPitch16 + srcByteX1 + 1];
    int cr01 = src[sy1 * srcPitch16 + srcByteX0 + 1];
    int cr11 = src[sy1 * srcPitch16 + srcByteX1 + 1];

    int crTop = cr00 + ((cr10 - cr00) * fx >> 16);
    int crBot = cr01 + ((cr11 - cr01) * fx >> 16);
    int cr = crTop + ((crBot - crTop) * fy >> 16);

    int dstIdx = dy * dstPitch16 + dx * 2;
    dst[dstIdx]     = (uint16_t)min(max(cb, 0), 1023);
    dst[dstIdx + 1] = (uint16_t)min(max(cr, 0), 1023);
}

extern "C" {

cudaError_t scale_bilinear_nv12(
    uint8_t* dst, int dstW, int dstH, int dstPitch,
    const uint8_t* src, int srcW, int srcH, int srcPitch,
    cudaStream_t stream)
{
    dim3 block(BLOCK_DIM_X, BLOCK_DIM_Y);

    // Scale Y plane (one byte per sample)
    dim3 gridY((dstW + block.x - 1) / block.x, (dstH + block.y - 1) / block.y);
    scale_bilinear_kernel<<<gridY, block, 0, stream>>>(
        dst, dstW, dstH, dstPitch, src, srcW, srcH, srcPitch);

    // Scale UV plane using UV-aware kernel that interpolates CbCr pairs
    // independently. Width is in CHROMA SAMPLES (width/2), not bytes.
    int chromaSrcW = srcW / 2;
    int chromaDstW = dstW / 2;
    int srcChromaH = srcH / 2;
    int dstChromaH = dstH / 2;
    const uint8_t* srcUV = src + srcPitch * srcH;
    uint8_t* dstUV = dst + dstPitch * dstH;
    dim3 gridUV((chromaDstW + block.x - 1) / block.x, (dstChromaH + block.y - 1) / block.y);
    scale_bilinear_uv_kernel<<<gridUV, block, 0, stream>>>(
        dstUV, chromaDstW, dstChromaH, dstPitch,
        srcUV, chromaSrcW, srcChromaH, srcPitch);

    return cudaGetLastError();
}

// scale_lanczos3_nv12: two-pass Lanczos-3 scale for NV12 frames.
// tmpBuf must be caller-allocated device memory with at least
// max(dstW * srcH, chromaDstW * srcChromaH * 2) floats = dstW * srcH floats.
// The UV pass uses 2 floats per chroma sample (Cb + Cr interleaved).
cudaError_t scale_lanczos3_nv12(
    uint8_t* dst, int dstW, int dstH, int dstPitch,
    const uint8_t* src, int srcW, int srcH, int srcPitch,
    float* tmpBuf,
    cudaStream_t stream)
{
    dim3 block(BLOCK_DIM_X, BLOCK_DIM_Y);

    // --- Y plane ---
    // Pass 1: horizontal (srcW x srcH → dstW x srcH)
    dim3 gridH_Y((dstW + block.x - 1) / block.x, (srcH + block.y - 1) / block.y);
    scale_lanczos3_h_kernel<<<gridH_Y, block, 0, stream>>>(
        tmpBuf, dstW, srcW, srcH, srcPitch, src);

    // Pass 2: vertical (dstW x srcH → dstW x dstH)
    dim3 gridV_Y((dstW + block.x - 1) / block.x, (dstH + block.y - 1) / block.y);
    scale_lanczos3_v_kernel<<<gridV_Y, block, 0, stream>>>(
        dst, dstW, dstH, dstPitch, tmpBuf, srcH);

    // --- UV plane: UV-aware Lanczos-3 ---
    // Interpolates Cb and Cr channels independently to prevent cross-channel mixing.
    int chromaSrcW = srcW / 2;
    int chromaDstW = dstW / 2;
    int srcChromaH = srcH / 2;
    int dstChromaH = dstH / 2;
    const uint8_t* srcUV = src + srcPitch * srcH;
    uint8_t*       dstUV = dst + dstPitch * dstH;

    // Pass 1 (horizontal): srcUV → tmpBuf (2 floats per chroma sample)
    dim3 gridH_UV((chromaDstW + block.x - 1) / block.x, (srcChromaH + block.y - 1) / block.y);
    scale_lanczos3_h_uv_kernel<<<gridH_UV, block, 0, stream>>>(
        tmpBuf, chromaDstW, chromaSrcW, srcChromaH, srcPitch, srcUV);

    // Pass 2 (vertical): tmpBuf → dstUV (interleaved uint8 Cb/Cr)
    dim3 gridV_UV((chromaDstW + block.x - 1) / block.x, (dstChromaH + block.y - 1) / block.y);
    scale_lanczos3_v_uv_kernel<<<gridV_UV, block, 0, stream>>>(
        dstUV, chromaDstW, dstChromaH, dstPitch, tmpBuf, srcChromaH);

    return cudaGetLastError();
}

// ============================================================================
// Lanczos-3 P210 (10-bit 4:2:2) scale kernels
// ============================================================================

// Pass 1 (horizontal): P210 uint16 src → tmpBuf float
// tmpBuf layout: row-major [srcH][dstW], row stride = dstW
__global__ void scale_lanczos3_p210_h_kernel(
    float* __restrict__ tmpBuf, int dstW, int srcW, int srcH, int srcPitch16,
    const uint16_t* __restrict__ src)
{
    int dx = blockIdx.x * blockDim.x + threadIdx.x;
    int dy = blockIdx.y * blockDim.y + threadIdx.y;
    if (dx >= dstW || dy >= srcH) return;

    float srcXf = (float)dx * (float)(srcW - 1) / (float)max(dstW - 1, 1);
    int   center = (int)srcXf;

    float acc  = 0.0f;
    float wsum = 0.0f;
    for (int k = -2; k <= 3; ++k) {
        int sx = center + k;
        if (sx < 0) sx = 0;
        if (sx >= srcW) sx = srcW - 1;
        float w = lanczos3(srcXf - (float)sx);
        acc  += w * (float)src[dy * srcPitch16 + sx];
        wsum += w;
    }

    tmpBuf[dy * dstW + dx] = (wsum != 0.0f) ? (acc / wsum) : 0.0f;
}

// Pass 2 (vertical): tmpBuf float → P210 uint16 dst
__global__ void scale_lanczos3_p210_v_kernel(
    uint16_t* __restrict__ dst, int dstW, int dstH, int dstPitch16,
    const float* __restrict__ tmpBuf, int srcH)
{
    int dx = blockIdx.x * blockDim.x + threadIdx.x;
    int dy = blockIdx.y * blockDim.y + threadIdx.y;
    if (dx >= dstW || dy >= dstH) return;

    float srcYf = (float)dy * (float)(srcH - 1) / (float)max(dstH - 1, 1);
    int   center = (int)srcYf;

    float acc  = 0.0f;
    float wsum = 0.0f;
    for (int k = -2; k <= 3; ++k) {
        int sy = center + k;
        if (sy < 0) sy = 0;
        if (sy >= srcH) sy = srcH - 1;
        float w = lanczos3(srcYf - (float)sy);
        acc  += w * tmpBuf[sy * dstW + dx];
        wsum += w;
    }

    float val = (wsum != 0.0f) ? (acc / wsum) : 0.0f;
    if (val < 0.0f)    val = 0.0f;
    if (val > 1023.0f) val = 1023.0f;
    dst[dy * dstPitch16 + dx] = (uint16_t)(val + 0.5f);
}

// Lanczos-3 horizontal pass for P210 UV plane (interleaved uint16 CbCr pairs).
// One thread per output chroma sample. Writes TWO floats per sample (Cb, Cr)
// into tmpBuf layout: [srcH][chromaDstW * 2], row stride = chromaDstW * 2.
// P210 UV is full height (4:2:2).
__global__ void scale_lanczos3_p210_h_uv_kernel(
    float* __restrict__ tmpBuf, int chromaDstW, int chromaSrcW, int srcH,
    int srcPitch16, const uint16_t* __restrict__ src)
{
    int dx = blockIdx.x * blockDim.x + threadIdx.x;
    int dy = blockIdx.y * blockDim.y + threadIdx.y;
    if (dx >= chromaDstW || dy >= srcH) return;

    float srcXf = (float)dx * (float)(chromaSrcW - 1) / (float)max(chromaDstW - 1, 1);
    int center = (int)srcXf;

    float accCb = 0.0f, accCr = 0.0f;
    float wsum = 0.0f;
    for (int k = -2; k <= 3; ++k) {
        int sx = center + k;
        if (sx < 0) sx = 0;
        if (sx >= chromaSrcW) sx = chromaSrcW - 1;
        float w = lanczos3(srcXf - (float)sx);
        accCb += w * (float)src[dy * srcPitch16 + sx * 2];
        accCr += w * (float)src[dy * srcPitch16 + sx * 2 + 1];
        wsum += w;
    }

    int idx = dy * chromaDstW * 2 + dx * 2;
    if (wsum != 0.0f) {
        tmpBuf[idx]     = accCb / wsum;
        tmpBuf[idx + 1] = accCr / wsum;
    } else {
        tmpBuf[idx]     = 0.0f;
        tmpBuf[idx + 1] = 0.0f;
    }
}

// Lanczos-3 vertical pass for P210 UV plane (interleaved uint16 CbCr pairs).
// Reads TWO floats per sample from tmpBuf, writes interleaved uint16 Cb/Cr to dst.
// P210 UV is full height (4:2:2).
__global__ void scale_lanczos3_p210_v_uv_kernel(
    uint16_t* __restrict__ dst, int chromaDstW, int dstH, int dstPitch16,
    const float* __restrict__ tmpBuf, int srcH)
{
    int dx = blockIdx.x * blockDim.x + threadIdx.x;
    int dy = blockIdx.y * blockDim.y + threadIdx.y;
    if (dx >= chromaDstW || dy >= dstH) return;

    float srcYf = (float)dy * (float)(srcH - 1) / (float)max(dstH - 1, 1);
    int center = (int)srcYf;

    float accCb = 0.0f, accCr = 0.0f;
    float wsum = 0.0f;
    for (int k = -2; k <= 3; ++k) {
        int sy = center + k;
        if (sy < 0) sy = 0;
        if (sy >= srcH) sy = srcH - 1;
        float w = lanczos3(srcYf - (float)sy);
        int idx = sy * chromaDstW * 2 + dx * 2;
        accCb += w * tmpBuf[idx];
        accCr += w * tmpBuf[idx + 1];
        wsum += w;
    }

    float valCb = (wsum != 0.0f) ? (accCb / wsum) : 0.0f;
    float valCr = (wsum != 0.0f) ? (accCr / wsum) : 0.0f;
    if (valCb < 0.0f) valCb = 0.0f; if (valCb > 1023.0f) valCb = 1023.0f;
    if (valCr < 0.0f) valCr = 0.0f; if (valCr > 1023.0f) valCr = 1023.0f;

    int dstIdx = dy * dstPitch16 + dx * 2;
    dst[dstIdx]     = (uint16_t)(valCb + 0.5f);
    dst[dstIdx + 1] = (uint16_t)(valCr + 0.5f);
}

// --- P210 scale wrapper ---

// scale_lanczos3_p210: two-pass Lanczos-3 scale for P210 frames.
// tmpBuf must be caller-allocated device memory with at least
// max(dstW * srcH, chromaDstW * srcH * 2) floats = dstW * srcH floats.
// P210: UV is full height (4:2:2).
cudaError_t scale_lanczos3_p210(
    uint8_t* dst, int dstW, int dstH, int dstPitch,
    const uint8_t* src, int srcW, int srcH, int srcPitch,
    float* tmpBuf,
    cudaStream_t stream)
{
    int dstPitch16 = dstPitch / 2;
    int srcPitch16 = srcPitch / 2;
    dim3 block(BLOCK_DIM_X, BLOCK_DIM_Y);

    // --- Y plane ---
    // Pass 1: horizontal (srcW x srcH -> dstW x srcH)
    dim3 gridH_Y((dstW + block.x - 1) / block.x, (srcH + block.y - 1) / block.y);
    scale_lanczos3_p210_h_kernel<<<gridH_Y, block, 0, stream>>>(
        tmpBuf, dstW, srcW, srcH, srcPitch16, (const uint16_t*)src);

    // Pass 2: vertical (dstW x srcH -> dstW x dstH)
    dim3 gridV_Y((dstW + block.x - 1) / block.x, (dstH + block.y - 1) / block.y);
    scale_lanczos3_p210_v_kernel<<<gridV_Y, block, 0, stream>>>(
        (uint16_t*)dst, dstW, dstH, dstPitch16, tmpBuf, srcH);

    // --- UV plane: P210 is 4:2:2 (full height, half width) ---
    int chromaSrcW = srcW / 2;
    int chromaDstW = dstW / 2;
    const uint16_t* srcUV = (const uint16_t*)(src + srcPitch * srcH);
    uint16_t*       dstUV = (uint16_t*)(dst + dstPitch * dstH);

    // Pass 1 (horizontal): srcUV -> tmpBuf (2 floats per chroma sample)
    dim3 gridH_UV((chromaDstW + block.x - 1) / block.x, (srcH + block.y - 1) / block.y);
    scale_lanczos3_p210_h_uv_kernel<<<gridH_UV, block, 0, stream>>>(
        tmpBuf, chromaDstW, chromaSrcW, srcH, srcPitch16, srcUV);

    // Pass 2 (vertical): tmpBuf -> dstUV (interleaved uint16 Cb/Cr)
    dim3 gridV_UV((chromaDstW + block.x - 1) / block.x, (dstH + block.y - 1) / block.y);
    scale_lanczos3_p210_v_uv_kernel<<<gridV_UV, block, 0, stream>>>(
        dstUV, chromaDstW, dstH, dstPitch16, tmpBuf, srcH);

    return cudaGetLastError();
}

cudaError_t scale_bilinear_p210(
    uint8_t* dst, int dstW, int dstH, int dstPitch,
    const uint8_t* src, int srcW, int srcH, int srcPitch,
    cudaStream_t stream)
{
    int dstPitch16 = dstPitch / 2;
    int srcPitch16 = srcPitch / 2;
    dim3 block(BLOCK_DIM_X, BLOCK_DIM_Y);

    // Scale Y plane (uint16 per sample)
    dim3 gridY((dstW + block.x - 1) / block.x, (dstH + block.y - 1) / block.y);
    scale_bilinear_p210_y_kernel<<<gridY, block, 0, stream>>>(
        (uint16_t*)dst, dstW, dstH, dstPitch16,
        (const uint16_t*)src, srcW, srcH, srcPitch16);

    // Scale UV plane (interleaved uint16 CbCr pairs)
    // P210: full height, half width for chroma
    int chromaSrcW = srcW / 2;
    int chromaDstW = dstW / 2;
    const uint16_t* srcUV = (const uint16_t*)(src + srcPitch * srcH);
    uint16_t* dstUV = (uint16_t*)(dst + dstPitch * dstH);
    dim3 gridUV((chromaDstW + block.x - 1) / block.x, (dstH + block.y - 1) / block.y);
    scale_bilinear_p210_uv_kernel<<<gridUV, block, 0, stream>>>(
        dstUV, chromaDstW, dstH, dstPitch16,
        srcUV, chromaSrcW, srcH, srcPitch16);

    return cudaGetLastError();
}

} // extern "C"
