#include "common.cuh"

// AR compositor kernel: alpha-blend a rendered RGBA layer onto an NV12 video
// frame with depth-aware occlusion masking.
//
// Inputs:
//   nv12       – NV12 video frame (Y + interleaved UV), modified in place
//   render     – 3D-rendered RGBA (uint8, 4 bytes/pixel), may be NULL
//   occlusion  – per-pixel foreground confidence (uint8, 0=bg 255=fg), may be NULL
//   render_w/h – render layer dimensions (may differ from frame)
//   width/h    – frame dimensions
//   pitch      – NV12 row pitch in bytes
//   threshold  – occlusion value above which real-world object occludes virtual (0-255)
__global__ void ar_composite_kernel(
    uint8_t* __restrict__ nv12,
    const uint8_t* __restrict__ render,
    const uint8_t* __restrict__ occlusion,
    int render_w, int render_h,
    int width, int height, int pitch,
    int threshold)
{
    int x = blockIdx.x * blockDim.x + threadIdx.x;
    int y = blockIdx.y * blockDim.y + threadIdx.y;
    if (x >= width || y >= height) return;

    // No render layer — passthrough
    if (render == NULL) return;

    // Sample rendered RGBA. If render dimensions differ from frame, use
    // nearest-neighbor mapping (same approach as dsk.cu rect kernel).
    int sx, sy;
    if (render_w == width && render_h == height) {
        sx = x;
        sy = y;
    } else {
        sx = x * render_w / max(width, 1);
        sy = y * render_h / max(height, 1);
        sx = min(sx, render_w - 1);
        sy = min(sy, render_h - 1);
    }

    int rgbaIdx = (sy * render_w + sx) * 4;
    int r = render[rgbaIdx];
    int g = render[rgbaIdx + 1];
    int b = render[rgbaIdx + 2];
    int a = render[rgbaIdx + 3];

    // Fully transparent — passthrough
    if (a == 0) return;

    // Occlusion check: if the real-world foreground confidence exceeds the
    // threshold, the real object is in front of the virtual — don't overlay.
    if (occlusion != NULL) {
        int occIdx = y * width + x;
        if (occlusion[occIdx] > threshold) return;
    }

    int inv = 256 - a;

    // BT.709 limited-range RGB → YCbCr (integer, matches dsk.cu coefficients)
    int overlayY  = 16  + ((47 * r + 157 * g + 16 * b + 128) >> 8);
    int overlayCb = 128 + ((-26 * r - 86 * g + 112 * b + 128) >> 8);
    int overlayCr = 128 + ((112 * r - 102 * g - 10 * b + 128) >> 8);

    // Clamp to valid range
    overlayY  = min(max(overlayY, 16), 235);
    overlayCb = min(max(overlayCb, 16), 240);
    overlayCr = min(max(overlayCr, 16), 240);

    // Blend Y plane
    int yIdx = y * pitch + x;
    nv12[yIdx] = (uint8_t)((nv12[yIdx] * inv + overlayY * a + 128) >> 8);

    // Blend UV plane (once per 2x2 block — top-left pixel only)
    if ((x & 1) == 0 && (y & 1) == 0) {
        int uvOffset = pitch * height;
        int uvIdx = (y / 2) * pitch + x;
        int curCb = nv12[uvOffset + uvIdx];
        int curCr = nv12[uvOffset + uvIdx + 1];
        nv12[uvOffset + uvIdx]     = (uint8_t)((curCb * inv + overlayCb * a + 128) >> 8);
        nv12[uvOffset + uvIdx + 1] = (uint8_t)((curCr * inv + overlayCr * a + 128) >> 8);
    }
}

// Convert float4 RGBA render output (OptiX) to packed uint8 RGBA.
// Each channel is clamped to [0,1] then scaled to [0,255].
__global__ void float4_to_rgba8_kernel(
    uint8_t* __restrict__ output,     // packed RGBA (W*H*4 bytes)
    const float4* __restrict__ input, // float4 per pixel
    int width, int height)
{
    int x = blockIdx.x * blockDim.x + threadIdx.x;
    int y = blockIdx.y * blockDim.y + threadIdx.y;
    if (x >= width || y >= height) return;

    int idx = y * width + x;
    float4 px = input[idx];

    // Clamp [0,1] and convert to uint8
    output[idx * 4 + 0] = (uint8_t)(fminf(fmaxf(px.x, 0.0f), 1.0f) * 255.0f + 0.5f);
    output[idx * 4 + 1] = (uint8_t)(fminf(fmaxf(px.y, 0.0f), 1.0f) * 255.0f + 0.5f);
    output[idx * 4 + 2] = (uint8_t)(fminf(fmaxf(px.z, 0.0f), 1.0f) * 255.0f + 0.5f);
    output[idx * 4 + 3] = (uint8_t)(fminf(fmaxf(px.w, 0.0f), 1.0f) * 255.0f + 0.5f);
}

extern "C" {

cudaError_t launch_float4_to_rgba8(
    uint8_t* output, const float4* input,
    int width, int height, cudaStream_t stream)
{
    dim3 block(BLOCK_DIM_X, BLOCK_DIM_Y);
    dim3 grid((width + block.x - 1) / block.x, (height + block.y - 1) / block.y);
    float4_to_rgba8_kernel<<<grid, block, 0, stream>>>(output, input, width, height);
    return cudaGetLastError();
}

cudaError_t launch_ar_composite(
    uint8_t* nv12,
    const uint8_t* render,
    const uint8_t* occlusion,
    int render_w, int render_h,
    int width, int height, int pitch,
    int threshold,
    cudaStream_t stream)
{
    dim3 block(BLOCK_DIM_X, BLOCK_DIM_Y);
    dim3 grid((width + block.x - 1) / block.x, (height + block.y - 1) / block.y);
    ar_composite_kernel<<<grid, block, 0, stream>>>(
        nv12, render, occlusion,
        render_w, render_h,
        width, height, pitch,
        threshold);
    return cudaGetLastError();
}

} // extern "C"
