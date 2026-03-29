#include "common.cuh"

// CorridorKey neural green screen keyer — CUDA preprocessing/postprocessing kernels.
//
// Pipeline: NV12 → RGB CHW (reuse nv12_to_rgb_chw from preprocess.cu)
//           → ImageNet normalize → concat hint → TRT infer
//           → composite alpha+fg onto program NV12 frame

// ImageNet normalization constants (sRGB domain).
#define IMGNET_MEAN_R 0.485f
#define IMGNET_MEAN_G 0.456f
#define IMGNET_MEAN_B 0.406f
#define IMGNET_STD_R  0.229f
#define IMGNET_STD_G  0.224f
#define IMGNET_STD_B  0.225f

// ---------------------------------------------------------------------------
// ck_imagenet_normalize: in-place ImageNet normalization on CHW float32 RGB.
// ---------------------------------------------------------------------------
__global__ void ck_imagenet_normalize_kernel(
    float* __restrict__ rgb,
    int H, int W)
{
    int idx = blockIdx.x * blockDim.x + threadIdx.x;
    int total = H * W;
    if (idx >= total) return;

    rgb[0 * total + idx] = (rgb[0 * total + idx] - IMGNET_MEAN_R) / IMGNET_STD_R;
    rgb[1 * total + idx] = (rgb[1 * total + idx] - IMGNET_MEAN_G) / IMGNET_STD_G;
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
// ck_composite_neural: composite neural key result onto a SEPARATE program frame.
//
// Reads model outputs (alpha + fg at model resolution), upscales to program
// resolution, applies despill, and alpha-blends onto the program NV12 frame.
// The program frame is the background; foreground pixels (alpha>0) from the
// model replace/blend over it.
// ---------------------------------------------------------------------------
__global__ void ck_composite_neural_kernel(
    uint8_t* __restrict__ progNV12,  // program NV12 frame (destination, modified in-place)
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

    // Fast path: fully transparent — leave program pixel unchanged.
    if (a < 1.0f / 255.0f) return;

    // Bilinear sample foreground RGB (CHW layout).
    auto sample_plane = [&](const float* plane) -> float {
        return (plane[iy * modelW + ix] * (1.0f - fx) + plane[iy * modelW + ix1] * fx) * (1.0f - fy)
             + (plane[iy1 * modelW + ix] * (1.0f - fx) + plane[iy1 * modelW + ix1] * fx) * fy;
    };

    float r = fminf(fmaxf(sample_plane(fg + 0 * modelPixels), 0.0f), 1.0f);
    float g = fminf(fmaxf(sample_plane(fg + 1 * modelPixels), 0.0f), 1.0f);
    float b = fminf(fmaxf(sample_plane(fg + 2 * modelPixels), 0.0f), 1.0f);

    // Green despill: redistribute excess green to R and B.
    float rb_avg = (r + b) * 0.5f;
    if (g > rb_avg) {
        float excess = (g - rb_avg) * 0.5f;
        g -= excess;
        r = fminf(r + excess * 0.5f, 1.0f);
        b = fminf(b + excess * 0.5f, 1.0f);
    }

    // Convert foreground sRGB [0,1] → BT.709 limited-range YCbCr.
    float r8 = r * 255.0f, g8 = g * 255.0f, b8 = b * 255.0f;
    float fgY  = 16.0f  + 0.2126f * r8 + 0.7152f * g8 + 0.0722f * b8;
    float fgCb = 128.0f - 0.1146f * r8 - 0.3854f * g8 + 0.5000f * b8;
    float fgCr = 128.0f + 0.5000f * r8 - 0.4542f * g8 - 0.0458f * b8;

    // Read existing program pixel (background).
    float progY = (float)progNV12[py * progPitch + px];

    // Alpha blend: foreground over program background.
    float outY = a * fgY + (1.0f - a) * progY;
    progNV12[py * progPitch + px] = (uint8_t)fminf(fmaxf(outY + 0.5f, 0.0f), 255.0f);

    // UV: process once per 2x2 block.
    if ((px & 1) == 0 && (py & 1) == 0) {
        int uvOffset = progPitch * progH;
        int cx = px / 2;
        int cy = py / 2;
        int uvIdx = uvOffset + cy * progPitch + cx * 2;

        float progCb = (float)progNV12[uvIdx];
        float progCr = (float)progNV12[uvIdx + 1];

        float outCb = a * fgCb + (1.0f - a) * progCb;
        float outCr = a * fgCr + (1.0f - a) * progCr;

        progNV12[uvIdx]     = (uint8_t)fminf(fmaxf(outCb + 0.5f, 0.0f), 255.0f);
        progNV12[uvIdx + 1] = (uint8_t)fminf(fmaxf(outCr + 0.5f, 0.0f), 255.0f);
    }
}

extern "C"
cudaError_t ck_composite_neural(
    uint8_t* progNV12,
    const float* alpha, const float* fg,
    int progW, int progH, int progPitch,
    int modelW, int modelH,
    cudaStream_t stream)
{
    dim3 block(16, 16);
    dim3 grid((progW + block.x - 1) / block.x, (progH + block.y - 1) / block.y);
    ck_composite_neural_kernel<<<grid, block, 0, stream>>>(
        progNV12, alpha, fg, progW, progH, progPitch, modelW, modelH);
    return cudaGetLastError();
}

// ---------------------------------------------------------------------------
// ck_composite_neural_p210: composite neural key result onto P210 program frame.
//
// P210: 10-bit 4:2:2 semi-planar (uint16, left-justified by 6).
//   Y plane:  uint16 per pixel, pitch16 = pitch_bytes/2
//   UV plane: full height (4:2:2), uint16 interleaved Cb/Cr at half-width
//   UV offset: pitch16 * height (in uint16 units)
//   Value range: Y [64,940], Cb/Cr [64,960], neutral 512 (all 10-bit)
// ---------------------------------------------------------------------------
__global__ void ck_composite_neural_p210_kernel(
    uint16_t* __restrict__ progP210,  // program P210 frame (modified in-place)
    const float* __restrict__ alpha,  // [1, 1, modelH, modelW]
    const float* __restrict__ fg,     // [1, 3, modelH, modelW] sRGB [0,1]
    int progW, int progH, int progPitch16,  // pitch in uint16 units
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

    if (a < 1.0f / 1023.0f) return;

    // Bilinear sample foreground RGB (CHW layout).
    auto sample_plane = [&](const float* plane) -> float {
        return (plane[iy * modelW + ix] * (1.0f - fx) + plane[iy * modelW + ix1] * fx) * (1.0f - fy)
             + (plane[iy1 * modelW + ix] * (1.0f - fx) + plane[iy1 * modelW + ix1] * fx) * fy;
    };

    float r = fminf(fmaxf(sample_plane(fg + 0 * modelPixels), 0.0f), 1.0f);
    float g = fminf(fmaxf(sample_plane(fg + 1 * modelPixels), 0.0f), 1.0f);
    float b = fminf(fmaxf(sample_plane(fg + 2 * modelPixels), 0.0f), 1.0f);

    // Green despill.
    float rb_avg = (r + b) * 0.5f;
    if (g > rb_avg) {
        float excess = (g - rb_avg) * 0.5f;
        g -= excess;
        r = fminf(r + excess * 0.5f, 1.0f);
        b = fminf(b + excess * 0.5f, 1.0f);
    }

    // Convert foreground sRGB [0,1] → BT.709 10-bit limited-range YCbCr.
    float r10 = r * 1023.0f, g10 = g * 1023.0f, b10 = b * 1023.0f;
    float fgY  = 64.0f  + 0.2126f * r10 + 0.7152f * g10 + 0.0722f * b10;
    float fgCb = 512.0f - 0.1146f * r10 - 0.3854f * g10 + 0.5000f * b10;
    float fgCr = 512.0f + 0.5000f * r10 - 0.4542f * g10 - 0.0458f * b10;

    // Read existing program Y (10-bit, left-justified in uint16: >> 6).
    float progY = (float)(progP210[py * progPitch16 + px] >> 6);

    // Alpha blend Y.
    float outY = a * fgY + (1.0f - a) * progY;
    int outY10 = (int)fminf(fmaxf(outY + 0.5f, 0.0f), 1023.0f);
    progP210[py * progPitch16 + px] = (uint16_t)(outY10 << 6);

    // UV: process once per horizontal pair, at every row (4:2:2 = full vertical res).
    if ((px & 1) == 0) {
        int uvOffset = progPitch16 * progH;
        int cx = px / 2;
        int uvIdx = uvOffset + py * progPitch16 + cx * 2;

        float progCb = (float)(progP210[uvIdx] >> 6);
        float progCr = (float)(progP210[uvIdx + 1] >> 6);

        float outCb = a * fgCb + (1.0f - a) * progCb;
        float outCr = a * fgCr + (1.0f - a) * progCr;

        int outCb10 = (int)fminf(fmaxf(outCb + 0.5f, 0.0f), 1023.0f);
        int outCr10 = (int)fminf(fmaxf(outCr + 0.5f, 0.0f), 1023.0f);

        progP210[uvIdx]     = (uint16_t)(outCb10 << 6);
        progP210[uvIdx + 1] = (uint16_t)(outCr10 << 6);
    }
}

extern "C"
cudaError_t ck_composite_neural_p210(
    uint8_t* progP210,
    const float* alpha, const float* fg,
    int progW, int progH, int progPitch,
    int modelW, int modelH,
    cudaStream_t stream)
{
    int progPitch16 = progPitch / 2;
    dim3 block(16, 16);
    dim3 grid((progW + block.x - 1) / block.x, (progH + block.y - 1) / block.y);
    ck_composite_neural_p210_kernel<<<grid, block, 0, stream>>>(
        (uint16_t*)progP210, alpha, fg, progW, progH, progPitch16, modelW, modelH);
    return cudaGetLastError();
}
