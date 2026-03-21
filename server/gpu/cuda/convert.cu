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

} // extern "C"
