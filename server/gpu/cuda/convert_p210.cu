#include "common.cuh"

// ============================================================================
// NV12 <-> P210 cross-format conversion + scale kernels
// ============================================================================
//
// NV12: uint8 samples, Y plane [height], UV plane [height/2] (4:2:0)
// P210: uint16 samples, Y plane [height], UV plane [height] (4:2:2)
// 8-bit -> 10-bit: << 2,  10-bit -> 8-bit: >> 2

// ---------------------------------------------------------------------------
// nv12_to_p210: same-resolution format conversion (no scale)
// ---------------------------------------------------------------------------

// Y plane: uint8 -> uint16 (<< 2)
__global__ void nv12_to_p210_y_kernel(
    uint16_t* __restrict__ dst, int dstPitch16,
    const uint8_t* __restrict__ src, int srcPitch,
    int width, int height)
{
    int x = blockIdx.x * blockDim.x + threadIdx.x;
    int y = blockIdx.y * blockDim.y + threadIdx.y;
    if (x >= width || y >= height) return;

    uint8_t val = src[y * srcPitch + x];
    dst[y * dstPitch16 + x] = (uint16_t)(val << 2);
}

// UV plane: NV12 half-height uint8 CbCr -> P210 full-height uint16 CbCr
// Each output row reads from the corresponding NV12 chroma row (y/2),
// effectively duplicating rows to go from 4:2:0 to 4:2:2.
// chromaW is in chroma samples (width/2).
__global__ void nv12_to_p210_uv_kernel(
    uint16_t* __restrict__ dst, int dstPitch16,
    const uint8_t* __restrict__ src, int srcPitch,
    int chromaW, int dstH, int srcChromaH)
{
    int x = blockIdx.x * blockDim.x + threadIdx.x;
    int y = blockIdx.y * blockDim.y + threadIdx.y;
    if (x >= chromaW || y >= dstH) return;

    // Map full-height output row to half-height input row
    int srcY = y * srcChromaH / dstH;
    if (srcY >= srcChromaH) srcY = srcChromaH - 1;

    int srcIdx = srcY * srcPitch + x * 2;
    uint8_t cb = src[srcIdx];
    uint8_t cr = src[srcIdx + 1];

    int dstIdx = y * dstPitch16 + x * 2;
    dst[dstIdx]     = (uint16_t)(cb << 2);
    dst[dstIdx + 1] = (uint16_t)(cr << 2);
}

// ---------------------------------------------------------------------------
// scale_nv12_to_p210: bilinear scale + format conversion
// ---------------------------------------------------------------------------

// Y plane: bilinear sample NV12 uint8 -> P210 uint16
__global__ void scale_nv12_to_p210_y_kernel(
    uint16_t* __restrict__ dst, int dstW, int dstH, int dstPitch16,
    const uint8_t* __restrict__ src, int srcW, int srcH, int srcPitch)
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

    int v00 = src[sy  * srcPitch + sx];
    int v10 = src[sy  * srcPitch + sx1];
    int v01 = src[sy1 * srcPitch + sx];
    int v11 = src[sy1 * srcPitch + sx1];

    int top = v00 + ((v10 - v00) * fx >> 16);
    int bot = v01 + ((v11 - v01) * fx >> 16);
    int val = top + ((bot - top) * fy >> 16);

    // 8-bit -> 10-bit
    dst[dy * dstPitch16 + dx] = (uint16_t)min(max(val << 2, 0), 1023);
}

