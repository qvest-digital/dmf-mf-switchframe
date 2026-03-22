#include "common.cuh"

// 3D LUT trilinear interpolation.
// LUT stored as flat float array: size^3 * 3 floats, red-major order
// (R varies fastest: index = (b * size * size + g * size + r) * 3).
__device__ void lut3d_lookup(
    const float* __restrict__ lut, int size,
    float r, float g, float b,
    float* out_r, float* out_g, float* out_b)
{
    // Scale input [0,1] to LUT index space
    float fr = r * (float)(size - 1);
    float fg = g * (float)(size - 1);
    float fb = b * (float)(size - 1);

    // Integer indices (clamped to valid range for interpolation)
    int r0 = min(max((int)fr, 0), size - 2);
    int g0 = min(max((int)fg, 0), size - 2);
    int b0 = min(max((int)fb, 0), size - 2);
    float dr = fr - (float)r0;
    float dg = fg - (float)g0;
    float db = fb - (float)b0;

    // Red-major indexing: idx = (b * size^2 + g * size + r) * 3
    int s2 = size * size;
    #define LUT3(ri, gi, bi, ch) lut[((bi) * s2 + (gi) * size + (ri)) * 3 + (ch)]

    // Trilinear interpolation: lerp along R, then G, then B.
    // 8 corner lookups, 3 channels each.
    for (int ch = 0; ch < 3; ch++) {
        float c000 = LUT3(r0,   g0,   b0,   ch);
        float c100 = LUT3(r0+1, g0,   b0,   ch);
        float c010 = LUT3(r0,   g0+1, b0,   ch);
        float c110 = LUT3(r0+1, g0+1, b0,   ch);
        float c001 = LUT3(r0,   g0,   b0+1, ch);
        float c101 = LUT3(r0+1, g0,   b0+1, ch);
        float c011 = LUT3(r0,   g0+1, b0+1, ch);
        float c111 = LUT3(r0+1, g0+1, b0+1, ch);

        // Lerp along R
        float c00 = c000 + (c100 - c000) * dr;
        float c10 = c010 + (c110 - c010) * dr;
        float c01 = c001 + (c101 - c001) * dr;
        float c11 = c011 + (c111 - c011) * dr;

        // Lerp along G
        float c0 = c00 + (c10 - c00) * dg;
        float c1 = c01 + (c11 - c01) * dg;

        // Lerp along B
        float val = c0 + (c1 - c0) * db;

        if (ch == 0) *out_r = val;
        else if (ch == 1) *out_g = val;
        else *out_b = val;
    }
    #undef LUT3
}

// Fused NV12 color grading kernel:
// Read NV12 pixel → BT.709 YCbCr→RGB → 3D LUT lookup → blend by intensity → RGB→YCbCr → write NV12.
// Each thread processes one luma pixel. UV writes are gated to top-left of each 2x2 block.
__global__ void colorgrade_lut3d_nv12_kernel(
    uint8_t* __restrict__ nv12,
    const float* __restrict__ lut,
    int lutSize,
    float intensity,  // 0.0 = bypass, 1.0 = full grade
    int width, int height, int pitch)
{
    int x = blockIdx.x * blockDim.x + threadIdx.x;
    int y = blockIdx.y * blockDim.y + threadIdx.y;
    if (x >= width || y >= height) return;

    // Read Y from luma plane
    int yIdx = y * pitch + x;
    float Y = (float)nv12[yIdx];

    // Read Cb/Cr from interleaved UV plane (shared by 2x2 luma block)
    int uvOff = pitch * height;
    int uvIdx = (y / 2) * pitch + (x & ~1);
    float Cb = (float)nv12[uvOff + uvIdx];
    float Cr = (float)nv12[uvOff + uvIdx + 1];

    // BT.709 limited-range YCbCr → linear RGB [0,1]
    float Yn  = (Y  - 16.0f) / 219.0f;
    float Cbn = (Cb - 128.0f) / 224.0f;
    float Crn = (Cr - 128.0f) / 224.0f;

    float r = Yn + 1.5748f * Crn;
    float g = Yn - 0.1873f * Cbn - 0.4681f * Crn;
    float b = Yn + 1.8556f * Cbn;

    r = fminf(fmaxf(r, 0.0f), 1.0f);
    g = fminf(fmaxf(g, 0.0f), 1.0f);
    b = fminf(fmaxf(b, 0.0f), 1.0f);

    // 3D LUT lookup with trilinear interpolation
    float lr, lg, lb;
    lut3d_lookup(lut, lutSize, r, g, b, &lr, &lg, &lb);

    // Blend original with graded by intensity
    float fr = r + (lr - r) * intensity;
    float fg = g + (lg - g) * intensity;
    float fb = b + (lb - b) * intensity;

    // RGB [0,1] → BT.709 limited-range YCbCr
    float Yout  = 16.0f  + 219.0f * (BT709_Y_R * fr + BT709_Y_G * fg + BT709_Y_B * fb);
    float Cbout = 128.0f + 224.0f * (-0.1146f * fr - 0.3854f * fg + 0.5f * fb);
    float Crout = 128.0f + 224.0f * (0.5f * fr - 0.4542f * fg - 0.0458f * fb);

    // Write Y (every pixel)
    nv12[yIdx] = (uint8_t)fminf(fmaxf(Yout + 0.5f, 16.0f), 235.0f);

    // Write UV (once per 2x2 block — top-left pixel only)
    if ((x & 1) == 0 && (y & 1) == 0) {
        nv12[uvOff + uvIdx]     = (uint8_t)fminf(fmaxf(Cbout + 0.5f, 16.0f), 240.0f);
        nv12[uvOff + uvIdx + 1] = (uint8_t)fminf(fmaxf(Crout + 0.5f, 16.0f), 240.0f);
    }
}

extern "C" {

cudaError_t launch_colorgrade_lut3d(
    uint8_t* nv12, const float* lut, int lutSize, float intensity,
    int width, int height, int pitch, cudaStream_t stream)
{
    dim3 block(BLOCK_DIM_X, BLOCK_DIM_Y);
    dim3 grid((width + BLOCK_DIM_X - 1) / BLOCK_DIM_X,
              (height + BLOCK_DIM_Y - 1) / BLOCK_DIM_Y);
    colorgrade_lut3d_nv12_kernel<<<grid, block, 0, stream>>>(
        nv12, lut, lutSize, intensity, width, height, pitch);
    return cudaGetLastError();
}

cudaError_t colorgrade_upload_lut(float** devLut, const float* hostLut, int count)
{
    size_t sz = (size_t)count * sizeof(float);
    cudaError_t err = cudaMalloc((void**)devLut, sz);
    if (err != cudaSuccess) return err;

    err = cudaMemcpy(*devLut, hostLut, sz, cudaMemcpyHostToDevice);
    if (err != cudaSuccess) {
        cudaFree(*devLut);
        *devLut = NULL;
        return err;
    }
    return cudaSuccess;
}

void colorgrade_free_lut(float* devLut) {
    if (devLut) cudaFree(devLut);
}

} // extern "C"
