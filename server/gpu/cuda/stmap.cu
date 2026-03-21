#include "common.cuh"

// ST map warp: remap each output pixel using normalized (S,T) coordinates.
// S maps to source X (0.0 = left, 1.0 = right), T maps to source Y (0.0 = top, 1.0 = bottom).
// Uses bilinear interpolation from global memory with float coordinates.
//
// The ST map is stored as two separate float arrays (S and T), interleaved
// into a single float2 array on the GPU for coalesced access.

// Y plane warp (full luma resolution)
__global__ void stmap_warp_y_kernel(
    uint8_t* __restrict__ dst, int dstPitch,
    const uint8_t* __restrict__ src, int srcPitch,
    const float* __restrict__ stS,  // normalized S coords (width * height)
    const float* __restrict__ stT,  // normalized T coords (width * height)
    int width, int height)
{
    int x = blockIdx.x * blockDim.x + threadIdx.x;
    int y = blockIdx.y * blockDim.y + threadIdx.y;
    if (x >= width || y >= height) return;

    int stIdx = y * width + x;
    float srcXf = stS[stIdx] * (float)(width - 1);
    float srcYf = stT[stIdx] * (float)(height - 1);

    // Clamp to valid range
    srcXf = fmaxf(0.0f, fminf(srcXf, (float)(width - 1)));
    srcYf = fmaxf(0.0f, fminf(srcYf, (float)(height - 1)));

    int sx = (int)srcXf;
    int sy = (int)srcYf;
    float fx = srcXf - sx;
    float fy = srcYf - sy;

    int sx1 = min(sx + 1, width - 1);
    int sy1 = min(sy + 1, height - 1);

    // Bilinear interpolation (4 source texels)
    float v00 = (float)src[sy  * srcPitch + sx];
    float v10 = (float)src[sy  * srcPitch + sx1];
    float v01 = (float)src[sy1 * srcPitch + sx];
    float v11 = (float)src[sy1 * srcPitch + sx1];

    float top = v00 + (v10 - v00) * fx;
    float bot = v01 + (v11 - v01) * fx;
    float val = top + (bot - top) * fy;

    dst[y * dstPitch + x] = (uint8_t)(val + 0.5f);
}

// UV plane warp (NV12 interleaved, half resolution)
// Averages ST coords from 2x2 luma block for chroma position.
__global__ void stmap_warp_uv_kernel(
    uint8_t* __restrict__ dstUV, int dstPitch,
    const uint8_t* __restrict__ srcUV, int srcPitch,
    const float* __restrict__ stS,
    const float* __restrict__ stT,
    int lumaW, int lumaH,
    int chromaW, int chromaH)
{
    int cx = blockIdx.x * blockDim.x + threadIdx.x;
    int cy = blockIdx.y * blockDim.y + threadIdx.y;
    if (cx >= chromaW || cy >= chromaH) return;

    // Average ST coords from the 2x2 luma block
    int lx = cx * 2;
    int ly = cy * 2;
    int lx1 = min(lx + 1, lumaW - 1);
    int ly1 = min(ly + 1, lumaH - 1);

    float s00 = stS[ly  * lumaW + lx];
    float s10 = stS[ly  * lumaW + lx1];
    float s01 = stS[ly1 * lumaW + lx];
    float s11 = stS[ly1 * lumaW + lx1];
    float t00 = stT[ly  * lumaW + lx];
    float t10 = stT[ly  * lumaW + lx1];
    float t01 = stT[ly1 * lumaW + lx];
    float t11 = stT[ly1 * lumaW + lx1];

    float avgS = (s00 + s10 + s01 + s11) * 0.25f;
    float avgT = (t00 + t10 + t01 + t11) * 0.25f;

    float srcXf = avgS * (float)(chromaW - 1);
    float srcYf = avgT * (float)(chromaH - 1);

    srcXf = fmaxf(0.0f, fminf(srcXf, (float)(chromaW - 1)));
    srcYf = fmaxf(0.0f, fminf(srcYf, (float)(chromaH - 1)));

    int sx = (int)srcXf;
    int sy = (int)srcYf;
    float fx = srcXf - sx;
    float fy = srcYf - sy;

    int sx1 = min(sx + 1, chromaW - 1);
    int sy1 = min(sy + 1, chromaH - 1);

    // Bilinear for U channel
    float u00 = (float)srcUV[sy  * srcPitch + sx  * 2];
    float u10 = (float)srcUV[sy  * srcPitch + sx1 * 2];
    float u01 = (float)srcUV[sy1 * srcPitch + sx  * 2];
    float u11 = (float)srcUV[sy1 * srcPitch + sx1 * 2];
    float uTop = u00 + (u10 - u00) * fx;
    float uBot = u01 + (u11 - u01) * fx;
    float uVal = uTop + (uBot - uTop) * fy;

    // Bilinear for V channel
    float v00 = (float)srcUV[sy  * srcPitch + sx  * 2 + 1];
    float v10 = (float)srcUV[sy  * srcPitch + sx1 * 2 + 1];
    float v01 = (float)srcUV[sy1 * srcPitch + sx  * 2 + 1];
    float v11 = (float)srcUV[sy1 * srcPitch + sx1 * 2 + 1];
    float vTop = v00 + (v10 - v00) * fx;
    float vBot = v01 + (v11 - v01) * fx;
    float vVal = vTop + (vBot - vTop) * fy;

    int idx = cy * dstPitch + cx * 2;
    dstUV[idx]     = (uint8_t)(uVal + 0.5f);
    dstUV[idx + 1] = (uint8_t)(vVal + 0.5f);
}

