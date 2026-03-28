#include "common.cuh"

// Convert YUV420p (3 separate planes) to NV12 (Y + interleaved UV)
// Input:  y_plane[width*height], cb_plane[width/2 * height/2], cr_plane[width/2 * height/2]
// Output: nv12[pitch * height + pitch * height/2] (Y plane then UV plane)
__global__ void yuv420p_to_nv12_kernel(
    uint8_t* __restrict__ nv12,
    const uint8_t* __restrict__ y_src,
    const uint8_t* __restrict__ cb_src,
    const uint8_t* __restrict__ cr_src,
    int width, int height, int nv12_pitch, int src_stride)
{
    int x = blockIdx.x * blockDim.x + threadIdx.x;
    int y = blockIdx.y * blockDim.y + threadIdx.y;

    // Copy Y plane
    if (x < width && y < height) {
        nv12[y * nv12_pitch + x] = y_src[y * src_stride + x];
    }

    // Interleave UV (one thread per chroma pair, half resolution)
    int chromaW = width / 2;
    int chromaH = height / 2;
    if (x < chromaW && y < chromaH) {
        int uv_offset = nv12_pitch * height;  // UV plane starts after Y
        int uv_idx = y * nv12_pitch + x * 2;
        int src_idx = y * (src_stride / 2) + x;
        nv12[uv_offset + uv_idx]     = cb_src[src_idx];
        nv12[uv_offset + uv_idx + 1] = cr_src[src_idx];
    }
}

// Convert NV12 to YUV420p
__global__ void nv12_to_yuv420p_kernel(
    uint8_t* __restrict__ y_dst,
    uint8_t* __restrict__ cb_dst,
    uint8_t* __restrict__ cr_dst,
    const uint8_t* __restrict__ nv12,
    int width, int height, int nv12_pitch, int dst_stride)
{
    int x = blockIdx.x * blockDim.x + threadIdx.x;
    int y = blockIdx.y * blockDim.y + threadIdx.y;

    // Copy Y plane
    if (x < width && y < height) {
        y_dst[y * dst_stride + x] = nv12[y * nv12_pitch + x];
    }

    // De-interleave UV
    int chromaW = width / 2;
    int chromaH = height / 2;
    if (x < chromaW && y < chromaH) {
        int uv_offset = nv12_pitch * height;
        int uv_idx = y * nv12_pitch + x * 2;
        int dst_idx = y * (dst_stride / 2) + x;
        cb_dst[dst_idx] = nv12[uv_offset + uv_idx];
        cr_dst[dst_idx] = nv12[uv_offset + uv_idx + 1];
    }
}

// Fill NV12 frame with constant color (black = Y:16, UV:128 for limited range)
__global__ void nv12_fill_kernel(
    uint8_t* __restrict__ nv12,
    int width, int height, int pitch,
    uint8_t yVal, uint8_t cbVal, uint8_t crVal)
{
    int x = blockIdx.x * blockDim.x + threadIdx.x;
    int y = blockIdx.y * blockDim.y + threadIdx.y;

    // Fill Y plane
    if (x < width && y < height) {
        nv12[y * pitch + x] = yVal;
    }

    // Fill UV plane (interleaved)
    int chromaW = width / 2;
    int chromaH = height / 2;
    if (x < chromaW && y < chromaH) {
        int uv_offset = pitch * height;
        int uv_idx = y * pitch + x * 2;
        nv12[uv_offset + uv_idx]     = cbVal;
        nv12[uv_offset + uv_idx + 1] = crVal;
    }
}

// Convert P016/P010 (16-bit per sample) to NV12 (8-bit per sample).
// P016: Y plane has uint16 samples, UV plane has interleaved uint16 pairs.
// NV12: Y plane has uint8 samples, UV plane has interleaved uint8 pairs.
// Conversion: right-shift by 8 (P016 stores 10-bit left-justified in 16-bit).
__global__ void p016_to_nv12_kernel(
    uint8_t* __restrict__ nv12_dst,
    const uint16_t* __restrict__ p016_y_src,
    const uint16_t* __restrict__ p016_uv_src,
    int width, int height,
    int dst_pitch, int src_pitch_bytes)
{
    int x = blockIdx.x * blockDim.x + threadIdx.x;
    int y = blockIdx.y * blockDim.y + threadIdx.y;

    int src_pitch_samples = src_pitch_bytes / 2;

    // Y plane: convert each uint16 → uint8 (top 8 bits)
    if (x < width && y < height) {
        uint16_t val = p016_y_src[y * src_pitch_samples + x];
        nv12_dst[y * dst_pitch + x] = (uint8_t)(val >> 8);
    }

    // UV plane: convert interleaved uint16 pairs → uint8 pairs
    int chromaW = width / 2;
    int chromaH = height / 2;
    if (x < chromaW && y < chromaH) {
        int uv_dst_offset = dst_pitch * height;
        int src_idx = y * src_pitch_samples + x * 2;
        uint16_t u_val = p016_uv_src[src_idx];
        uint16_t v_val = p016_uv_src[src_idx + 1];
        int dst_idx = uv_dst_offset + y * dst_pitch + x * 2;
        nv12_dst[dst_idx]     = (uint8_t)(u_val >> 8);
        nv12_dst[dst_idx + 1] = (uint8_t)(v_val >> 8);
    }
}

