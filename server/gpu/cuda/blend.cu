#include "common.cuh"

// Uniform blend: dst = (a * inv + b * pos + 128) >> 8
// Works for both Y and UV planes (NV12)
__global__ void blend_uniform_kernel(
    uint8_t* __restrict__ dst,
    const uint8_t* __restrict__ a,
    const uint8_t* __restrict__ b,
    int pos256,  // 0-256 fixed-point blend position
    int width, int height, int pitch)
{
    int x = blockIdx.x * blockDim.x + threadIdx.x;
    int y = blockIdx.y * blockDim.y + threadIdx.y;
    if (x >= width || y >= height) return;

    int idx = y * pitch + x;
    int inv = 256 - pos256;
    dst[idx] = (uint8_t)((a[idx] * inv + b[idx] * pos256 + 128) >> 8);
}

// Fade to/from constant value (FTB, dip phase)
__global__ void blend_fade_const_kernel(
    uint8_t* __restrict__ dst,
    const uint8_t* __restrict__ src,
    uint8_t constVal,
    int pos256,
    int width, int height, int pitch)
{
    int x = blockIdx.x * blockDim.x + threadIdx.x;
    int y = blockIdx.y * blockDim.y + threadIdx.y;
    if (x >= width || y >= height) return;

    int idx = y * pitch + x;
    int inv = 256 - pos256;
    dst[idx] = (uint8_t)((src[idx] * inv + constVal * pos256 + 128) >> 8);
}

// Per-pixel alpha blend (wipes, stingers)
__global__ void blend_alpha_kernel(
    uint8_t* __restrict__ dst,
    const uint8_t* __restrict__ a,
    const uint8_t* __restrict__ b,
    const uint8_t* __restrict__ alpha,
    int width, int height, int pitch, int alphaPitch)
{
    int x = blockIdx.x * blockDim.x + threadIdx.x;
    int y = blockIdx.y * blockDim.y + threadIdx.y;
    if (x >= width || y >= height) return;

    int idx = y * pitch + x;
    int aidx = y * alphaPitch + x;
    int al = alpha[aidx] + (alpha[aidx] >> 7); // match CPU: 0-256 range
    int inv = 256 - al;
    dst[idx] = (uint8_t)((a[idx] * inv + b[idx] * al + 128) >> 8);
}

// Generate wipe alpha mask
__global__ void wipe_mask_kernel(
    uint8_t* __restrict__ mask,
    int width, int height, int pitch,
    float position,
    int direction,  // 0=h-left, 1=h-right, 2=v-top, 3=v-bottom, 4=box-center, 5=box-edges
    int softEdge)
{
    int x = blockIdx.x * blockDim.x + threadIdx.x;
    int y = blockIdx.y * blockDim.y + threadIdx.y;
    if (x >= width || y >= height) return;

    float threshold;
    switch (direction) {
    case 0: threshold = (float)x / width; break;
    case 1: threshold = 1.0f - (float)x / width; break;
    case 2: threshold = (float)y / height; break;
    case 3: threshold = 1.0f - (float)y / height; break;
    case 4: {
        float cx = fabsf((float)x / width - 0.5f) * 2.0f;
        float cy = fabsf((float)y / height - 0.5f) * 2.0f;
        threshold = fmaxf(cx, cy);
        break;
    }
    case 5: {
        float cx = fabsf((float)x / width - 0.5f) * 2.0f;
        float cy = fabsf((float)y / height - 0.5f) * 2.0f;
        threshold = 1.0f - fmaxf(cx, cy);
        break;
    }
    default: threshold = (float)x / width; break;
    }

    float edgeF = (float)softEdge / (direction <= 1 ? width : height);
    float alpha;
    if (edgeF < 0.001f) {
        alpha = (position >= threshold) ? 1.0f : 0.0f;
    } else if (position <= threshold - edgeF) {
        alpha = 0.0f;
    } else if (position >= threshold + edgeF) {
        alpha = 1.0f;
    } else {
        alpha = (position - threshold + edgeF) / (2.0f * edgeF);
    }

    mask[y * pitch + x] = (uint8_t)(alpha * 255.0f + 0.5f);
}

// Downsample alpha from luma to chroma resolution (2x2 average)
__global__ void downsample_alpha_2x2_kernel(
    uint8_t* __restrict__ dst,
    const uint8_t* __restrict__ src,
    int chromaW, int chromaH, int srcPitch, int dstPitch)
{
    int cx = blockIdx.x * blockDim.x + threadIdx.x;
    int cy = blockIdx.y * blockDim.y + threadIdx.y;
    if (cx >= chromaW || cy >= chromaH) return;

    int lx = cx * 2;
    int ly = cy * 2;
    int avg = (src[ly * srcPitch + lx] +
               src[ly * srcPitch + lx + 1] +
               src[(ly+1) * srcPitch + lx] +
               src[(ly+1) * srcPitch + lx + 1] + 2) >> 2;
    dst[cy * dstPitch + cx] = (uint8_t)avg;
}

