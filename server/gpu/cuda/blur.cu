#include "common.cuh"

// Horizontal Gaussian blur pass: src uint8 -> tmp float
__global__ void gaussian_blur_h_kernel(
    float* __restrict__ tmp,
    const uint8_t* __restrict__ src,
    const float* __restrict__ weights,
    int width, int height, int pitch, int radius)
{
    int x = blockIdx.x * blockDim.x + threadIdx.x;
    int y = blockIdx.y * blockDim.y + threadIdx.y;
    if (x >= width || y >= height) return;

    float acc = 0.0f;
    for (int k = -radius; k <= radius; ++k) {
        int sx = x + k;
        if (sx < 0) sx = 0;
        if (sx >= width) sx = width - 1;
        acc += weights[k + radius] * (float)src[y * pitch + sx];
    }
    tmp[y * pitch + x] = acc;
}

// Vertical Gaussian blur pass: tmp float -> dst uint8
__global__ void gaussian_blur_v_kernel(
    uint8_t* __restrict__ dst,
    const float* __restrict__ tmp,
    const float* __restrict__ weights,
    int width, int height, int pitch, int radius)
{
    int x = blockIdx.x * blockDim.x + threadIdx.x;
    int y = blockIdx.y * blockDim.y + threadIdx.y;
    if (x >= width || y >= height) return;

    float acc = 0.0f;
    for (int k = -radius; k <= radius; ++k) {
        int sy = y + k;
        if (sy < 0) sy = 0;
        if (sy >= height) sy = height - 1;
        acc += weights[k + radius] * tmp[sy * pitch + x];
    }
    if (acc < 0.0f)   acc = 0.0f;
    if (acc > 255.0f) acc = 255.0f;
    dst[y * pitch + x] = (uint8_t)(acc + 0.5f);
}

// Horizontal UV blur: src uint8 interleaved CbCr -> tmp float interleaved
__global__ void gaussian_blur_uv_h_kernel(
    float* __restrict__ tmp,
    const uint8_t* __restrict__ src,
    const float* __restrict__ weights,
    int chromaW, int chromaH, int pitch, int radius)
{
    int cx = blockIdx.x * blockDim.x + threadIdx.x;
    int cy = blockIdx.y * blockDim.y + threadIdx.y;
    if (cx >= chromaW || cy >= chromaH) return;

    float accCb = 0.0f;
    float accCr = 0.0f;
    for (int k = -radius; k <= radius; ++k) {
        int sx = cx + k;
        if (sx < 0) sx = 0;
        if (sx >= chromaW) sx = chromaW - 1;
        int byteIdx = cy * pitch + sx * 2;
        accCb += weights[k + radius] * (float)src[byteIdx];
        accCr += weights[k + radius] * (float)src[byteIdx + 1];
    }
    int outIdx = cy * pitch + cx * 2;
    tmp[outIdx]     = accCb;
    tmp[outIdx + 1] = accCr;
}

// Vertical UV blur: tmp float interleaved -> dst uint8 interleaved
__global__ void gaussian_blur_uv_v_kernel(
    uint8_t* __restrict__ dst,
    const float* __restrict__ tmp,
    const float* __restrict__ weights,
    int chromaW, int chromaH, int pitch, int radius)
{
    int cx = blockIdx.x * blockDim.x + threadIdx.x;
    int cy = blockIdx.y * blockDim.y + threadIdx.y;
    if (cx >= chromaW || cy >= chromaH) return;

    float accCb = 0.0f;
    float accCr = 0.0f;
    for (int k = -radius; k <= radius; ++k) {
        int sy = cy + k;
        if (sy < 0) sy = 0;
        if (sy >= chromaH) sy = chromaH - 1;
        int inIdx = sy * pitch + cx * 2;
        accCb += weights[k + radius] * tmp[inIdx];
        accCr += weights[k + radius] * tmp[inIdx + 1];
    }
    if (accCb < 0.0f)   accCb = 0.0f;
    if (accCb > 255.0f) accCb = 255.0f;
    if (accCr < 0.0f)   accCr = 0.0f;
    if (accCr > 255.0f) accCr = 255.0f;
    int outIdx = cy * pitch + cx * 2;
    dst[outIdx]     = (uint8_t)(accCb + 0.5f);
    dst[outIdx + 1] = (uint8_t)(accCr + 0.5f);
}

