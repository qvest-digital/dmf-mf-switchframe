#include "common.cuh"

// PIP composite: scale source and place into destination region
// One thread per output pixel in the rect. Bilinear interpolation for scaling.
__global__ void pip_composite_y_kernel(
    uint8_t* __restrict__ dst, int dstW, int dstH, int dstPitch,
    const uint8_t* __restrict__ src, int srcW, int srcH, int srcPitch,
    int rectX, int rectY, int rectW, int rectH,
    int alpha256)  // 0-256 for dissolve, 256 = opaque
{
    int lx = blockIdx.x * blockDim.x + threadIdx.x;
    int ly = blockIdx.y * blockDim.y + threadIdx.y;
    if (lx >= rectW || ly >= rectH) return;

    int dx = rectX + lx;
    int dy = rectY + ly;
    if (dx >= dstW || dy >= dstH || dx < 0 || dy < 0) return;

    // Map local rect coords to source coords (float to avoid overflow)
    float srcXf = (float)lx * (float)(srcW - 1) / (float)max(rectW - 1, 1);
    float srcYf = (float)ly * (float)(srcH - 1) / (float)max(rectH - 1, 1);
    int sx = (int)srcXf;
    int sy = (int)srcYf;
    float fx = srcXf - sx;
    float fy = srcYf - sy;

    int sx1 = min(sx + 1, srcW - 1);
    int sy1 = min(sy + 1, srcH - 1);

    int v00 = src[sy  * srcPitch + sx];
    int v10 = src[sy  * srcPitch + sx1];
    int v01 = src[sy1 * srcPitch + sx];
    int v11 = src[sy1 * srcPitch + sx1];

    float top = v00 + (v10 - v00) * fx;
    float bot = v01 + (v11 - v01) * fx;
    int val = (int)(top + (bot - top) * fy + 0.5f);

    int dstIdx = dy * dstPitch + dx;
    if (alpha256 >= 256) {
        dst[dstIdx] = (uint8_t)val;
    } else {
        int inv = 256 - alpha256;
        dst[dstIdx] = (uint8_t)((dst[dstIdx] * inv + val * alpha256 + 128) >> 8);
    }
}

// PIP composite for UV plane (NV12 interleaved, half resolution)
__global__ void pip_composite_uv_kernel(
    uint8_t* __restrict__ dstUV, int dstW, int dstChromaH, int dstPitch,
    const uint8_t* __restrict__ srcUV, int srcW, int srcChromaH, int srcPitch,
    int rectX, int rectY, int rectCW, int rectCH,
    int alpha256)
{
    int lx = blockIdx.x * blockDim.x + threadIdx.x;
    int ly = blockIdx.y * blockDim.y + threadIdx.y;
    if (lx >= rectCW || ly >= rectCH) return;

    int dx = rectX + lx * 2; // UV is interleaved pairs
    int dy = rectY / 2 + ly;
    if (dx + 1 >= dstW || dy >= dstChromaH || dx < 0 || dy < 0) return;

    // Map to source chroma coords
    float srcXf = (float)lx * (float)(srcW / 2 - 1) / (float)max(rectCW - 1, 1);
    float srcYf = (float)ly * (float)(srcChromaH - 1) / (float)max(rectCH - 1, 1);
    int sx = (int)srcXf;
    int sy = (int)srcYf;
    sx = min(sx, srcW / 2 - 1);
    sy = min(sy, srcChromaH - 1);

    int srcIdx = sy * srcPitch + sx * 2;
    int dstIdx = dy * dstPitch + (rectX / 2) * 2 + lx * 2;
    if (dstIdx + 1 >= dstPitch * dstChromaH) return;

    if (alpha256 >= 256) {
        dstUV[dstIdx]     = srcUV[srcIdx];
        dstUV[dstIdx + 1] = srcUV[srcIdx + 1];
    } else {
        int inv = 256 - alpha256;
        dstUV[dstIdx]     = (uint8_t)((dstUV[dstIdx]     * inv + srcUV[srcIdx]     * alpha256 + 128) >> 8);
        dstUV[dstIdx + 1] = (uint8_t)((dstUV[dstIdx + 1] * inv + srcUV[srcIdx + 1] * alpha256 + 128) >> 8);
    }
}