// UV plane: bilinear sample NV12 half-height uint8 CbCr -> P210 full-height uint16 CbCr
// chromaDstW/chromaSrcW are in chroma samples (width/2).
__global__ void scale_nv12_to_p210_uv_kernel(
    uint16_t* __restrict__ dst, int chromaDstW, int dstH, int dstPitch16,
    const uint8_t* __restrict__ src, int chromaSrcW, int srcChromaH, int srcPitch)
{
    int dx = blockIdx.x * blockDim.x + threadIdx.x;
    int dy = blockIdx.y * blockDim.y + threadIdx.y;
    if (dx >= chromaDstW || dy >= dstH) return;

    float srcXf = (float)dx * (float)(chromaSrcW - 1) / (float)max(chromaDstW - 1, 1);
    float srcYf = (float)dy * (float)(srcChromaH - 1) / (float)max(dstH - 1, 1);
    int sx = (int)srcXf;
    int sy = (int)srcYf;
    int fx = (int)((srcXf - sx) * 65536.0f);
    int fy = (int)((srcYf - sy) * 65536.0f);

    int sx1 = min(sx + 1, chromaSrcW - 1);
    int sy1 = min(sy + 1, srcChromaH - 1);

    int srcByteX0 = sx * 2;
    int srcByteX1 = sx1 * 2;

    // Cb
    int cb00 = src[sy  * srcPitch + srcByteX0];
    int cb10 = src[sy  * srcPitch + srcByteX1];
    int cb01 = src[sy1 * srcPitch + srcByteX0];
    int cb11 = src[sy1 * srcPitch + srcByteX1];

    int cbTop = cb00 + ((cb10 - cb00) * fx >> 16);
    int cbBot = cb01 + ((cb11 - cb01) * fx >> 16);
    int cb = cbTop + ((cbBot - cbTop) * fy >> 16);

    // Cr
    int cr00 = src[sy  * srcPitch + srcByteX0 + 1];
    int cr10 = src[sy  * srcPitch + srcByteX1 + 1];
    int cr01 = src[sy1 * srcPitch + srcByteX0 + 1];
    int cr11 = src[sy1 * srcPitch + srcByteX1 + 1];

    int crTop = cr00 + ((cr10 - cr00) * fx >> 16);
    int crBot = cr01 + ((cr11 - cr01) * fx >> 16);
    int cr = crTop + ((crBot - crTop) * fy >> 16);

    int dstIdx = dy * dstPitch16 + dx * 2;
    dst[dstIdx]     = (uint16_t)min(max(cb << 2, 0), 1023);
    dst[dstIdx + 1] = (uint16_t)min(max(cr << 2, 0), 1023);
}

// ---------------------------------------------------------------------------
// scale_p210_to_nv12: bilinear scale + format conversion (reverse)
// ---------------------------------------------------------------------------

// Y plane: bilinear sample P210 uint16 -> NV12 uint8
__global__ void scale_p210_to_nv12_y_kernel(
    uint8_t* __restrict__ dst, int dstW, int dstH, int dstPitch,
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

    // 10-bit -> 8-bit
    dst[dy * dstPitch + dx] = (uint8_t)min(max(val >> 2, 0), 255);
}

// UV plane: bilinear sample P210 full-height uint16 CbCr -> NV12 half-height uint8 CbCr
// chromaDstW/chromaSrcW in chroma samples (width/2).
// P210 UV has srcH rows (full height), NV12 UV has dstChromaH rows (half height).
__global__ void scale_p210_to_nv12_uv_kernel(
    uint8_t* __restrict__ dst, int chromaDstW, int dstChromaH, int dstPitch,
    const uint16_t* __restrict__ src, int chromaSrcW, int srcH, int srcPitch16)
{
    int dx = blockIdx.x * blockDim.x + threadIdx.x;
    int dy = blockIdx.y * blockDim.y + threadIdx.y;
    if (dx >= chromaDstW || dy >= dstChromaH) return;

    float srcXf = (float)dx * (float)(chromaSrcW - 1) / (float)max(chromaDstW - 1, 1);
    float srcYf = (float)dy * (float)(srcH - 1) / (float)max(dstChromaH - 1, 1);
    int sx = (int)srcXf;
    int sy = (int)srcYf;
    int fx = (int)((srcXf - sx) * 65536.0f);
    int fy = (int)((srcYf - sy) * 65536.0f);

    int sx1 = min(sx + 1, chromaSrcW - 1);
    int sy1 = min(sy + 1, srcH - 1);

    int srcSampleX0 = sx * 2;
    int srcSampleX1 = sx1 * 2;

    // Cb (even element in interleaved pairs)
    int cb00 = src[sy  * srcPitch16 + srcSampleX0];
    int cb10 = src[sy  * srcPitch16 + srcSampleX1];
    int cb01 = src[sy1 * srcPitch16 + srcSampleX0];
    int cb11 = src[sy1 * srcPitch16 + srcSampleX1];

    int cbTop = cb00 + ((cb10 - cb00) * fx >> 16);
    int cbBot = cb01 + ((cb11 - cb01) * fx >> 16);
    int cb = cbTop + ((cbBot - cbTop) * fy >> 16);

    // Cr (odd element)
    int cr00 = src[sy  * srcPitch16 + srcSampleX0 + 1];
    int cr10 = src[sy  * srcPitch16 + srcSampleX1 + 1];
    int cr01 = src[sy1 * srcPitch16 + srcSampleX0 + 1];
    int cr11 = src[sy1 * srcPitch16 + srcSampleX1 + 1];

    int crTop = cr00 + ((cr10 - cr00) * fx >> 16);
    int crBot = cr01 + ((cr11 - cr01) * fx >> 16);
    int cr = crTop + ((crBot - crTop) * fy >> 16);

    int dstByte = dy * dstPitch + dx * 2;
    dst[dstByte]     = (uint8_t)min(max(cb >> 2, 0), 255);
    dst[dstByte + 1] = (uint8_t)min(max(cr >> 2, 0), 255);
}

