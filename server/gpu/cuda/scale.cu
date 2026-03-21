#include "common.cuh"

// Bilinear scale for a single NV12 plane (Y or UV)
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

// ---------------------------------------------------------------------------
// Lanczos-3 separable two-pass scaler
// ---------------------------------------------------------------------------
// L(x) = sinc(x)*sinc(x/3) for |x| < 3, else 0
// sinc(x) = sin(pi*x)/(pi*x), sinc(0) = 1
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

extern "C" {

cudaError_t scale_bilinear_nv12(
    uint8_t* dst, int dstW, int dstH, int dstPitch,
    const uint8_t* src, int srcW, int srcH, int srcPitch,
    cudaStream_t stream)
{
    dim3 block(BLOCK_DIM_X, BLOCK_DIM_Y);

    // Scale Y plane
    dim3 gridY((dstW + block.x - 1) / block.x, (dstH + block.y - 1) / block.y);
    scale_bilinear_kernel<<<gridY, block, 0, stream>>>(
        dst, dstW, dstH, dstPitch, src, srcW, srcH, srcPitch);

    // Scale UV plane (interleaved, half height, same byte width as Y for NV12)
    int srcChromaH = srcH / 2;
    int dstChromaH = dstH / 2;
    const uint8_t* srcUV = src + srcPitch * srcH;
    uint8_t* dstUV = dst + dstPitch * dstH;
    dim3 gridUV((dstW + block.x - 1) / block.x, (dstChromaH + block.y - 1) / block.y);
    scale_bilinear_kernel<<<gridUV, block, 0, stream>>>(
        dstUV, dstW, dstChromaH, dstPitch, srcUV, srcW, srcChromaH, srcPitch);

    return cudaGetLastError();
}

// scale_lanczos3_nv12: two-pass Lanczos-3 scale for NV12 frames.
// tmpBuf must be caller-allocated device memory with at least
// max(dstW * srcH, dstW * (srcH/2)) floats = dstW * srcH floats.
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

    // --- UV plane (NV12: interleaved Cb/Cr, half width and half height) ---
    int srcChromaH = srcH / 2;
    int dstChromaH = dstH / 2;
    const uint8_t* srcUV = src + srcPitch * srcH;
    uint8_t*       dstUV = dst + dstPitch * dstH;

    // Pass 1: horizontal
    dim3 gridH_UV((dstW + block.x - 1) / block.x, (srcChromaH + block.y - 1) / block.y);
    scale_lanczos3_h_kernel<<<gridH_UV, block, 0, stream>>>(
        tmpBuf, dstW, srcW, srcChromaH, srcPitch, srcUV);

    // Pass 2: vertical
    dim3 gridV_UV((dstW + block.x - 1) / block.x, (dstChromaH + block.y - 1) / block.y);
    scale_lanczos3_v_kernel<<<gridV_UV, block, 0, stream>>>(
        dstUV, dstW, dstChromaH, dstPitch, tmpBuf, srcChromaH);

    return cudaGetLastError();
}

} // extern "C"
