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

// Occlusion-aware full-frame RGBA overlay onto NV12.
// The occlusion mask (uint8, 0=bg 255=fg) modulates overlay alpha so that
// 2D graphics appear BEHIND real-world foreground objects (people, desks).
// effective_alpha = overlay_alpha * (255 - occlusion[px]) / 255
// When occlusion is NULL, behaves identically to alpha_blend_rgba_nv12_kernel.
__global__ void alpha_blend_rgba_nv12_occ_kernel(
    uint8_t* __restrict__ nv12,
    const uint8_t* __restrict__ rgba,
    const uint8_t* __restrict__ occlusion,
    int occ_w, int occ_h,
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

    // Apply occlusion mask: reduce alpha where foreground is detected.
    // Nearest-neighbor sample when occlusion resolution differs from frame.
    if (occlusion != NULL) {
        int ox, oy;
        if (occ_w == width && occ_h == height) {
            ox = x; oy = y;
        } else {
            ox = x * occ_w / max(width, 1);
            oy = y * occ_h / max(height, 1);
            ox = min(ox, occ_w - 1);
            oy = min(oy, occ_h - 1);
        }
        int occVal = occlusion[oy * occ_w + ox];
        // effective_alpha = a * (255 - occVal) / 255
        a = (a * (255 - occVal) + 127) / 255;
        if (a == 0) return;
    }

    int inv = 256 - a;

    int overlayY  = 16  + ((47 * r + 157 * g + 16 * b + 128) >> 8);
    int overlayCb = 128 + ((-26 * r - 86 * g + 112 * b + 128) >> 8);
    int overlayCr = 128 + ((112 * r - 102 * g - 10 * b + 128) >> 8);

    overlayY  = min(max(overlayY, 16), 235);
    overlayCb = min(max(overlayCb, 16), 240);
    overlayCr = min(max(overlayCr, 16), 240);

    int yIdx = y * nv12Pitch + x;
    nv12[yIdx] = (uint8_t)((nv12[yIdx] * inv + overlayY * a + 128) >> 8);

    if ((x & 1) == 0 && (y & 1) == 0) {
        int uvOffset = nv12Pitch * height;
        int uvIdx = (y / 2) * nv12Pitch + x;
        int curCb = nv12[uvOffset + uvIdx];
        int curCr = nv12[uvOffset + uvIdx + 1];
        nv12[uvOffset + uvIdx]     = (uint8_t)((curCb * inv + overlayCb * a + 128) >> 8);
        nv12[uvOffset + uvIdx + 1] = (uint8_t)((curCr * inv + overlayCr * a + 128) >> 8);
    }
}

