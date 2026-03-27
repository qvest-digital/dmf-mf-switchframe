#include "common.cuh"
#include <cuda_fp16.h>

// Bilinear sample from float16 depth map at fractional coordinates.
// Returns the interpolated depth value as float32.
__device__ float bilinear_sample_depth(
    const __half* __restrict__ depth,
    float fx, float fy,
    int depth_w, int depth_h)
{
    // Clamp to valid range
    fx = fmaxf(0.0f, fminf(fx, (float)(depth_w - 1)));
    fy = fmaxf(0.0f, fminf(fy, (float)(depth_h - 1)));

    int x0 = (int)fx;
    int y0 = (int)fy;
    int x1 = min(x0 + 1, depth_w - 1);
    int y1 = min(y0 + 1, depth_h - 1);

    float dx = fx - (float)x0;
    float dy = fy - (float)y0;

    float d00 = __half2float(depth[y0 * depth_w + x0]);
    float d10 = __half2float(depth[y0 * depth_w + x1]);
    float d01 = __half2float(depth[y1 * depth_w + x0]);
    float d11 = __half2float(depth[y1 * depth_w + x1]);

    float top    = d00 * (1.0f - dx) + d10 * dx;
    float bottom = d01 * (1.0f - dx) + d11 * dx;
    return top * (1.0f - dy) + bottom * dy;
}

// Fuse depth map and SAM segmentation mask into a single occlusion confidence map.
//
// Output: 255 = definite foreground (don't overlay AR), 0 = background (safe to overlay).
// Depth map is at lower resolution; bilinear interpolation maps to full frame coords.
// SAM mask provides sharp object edges and takes priority where present.
__global__ void fuse_occlusion_kernel(
    uint8_t* __restrict__ output,
    const __half* __restrict__ depth,
    const uint8_t* __restrict__ sam_mask,
    int width, int height,
    int depth_w, int depth_h,
    float depth_near,
    float softness)
{
    int x = blockIdx.x * blockDim.x + threadIdx.x;
    int y = blockIdx.y * blockDim.y + threadIdx.y;
    if (x >= width || y >= height) return;

    int idx = y * width + x;
    float confidence = 0.0f;

    // Sample depth with bilinear interpolation
    if (depth != NULL) {
        float fx = (float)x * (float)depth_w / (float)width;
        float fy = (float)y * (float)depth_h / (float)height;
        float sampled_depth = bilinear_sample_depth(depth, fx, fy, depth_w, depth_h);

        // Convert depth to foreground confidence: closer (lower depth) = higher confidence.
        // depth_near is the threshold below which pixels are considered foreground.
        // softness controls the transition width.
        float soft = fmaxf(softness, 0.001f); // prevent division by zero
        confidence = (depth_near - sampled_depth) / soft;
        confidence = fmaxf(0.0f, fminf(1.0f, confidence));
    }

    uint8_t result = (uint8_t)(confidence * 255.0f + 0.5f);

    // SAM mask overrides depth where present (sharper edges)
    if (sam_mask != NULL) {
        uint8_t sam_val = sam_mask[idx];
        if (sam_val > 128) {
            result = max(result, sam_val);  // SAM takes priority
        }
    }

    output[idx] = result;
}

extern "C" {

cudaError_t launch_fuse_occlusion(
    uint8_t* output,
    const void* depth,
    const uint8_t* sam_mask,
    int width, int height,
    int depth_w, int depth_h,
    float depth_near, float softness,
    cudaStream_t stream)
{
    dim3 block(BLOCK_DIM_X, BLOCK_DIM_Y);
    dim3 grid((width + block.x - 1) / block.x, (height + block.y - 1) / block.y);
    fuse_occlusion_kernel<<<grid, block, 0, stream>>>(
        output,
        (const __half*)depth,
        sam_mask,
        width, height,
        depth_w, depth_h,
        depth_near, softness);
    return cudaGetLastError();
}

} // extern "C"
