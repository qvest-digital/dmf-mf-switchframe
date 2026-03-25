#include "common.cuh"

// CorridorKey neural green screen keyer — CUDA preprocessing/postprocessing kernels.
//
// Pipeline: NV12 → RGB CHW (reuse nv12_to_rgb_chw from preprocess.cu)
//           → ImageNet normalize → concat hint → TRT infer
//           → composite alpha+fg onto NV12 program frame

// ImageNet normalization constants (sRGB domain).
#define IMGNET_MEAN_R 0.485f
#define IMGNET_MEAN_G 0.456f
#define IMGNET_MEAN_B 0.406f
#define IMGNET_STD_R  0.229f
#define IMGNET_STD_G  0.224f
#define IMGNET_STD_B  0.225f

// ---------------------------------------------------------------------------
// ck_imagenet_normalize: in-place ImageNet normalization on CHW float32 RGB.
//   rgb: [3, H, W] float32, values in [0,1] → normalized to ImageNet stats
// ---------------------------------------------------------------------------
__global__ void ck_imagenet_normalize_kernel(
    float* __restrict__ rgb,
    int H, int W)
{
    int idx = blockIdx.x * blockDim.x + threadIdx.x;
    int total = H * W;
    if (idx >= total) return;

    // R plane
    rgb[0 * total + idx] = (rgb[0 * total + idx] - IMGNET_MEAN_R) / IMGNET_STD_R;
    // G plane
    rgb[1 * total + idx] = (rgb[1 * total + idx] - IMGNET_MEAN_G) / IMGNET_STD_G;
    // B plane
    rgb[2 * total + idx] = (rgb[2 * total + idx] - IMGNET_MEAN_B) / IMGNET_STD_B;
}

extern "C"
cudaError_t ck_imagenet_normalize(
    float* rgb,
    int H, int W,
    cudaStream_t stream)
{
    int total = H * W;
    int threads = 256;
    int blocks = (total + threads - 1) / threads;
    ck_imagenet_normalize_kernel<<<blocks, threads, 0, stream>>>(rgb, H, W);
    return cudaGetLastError();
}

// ---------------------------------------------------------------------------
// ck_resize_mask_u8_f32: bilinear resize uint8 [srcH, srcW] → float32 [dstH, dstW]
//   Produces a single-channel mask in [0,1] range for the model's 4th input channel.
// ---------------------------------------------------------------------------
__global__ void ck_resize_mask_u8_f32_kernel(
    float* __restrict__ dst,
    const uint8_t* __restrict__ src,
    int srcW, int srcH, int srcPitch,
    int dstW, int dstH)
{
    int ox = blockIdx.x * blockDim.x + threadIdx.x;
    int oy = blockIdx.y * blockDim.y + threadIdx.y;
    if (ox >= dstW || oy >= dstH) return;

    float sx = (float)ox * (float)(srcW - 1) / (float)(dstW - 1);
    float sy = (float)oy * (float)(srcH - 1) / (float)(dstH - 1);

    int ix = (int)sx, iy = (int)sy;
    float fx = sx - ix, fy = sy - iy;
    int ix1 = min(ix + 1, srcW - 1);
    int iy1 = min(iy + 1, srcH - 1);

    float v = (src[iy * srcPitch + ix] * (1.0f - fx) + src[iy * srcPitch + ix1] * fx) * (1.0f - fy)
            + (src[iy1 * srcPitch + ix] * (1.0f - fx) + src[iy1 * srcPitch + ix1] * fx) * fy;

    dst[oy * dstW + ox] = v / 255.0f;
}

extern "C"
cudaError_t ck_resize_mask_u8_f32(
    float* dst,
    const uint8_t* src, int srcW, int srcH, int srcPitch,
    int dstW, int dstH,
    cudaStream_t stream)
{
    dim3 block(16, 16);
    dim3 grid((dstW + block.x - 1) / block.x, (dstH + block.y - 1) / block.y);
    ck_resize_mask_u8_f32_kernel<<<grid, block, 0, stream>>>(
        dst, src, srcW, srcH, srcPitch, dstW, dstH);
    return cudaGetLastError();
}

