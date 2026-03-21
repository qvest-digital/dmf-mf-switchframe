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

} // extern "C"