// ============================================================================
// P210 (10-bit 4:2:2) Gaussian blur kernels
// ============================================================================
// P210 Y plane: uint16 samples. UV plane: interleaved uint16 CbCr, full height.
// All values clamped to [0, 1023].

// Horizontal P210 Y blur: src uint16 -> tmp float
__global__ void gaussian_blur_p210_h_kernel(
    float* __restrict__ tmp,
    const uint16_t* __restrict__ src,
    const float* __restrict__ weights,
    int width, int height, int samplesPerRow, int radius)
{
    int x = blockIdx.x * blockDim.x + threadIdx.x;
    int y = blockIdx.y * blockDim.y + threadIdx.y;
    if (x >= width || y >= height) return;

    float acc = 0.0f;
    for (int k = -radius; k <= radius; ++k) {
        int sx = x + k;
        if (sx < 0) sx = 0;
        if (sx >= width) sx = width - 1;
        acc += weights[k + radius] * (float)src[y * samplesPerRow + sx];
    }
    tmp[y * samplesPerRow + x] = acc;
}

// Vertical P210 Y blur: tmp float -> dst uint16
__global__ void gaussian_blur_p210_v_kernel(
    uint16_t* __restrict__ dst,
    const float* __restrict__ tmp,
    const float* __restrict__ weights,
    int width, int height, int samplesPerRow, int radius)
{
    int x = blockIdx.x * blockDim.x + threadIdx.x;
    int y = blockIdx.y * blockDim.y + threadIdx.y;
    if (x >= width || y >= height) return;

    float acc = 0.0f;
    for (int k = -radius; k <= radius; ++k) {
        int sy = y + k;
        if (sy < 0) sy = 0;
        if (sy >= height) sy = height - 1;
        acc += weights[k + radius] * tmp[sy * samplesPerRow + x];
    }
    if (acc < 0.0f)    acc = 0.0f;
    if (acc > 1023.0f) acc = 1023.0f;
    dst[y * samplesPerRow + x] = (uint16_t)(acc + 0.5f);
}

// Horizontal P210 UV blur: interleaved uint16 CbCr -> float
__global__ void gaussian_blur_p210_uv_h_kernel(
    float* __restrict__ tmp,
    const uint16_t* __restrict__ src,
    const float* __restrict__ weights,
    int chromaW, int height, int samplesPerRow, int radius)
{
    int cx = blockIdx.x * blockDim.x + threadIdx.x;
    int cy = blockIdx.y * blockDim.y + threadIdx.y;
    if (cx >= chromaW || cy >= height) return;

    float accCb = 0.0f;
    float accCr = 0.0f;
    for (int k = -radius; k <= radius; ++k) {
        int sx = cx + k;
        if (sx < 0) sx = 0;
        if (sx >= chromaW) sx = chromaW - 1;
        accCb += weights[k + radius] * (float)src[cy * samplesPerRow + sx * 2];
        accCr += weights[k + radius] * (float)src[cy * samplesPerRow + sx * 2 + 1];
    }
    int outIdx = cy * samplesPerRow + cx * 2;
    tmp[outIdx]     = accCb;
    tmp[outIdx + 1] = accCr;
}

// Vertical P210 UV blur: float -> interleaved uint16 CbCr
__global__ void gaussian_blur_p210_uv_v_kernel(
    uint16_t* __restrict__ dst,
    const float* __restrict__ tmp,
    const float* __restrict__ weights,
    int chromaW, int height, int samplesPerRow, int radius)
{
    int cx = blockIdx.x * blockDim.x + threadIdx.x;
    int cy = blockIdx.y * blockDim.y + threadIdx.y;
    if (cx >= chromaW || cy >= height) return;

    float accCb = 0.0f;
    float accCr = 0.0f;
    for (int k = -radius; k <= radius; ++k) {
        int sy = cy + k;
        if (sy < 0) sy = 0;
        if (sy >= height) sy = height - 1;
        int inIdx = sy * samplesPerRow + cx * 2;
        accCb += weights[k + radius] * tmp[inIdx];
        accCr += weights[k + radius] * tmp[inIdx + 1];
    }
    if (accCb < 0.0f)    accCb = 0.0f;
    if (accCb > 1023.0f) accCb = 1023.0f;
    if (accCr < 0.0f)    accCr = 0.0f;
    if (accCr > 1023.0f) accCr = 1023.0f;
    int outIdx = cy * samplesPerRow + cx * 2;
    dst[outIdx]     = (uint16_t)(accCb + 0.5f);
    dst[outIdx + 1] = (uint16_t)(accCr + 0.5f);
}

