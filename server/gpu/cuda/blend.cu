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

// Downsample luma-resolution alpha to NV12 UV-plane width.
// For each chroma sample (cx, cy), computes a 2x2 average from the luma mask
// and writes the result to both the Cb position (2*cx) and Cr position (2*cx+1)
// in the interleaved UV output row. This produces a width-wide alpha buffer
// (same layout as the NV12 UV plane) suitable for blend_alpha_kernel.
__global__ void downsample_alpha_to_nv12_uv_kernel(
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
    // Write to both Cb (even) and Cr (odd) interleaved positions
    dst[cy * dstPitch + cx * 2]     = (uint8_t)avg;
    dst[cy * dstPitch + cx * 2 + 1] = (uint8_t)avg;
}

// ============================================================================
// P210 (10-bit 4:2:2) blend kernels
// ============================================================================
// P210 uses uint16 samples [0, 1023]. Both Y and UV planes are full height.
// Blend arithmetic: (sa*(256-pos) + sb*pos + 128) >> 8, clamped to [0, 1023].

// Uniform blend for P210: dst = (a * inv + b * pos + 128) >> 8
// Operates on uint16 samples. Works for both Y and UV planes.
__global__ void blend_uniform_p210_kernel(
    uint16_t* __restrict__ dst,
    const uint16_t* __restrict__ a,
    const uint16_t* __restrict__ b,
    int pos256,
    int width, int height, int pitch16)
{
    int x = blockIdx.x * blockDim.x + threadIdx.x;
    int y = blockIdx.y * blockDim.y + threadIdx.y;
    if (x >= width || y >= height) return;

    int idx = y * pitch16 + x;
    int inv = 256 - pos256;
    int val = ((int)a[idx] * inv + (int)b[idx] * pos256 + 128) >> 8;
    dst[idx] = (uint16_t)min(max(val, 0), 1023);
}

// Fade to constant value for P210 (FTB, dip phase)
__global__ void blend_fade_const_p210_kernel(
    uint16_t* __restrict__ dst,
    const uint16_t* __restrict__ src,
    uint16_t constVal,
    int pos256,
    int width, int height, int pitch16)
{
    int x = blockIdx.x * blockDim.x + threadIdx.x;
    int y = blockIdx.y * blockDim.y + threadIdx.y;
    if (x >= width || y >= height) return;

    int idx = y * pitch16 + x;
    int inv = 256 - pos256;
    int val = ((int)src[idx] * inv + (int)constVal * pos256 + 128) >> 8;
    dst[idx] = (uint16_t)min(max(val, 0), 1023);
}

// ============================================================================
// Gradient map wipe blend kernels
// ============================================================================

struct WipeMapBlendParams {
    int width, height, pitch, gradPitch;
    float position, softEdge, borderWidth;
    uint8_t borderColorY, borderColorCb, borderColorCr;
    int reverse, multiply;
};

// Y plane gradient map wipe
__global__ void wipe_map_blend_y_kernel(
    const uint8_t* __restrict__ srcA,
    const uint8_t* __restrict__ srcB,
    const uint8_t* __restrict__ gradMap,
    uint8_t* __restrict__ dst,
    WipeMapBlendParams p)
{
    int x = blockIdx.x * blockDim.x + threadIdx.x;
    int y = blockIdx.y * blockDim.y + threadIdx.y;
    if (x >= p.width || y >= p.height) return;

    int idx = y * p.pitch + x;
    int gradIdx = y * p.gradPitch + x;

    int grad = (int)gradMap[gradIdx];
    if (p.reverse) grad = 255 - grad;
    if (p.multiply > 1) grad = (grad * p.multiply) % 256;

    int threshold = (int)(p.position * 255.0f);
    int dist = grad - threshold;
    int halfEdge = max((int)(p.softEdge * 255.0f), 1);

    int alpha;
    if (dist < -halfEdge) { alpha = 255; }
    else if (dist > halfEdge) { alpha = 0; }
    else { alpha = (halfEdge - dist) * 255 / (2 * halfEdge); }

    int borderHalf = (int)(p.borderWidth * 255.0f);
    if (borderHalf > 0 && abs(dist) < borderHalf) {
        dst[idx] = p.borderColorY;
    } else {
        dst[idx] = (uint8_t)(((int)srcB[idx] * alpha + (int)srcA[idx] * (255 - alpha)) >> 8);
    }
}

