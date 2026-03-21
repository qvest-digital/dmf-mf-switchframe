#include "common.cuh"

// Fused NV12 → RGB float32 CHW with bilinear scale.
// One thread per output pixel (256x256 = 65536 threads).
// BT.709 limited-range: R = 1.164*(Y-16) + 1.793*(Cr-128)
//                        G = 1.164*(Y-16) - 0.213*(Cb-128) - 0.533*(Cr-128)
//                        B = 1.164*(Y-16) + 2.112*(Cb-128)
__global__ void nv12_to_rgb_chw_kernel(
    float* __restrict__ rgbOut,      // [3, outH, outW] CHW planar
    const uint8_t* __restrict__ nv12,
    int srcW, int srcH, int srcPitch,
    int outW, int outH)
{
    int ox = blockIdx.x * blockDim.x + threadIdx.x;
    int oy = blockIdx.y * blockDim.y + threadIdx.y;
    if (ox >= outW || oy >= outH) return;

    // Map output to source coords (bilinear)
    float sx = (float)ox * (float)(srcW - 1) / (float)(outW - 1);
    float sy = (float)oy * (float)(srcH - 1) / (float)(outH - 1);

    // Bilinear sample Y from Y plane
    int ix = (int)sx, iy = (int)sy;
    float fx = sx - ix, fy = sy - iy;
    int ix1 = min(ix + 1, srcW - 1);
    int iy1 = min(iy + 1, srcH - 1);

    float Y = (nv12[iy * srcPitch + ix] * (1.0f - fx) + nv12[iy * srcPitch + ix1] * fx) * (1.0f - fy)
            + (nv12[iy1 * srcPitch + ix] * (1.0f - fx) + nv12[iy1 * srcPitch + ix1] * fx) * fy;

    // Nearest-neighbor sample Cb,Cr from UV plane (half res, interleaved)
    int uvOffset = srcPitch * srcH;
    int cx = (int)(sx + 0.5f) / 2;
    int cy = (int)(sy + 0.5f) / 2;
    cx = min(cx, srcW / 2 - 1);
    cy = min(cy, srcH / 2 - 1);
    float Cb = (float)nv12[uvOffset + cy * srcPitch + cx * 2];
    float Cr = (float)nv12[uvOffset + cy * srcPitch + cx * 2 + 1];

    // BT.709 limited-range YCbCr → RGB
    float y_adj = 1.164f * (Y - 16.0f);
    float r = y_adj + 1.793f * (Cr - 128.0f);
    float g = y_adj - 0.213f * (Cb - 128.0f) - 0.533f * (Cr - 128.0f);
    float b = y_adj + 2.112f * (Cb - 128.0f);

    // Clamp to [0, 255] then normalize to [0, 1]
    r = fminf(fmaxf(r, 0.0f), 255.0f) / 255.0f;
    g = fminf(fmaxf(g, 0.0f), 255.0f) / 255.0f;
    b = fminf(fmaxf(b, 0.0f), 255.0f) / 255.0f;

    // Write CHW planar: R plane, then G plane, then B plane
    int pixIdx = oy * outW + ox;
    rgbOut[0 * outH * outW + pixIdx] = r;
    rgbOut[1 * outH * outW + pixIdx] = g;
    rgbOut[2 * outH * outW + pixIdx] = b;
}

// NHWC variant: output layout [1, outH, outW, 3] for models expecting HWC format
// (e.g., MediaPipe Selfie Segmentation with NHWC input).
__global__ void nv12_to_rgb_nhwc_kernel(
    float* __restrict__ rgbOut,      // [outH, outW, 3] HWC interleaved
    const uint8_t* __restrict__ nv12,
    int srcW, int srcH, int srcPitch,
    int outW, int outH)
{
    int ox = blockIdx.x * blockDim.x + threadIdx.x;
    int oy = blockIdx.y * blockDim.y + threadIdx.y;
    if (ox >= outW || oy >= outH) return;

    float sx = (float)ox * (float)(srcW - 1) / (float)(outW - 1);
    float sy = (float)oy * (float)(srcH - 1) / (float)(outH - 1);

    int ix = (int)sx, iy = (int)sy;
    float fx = sx - ix, fy = sy - iy;
    int ix1 = min(ix + 1, srcW - 1);
    int iy1 = min(iy + 1, srcH - 1);

    float Y = (nv12[iy * srcPitch + ix] * (1.0f - fx) + nv12[iy * srcPitch + ix1] * fx) * (1.0f - fy)
            + (nv12[iy1 * srcPitch + ix] * (1.0f - fx) + nv12[iy1 * srcPitch + ix1] * fx) * fy;

    int uvOffset = srcPitch * srcH;
    int cx = (int)(sx + 0.5f) / 2;
    int cy = (int)(sy + 0.5f) / 2;
    cx = min(cx, srcW / 2 - 1);
    cy = min(cy, srcH / 2 - 1);
    float Cb = (float)nv12[uvOffset + cy * srcPitch + cx * 2];
    float Cr = (float)nv12[uvOffset + cy * srcPitch + cx * 2 + 1];

    float y_adj = 1.164f * (Y - 16.0f);
    float r = fminf(fmaxf(y_adj + 1.793f * (Cr - 128.0f), 0.0f), 255.0f) / 255.0f;
    float g = fminf(fmaxf(y_adj - 0.213f * (Cb - 128.0f) - 0.533f * (Cr - 128.0f), 0.0f), 255.0f) / 255.0f;
    float b = fminf(fmaxf(y_adj + 2.112f * (Cb - 128.0f), 0.0f), 255.0f) / 255.0f;

    // Write HWC interleaved
    int idx = (oy * outW + ox) * 3;
    rgbOut[idx + 0] = r;
    rgbOut[idx + 1] = g;
    rgbOut[idx + 2] = b;
}

