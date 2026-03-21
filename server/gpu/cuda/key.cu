#include "common.cuh"

// Fused chroma key: compute mask from NV12 UV plane + optional spill suppression
// Each thread handles one luma pixel. Chroma is read at half-resolution from NV12 UV plane.
__global__ void chroma_key_nv12_kernel(
    uint8_t* __restrict__ nv12,        // NV12 frame (modified in-place for spill)
    uint8_t* __restrict__ mask,         // Output: luma-resolution alpha mask
    int width, int height, int pitch,
    uint8_t keyCb, uint8_t keyCr,
    int simDistSq, int totalDistSq,
    float spillSuppress,
    uint8_t spillReplaceCb, uint8_t spillReplaceCr)
{
    int x = blockIdx.x * blockDim.x + threadIdx.x;
    int y = blockIdx.y * blockDim.y + threadIdx.y;
    if (x >= width || y >= height) return;

    // Read chroma for this 2x2 block from NV12 UV plane
    int cx = x / 2;
    int cy = y / 2;
    int uvOffset = pitch * height;
    int uvIdx = cy * pitch + cx * 2;
    int cb = nv12[uvOffset + uvIdx];
    int cr = nv12[uvOffset + uvIdx + 1];

    // Squared distance from key color in Cb/Cr space
    int dcb = cb - (int)keyCb;
    int dcr = cr - (int)keyCr;
    int distSq = dcb * dcb + dcr * dcr;

    // Generate alpha mask
    uint8_t alpha;
    if (distSq <= simDistSq) {
        alpha = 0;  // fully keyed (transparent)
    } else if (distSq >= totalDistSq) {
        alpha = 255;  // fully opaque
    } else {
        float t = (float)(distSq - simDistSq) / (float)(totalDistSq - simDistSq);
        alpha = (uint8_t)(sqrtf(t) * 255.0f);
    }
    mask[y * pitch + x] = alpha;

    // Spill suppression: desaturate chroma near key color
    // Only modify chroma once per 2x2 block (top-left pixel of the block)
    if (spillSuppress > 0.0f && (x & 1) == 0 && (y & 1) == 0 && distSq < totalDistSq) {
        float amount = 1.0f - sqrtf((float)distSq / (float)totalDistSq);
        amount *= spillSuppress;
        int newCb = cb + (int)((spillReplaceCb - cb) * amount);
        int newCr = cr + (int)((spillReplaceCr - cr) * amount);
        nv12[uvOffset + uvIdx]     = (uint8_t)min(max(newCb, 16), 240);
        nv12[uvOffset + uvIdx + 1] = (uint8_t)min(max(newCr, 16), 240);
    }
}

// Luma key: threshold-based with precomputed LUT in constant memory
__constant__ uint8_t d_lumaKeyLUT[256];

__global__ void luma_key_nv12_kernel(
    const uint8_t* __restrict__ nv12,
    uint8_t* __restrict__ mask,
    int width, int height, int pitch)
{
    int x = blockIdx.x * blockDim.x + threadIdx.x;
    int y = blockIdx.y * blockDim.y + threadIdx.y;
    if (x >= width || y >= height) return;

    mask[y * pitch + x] = d_lumaKeyLUT[nv12[y * pitch + x]];
}

extern "C" {

cudaError_t chroma_key_nv12(
    uint8_t* nv12, uint8_t* mask,
    int width, int height, int pitch,
    uint8_t keyCb, uint8_t keyCr,
    int simDistSq, int totalDistSq,
    float spillSuppress,
    uint8_t spillReplaceCb, uint8_t spillReplaceCr,
    cudaStream_t stream)
{
    dim3 block(BLOCK_DIM_X, BLOCK_DIM_Y);
    dim3 grid((width + block.x - 1) / block.x, (height + block.y - 1) / block.y);
    chroma_key_nv12_kernel<<<grid, block, 0, stream>>>(
        nv12, mask, width, height, pitch,
        keyCb, keyCr, simDistSq, totalDistSq,
        spillSuppress, spillReplaceCb, spillReplaceCr);
    return cudaGetLastError();
}

cudaError_t luma_key_upload_lut(const uint8_t* lut) {
    return cudaMemcpyToSymbol(d_lumaKeyLUT, lut, 256);
}

cudaError_t luma_key_nv12(
    const uint8_t* nv12, uint8_t* mask,
    int width, int height, int pitch,
    cudaStream_t stream)
{
    dim3 block(BLOCK_DIM_X, BLOCK_DIM_Y);
    dim3 grid((width + block.x - 1) / block.x, (height + block.y - 1) / block.y);
    luma_key_nv12_kernel<<<grid, block, 0, stream>>>(nv12, mask, width, height, pitch);
    return cudaGetLastError();
}

} // extern "C"