// UV plane gradient map wipe (NV12 interleaved CbCr)
__global__ void wipe_map_blend_uv_kernel(
    const uint8_t* __restrict__ srcAUV,
    const uint8_t* __restrict__ srcBUV,
    const uint8_t* __restrict__ gradMap,
    uint8_t* __restrict__ dstUV,
    WipeMapBlendParams p)
{
    int cx = blockIdx.x * blockDim.x + threadIdx.x;
    int cy = blockIdx.y * blockDim.y + threadIdx.y;
    int chromaW = p.width / 2;
    int chromaH = p.height / 2;
    if (cx >= chromaW || cy >= chromaH) return;

    int uvIdx = cy * p.pitch + cx * 2;

    // Sample gradient at luma resolution (2x2 block) and average
    int lumaX = cx * 2;
    int lumaY = cy * 2;
    int g00 = gradMap[lumaY * p.gradPitch + lumaX];
    int g10 = gradMap[lumaY * p.gradPitch + min(lumaX + 1, p.width - 1)];
    int g01 = gradMap[min(lumaY + 1, p.height - 1) * p.gradPitch + lumaX];
    int g11 = gradMap[min(lumaY + 1, p.height - 1) * p.gradPitch + min(lumaX + 1, p.width - 1)];
    int grad = (g00 + g10 + g01 + g11 + 2) >> 2;

    if (p.reverse) grad = 255 - grad;
    if (p.multiply > 1) grad = (grad * p.multiply) % 256;

    int threshold = (int)(p.position * 255.0f);
    int dist = grad - threshold;
    int halfEdge = max((int)(p.softEdge * 255.0f), 1);

    int alpha;
    if (dist < -halfEdge) { alpha = 255; }
    else if (dist > halfEdge) { alpha = 0; }
    else { alpha = (halfEdge - dist) * 255 / (2 * halfEdge); }

    int borderHalf = (int)(p.borderWidth * 255.0f);
    if (borderHalf > 0 && abs(dist) < borderHalf) {
        dstUV[uvIdx]     = p.borderColorCb;
        dstUV[uvIdx + 1] = p.borderColorCr;
    } else {
        dstUV[uvIdx]     = (uint8_t)(((int)srcBUV[uvIdx]     * alpha + (int)srcAUV[uvIdx]     * (255 - alpha)) >> 8);
        dstUV[uvIdx + 1] = (uint8_t)(((int)srcBUV[uvIdx + 1] * alpha + (int)srcAUV[uvIdx + 1] * (255 - alpha)) >> 8);
    }
}