extern "C" {
cudaError_t nv12_to_rgb_chw(
    float* rgbOut,
    const uint8_t* nv12,
    int srcW, int srcH, int srcPitch,
    int outW, int outH,
    cudaStream_t stream)
{
    dim3 block(16, 16);  // 256 threads, good for 256x256 output
    dim3 grid((outW + block.x - 1) / block.x, (outH + block.y - 1) / block.y);
    nv12_to_rgb_chw_kernel<<<grid, block, 0, stream>>>(
        rgbOut, nv12, srcW, srcH, srcPitch, outW, outH);
    return cudaGetLastError();
}
cudaError_t nv12_to_rgb_nhwc(
    float* rgbOut,
    const uint8_t* nv12,
    int srcW, int srcH, int srcPitch,
    int outW, int outH,
    cudaStream_t stream)
{
    dim3 block(16, 16);
    dim3 grid((outW + block.x - 1) / block.x, (outH + block.y - 1) / block.y);
    nv12_to_rgb_nhwc_kernel<<<grid, block, 0, stream>>>(
        rgbOut, nv12, srcW, srcH, srcPitch, outW, outH);
    return cudaGetLastError();
}
// Convert float32 [srcH, srcW, 1] mask to uint8 and bilinear upscale to target resolution.
// Each thread computes one output pixel by sampling the float source mask with bilinear
// interpolation, clamping to [0,1], and scaling to [0,255].
__global__ void mask_float_to_u8_scale_kernel(
    uint8_t* __restrict__ dst, int dstW, int dstH,
    const float* __restrict__ src, int srcW, int srcH)
{
    int ox = blockIdx.x * blockDim.x + threadIdx.x;
    int oy = blockIdx.y * blockDim.y + threadIdx.y;
    if (ox >= dstW || oy >= dstH) return;

    // Map output pixel to source coordinates
    float sx = (dstW > 1) ? (float)ox * (float)(srcW - 1) / (float)(dstW - 1) : 0.0f;
    float sy = (dstH > 1) ? (float)oy * (float)(srcH - 1) / (float)(dstH - 1) : 0.0f;

    // Bilinear interpolation
    int ix = (int)sx;
    int iy = (int)sy;
    float fx = sx - ix;
    float fy = sy - iy;
    int ix1 = min(ix + 1, srcW - 1);
    int iy1 = min(iy + 1, srcH - 1);

    float v00 = src[iy  * srcW + ix];
    float v10 = src[iy  * srcW + ix1];
    float v01 = src[iy1 * srcW + ix];
    float v11 = src[iy1 * srcW + ix1];

    float val = v00 * (1.0f - fx) * (1.0f - fy)
              + v10 * fx * (1.0f - fy)
              + v01 * (1.0f - fx) * fy
              + v11 * fx * fy;

    // Clamp to [0, 1] and scale to [0, 255]
    val = fminf(fmaxf(val, 0.0f), 1.0f);
    dst[oy * dstW + ox] = (uint8_t)(val * 255.0f + 0.5f);
}

cudaError_t mask_to_u8_upscale(
    uint8_t* dst, int dstW, int dstH,
    const float* src, int srcW, int srcH,
    cudaStream_t stream)
{
    dim3 block(16, 16);
    dim3 grid((dstW + block.x - 1) / block.x, (dstH + block.y - 1) / block.y);
    mask_float_to_u8_scale_kernel<<<grid, block, 0, stream>>>(
        dst, dstW, dstH, src, srcW, srcH);
    return cudaGetLastError();
}
} // extern "C"
