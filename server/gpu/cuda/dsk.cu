#include "common.cuh"

// Alpha blend RGBA overlay onto NV12 frame
// BT.709 limited-range conversion:
//   Y  = 16  + ((47*R + 157*G + 16*B + 128) >> 8)
//   Cb = 128 + ((-26*R - 86*G + 112*B + 128) >> 8)
//   Cr = 128 + ((112*R - 102*G - 10*B + 128) >> 8)
__global__ void alpha_blend_rgba_nv12_kernel(
    uint8_t* __restrict__ nv12,
    const uint8_t* __restrict__ rgba,
    int width, int height, int nv12Pitch, int rgbaPitch,
    int alphaScale256)
{
    int x = blockIdx.x * blockDim.x + threadIdx.x;
    int y = blockIdx.y * blockDim.y + threadIdx.y;
    if (x >= width || y >= height) return;

    int rgbaIdx = y * rgbaPitch + x * 4;
    int r = rgba[rgbaIdx];
    int g = rgba[rgbaIdx + 1];
    int b = rgba[rgbaIdx + 2];
    int a = rgba[rgbaIdx + 3];

    // Apply global alpha scale
    a = (a * alphaScale256 + 128) >> 8;
    if (a == 0) return;

    int inv = 256 - a;

    // BT.709 limited-range conversion (integer, matching CPU coefficients)
    int overlayY  = 16  + ((47 * r + 157 * g + 16 * b + 128) >> 8);
    int overlayCb = 128 + ((-26 * r - 86 * g + 112 * b + 128) >> 8);
    int overlayCr = 128 + ((112 * r - 102 * g - 10 * b + 128) >> 8);

    // Clamp to valid range
    overlayY  = min(max(overlayY, 16), 235);
    overlayCb = min(max(overlayCb, 16), 240);
    overlayCr = min(max(overlayCr, 16), 240);

    // Blend Y plane
    int yIdx = y * nv12Pitch + x;
    nv12[yIdx] = (uint8_t)((nv12[yIdx] * inv + overlayY * a + 128) >> 8);

    // Blend UV plane (once per 2x2 block — top-left pixel)
    if ((x & 1) == 0 && (y & 1) == 0) {
        int uvOffset = nv12Pitch * height;
        int uvIdx = (y / 2) * nv12Pitch + x;
        int curCb = nv12[uvOffset + uvIdx];
        int curCr = nv12[uvOffset + uvIdx + 1];
        nv12[uvOffset + uvIdx]     = (uint8_t)((curCb * inv + overlayCb * a + 128) >> 8);
        nv12[uvOffset + uvIdx + 1] = (uint8_t)((curCr * inv + overlayCr * a + 128) >> 8);
    }
}

// Rectangular RGBA overlay with nearest-neighbor scaling onto NV12
__global__ void alpha_blend_rgba_rect_nv12_kernel(
    uint8_t* __restrict__ nv12,
    const uint8_t* __restrict__ rgba, int overlayW, int overlayH, int rgbaPitch,
    int nv12Pitch, int frameW, int frameH,
    int rectX, int rectY, int rectW, int rectH,
    int alphaScale256)
{
    int lx = blockIdx.x * blockDim.x + threadIdx.x;
    int ly = blockIdx.y * blockDim.y + threadIdx.y;
    if (lx >= rectW || ly >= rectH) return;

    int dx = rectX + lx;
    int dy = rectY + ly;
    if (dx >= frameW || dy >= frameH || dx < 0 || dy < 0) return;

    // Nearest-neighbor sample from overlay
    int sx = lx * overlayW / max(rectW, 1);
    int sy = ly * overlayH / max(rectH, 1);
    sx = min(sx, overlayW - 1);
    sy = min(sy, overlayH - 1);
    int rgbaIdx = sy * rgbaPitch + sx * 4;

    int r = rgba[rgbaIdx];
    int g = rgba[rgbaIdx + 1];
    int b = rgba[rgbaIdx + 2];
    int a = rgba[rgbaIdx + 3];

    a = (a * alphaScale256 + 128) >> 8;
    if (a == 0) return;

    int inv = 256 - a;

    int overlayY  = 16  + ((47 * r + 157 * g + 16 * b + 128) >> 8);
    int overlayCb = 128 + ((-26 * r - 86 * g + 112 * b + 128) >> 8);
    int overlayCr = 128 + ((112 * r - 102 * g - 10 * b + 128) >> 8);

    overlayY  = min(max(overlayY, 16), 235);
    overlayCb = min(max(overlayCb, 16), 240);
    overlayCr = min(max(overlayCr, 16), 240);

    int yIdx = dy * nv12Pitch + dx;
    nv12[yIdx] = (uint8_t)((nv12[yIdx] * inv + overlayY * a + 128) >> 8);

    if ((dx & 1) == 0 && (dy & 1) == 0) {
        int uvOffset = nv12Pitch * frameH;
        int uvIdx = (dy / 2) * nv12Pitch + dx;
        int curCb = nv12[uvOffset + uvIdx];
        int curCr = nv12[uvOffset + uvIdx + 1];
        nv12[uvOffset + uvIdx]     = (uint8_t)((curCb * inv + overlayCb * a + 128) >> 8);
        nv12[uvOffset + uvIdx + 1] = (uint8_t)((curCr * inv + overlayCr * a + 128) >> 8);
    }
}

extern "C" {

cudaError_t alpha_blend_rgba_nv12(
    uint8_t* nv12, const uint8_t* rgba,
    int width, int height, int nv12Pitch, int rgbaPitch,
    int alphaScale256, cudaStream_t stream)
{
    dim3 block(BLOCK_DIM_X, BLOCK_DIM_Y);
    dim3 grid((width + block.x - 1) / block.x, (height + block.y - 1) / block.y);
    alpha_blend_rgba_nv12_kernel<<<grid, block, 0, stream>>>(
        nv12, rgba, width, height, nv12Pitch, rgbaPitch, alphaScale256);
    return cudaGetLastError();
}

cudaError_t alpha_blend_rgba_rect_nv12(
    uint8_t* nv12,
    const uint8_t* rgba, int overlayW, int overlayH, int rgbaPitch,
    int nv12Pitch, int frameW, int frameH,
    int rectX, int rectY, int rectW, int rectH,
    int alphaScale256, cudaStream_t stream)
{
    dim3 block(BLOCK_DIM_X, BLOCK_DIM_Y);
    dim3 grid((rectW + block.x - 1) / block.x, (rectH + block.y - 1) / block.y);
    alpha_blend_rgba_rect_nv12_kernel<<<grid, block, 0, stream>>>(
        nv12, rgba, overlayW, overlayH, rgbaPitch,
        nv12Pitch, frameW, frameH,
        rectX, rectY, rectW, rectH, alphaScale256);
    return cudaGetLastError();
}

} // extern "C"