extern "C" {

cudaError_t wipe_map_blend_nv12(
    const uint8_t* srcA, const uint8_t* srcB,
    const uint8_t* gradMap, uint8_t* dst,
    int width, int height, int pitch, int gradPitch,
    float position, float softEdge, float borderWidth,
    uint8_t borderColorY, uint8_t borderColorCb, uint8_t borderColorCr,
    int reverse, int multiply, cudaStream_t stream)
{
    WipeMapBlendParams p;
    p.width = width; p.height = height; p.pitch = pitch; p.gradPitch = gradPitch;
    p.position = position; p.softEdge = softEdge; p.borderWidth = borderWidth;
    p.borderColorY = borderColorY; p.borderColorCb = borderColorCb; p.borderColorCr = borderColorCr;
    p.reverse = reverse; p.multiply = multiply;

    dim3 block(BLOCK_DIM_X, BLOCK_DIM_Y);

    // Y plane
    dim3 gridY((width + block.x - 1) / block.x, (height + block.y - 1) / block.y);
    wipe_map_blend_y_kernel<<<gridY, block, 0, stream>>>(srcA, srcB, gradMap, dst, p);

    // UV plane
    int chromaW = width / 2;
    int chromaH = height / 2;
    dim3 gridUV((chromaW + block.x - 1) / block.x, (chromaH + block.y - 1) / block.y);
    const uint8_t* srcAUV = srcA + pitch * height;
    const uint8_t* srcBUV = srcB + pitch * height;
    uint8_t* dstUV = dst + pitch * height;
    wipe_map_blend_uv_kernel<<<gridUV, block, 0, stream>>>(srcAUV, srcBUV, gradMap, dstUV, p);

    return cudaGetLastError();
}

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

    // Downsample luma-resolution mask to NV12-UV-plane width.
    // The output has 'width' bytes per row (each chroma alpha written to both
    // Cb and Cr interleaved positions), stored in the UV-plane slot of maskBuf.
    int chromaW = width / 2;
    int chromaH = height / 2;
    uint8_t* uvMask = maskBuf + pitch * height; // UV-plane slot of maskBuf
    dim3 gridC((chromaW + block.x - 1) / block.x, (chromaH + block.y - 1) / block.y);
    downsample_alpha_to_nv12_uv_kernel<<<gridC, block, 0, stream>>>(uvMask, maskBuf, chromaW, chromaH, pitch, pitch);

    // Blend UV plane with the per-pixel downsampled alpha (full pitch width, half height)
    uint8_t* dstUV = dst + pitch * height;
    const uint8_t* aUV = a + pitch * height;
    const uint8_t* bUV = b + pitch * height;
    dim3 gridUV((width + block.x - 1) / block.x, (chromaH + block.y - 1) / block.y);
    blend_alpha_kernel<<<gridUV, block, 0, stream>>>(dstUV, aUV, bUV, uvMask, width, chromaH, pitch, pitch);

    return cudaGetLastError();
}

cudaError_t blend_stinger_nv12(
    uint8_t* dst, const uint8_t* base, const uint8_t* overlay,
    const uint8_t* alpha, int width, int height, int pitch, int alphaPitch,
    uint8_t* uvAlphaBuf, cudaStream_t stream)
{
    dim3 block(BLOCK_DIM_X, BLOCK_DIM_Y);
    dim3 grid((width + block.x - 1) / block.x, (height + block.y - 1) / block.y);

    // Blend Y plane with per-pixel alpha (alpha is at luma resolution)
    blend_alpha_kernel<<<grid, block, 0, stream>>>(dst, base, overlay, alpha, width, height, pitch, alphaPitch);

    // Downsample luma-resolution alpha to NV12-UV-plane width.
    // Each chroma alpha value is written to both Cb and Cr interleaved positions,
    // producing a width-wide alpha row suitable for blend_alpha_kernel on UV.
    int chromaW = width / 2;
    int chromaH = height / 2;
    dim3 gridC((chromaW + block.x - 1) / block.x, (chromaH + block.y - 1) / block.y);
    downsample_alpha_to_nv12_uv_kernel<<<gridC, block, 0, stream>>>(uvAlphaBuf, alpha, chromaW, chromaH, alphaPitch, pitch);

    // Blend UV plane with per-pixel downsampled alpha
    uint8_t* dstUV = dst + pitch * height;
    const uint8_t* baseUV = base + pitch * height;
    const uint8_t* overlayUV = overlay + pitch * height;
    dim3 gridUV((width + block.x - 1) / block.x, (chromaH + block.y - 1) / block.y);
    blend_alpha_kernel<<<gridUV, block, 0, stream>>>(dstUV, baseUV, overlayUV, uvAlphaBuf, width, chromaH, pitch, pitch);

    return cudaGetLastError();
}

