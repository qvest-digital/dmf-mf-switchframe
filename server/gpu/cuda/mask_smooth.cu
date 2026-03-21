#include "common.cuh"

// Exponential Moving Average blend for segmentation mask stabilization.
//
// Per-pixel EMA: output[i] = prev[i] * alpha + curr[i] * (1 - alpha)
//
// alpha = 0.0 → output is exactly curr (no smoothing)
// alpha = 1.0 → output is exactly prev (freeze on previous frame)
// alpha = 0.5 → equal blend (moderate smoothing)
// alpha = 0.7 → heavy smoothing, stable but ~3-4 frames lag
__global__ void mask_ema_kernel(
    uint8_t* __restrict__ output,
    const uint8_t* __restrict__ prev,
    const uint8_t* __restrict__ curr,
    float alpha,
    int size)
{
    int idx = blockIdx.x * blockDim.x + threadIdx.x;
    if (idx >= size) return;
    float p = (float)prev[idx];
    float c = (float)curr[idx];
    float result = p * alpha + c * (1.0f - alpha);
    output[idx] = (uint8_t)(result + 0.5f);
}

// 3×3 morphological erosion for mask boundary cleanup.
//
// Each output pixel is the minimum of its 3×3 neighbourhood. This shrinks
// bright (foreground) regions and removes thin artifacts at person boundaries.
// Border pixels clamp to source edges (no zero-padding artefacts).
__global__ void mask_erode_3x3_kernel(
    uint8_t* __restrict__ dst,
    const uint8_t* __restrict__ src,
    int width, int height)
{
    int x = blockIdx.x * blockDim.x + threadIdx.x;
    int y = blockIdx.y * blockDim.y + threadIdx.y;
    if (x >= width || y >= height) return;

    uint8_t minVal = 255;
    for (int dy = -1; dy <= 1; dy++) {
        int ny = y + dy;
        if (ny < 0) ny = 0;
        if (ny >= height) ny = height - 1;
        for (int dx = -1; dx <= 1; dx++) {
            int nx = x + dx;
            if (nx < 0) nx = 0;
            if (nx >= width) nx = width - 1;
            uint8_t v = src[ny * width + nx];
            if (v < minVal) minVal = v;
        }
    }
    dst[y * width + x] = minVal;
}

extern "C" {

// mask_ema blends prev and curr masks using an exponential moving average.
// output, prev, curr must be device pointers to size bytes (one byte per pixel).
// alpha=0 → use curr; alpha=1 → use prev; typical value: 0.5–0.7.
cudaError_t mask_ema(
    uint8_t* output,
    const uint8_t* prev,
    const uint8_t* curr,
    float alpha,
    int size,
    cudaStream_t stream)
{
    const int blockSize = 256;
    int grid = (size + blockSize - 1) / blockSize;
    mask_ema_kernel<<<grid, blockSize, 0, stream>>>(output, prev, curr, alpha, size);
    return cudaGetLastError();
}

// mask_erode_3x3 applies a 3×3 morphological erosion to the src mask.
// dst and src must be device pointers to width*height bytes.
cudaError_t mask_erode_3x3(
    uint8_t* dst,
    const uint8_t* src,
    int width, int height,
    cudaStream_t stream)
{
    dim3 block(16, 16);
    dim3 grid((width + block.x - 1) / block.x, (height + block.y - 1) / block.y);
    mask_erode_3x3_kernel<<<grid, block, 0, stream>>>(dst, src, width, height);
    return cudaGetLastError();
}

} // extern "C"