extern "C" {

cudaError_t gaussian_blur_nv12(
    uint8_t* dst, const uint8_t* src,
    float* tmpBuf, const float* weights,
    int width, int height, int pitch, int radius,
    cudaStream_t stream)
{
    dim3 block(BLOCK_DIM_X, BLOCK_DIM_Y);

    // --- Y plane ---
    dim3 gridY((width + block.x - 1) / block.x, (height + block.y - 1) / block.y);
    // Pass 1: horizontal
    gaussian_blur_h_kernel<<<gridY, block, 0, stream>>>(tmpBuf, src, weights, width, height, pitch, radius);
    // Pass 2: vertical
    gaussian_blur_v_kernel<<<gridY, block, 0, stream>>>(dst, tmpBuf, weights, width, height, pitch, radius);

    // --- UV plane ---
    int chromaW = width / 2;
    int chromaH = height / 2;
    int uvOffset = pitch * height;
    dim3 gridUV((chromaW + block.x - 1) / block.x, (chromaH + block.y - 1) / block.y);
    // Pass 3: horizontal UV
    gaussian_blur_uv_h_kernel<<<gridUV, block, 0, stream>>>(
        tmpBuf + uvOffset, src + uvOffset, weights, chromaW, chromaH, pitch, radius);
    // Pass 4: vertical UV
    gaussian_blur_uv_v_kernel<<<gridUV, block, 0, stream>>>(
        dst + uvOffset, tmpBuf + uvOffset, weights, chromaW, chromaH, pitch, radius);

    return cudaGetLastError();
}

cudaError_t gaussian_blur_p210(
    uint16_t* dst, const uint16_t* src,
    float* tmpBuf, const float* weights,
    int width, int height, int pitch, int radius,
    cudaStream_t stream)
{
    int samplesPerRow = pitch / 2; // pitch is in bytes, uint16 samples
    dim3 block(BLOCK_DIM_X, BLOCK_DIM_Y);

    // --- Y plane ---
    dim3 gridY((width + block.x - 1) / block.x, (height + block.y - 1) / block.y);
    // Pass 1: horizontal Y
    gaussian_blur_p210_h_kernel<<<gridY, block, 0, stream>>>(
        tmpBuf, src, weights, width, height, samplesPerRow, radius);
    // Pass 2: vertical Y
    gaussian_blur_p210_v_kernel<<<gridY, block, 0, stream>>>(
        dst, tmpBuf, weights, width, height, samplesPerRow, radius);

    // --- UV plane (full height for 4:2:2) ---
    int chromaW = width / 2;
    // UV starts at pitch * height bytes from base. In uint16 terms: samplesPerRow * height.
    int uvSampleOffset = samplesPerRow * height;
    dim3 gridUV((chromaW + block.x - 1) / block.x, (height + block.y - 1) / block.y);
    // Pass 3: horizontal UV
    gaussian_blur_p210_uv_h_kernel<<<gridUV, block, 0, stream>>>(
        tmpBuf + uvSampleOffset, src + uvSampleOffset, weights,
        chromaW, height, samplesPerRow, radius);
    // Pass 4: vertical UV
    gaussian_blur_p210_uv_v_kernel<<<gridUV, block, 0, stream>>>(
        dst + uvSampleOffset, tmpBuf + uvSampleOffset, weights,
        chromaW, height, samplesPerRow, radius);

    return cudaGetLastError();
}

} // extern "C"