// Draw border around a rectangle (4 strips: top, bottom, left, right)
// Thread indices are relative to the outer bounding box origin (outerX, outerY).
// outerX and outerY are passed explicitly so the kernel can map back to absolute
// frame coordinates without recomputing them from rectX/rectY/thickness.
__global__ void draw_border_nv12_kernel(
    uint8_t* __restrict__ dst, int dstW, int dstH, int dstPitch,
    int rectX, int rectY, int rectW, int rectH,
    int outerX, int outerY, int outerW, int outerH,
    int thickness, uint8_t colorY, uint8_t colorCb, uint8_t colorCr)
{
    int lx = blockIdx.x * blockDim.x + threadIdx.x;
    int ly = blockIdx.y * blockDim.y + threadIdx.y;

    // Convert local (outer-box-relative) coords to absolute frame coords
    int x = outerX + lx;
    int y = outerY + ly;

    if (lx >= outerW || ly >= outerH) return;
    if (x >= dstW || y >= dstH || x < 0 || y < 0) return;

    // Inside the rect itself? Skip (not border)
    if (x >= rectX && x < rectX + rectW && y >= rectY && y < rectY + rectH) return;

    // Y plane
    dst[y * dstPitch + x] = colorY;

    // UV plane (once per 2x2 block)
    if ((x & 1) == 0 && (y & 1) == 0) {
        int uvOffset = dstPitch * dstH;
        int uvIdx = (y / 2) * dstPitch + x;
        dst[uvOffset + uvIdx]     = colorCb;
        dst[uvOffset + uvIdx + 1] = colorCr;
    }
}

// Fill rectangle with constant NV12 color
__global__ void fill_rect_nv12_kernel(
    uint8_t* __restrict__ dst, int dstW, int dstH, int dstPitch,
    int rectX, int rectY, int rectW, int rectH,
    uint8_t colorY, uint8_t colorCb, uint8_t colorCr)
{
    int lx = blockIdx.x * blockDim.x + threadIdx.x;
    int ly = blockIdx.y * blockDim.y + threadIdx.y;
    if (lx >= rectW || ly >= rectH) return;

    int x = rectX + lx;
    int y = rectY + ly;
    if (x >= dstW || y >= dstH || x < 0 || y < 0) return;

    dst[y * dstPitch + x] = colorY;

    if ((lx & 1) == 0 && (ly & 1) == 0) {
        int uvOffset = dstPitch * dstH;
        int uvIdx = (y / 2) * dstPitch + x;
        dst[uvOffset + uvIdx]     = colorCb;
        dst[uvOffset + uvIdx + 1] = colorCr;
    }
}

extern "C" {

cudaError_t pip_composite_nv12(
    uint8_t* dst, int dstW, int dstH, int dstPitch,
    const uint8_t* src, int srcW, int srcH, int srcPitch,
    int rectX, int rectY, int rectW, int rectH,
    int alpha256, cudaStream_t stream)
{
    dim3 block(BLOCK_DIM_X, BLOCK_DIM_Y);

    // Y plane
    dim3 gridY((rectW + block.x - 1) / block.x, (rectH + block.y - 1) / block.y);
    pip_composite_y_kernel<<<gridY, block, 0, stream>>>(
        dst, dstW, dstH, dstPitch,
        src, srcW, srcH, srcPitch,
        rectX, rectY, rectW, rectH, alpha256);

    // UV plane
    int chromaRW = rectW / 2;
    int chromaRH = rectH / 2;
    if (chromaRW > 0 && chromaRH > 0) {
        dim3 gridUV((chromaRW + block.x - 1) / block.x, (chromaRH + block.y - 1) / block.y);
        pip_composite_uv_kernel<<<gridUV, block, 0, stream>>>(
            dst + dstPitch * dstH, dstW, dstH / 2, dstPitch,
            src + srcPitch * srcH, srcW, srcH / 2, srcPitch,
            rectX, rectY, chromaRW, chromaRH, alpha256);
    }

    return cudaGetLastError();
}

cudaError_t draw_border_nv12(
    uint8_t* dst, int dstW, int dstH, int dstPitch,
    int rectX, int rectY, int rectW, int rectH,
    int thickness, uint8_t colorY, uint8_t colorCb, uint8_t colorCr,
    cudaStream_t stream)
{
    int outerX = rectX - thickness;
    int outerY = rectY - thickness;
    int outerW = rectW + thickness * 2;
    int outerH = rectH + thickness * 2;
    dim3 block(BLOCK_DIM_X, BLOCK_DIM_Y);
    dim3 grid((outerW + block.x - 1) / block.x, (outerH + block.y - 1) / block.y);
    draw_border_nv12_kernel<<<grid, block, 0, stream>>>(
        dst, dstW, dstH, dstPitch,
        rectX, rectY, rectW, rectH,
        outerX, outerY, outerW, outerH,
        thickness, colorY, colorCb, colorCr);
    return cudaGetLastError();
}

cudaError_t fill_rect_nv12(
    uint8_t* dst, int dstW, int dstH, int dstPitch,
    int rectX, int rectY, int rectW, int rectH,
    uint8_t colorY, uint8_t colorCb, uint8_t colorCr,
    cudaStream_t stream)
{
    dim3 block(BLOCK_DIM_X, BLOCK_DIM_Y);
    dim3 grid((rectW + block.x - 1) / block.x, (rectH + block.y - 1) / block.y);
    fill_rect_nv12_kernel<<<grid, block, 0, stream>>>(
        dst, dstW, dstH, dstPitch,
        rectX, rectY, rectW, rectH,
        colorY, colorCb, colorCr);
    return cudaGetLastError();
}

} // extern "C"
