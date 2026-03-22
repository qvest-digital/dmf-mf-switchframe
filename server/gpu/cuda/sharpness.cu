#include "common.cuh"

// Unsharp mask sharpening on NV12 luma plane only.
// For each pixel: sharpened = original + amount * (original - box_blur_3x3)
// UV plane is untouched — sharpening chroma causes color artifacts.
__global__ void sharpenLumaKernel(
    uint8_t* __restrict__ frame,
    int width, int height, int pitch,
    float amount)
{
    int x = blockIdx.x * blockDim.x + threadIdx.x;
    int y = blockIdx.y * blockDim.y + threadIdx.y;

    if (x >= width || y >= height) return;

    // Skip border pixels (no valid 3x3 neighborhood)
    if (x < 1 || y < 1 || x >= width - 1 || y >= height - 1) return;

    // 3x3 box blur
    float sum = 0.0f;
    for (int dy = -1; dy <= 1; dy++) {
        for (int dx = -1; dx <= 1; dx++) {
            sum += (float)frame[(y + dy) * pitch + (x + dx)];
        }
    }
    float blurred = sum * (1.0f / 9.0f);

    float original = (float)frame[y * pitch + x];
    float sharpened = original + amount * (original - blurred);

    // Clamp to BT.709 limited range luma [16, 235]
    sharpened = fminf(fmaxf(sharpened, 16.0f), 235.0f);

    frame[y * pitch + x] = (uint8_t)(sharpened + 0.5f);
}

extern "C" {

cudaError_t launch_sharpen_luma(
    uint8_t* frame,
    int width, int height, int pitch,
    float amount,
    cudaStream_t stream)
{
    if (amount <= 0.0f) return cudaSuccess;

    dim3 block(BLOCK_DIM_X, BLOCK_DIM_Y);
    dim3 grid((width + BLOCK_DIM_X - 1) / BLOCK_DIM_X,
              (height + BLOCK_DIM_Y - 1) / BLOCK_DIM_Y);

    sharpenLumaKernel<<<grid, block, 0, stream>>>(
        frame, width, height, pitch, amount);
    return cudaGetLastError();
}

} // extern "C"