// ---------------------------------------------------------------------------
// ck_composite_neural: composite neural key result onto NV12 program frame.
//
// Reads model outputs (alpha [1,1,modelH,modelW] and fg [1,3,modelH,modelW])
// at model resolution, bilinear-upscales to program resolution, applies green
// despill on the foreground, and alpha-blends onto the NV12 frame.
//
// All in one fused kernel to avoid intermediate full-resolution buffers.
// ---------------------------------------------------------------------------
__global__ void ck_composite_neural_kernel(
    uint8_t* __restrict__ nv12,     // program NV12 frame (in-place)
    const float* __restrict__ alpha, // [1, 1, modelH, modelW]
    const float* __restrict__ fg,    // [1, 3, modelH, modelW] sRGB [0,1]
    int progW, int progH, int progPitch,
    int modelW, int modelH)
{
    int px = blockIdx.x * blockDim.x + threadIdx.x;
    int py = blockIdx.y * blockDim.y + threadIdx.y;
    if (px >= progW || py >= progH) return;

    // Map program pixel to model coordinates (bilinear).
    float mx = (float)px * (float)(modelW - 1) / (float)(progW - 1);
    float my = (float)py * (float)(modelH - 1) / (float)(progH - 1);

    int ix = (int)mx, iy = (int)my;
    float fx = mx - ix, fy = my - iy;
    int ix1 = min(ix + 1, modelW - 1);
    int iy1 = min(iy + 1, modelH - 1);

    int modelPixels = modelW * modelH;

    // Bilinear sample alpha.
    float a = (alpha[iy * modelW + ix] * (1.0f - fx) + alpha[iy * modelW + ix1] * fx) * (1.0f - fy)
            + (alpha[iy1 * modelW + ix] * (1.0f - fx) + alpha[iy1 * modelW + ix1] * fx) * fy;
    a = fminf(fmaxf(a, 0.0f), 1.0f);

    // Bilinear sample foreground RGB (CHW layout: R plane, G plane, B plane).
    auto sample_plane = [&](const float* plane) -> float {
        return (plane[iy * modelW + ix] * (1.0f - fx) + plane[iy * modelW + ix1] * fx) * (1.0f - fy)
             + (plane[iy1 * modelW + ix] * (1.0f - fx) + plane[iy1 * modelW + ix1] * fx) * fy;
    };

    float r = sample_plane(fg + 0 * modelPixels);
    float g = sample_plane(fg + 1 * modelPixels);
    float b = sample_plane(fg + 2 * modelPixels);

    // Clamp to [0,1].
    r = fminf(fmaxf(r, 0.0f), 1.0f);
    g = fminf(fmaxf(g, 0.0f), 1.0f);
    b = fminf(fmaxf(b, 0.0f), 1.0f);

    // Green despill: if green exceeds average of R and B, redistribute.
    float rb_avg = (r + b) * 0.5f;
    if (g > rb_avg) {
        float excess = (g - rb_avg) * 0.5f;
        g -= excess;
        r += excess * 0.5f;
        b += excess * 0.5f;
        // Re-clamp after redistribution.
        r = fminf(r, 1.0f);
        g = fminf(g, 1.0f);
        b = fminf(b, 1.0f);
    }

    // Convert foreground sRGB [0,1] → BT.709 limited-range YCbCr.
    float r8 = r * 255.0f, g8 = g * 255.0f, b8 = b * 255.0f;
    float fgY  = 16.0f  + 0.2126f * r8 + 0.7152f * g8 + 0.0722f * b8;
    float fgCb = 128.0f - 0.1146f * r8 - 0.3854f * g8 + 0.5000f * b8;
    float fgCr = 128.0f + 0.5000f * r8 - 0.4542f * g8 - 0.0458f * b8;

    // Premultiplied output: fg * alpha. Background (alpha=0) becomes black.
    // BT.709 black: Y=16, Cb=128, Cr=128 (limited range).
    float outY = a * fgY + (1.0f - a) * 16.0f;
    nv12[py * progPitch + px] = (uint8_t)fminf(fmaxf(outY + 0.5f, 0.0f), 255.0f);

    // UV: process once per 2x2 block (top-left pixel of each block).
    if ((px & 1) == 0 && (py & 1) == 0) {
        int uvOffset = progPitch * progH;
        int cx = px / 2;
        int cy = py / 2;
        int uvIdx = uvOffset + cy * progPitch + cx * 2;

        float outCb = a * fgCb + (1.0f - a) * 128.0f;
        float outCr = a * fgCr + (1.0f - a) * 128.0f;

        nv12[uvIdx]     = (uint8_t)fminf(fmaxf(outCb + 0.5f, 0.0f), 255.0f);
        nv12[uvIdx + 1] = (uint8_t)fminf(fmaxf(outCr + 0.5f, 0.0f), 255.0f);
    }
}

extern "C"
cudaError_t ck_composite_neural(
    uint8_t* nv12,
    const float* alpha, const float* fg,
    int progW, int progH, int progPitch,
    int modelW, int modelH,
    cudaStream_t stream)
{
    dim3 block(16, 16);
    dim3 grid((progW + block.x - 1) / block.x, (progH + block.y - 1) / block.y);
    ck_composite_neural_kernel<<<grid, block, 0, stream>>>(
        nv12, alpha, fg, progW, progH, progPitch, modelW, modelH);
    return cudaGetLastError();
}