extern "C" {

cudaError_t blend_uniform_nv12(
    uint8_t* dst, const uint8_t* a, const uint8_t* b,
    int pos256, int width, int height, int pitch, cudaStream_t stream)
{
    // Blend Y plane
    dim3 block(BLOCK_DIM_X, BLOCK_DIM_Y);
    dim3 grid((width + block.x - 1) / block.x, (height + block.y - 1) / block.y);
    blend_uniform_kernel<<<grid, block, 0, stream>>>(dst, a, b, pos256, width, height, pitch);

    // Blend UV plane (same operation, half height, full pitch width since UV is interleaved)
    int uvH = height / 2;
    dim3 gridUV((width + block.x - 1) / block.x, (uvH + block.y - 1) / block.y);
    uint8_t* dstUV = dst + pitch * height;
    const uint8_t* aUV = a + pitch * height;
    const uint8_t* bUV = b + pitch * height;
    blend_uniform_kernel<<<gridUV, block, 0, stream>>>(dstUV, aUV, bUV, pos256, width, uvH, pitch);

    return cudaGetLastError();
}

cudaError_t blend_fade_const_nv12(
    uint8_t* dst, const uint8_t* src,
    uint8_t constY, uint8_t constUV, int pos256,
    int width, int height, int pitch, cudaStream_t stream)
{
    dim3 block(BLOCK_DIM_X, BLOCK_DIM_Y);
    dim3 grid((width + block.x - 1) / block.x, (height + block.y - 1) / block.y);
    blend_fade_const_kernel<<<grid, block, 0, stream>>>(dst, src, constY, pos256, width, height, pitch);

    int uvH = height / 2;
    dim3 gridUV((width + block.x - 1) / block.x, (uvH + block.y - 1) / block.y);
    uint8_t* dstUV = dst + pitch * height;
    const uint8_t* srcUV = src + pitch * height;
    blend_fade_const_kernel<<<gridUV, block, 0, stream>>>(dstUV, srcUV, constUV, pos256, width, uvH, pitch);

    return cudaGetLastError();
}

cudaError_t blend_wipe_nv12(
    uint8_t* dst, const uint8_t* a, const uint8_t* b,
    float position, int direction, int softEdge,
    int width, int height, int pitch,
    uint8_t* maskBuf, cudaStream_t stream)
{
    dim3 block(BLOCK_DIM_X, BLOCK_DIM_Y);
    dim3 grid((width + block.x - 1) / block.x, (height + block.y - 1) / block.y);

    // Generate wipe mask at luma resolution
    wipe_mask_kernel<<<grid, block, 0, stream>>>(maskBuf, width, height, pitch, position, direction, softEdge);

    // Blend Y plane with per-pixel alpha
    blend_alpha_kernel<<<grid, block, 0, stream>>>(dst, a, b, maskBuf, width, height, pitch, pitch);

    // Downsample alpha for UV, then blend UV
    int chromaW = width / 2;
    int chromaH = height / 2;
    uint8_t* chromaMask = maskBuf + pitch * height; // reuse tail of mask buffer
    dim3 gridC((chromaW + block.x - 1) / block.x, (chromaH + block.y - 1) / block.y);
    downsample_alpha_2x2_kernel<<<gridC, block, 0, stream>>>(chromaMask, maskBuf, chromaW, chromaH, pitch, pitch);

    // Blend UV with downsampled alpha (UV is interleaved, process as width x chromaH)
    uint8_t* dstUV = dst + pitch * height;
    const uint8_t* aUV = a + pitch * height;
    const uint8_t* bUV = b + pitch * height;
    dim3 gridUV((width + block.x - 1) / block.x, (chromaH + block.y - 1) / block.y);
    // For UV we need chroma-resolution alpha expanded to interleaved width
    // Simpler: blend UV uniformly with the average alpha position (approximate)
    int avgAlpha = (int)(position * 256.0f);
    blend_uniform_kernel<<<gridUV, block, 0, stream>>>(dstUV, aUV, bUV, avgAlpha, width, chromaH, pitch);

    return cudaGetLastError();
}

cudaError_t blend_stinger_nv12(
    uint8_t* dst, const uint8_t* base, const uint8_t* overlay,
    const uint8_t* alpha, int width, int height, int pitch, int alphaPitch,
    cudaStream_t stream)
{
    dim3 block(BLOCK_DIM_X, BLOCK_DIM_Y);
    dim3 grid((width + block.x - 1) / block.x, (height + block.y - 1) / block.y);

    // Blend Y plane with per-pixel alpha
    blend_alpha_kernel<<<grid, block, 0, stream>>>(dst, base, overlay, alpha, width, height, pitch, alphaPitch);

    // For UV: use uniform blend at average alpha (simplified for Phase 5)
    // Full per-pixel chroma alpha requires downsample + expand for NV12 interleaved UV
    int chromaH = height / 2;
    dim3 gridUV((width + block.x - 1) / block.x, (chromaH + block.y - 1) / block.y);
    uint8_t* dstUV = dst + pitch * height;
    const uint8_t* baseUV = base + pitch * height;
    const uint8_t* overlayUV = overlay + pitch * height;
    blend_alpha_kernel<<<gridUV, block, 0, stream>>>(dstUV, baseUV, overlayUV, alpha, width, chromaH, pitch, alphaPitch);

    return cudaGetLastError();
}

} // extern "C"