// ============================================================================
// P210 (10-bit 4:2:2 semi-planar) conversion kernels
// ============================================================================

// Convert YUV422P10LE (3 separate uint16 planes) to P210 (Y + interleaved UV)
__global__ void yuv422p10_to_p210_kernel(
    uint16_t* __restrict__ dst,
    const uint16_t* __restrict__ y_src,
    const uint16_t* __restrict__ cb_src,
    const uint16_t* __restrict__ cr_src,
    int width, int height, int dstPitch16, int srcStride)
{
    int x = blockIdx.x * blockDim.x + threadIdx.x;
    int y = blockIdx.y * blockDim.y + threadIdx.y;

    if (x < width && y < height) {
        dst[y * dstPitch16 + x] = y_src[y * srcStride + x];
    }

    int chromaW = width / 2;
    if (x < chromaW && y < height) {
        int uv_offset = dstPitch16 * height;
        int uv_idx = y * dstPitch16 + x * 2;
        int src_idx = y * (srcStride / 2) + x;
        dst[uv_offset + uv_idx]     = cb_src[src_idx];
        dst[uv_offset + uv_idx + 1] = cr_src[src_idx];
    }
}

// Convert P210 to YUV422P10LE (deinterleave)
__global__ void p210_to_yuv422p10_kernel(
    uint16_t* __restrict__ y_dst,
    uint16_t* __restrict__ cb_dst,
    uint16_t* __restrict__ cr_dst,
    const uint16_t* __restrict__ src,
    int width, int height, int srcPitch16, int dstStride)
{
    int x = blockIdx.x * blockDim.x + threadIdx.x;
    int y = blockIdx.y * blockDim.y + threadIdx.y;

    if (x < width && y < height) {
        y_dst[y * dstStride + x] = src[y * srcPitch16 + x];
    }

    int chromaW = width / 2;
    if (x < chromaW && y < height) {
        int uv_offset = srcPitch16 * height;
        int uv_idx = y * srcPitch16 + x * 2;
        int dst_idx = y * (dstStride / 2) + x;
        cb_dst[dst_idx] = src[uv_offset + uv_idx];
        cr_dst[dst_idx] = src[uv_offset + uv_idx + 1];
    }
}

// Fill P210 frame with constant color (10-bit limited range)
__global__ void p210_fill_kernel(
    uint16_t* __restrict__ p210,
    int width, int height, int pitch16,
    uint16_t yVal, uint16_t cbVal, uint16_t crVal)
{
    int x = blockIdx.x * blockDim.x + threadIdx.x;
    int y = blockIdx.y * blockDim.y + threadIdx.y;

    if (x < width && y < height) {
        p210[y * pitch16 + x] = yVal;
    }

    int chromaW = width / 2;
    if (x < chromaW && y < height) {
        int uv_offset = pitch16 * height;
        int uv_idx = y * pitch16 + x * 2;
        p210[uv_offset + uv_idx]     = cbVal;
        p210[uv_offset + uv_idx + 1] = crVal;
    }
}

