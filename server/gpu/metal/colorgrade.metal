#include "common.h"

// 3D LUT trilinear interpolation (shared by NV12 and P210 kernels).
static float3 lut3d_lookup(
    device const float* lut, int size,
    float r, float g, float b)
{
    float fr = r * float(size - 1);
    float fg = g * float(size - 1);
    float fb = b * float(size - 1);

    int r0 = min(max(int(fr), 0), size - 2);
    int g0 = min(max(int(fg), 0), size - 2);
    int b0 = min(max(int(fb), 0), size - 2);
    float dr = fr - float(r0);
    float dg = fg - float(g0);
    float db = fb - float(b0);

    int s2 = size * size;
    #define LUT3(ri, gi, bi, ch) lut[((bi) * s2 + (gi) * size + (ri)) * 3 + (ch)]

    float3 result;
    for (int ch = 0; ch < 3; ch++) {
        float c000 = LUT3(r0,   g0,   b0,   ch);
        float c100 = LUT3(r0+1, g0,   b0,   ch);
        float c010 = LUT3(r0,   g0+1, b0,   ch);
        float c110 = LUT3(r0+1, g0+1, b0,   ch);
        float c001 = LUT3(r0,   g0,   b0+1, ch);
        float c101 = LUT3(r0+1, g0,   b0+1, ch);
        float c011 = LUT3(r0,   g0+1, b0+1, ch);
        float c111 = LUT3(r0+1, g0+1, b0+1, ch);

        float c00 = c000 + (c100 - c000) * dr;
        float c10 = c010 + (c110 - c010) * dr;
        float c01 = c001 + (c101 - c001) * dr;
        float c11 = c011 + (c111 - c011) * dr;
        float c0 = c00 + (c10 - c00) * dg;
        float c1 = c01 + (c11 - c01) * dg;
        float val = c0 + (c1 - c0) * db;

        if (ch == 0) result.x = val;
        else if (ch == 1) result.y = val;
        else result.z = val;
    }
    #undef LUT3
    return result;
}

// NV12 (8-bit) color grading kernel.
struct ColorGradeNV12Params {
    uint32_t width;
    uint32_t height;
    uint32_t pitch;
    int32_t  lutSize;
    float    intensity;
};

kernel void colorgrade_lut3d_nv12(
    device uint8_t*       nv12     [[buffer(0)]],
    device const float*   lut      [[buffer(1)]],
    constant ColorGradeNV12Params& p [[buffer(2)]],
    uint2 gid [[thread_position_in_grid]])
{
    if (gid.x >= p.width || gid.y >= p.height) return;

    int yIdx = gid.y * p.pitch + gid.x;
    float Y = float(nv12[yIdx]);

    int uvOff = p.pitch * p.height;
    int uvIdx = (gid.y / 2) * p.pitch + (gid.x & ~1u);
    float Cb = float(nv12[uvOff + uvIdx]);
    float Cr = float(nv12[uvOff + uvIdx + 1]);

    // BT.709 8-bit limited-range YCbCr -> RGB [0,1]
    float Yn  = (Y  - 16.0f) / 219.0f;
    float Cbn = (Cb - 128.0f) / 224.0f;
    float Crn = (Cr - 128.0f) / 224.0f;

    float r = Yn + 1.5748f * Crn;
    float g = Yn - 0.1873f * Cbn - 0.4681f * Crn;
    float b = Yn + 1.8556f * Cbn;
    r = clamp(r, 0.0f, 1.0f);
    g = clamp(g, 0.0f, 1.0f);
    b = clamp(b, 0.0f, 1.0f);

    float3 graded = lut3d_lookup(lut, p.lutSize, r, g, b);
    float fr = r + (graded.x - r) * p.intensity;
    float fg = g + (graded.y - g) * p.intensity;
    float fb = b + (graded.z - b) * p.intensity;

    float Yout  = 16.0f  + 219.0f * (BT709_Y_R * fr + BT709_Y_G * fg + BT709_Y_B * fb);
    float Cbout = 128.0f + 224.0f * (-0.1146f * fr - 0.3854f * fg + 0.5f * fb);
    float Crout = 128.0f + 224.0f * (0.5f * fr - 0.4542f * fg - 0.0458f * fb);

    nv12[yIdx] = uint8_t(clamp(Yout + 0.5f, 16.0f, 235.0f));

    if ((gid.x & 1u) == 0 && (gid.y & 1u) == 0) {
        nv12[uvOff + uvIdx]     = uint8_t(clamp(Cbout + 0.5f, 16.0f, 240.0f));
        nv12[uvOff + uvIdx + 1] = uint8_t(clamp(Crout + 0.5f, 16.0f, 240.0f));
    }
}

// P210 (10-bit 4:2:2) color grading kernel.
struct ColorGradeP210Params {
    uint32_t width;
    uint32_t height;
    uint32_t pitch;
    int32_t  lutSize;
    float    intensity;
};

kernel void colorgrade_lut3d_p210(
    device uint8_t*       p210     [[buffer(0)]],
    device const float*   lut      [[buffer(1)]],
    constant ColorGradeP210Params& p [[buffer(2)]],
    uint2 gid [[thread_position_in_grid]])
{
    if (gid.x >= p.width || gid.y >= p.height) return;

    // Y plane: uint16 at byte offset y * pitch + x * 2
    device uint16_t* yRow = (device uint16_t*)(p210 + gid.y * p.pitch);
    float Y = float(yRow[gid.x]);

    // UV plane: interleaved Cb/Cr uint16 at offset pitch * height
    int uvOff = p.pitch * p.height;
    device uint16_t* uvRow = (device uint16_t*)(p210 + uvOff + gid.y * p.pitch);
    int uvX = (gid.x & ~1u);
    float Cb = float(uvRow[uvX]);
    float Cr = float(uvRow[uvX + 1]);

    // BT.709 10-bit limited-range YCbCr -> RGB [0,1]
    float Yn  = (Y  - 64.0f) / 876.0f;
    float Cbn = (Cb - 512.0f) / 896.0f;
    float Crn = (Cr - 512.0f) / 896.0f;

    float r = Yn + 1.5748f * Crn;
    float g = Yn - 0.1873f * Cbn - 0.4681f * Crn;
    float b = Yn + 1.8556f * Cbn;
    r = clamp(r, 0.0f, 1.0f);
    g = clamp(g, 0.0f, 1.0f);
    b = clamp(b, 0.0f, 1.0f);

    float3 graded = lut3d_lookup(lut, p.lutSize, r, g, b);
    float fr = r + (graded.x - r) * p.intensity;
    float fg = g + (graded.y - g) * p.intensity;
    float fb = b + (graded.z - b) * p.intensity;

    // RGB [0,1] -> BT.709 10-bit limited-range YCbCr
    float Yout  = 64.0f  + 876.0f * (BT709_Y_R * fr + BT709_Y_G * fg + BT709_Y_B * fb);
    float Cbout = 512.0f + 896.0f * (-0.1146f * fr - 0.3854f * fg + 0.5f * fb);
    float Crout = 512.0f + 896.0f * (0.5f * fr - 0.4542f * fg - 0.0458f * fb);

    // Write Y (every pixel) - clamp to 10-bit limited range
    yRow[gid.x] = uint16_t(clamp(Yout + 0.5f, 64.0f, 940.0f));

    // Write UV (once per 2-pixel pair)
    if ((gid.x & 1u) == 0) {
        uvRow[uvX]     = uint16_t(clamp(Cbout + 0.5f, 64.0f, 960.0f));
        uvRow[uvX + 1] = uint16_t(clamp(Crout + 0.5f, 64.0f, 960.0f));
    }
}