// ============================================================================
// C wrapper functions
// ============================================================================

extern "C" {

// nv12_to_p210: same-resolution NV12 -> P210 conversion.
// src is NV12 (uint8, 4:2:0), dst is P210 (uint16, 4:2:2).
// Pitch values are in bytes.
cudaError_t nv12_to_p210(
    uint8_t* dst, int dstPitch,
    const uint8_t* src, int srcPitch,
    int width, int height,
    cudaStream_t stream)
{
    int dstPitch16 = dstPitch / 2;
    dim3 block(BLOCK_DIM_X, BLOCK_DIM_Y);

    // Y plane
    dim3 gridY((width + block.x - 1) / block.x, (height + block.y - 1) / block.y);
    nv12_to_p210_y_kernel<<<gridY, block, 0, stream>>>(
        (uint16_t*)dst, dstPitch16,
        src, srcPitch,
        width, height);

    // UV plane: NV12 half-height -> P210 full-height
    int chromaW = width / 2;
    int srcChromaH = height / 2;
    const uint8_t* srcUV = src + srcPitch * height;
    uint16_t* dstUV = (uint16_t*)(dst + dstPitch * height);

    dim3 gridUV((chromaW + block.x - 1) / block.x, (height + block.y - 1) / block.y);
    nv12_to_p210_uv_kernel<<<gridUV, block, 0, stream>>>(
        dstUV, dstPitch16,
        srcUV, srcPitch,
        chromaW, height, srcChromaH);

    return cudaGetLastError();
}

// scale_nv12_to_p210: bilinear scale NV12 -> P210.
// src is NV12 (uint8, 4:2:0), dst is P210 (uint16, 4:2:2).
// Pitch values are in bytes.
cudaError_t scale_nv12_to_p210(
    uint8_t* dst, int dstW, int dstH, int dstPitch,
    const uint8_t* src, int srcW, int srcH, int srcPitch,
    cudaStream_t stream)
{
    int dstPitch16 = dstPitch / 2;
    dim3 block(BLOCK_DIM_X, BLOCK_DIM_Y);

    // Y plane
    dim3 gridY((dstW + block.x - 1) / block.x, (dstH + block.y - 1) / block.y);
    scale_nv12_to_p210_y_kernel<<<gridY, block, 0, stream>>>(
        (uint16_t*)dst, dstW, dstH, dstPitch16,
        src, srcW, srcH, srcPitch);

    // UV plane: NV12 half-height -> P210 full-height
    int chromaSrcW = srcW / 2;
    int chromaDstW = dstW / 2;
    int srcChromaH = srcH / 2;
    const uint8_t* srcUV = src + srcPitch * srcH;
    uint16_t* dstUV = (uint16_t*)(dst + dstPitch * dstH);

    dim3 gridUV((chromaDstW + block.x - 1) / block.x, (dstH + block.y - 1) / block.y);
    scale_nv12_to_p210_uv_kernel<<<gridUV, block, 0, stream>>>(
        dstUV, chromaDstW, dstH, dstPitch16,
        srcUV, chromaSrcW, srcChromaH, srcPitch);

    return cudaGetLastError();
}

// scale_p210_to_nv12: bilinear scale P210 -> NV12.
// src is P210 (uint16, 4:2:2), dst is NV12 (uint8, 4:2:0).
// Pitch values are in bytes.
cudaError_t scale_p210_to_nv12(
    uint8_t* dst, int dstW, int dstH, int dstPitch,
    const uint8_t* src, int srcW, int srcH, int srcPitch,
    cudaStream_t stream)
{
    int srcPitch16 = srcPitch / 2;
    dim3 block(BLOCK_DIM_X, BLOCK_DIM_Y);

    // Y plane
    dim3 gridY((dstW + block.x - 1) / block.x, (dstH + block.y - 1) / block.y);
    scale_p210_to_nv12_y_kernel<<<gridY, block, 0, stream>>>(
        dst, dstW, dstH, dstPitch,
        (const uint16_t*)src, srcW, srcH, srcPitch16);

    // UV plane: P210 full-height -> NV12 half-height
    int chromaSrcW = srcW / 2;
    int chromaDstW = dstW / 2;
    int dstChromaH = dstH / 2;
    const uint16_t* srcUV = (const uint16_t*)(src + srcPitch * srcH);
    uint8_t* dstUV = dst + dstPitch * dstH;

    dim3 gridUV((chromaDstW + block.x - 1) / block.x, (dstChromaH + block.y - 1) / block.y);
    scale_p210_to_nv12_uv_kernel<<<gridUV, block, 0, stream>>>(
        dstUV, chromaDstW, dstChromaH, dstPitch,
        srcUV, chromaSrcW, srcH, srcPitch16);

    return cudaGetLastError();
}

} // extern "C"