// C wrapper functions for cgo
extern "C" {

cudaError_t yuv420p_to_nv12(
    uint8_t* nv12, const uint8_t* y, const uint8_t* cb, const uint8_t* cr,
    int width, int height, int nv12_pitch, int src_stride, cudaStream_t stream)
{
    dim3 block(BLOCK_DIM_X, BLOCK_DIM_Y);
    dim3 grid((width + block.x - 1) / block.x, (height + block.y - 1) / block.y);
    yuv420p_to_nv12_kernel<<<grid, block, 0, stream>>>(
        nv12, y, cb, cr, width, height, nv12_pitch, src_stride);
    return cudaGetLastError();
}

cudaError_t nv12_to_yuv420p(
    uint8_t* y, uint8_t* cb, uint8_t* cr, const uint8_t* nv12,
    int width, int height, int nv12_pitch, int dst_stride, cudaStream_t stream)
{
    dim3 block(BLOCK_DIM_X, BLOCK_DIM_Y);
    dim3 grid((width + block.x - 1) / block.x, (height + block.y - 1) / block.y);
    nv12_to_yuv420p_kernel<<<grid, block, 0, stream>>>(
        y, cb, cr, nv12, width, height, nv12_pitch, dst_stride);
    return cudaGetLastError();
}

cudaError_t nv12_fill(
    uint8_t* nv12, int width, int height, int pitch,
    uint8_t yVal, uint8_t cbVal, uint8_t crVal, cudaStream_t stream)
{
    dim3 block(BLOCK_DIM_X, BLOCK_DIM_Y);
    dim3 grid((width + block.x - 1) / block.x, (height + block.y - 1) / block.y);
    nv12_fill_kernel<<<grid, block, 0, stream>>>(
        nv12, width, height, pitch, yVal, cbVal, crVal);
    return cudaGetLastError();
}

cudaError_t p016_to_nv12(
    uint8_t* nv12_dst,
    const uint16_t* p016_y_src, const uint16_t* p016_uv_src,
    int width, int height,
    int dst_pitch, int src_pitch_bytes, cudaStream_t stream)
{
    dim3 block(BLOCK_DIM_X, BLOCK_DIM_Y);
    dim3 grid((width + block.x - 1) / block.x, (height + block.y - 1) / block.y);
    p016_to_nv12_kernel<<<grid, block, 0, stream>>>(
        nv12_dst, p016_y_src, p016_uv_src,
        width, height, dst_pitch, src_pitch_bytes);
    return cudaGetLastError();
}

cudaError_t yuv422p10_to_p210(
    uint8_t* dst, const uint8_t* src,
    int width, int height, int dstPitch, int srcStride,
    cudaStream_t stream)
{
    int dstPitch16 = dstPitch / 2;
    int ySize = srcStride * height;
    int chromaW = srcStride / 2;

    const uint16_t* ySrc  = (const uint16_t*)src;
    const uint16_t* cbSrc = ySrc + ySize;
    const uint16_t* crSrc = cbSrc + chromaW * height;

    dim3 block(BLOCK_DIM_X, BLOCK_DIM_Y);
    dim3 grid((width + block.x - 1) / block.x, (height + block.y - 1) / block.y);
    yuv422p10_to_p210_kernel<<<grid, block, 0, stream>>>(
        (uint16_t*)dst, ySrc, cbSrc, crSrc,
        width, height, dstPitch16, srcStride);
    return cudaGetLastError();
}

cudaError_t p210_to_yuv422p10(
    uint8_t* dst, const uint8_t* src,
    int width, int height, int srcPitch, int dstStride,
    cudaStream_t stream)
{
    int srcPitch16 = srcPitch / 2;
    int ySize = dstStride * height;
    int chromaW = dstStride / 2;

    uint16_t* yDst  = (uint16_t*)dst;
    uint16_t* cbDst = yDst + ySize;
    uint16_t* crDst = cbDst + chromaW * height;

    dim3 block(BLOCK_DIM_X, BLOCK_DIM_Y);
    dim3 grid((width + block.x - 1) / block.x, (height + block.y - 1) / block.y);
    p210_to_yuv422p10_kernel<<<grid, block, 0, stream>>>(
        yDst, cbDst, crDst,
        (const uint16_t*)src,
        width, height, srcPitch16, dstStride);
    return cudaGetLastError();
}

cudaError_t p210_fill(
    uint8_t* p210, int width, int height, int pitch,
    uint16_t yVal, uint16_t cbVal, uint16_t crVal, cudaStream_t stream)
{
    int pitch16 = pitch / 2;
    dim3 block(BLOCK_DIM_X, BLOCK_DIM_Y);
    dim3 grid((width + block.x - 1) / block.x, (height + block.y - 1) / block.y);
    p210_fill_kernel<<<grid, block, 0, stream>>>(
        (uint16_t*)p210, width, height, pitch16, yVal, cbVal, crVal);
    return cudaGetLastError();
}

} // extern "C"