// Occlusion-aware rectangular RGBA overlay onto NV12.
__global__ void alpha_blend_rgba_rect_nv12_occ_kernel(
    uint8_t* __restrict__ nv12,
    const uint8_t* __restrict__ rgba, int overlayW, int overlayH, int rgbaPitch,
    const uint8_t* __restrict__ occlusion, int occ_w, int occ_h,
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

    // Apply occlusion mask at destination coordinates.
    if (occlusion != NULL) {
        int ox, oy;
        if (occ_w == frameW && occ_h == frameH) {
            ox = dx; oy = dy;
        } else {
            ox = dx * occ_w / max(frameW, 1);
            oy = dy * occ_h / max(frameH, 1);
            ox = min(ox, occ_w - 1);
            oy = min(oy, occ_h - 1);
        }
        int occVal = occlusion[oy * occ_w + ox];
        a = (a * (255 - occVal) + 127) / 255;
        if (a == 0) return;
    }

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

// ============================================================================
// P210 (10-bit 4:2:2) DSK overlay kernel
// ============================================================================
// Alpha-blends RGBA (8-bit per channel) overlay onto P210 frame.
// BT.709 10-bit limited-range conversion uses the same 8-bit coefficients
// as the NV12 path (47, 157, 16 for Y etc.) but divides by 64 instead of 256
// to produce 10-bit output: effectively 4x the 8-bit result.
//   Y  = 64   + ((47*R + 157*G + 16*B + 32) >> 6)
//   Cb = 512  + ((-26*R - 86*G + 112*B + 32) >> 6)
//   Cr = 512  + ((112*R - 102*G - 10*B + 32) >> 6)

__global__ void dsk_overlay_p210_kernel(
    uint16_t* __restrict__ p210,
    const uint8_t* __restrict__ rgba,
    int width, int height, int p210Pitch16, int rgbaPitch,
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

    // BT.709 10-bit limited-range conversion
    // Same coefficients as 8-bit NV12, but >> 6 instead of >> 8 to produce 10-bit
    int overlayY  = 64  + ((47 * r + 157 * g + 16 * b + 32) >> 6);
    int overlayCb = 512 + ((-26 * r - 86 * g + 112 * b + 32) >> 6);
    int overlayCr = 512 + ((112 * r - 102 * g - 10 * b + 32) >> 6);

    // Clamp to 10-bit valid range
    overlayY  = min(max(overlayY, 64), 940);
    overlayCb = min(max(overlayCb, 64), 960);
    overlayCr = min(max(overlayCr, 64), 960);

    // Blend Y plane
    int yIdx = y * p210Pitch16 + x;
    p210[yIdx] = (uint16_t)(((int)p210[yIdx] * inv + overlayY * a + 128) >> 8);

    // Blend UV plane (once per pair — even pixels only)
    // P210 is 4:2:2: UV is every other pixel horizontally, every row
    if ((x & 1) == 0) {
        int uvOffset = p210Pitch16 * height;
        int uvIdx = y * p210Pitch16 + x;  // x is even, UV pairs at x, x+1
        int curCb = p210[uvOffset + uvIdx];
        int curCr = p210[uvOffset + uvIdx + 1];
        p210[uvOffset + uvIdx]     = (uint16_t)min(max(((int)curCb * inv + overlayCb * a + 128) >> 8, 0), 1023);
        p210[uvOffset + uvIdx + 1] = (uint16_t)min(max(((int)curCr * inv + overlayCr * a + 128) >> 8, 0), 1023);
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

cudaError_t alpha_blend_rgba_nv12_occ(
    uint8_t* nv12, const uint8_t* rgba,
    const uint8_t* occlusion, int occ_w, int occ_h,
    int width, int height, int nv12Pitch, int rgbaPitch,
    int alphaScale256, cudaStream_t stream)
{
    dim3 block(BLOCK_DIM_X, BLOCK_DIM_Y);
    dim3 grid((width + block.x - 1) / block.x, (height + block.y - 1) / block.y);
    alpha_blend_rgba_nv12_occ_kernel<<<grid, block, 0, stream>>>(
        nv12, rgba, occlusion, occ_w, occ_h,
        width, height, nv12Pitch, rgbaPitch, alphaScale256);
    return cudaGetLastError();
}

cudaError_t alpha_blend_rgba_rect_nv12_occ(
    uint8_t* nv12,
    const uint8_t* rgba, int overlayW, int overlayH, int rgbaPitch,
    const uint8_t* occlusion, int occ_w, int occ_h,
    int nv12Pitch, int frameW, int frameH,
    int rectX, int rectY, int rectW, int rectH,
    int alphaScale256, cudaStream_t stream)
{
    dim3 block(BLOCK_DIM_X, BLOCK_DIM_Y);
    dim3 grid((rectW + block.x - 1) / block.x, (rectH + block.y - 1) / block.y);
    alpha_blend_rgba_rect_nv12_occ_kernel<<<grid, block, 0, stream>>>(
        nv12, rgba, overlayW, overlayH, rgbaPitch,
        occlusion, occ_w, occ_h,
        nv12Pitch, frameW, frameH,
        rectX, rectY, rectW, rectH, alphaScale256);
    return cudaGetLastError();
}

// Rectangular RGBA overlay with nearest-neighbor scaling onto P210.
// Analogous to alpha_blend_rgba_rect_nv12_kernel but for 10-bit 4:2:2.
__global__ void dsk_overlay_rect_p210_kernel(
    uint16_t* __restrict__ p210,
    const uint8_t* __restrict__ rgba, int overlayW, int overlayH, int rgbaPitch,
    int p210Pitch16, int frameW, int frameH,
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

    // BT.709 10-bit limited-range conversion
    int overlayY  = 64  + ((47 * r + 157 * g + 16 * b + 32) >> 6);
    int overlayCb = 512 + ((-26 * r - 86 * g + 112 * b + 32) >> 6);
    int overlayCr = 512 + ((112 * r - 102 * g - 10 * b + 32) >> 6);

    overlayY  = min(max(overlayY, 64), 940);
    overlayCb = min(max(overlayCb, 64), 960);
    overlayCr = min(max(overlayCr, 64), 960);

    // Blend Y plane
    int yIdx = dy * p210Pitch16 + dx;
    p210[yIdx] = (uint16_t)(((int)p210[yIdx] * inv + overlayY * a + 128) >> 8);

    // Blend UV plane (once per pair — even pixels only, every row for 4:2:2)
    if ((dx & 1) == 0) {
        int uvOffset = p210Pitch16 * frameH;
        int uvIdx = dy * p210Pitch16 + dx;
        int curCb = p210[uvOffset + uvIdx];
        int curCr = p210[uvOffset + uvIdx + 1];
        p210[uvOffset + uvIdx]     = (uint16_t)min(max(((int)curCb * inv + overlayCb * a + 128) >> 8, 0), 1023);
        p210[uvOffset + uvIdx + 1] = (uint16_t)min(max(((int)curCr * inv + overlayCr * a + 128) >> 8, 0), 1023);
    }
}

// --- P210 DSK overlay wrappers ---

cudaError_t alpha_blend_rgba_p210(
    uint8_t* p210, const uint8_t* rgba,
    int width, int height, int p210Pitch, int rgbaPitch,
    int alphaScale256, cudaStream_t stream)
{
    int p210Pitch16 = p210Pitch / 2;
    dim3 block(BLOCK_DIM_X, BLOCK_DIM_Y);
    dim3 grid((width + block.x - 1) / block.x, (height + block.y - 1) / block.y);
    dsk_overlay_p210_kernel<<<grid, block, 0, stream>>>(
        (uint16_t*)p210, rgba, width, height, p210Pitch16, rgbaPitch, alphaScale256);
    return cudaGetLastError();
}

cudaError_t alpha_blend_rgba_rect_p210(
    uint8_t* p210,
    const uint8_t* rgba, int overlayW, int overlayH, int rgbaPitch,
    int p210Pitch, int frameW, int frameH,
    int rectX, int rectY, int rectW, int rectH,
    int alphaScale256, cudaStream_t stream)
{
    int p210Pitch16 = p210Pitch / 2;
    dim3 block(BLOCK_DIM_X, BLOCK_DIM_Y);
    dim3 grid((rectW + block.x - 1) / block.x, (rectH + block.y - 1) / block.y);
    dsk_overlay_rect_p210_kernel<<<grid, block, 0, stream>>>(
        (uint16_t*)p210, rgba, overlayW, overlayH, rgbaPitch,
        p210Pitch16, frameW, frameH,
        rectX, rectY, rectW, rectH, alphaScale256);
    return cudaGetLastError();
}

} // extern "C"