// ---------------------------------------------------------------------------
// p210_to_p010_msb: P210 (LSB, 4:2:2) -> P010 (MSB, 4:2:0)
// Chroma downsample (average adjacent rows) + left-shift 6 for MSB alignment.
// Used at NVENC encode time — NVENC doesn't support 4:2:2.
// ---------------------------------------------------------------------------

// Y plane: uint16 LSB -> uint16 MSB (<<6), same dimensions
__global__ void p210_to_p010_msb_y_kernel(
    uint16_t* __restrict__ dst, int dstPitch16,
    const uint16_t* __restrict__ src, int srcPitch16,
    int width, int height)
{
    int x = blockIdx.x * blockDim.x + threadIdx.x;
    int y = blockIdx.y * blockDim.y + threadIdx.y;
    if (x >= width || y >= height) return;
    dst[y * dstPitch16 + x] = src[y * srcPitch16 + x] << 6;
}

// UV plane: P210 full-height uint16 CbCr -> P010 half-height uint16 CbCr
// Average pairs of adjacent rows + <<6 shift.
__global__ void p210_to_p010_msb_uv_kernel(
    uint16_t* __restrict__ dst, int dstPitch16,
    const uint16_t* __restrict__ src, int srcPitch16,
    int chromaW, int chromaH)  // chromaH = output height (input height / 2)
{
    int x = blockIdx.x * blockDim.x + threadIdx.x;
    int y = blockIdx.y * blockDim.y + threadIdx.y;
    if (x >= chromaW * 2 || y >= chromaH) return;  // chromaW*2 for interleaved CbCr

    // Read two source rows (4:2:2 full height) and average
    uint16_t v0 = src[(y * 2) * srcPitch16 + x];
    uint16_t v1 = src[(y * 2 + 1) * srcPitch16 + x];
    uint16_t avg = (uint16_t)(((uint32_t)v0 + (uint32_t)v1 + 1) >> 1);
    dst[y * dstPitch16 + x] = avg << 6;
}

extern "C" cudaError_t p210_to_p010_msb(
    uint8_t* dst, int dstWidth, int dstHeight, int dstPitch,
    const uint8_t* src, int srcWidth, int srcHeight, int srcPitch,
    cudaStream_t stream)
{
    int dstPitch16 = dstPitch / 2;
    int srcPitch16 = srcPitch / 2;

    // Y plane
    dim3 block(32, 8);
    dim3 gridY((srcWidth + 31) / 32, (srcHeight + 7) / 8);
    p210_to_p010_msb_y_kernel<<<gridY, block, 0, stream>>>(
        (uint16_t*)dst, dstPitch16,
        (const uint16_t*)src, srcPitch16,
        srcWidth, srcHeight);
    cudaError_t err = cudaGetLastError();
    if (err != cudaSuccess) return err;

    // UV plane: P210 UV at src + srcPitch*srcHeight, P010 UV at dst + dstPitch*dstHeight
    int chromaW = srcWidth / 2;
    int chromaH = srcHeight / 2;  // output half-height (4:2:0)
    dim3 gridUV((chromaW * 2 + 31) / 32, (chromaH + 7) / 8);
    p210_to_p010_msb_uv_kernel<<<gridUV, block, 0, stream>>>(
        (uint16_t*)(dst + dstPitch * dstHeight), dstPitch16,
        (const uint16_t*)(src + srcPitch * srcHeight), srcPitch16,
        chromaW, chromaH);
    return cudaGetLastError();
}