// Per-pixel alpha blend for P210 (uint16 samples, uint8 alpha mask).
// Alpha mask is at luma resolution (uint8). Samples are [0, 1023].
__global__ void blend_alpha_p210_kernel(
    uint16_t* __restrict__ dst,
    const uint16_t* __restrict__ a,
    const uint16_t* __restrict__ b,
    const uint8_t* __restrict__ alpha,
    int width, int height, int pitch16, int alphaPitch)
{
    int x = blockIdx.x * blockDim.x + threadIdx.x;
    int y = blockIdx.y * blockDim.y + threadIdx.y;
    if (x >= width || y >= height) return;

    int idx = y * pitch16 + x;
    int aidx = y * alphaPitch + x;
    int al = alpha[aidx] + (alpha[aidx] >> 7); // match CPU: 0-256 range
    int inv = 256 - al;
    int val = ((int)a[idx] * inv + (int)b[idx] * al + 128) >> 8;
    dst[idx] = (uint16_t)min(max(val, 0), 1023);
}

// Downsample luma-resolution uint8 alpha to P210 UV-plane layout.
// P210 4:2:2: UV has FULL height, half width. For each row, average 2 horizontal
// luma pixels per chroma sample. Write the same alpha to both Cb and Cr
// interleaved positions (2*cx and 2*cx+1) in the output.
__global__ void downsample_alpha_to_p210_uv_kernel(
    uint8_t* __restrict__ dst,
    const uint8_t* __restrict__ src,
    int chromaW, int chromaH, int srcPitch, int dstPitch)
{
    int cx = blockIdx.x * blockDim.x + threadIdx.x;
    int cy = blockIdx.y * blockDim.y + threadIdx.y;
    if (cx >= chromaW || cy >= chromaH) return;

    // P210 4:2:2: UV is full height, half width. Average 2 horizontal luma samples.
    int lx = cx * 2;
    int avg = (src[cy * srcPitch + lx] +
               src[cy * srcPitch + lx + 1] + 1) >> 1;
    // Write to both Cb (even) and Cr (odd) interleaved positions
    dst[cy * dstPitch + cx * 2]     = (uint8_t)avg;
    dst[cy * dstPitch + cx * 2 + 1] = (uint8_t)avg;
}

// --- P210 blend wrappers ---

cudaError_t blend_wipe_p210(
    uint8_t* dst, const uint8_t* a, const uint8_t* b,
    float position, int direction, int softEdge,
    int width, int height, int pitch,
    uint8_t* maskBuf, cudaStream_t stream)
{
    int pitch16 = pitch / 2;
    dim3 block(BLOCK_DIM_X, BLOCK_DIM_Y);

    // Pass 1: Generate wipe mask at luma resolution (uint8, format-independent)
    dim3 grid((width + block.x - 1) / block.x, (height + block.y - 1) / block.y);
    wipe_mask_kernel<<<grid, block, 0, stream>>>(maskBuf, width, height, pitch, position, direction, softEdge);

    // Pass 2: Alpha-blend Y plane (P210 uint16 samples with uint8 alpha)
    blend_alpha_p210_kernel<<<grid, block, 0, stream>>>(
        (uint16_t*)dst, (const uint16_t*)a, (const uint16_t*)b,
        maskBuf, width, height, pitch16, pitch);

    // Pass 3: Downsample luma mask to P210 UV resolution.
    // P210 4:2:2: UV has full height, half width.
    int chromaW = width / 2;
    int chromaH = height;  // Full height for 4:2:2
    uint8_t* uvMask = maskBuf + pitch * height;  // UV-plane slot of maskBuf
    dim3 gridC((chromaW + block.x - 1) / block.x, (chromaH + block.y - 1) / block.y);
    downsample_alpha_to_p210_uv_kernel<<<gridC, block, 0, stream>>>(uvMask, maskBuf, chromaW, chromaH, pitch, pitch);

    // Pass 4: Alpha-blend UV plane (P210: full height, width uint16 samples)
    uint16_t* dstUV = (uint16_t*)(dst + pitch * height);
    const uint16_t* aUV = (const uint16_t*)(a + pitch * height);
    const uint16_t* bUV = (const uint16_t*)(b + pitch * height);
    dim3 gridUV((width + block.x - 1) / block.x, (chromaH + block.y - 1) / block.y);
    blend_alpha_p210_kernel<<<gridUV, block, 0, stream>>>(
        dstUV, aUV, bUV, uvMask, width, chromaH, pitch16, pitch);

    return cudaGetLastError();
}