extern "C" {

cudaError_t stmap_warp_nv12(
    uint8_t* dst, int dstPitch,
    const uint8_t* src, int srcPitch,
    const float* stS, const float* stT,
    int width, int height, cudaStream_t stream)
{
    dim3 block(BLOCK_DIM_X, BLOCK_DIM_Y);

    // Warp Y plane
    dim3 gridY((width + block.x - 1) / block.x, (height + block.y - 1) / block.y);
    stmap_warp_y_kernel<<<gridY, block, 0, stream>>>(
        dst, dstPitch, src, srcPitch, stS, stT, width, height);

    // Warp UV plane
    int chromaW = width / 2;
    int chromaH = height / 2;
    dim3 gridUV((chromaW + block.x - 1) / block.x, (chromaH + block.y - 1) / block.y);
    stmap_warp_uv_kernel<<<gridUV, block, 0, stream>>>(
        dst + dstPitch * height, dstPitch,
        src + srcPitch * height, srcPitch,
        stS, stT,
        width, height, chromaW, chromaH);

    return cudaGetLastError();
}

cudaError_t stmap_upload(
    float** devS, float** devT, int width, int height,
    const float* hostS, const float* hostT)
{
    size_t sz = (size_t)width * height * sizeof(float);
    cudaError_t err;

    err = cudaMalloc((void**)devS, sz);
    if (err != cudaSuccess) return err;

    err = cudaMalloc((void**)devT, sz);
    if (err != cudaSuccess) {
        cudaFree(*devS);
        *devS = NULL;
        return err;
    }

    err = cudaMemcpy(*devS, hostS, sz, cudaMemcpyHostToDevice);
    if (err != cudaSuccess) {
        cudaFree(*devS); cudaFree(*devT);
        *devS = NULL; *devT = NULL;
        return err;
    }

    err = cudaMemcpy(*devT, hostT, sz, cudaMemcpyHostToDevice);
    if (err != cudaSuccess) {
        cudaFree(*devS); cudaFree(*devT);
        *devS = NULL; *devT = NULL;
        return err;
    }

    return cudaSuccess;
}

void stmap_free(float* devS, float* devT) {
    if (devS) cudaFree(devS);
    if (devT) cudaFree(devT);
}

} // extern "C"