cudaError_t blend_stinger_p210(
    uint8_t* dst, const uint8_t* base, const uint8_t* overlay,
    const uint8_t* alpha, int width, int height, int pitch, int alphaPitch,
    uint8_t* uvAlphaBuf, cudaStream_t stream)
{
    int pitch16 = pitch / 2;
    dim3 block(BLOCK_DIM_X, BLOCK_DIM_Y);
    dim3 grid((width + block.x - 1) / block.x, (height + block.y - 1) / block.y);

    // Pass 1: Alpha-blend Y plane (P210 uint16 samples with uint8 alpha at luma resolution)
    blend_alpha_p210_kernel<<<grid, block, 0, stream>>>(
        (uint16_t*)dst, (const uint16_t*)base, (const uint16_t*)overlay,
        alpha, width, height, pitch16, alphaPitch);

    // Pass 2: Downsample luma alpha to P210 UV resolution.
    // P210 4:2:2: UV has full height, half width.
    int chromaW = width / 2;
    int chromaH = height;  // Full height for 4:2:2
    dim3 gridC((chromaW + block.x - 1) / block.x, (chromaH + block.y - 1) / block.y);
    downsample_alpha_to_p210_uv_kernel<<<gridC, block, 0, stream>>>(uvAlphaBuf, alpha, chromaW, chromaH, alphaPitch, pitch);

    // Pass 3: Alpha-blend UV plane using downsampled alpha (P210: full height)
    uint16_t* dstUV = (uint16_t*)(dst + pitch * height);
    const uint16_t* baseUV = (const uint16_t*)(base + pitch * height);
    const uint16_t* overlayUV = (const uint16_t*)(overlay + pitch * height);
    dim3 gridUV((width + block.x - 1) / block.x, (chromaH + block.y - 1) / block.y);
    blend_alpha_p210_kernel<<<gridUV, block, 0, stream>>>(
        dstUV, baseUV, overlayUV, uvAlphaBuf, width, chromaH, pitch16, pitch);

    return cudaGetLastError();
}

cudaError_t blend_uniform_p210(
    uint8_t* dst, const uint8_t* a, const uint8_t* b,
    int pos256, int width, int height, int pitch, cudaStream_t stream)
{
    // P210: both Y and UV planes are full height. Total samples = width * height * 2
    // (Y plane: width samples per row, UV plane: width samples per row as interleaved pairs)
    // We can blend both planes in a single dispatch since they're contiguous uint16 samples.
    int pitch16 = pitch / 2;
    int totalHeight = height * 2;  // Y + UV (both full height for 4:2:2)
    dim3 block(BLOCK_DIM_X, BLOCK_DIM_Y);
    dim3 grid((width + block.x - 1) / block.x, (totalHeight + block.y - 1) / block.y);
    blend_uniform_p210_kernel<<<grid, block, 0, stream>>>(
        (uint16_t*)dst, (const uint16_t*)a, (const uint16_t*)b,
        pos256, width, totalHeight, pitch16);
    return cudaGetLastError();
}

cudaError_t blend_fade_const_p210(
    uint8_t* dst, const uint8_t* src,
    uint16_t constY, uint16_t constUV, int pos256,
    int width, int height, int pitch, cudaStream_t stream)
{
    int pitch16 = pitch / 2;
    dim3 block(BLOCK_DIM_X, BLOCK_DIM_Y);

    // Y plane: fade to constY
    dim3 gridY((width + block.x - 1) / block.x, (height + block.y - 1) / block.y);
    blend_fade_const_p210_kernel<<<gridY, block, 0, stream>>>(
        (uint16_t*)dst, (const uint16_t*)src, constY, pos256, width, height, pitch16);

    // UV plane: fade to constUV (full height for 4:2:2)
    uint16_t* dstUV = (uint16_t*)(dst + pitch * height);
    const uint16_t* srcUV = (const uint16_t*)(src + pitch * height);
    blend_fade_const_p210_kernel<<<gridY, block, 0, stream>>>(
        dstUV, srcUV, constUV, pos256, width, height, pitch16);

    return cudaGetLastError();
}

// ============================================================================
// P210 gradient map wipe blend kernels
// ============================================================================

struct WipeMapBlendP210Params {
    int width, height, pitch, gradPitch;
    float position, softEdge, borderWidth;
    uint16_t borderColorY, borderColorCb, borderColorCr;
    int reverse, multiply;
};

// Y plane gradient map wipe for P210.
// Gradient map is uploaded as NV12 (8-bit Y plane) regardless of pool format.
// We read it as uint8 with byte pitch — no format conversion needed.
__global__ void wipe_map_blend_y_p210_kernel(
    const uint16_t* __restrict__ srcA,
    const uint16_t* __restrict__ srcB,
    const uint8_t* __restrict__ gradMap,
    uint16_t* __restrict__ dst,
    WipeMapBlendP210Params p)
{
    int x = blockIdx.x * blockDim.x + threadIdx.x;
    int y = blockIdx.y * blockDim.y + threadIdx.y;
    if (x >= p.width || y >= p.height) return;

    int pitch16 = p.pitch / 2;
    int idx = y * pitch16 + x;
    int gradIdx = y * p.gradPitch + x;

    // Read 8-bit gradient directly (NV12 upload keeps it as uint8)
    int grad = (int)gradMap[gradIdx];
    if (p.reverse) grad = 255 - grad;
    if (p.multiply > 1) grad = (grad * p.multiply) % 256;

    int threshold = (int)(p.position * 255.0f);
    int dist = grad - threshold;
    int halfEdge = max((int)(p.softEdge * 255.0f), 1);

    int alpha;
    if (dist < -halfEdge) { alpha = 255; }
    else if (dist > halfEdge) { alpha = 0; }
    else { alpha = (halfEdge - dist) * 255 / (2 * halfEdge); }

    int borderHalf = (int)(p.borderWidth * 255.0f);
    if (borderHalf > 0 && abs(dist) < borderHalf) {
        dst[idx] = p.borderColorY;
    } else {
        int val = ((int)srcB[idx] * alpha + (int)srcA[idx] * (255 - alpha)) >> 8;
        dst[idx] = (uint16_t)min(max(val, 0), 1023);
    }
}

// UV plane gradient map wipe for P210 (interleaved uint16 CbCr).
// P210 4:2:2: UV has full height, half width. Each chroma sample corresponds
// to 2 horizontal luma pixels. We average the 2 horizontal gradient values.
// Gradient map is NV12 uint8 with byte pitch.
__global__ void wipe_map_blend_uv_p210_kernel(
    const uint16_t* __restrict__ srcAUV,
    const uint16_t* __restrict__ srcBUV,
    const uint8_t* __restrict__ gradMap,
    uint16_t* __restrict__ dstUV,
    WipeMapBlendP210Params p)
{
    int cx = blockIdx.x * blockDim.x + threadIdx.x;
    int cy = blockIdx.y * blockDim.y + threadIdx.y;
    int chromaW = p.width / 2;
    int chromaH = p.height;  // Full height for 4:2:2
    if (cx >= chromaW || cy >= chromaH) return;

    int pitch16 = p.pitch / 2;
    int uvIdx = cy * pitch16 + cx * 2;

    // Sample 8-bit gradient at luma resolution (2 horizontal pixels) and average
    int lumaX = cx * 2;
    int g0 = (int)gradMap[cy * p.gradPitch + lumaX];
    int g1 = (int)gradMap[cy * p.gradPitch + min(lumaX + 1, p.width - 1)];
    int grad = (g0 + g1 + 1) >> 1;

    if (p.reverse) grad = 255 - grad;
    if (p.multiply > 1) grad = (grad * p.multiply) % 256;

    int threshold = (int)(p.position * 255.0f);
    int dist = grad - threshold;
    int halfEdge = max((int)(p.softEdge * 255.0f), 1);

    int alpha;
    if (dist < -halfEdge) { alpha = 255; }
    else if (dist > halfEdge) { alpha = 0; }
    else { alpha = (halfEdge - dist) * 255 / (2 * halfEdge); }

    int borderHalf = (int)(p.borderWidth * 255.0f);
    if (borderHalf > 0 && abs(dist) < borderHalf) {
        dstUV[uvIdx]     = p.borderColorCb;
        dstUV[uvIdx + 1] = p.borderColorCr;
    } else {
        int valCb = ((int)srcBUV[uvIdx]     * alpha + (int)srcAUV[uvIdx]     * (255 - alpha)) >> 8;
        int valCr = ((int)srcBUV[uvIdx + 1] * alpha + (int)srcAUV[uvIdx + 1] * (255 - alpha)) >> 8;
        dstUV[uvIdx]     = (uint16_t)min(max(valCb, 0), 1023);
        dstUV[uvIdx + 1] = (uint16_t)min(max(valCr, 0), 1023);
    }
}

cudaError_t wipe_map_blend_p210(
    const uint8_t* srcA, const uint8_t* srcB,
    const uint8_t* gradMap, uint8_t* dst,
    int width, int height, int pitch, int gradPitch,
    float position, float softEdge, float borderWidth,
    uint16_t borderColorY, uint16_t borderColorCb, uint16_t borderColorCr,
    int reverse, int multiply, cudaStream_t stream)
{
    WipeMapBlendP210Params p;
    p.width = width; p.height = height; p.pitch = pitch; p.gradPitch = gradPitch;
    p.position = position; p.softEdge = softEdge; p.borderWidth = borderWidth;
    p.borderColorY = borderColorY; p.borderColorCb = borderColorCb; p.borderColorCr = borderColorCr;
    p.reverse = reverse; p.multiply = multiply;

    dim3 block(BLOCK_DIM_X, BLOCK_DIM_Y);

    // Y plane
    dim3 gridY((width + block.x - 1) / block.x, (height + block.y - 1) / block.y);
    wipe_map_blend_y_p210_kernel<<<gridY, block, 0, stream>>>(
        (const uint16_t*)srcA, (const uint16_t*)srcB,
        gradMap, (uint16_t*)dst, p);

    // UV plane (full height for 4:2:2)
    int chromaW = width / 2;
    int chromaH = height;
    dim3 gridUV((chromaW + block.x - 1) / block.x, (chromaH + block.y - 1) / block.y);
    const uint16_t* srcAUV = (const uint16_t*)(srcA + pitch * height);
    const uint16_t* srcBUV = (const uint16_t*)(srcB + pitch * height);
    uint16_t* dstUV = (uint16_t*)(dst + pitch * height);
    wipe_map_blend_uv_p210_kernel<<<gridUV, block, 0, stream>>>(
        srcAUV, srcBUV, gradMap, dstUV, p);

    return cudaGetLastError();
}

} // extern "C"
