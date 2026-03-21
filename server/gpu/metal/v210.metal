#include <metal_stdlib>
using namespace metal;

struct V210ToNV12Params {
    uint width;
    uint height;
    uint nv12Pitch;
    uint v210Stride32;  // stride in uint32 elements
};

struct NV12ToV210Params {
    uint width;
    uint height;
    uint nv12Pitch;
    uint v210Stride32;
};

// V210 -> NV12 conversion kernel
// Each thread processes one 6-pixel V210 group for one row.
kernel void v210_to_nv12(
    device uint8_t* nv12                [[buffer(0)]],
    device const uint32_t* v210         [[buffer(1)]],
    constant V210ToNV12Params& params   [[buffer(2)]],
    uint2 gid [[thread_position_in_grid]])
{
    uint groupX = gid.x;
    uint y = gid.y;
    uint groupsPerRow = params.width / 6;
    if (groupX >= groupsPerRow || y >= params.height) return;

    uint v210Idx = y * params.v210Stride32 + groupX * 4;
    uint32_t w0 = v210[v210Idx];
    uint32_t w1 = v210[v210Idx + 1];
    uint32_t w2 = v210[v210Idx + 2];
    uint32_t w3 = v210[v210Idx + 3];

    uint px = groupX * 6;
    uint8_t y0 = uint8_t(((w0 >> 10) & 0x3FF) >> 2);
    uint8_t y1 = uint8_t(((w1)       & 0x3FF) >> 2);
    uint8_t y2 = uint8_t(((w1 >> 20) & 0x3FF) >> 2);
    uint8_t y3 = uint8_t(((w2 >> 10) & 0x3FF) >> 2);
    uint8_t y4 = uint8_t(((w3)       & 0x3FF) >> 2);
    uint8_t y5 = uint8_t(((w3 >> 20) & 0x3FF) >> 2);

    nv12[y * params.nv12Pitch + px]     = y0;
    nv12[y * params.nv12Pitch + px + 1] = y1;
    nv12[y * params.nv12Pitch + px + 2] = y2;
    nv12[y * params.nv12Pitch + px + 3] = y3;
    nv12[y * params.nv12Pitch + px + 4] = y4;
    nv12[y * params.nv12Pitch + px + 5] = y5;

    uint8_t cb0 = uint8_t(((w0)       & 0x3FF) >> 2);
    uint8_t cr0 = uint8_t(((w0 >> 20) & 0x3FF) >> 2);
    uint8_t cb2 = uint8_t(((w1 >> 10) & 0x3FF) >> 2);
    uint8_t cr2 = uint8_t(((w2)       & 0x3FF) >> 2);
    uint8_t cb4 = uint8_t(((w2 >> 20) & 0x3FF) >> 2);
    uint8_t cr4 = uint8_t(((w3 >> 10) & 0x3FF) >> 2);

    if ((y & 1) == 0 && y + 1 < params.height) {
        uint v210IdxNext = (y + 1) * params.v210Stride32 + groupX * 4;
        uint32_t nw0 = v210[v210IdxNext];
        uint32_t nw1 = v210[v210IdxNext + 1];
        uint32_t nw2 = v210[v210IdxNext + 2];
        uint32_t nw3 = v210[v210IdxNext + 3];

        uint8_t ncb0 = uint8_t(((nw0)       & 0x3FF) >> 2);
        uint8_t ncr0 = uint8_t(((nw0 >> 20) & 0x3FF) >> 2);
        uint8_t ncb2 = uint8_t(((nw1 >> 10) & 0x3FF) >> 2);
        uint8_t ncr2 = uint8_t(((nw2)       & 0x3FF) >> 2);
        uint8_t ncb4 = uint8_t(((nw2 >> 20) & 0x3FF) >> 2);
        uint8_t ncr4 = uint8_t(((nw3 >> 10) & 0x3FF) >> 2);

        uint uvOffset = params.nv12Pitch * params.height;
        uint uvY = y / 2;
        uint cx = groupX * 3;

        uint uvIdx0 = uvY * params.nv12Pitch + cx * 2;
        nv12[uvOffset + uvIdx0]     = uint8_t((uint(cb0) + uint(ncb0) + 1) >> 1);
        nv12[uvOffset + uvIdx0 + 1] = uint8_t((uint(cr0) + uint(ncr0) + 1) >> 1);

        if (cx + 1 < params.width / 2) {
            uint uvIdx1 = uvY * params.nv12Pitch + (cx + 1) * 2;
            nv12[uvOffset + uvIdx1]     = uint8_t((uint(cb2) + uint(ncb2) + 1) >> 1);
            nv12[uvOffset + uvIdx1 + 1] = uint8_t((uint(cr2) + uint(ncr2) + 1) >> 1);
        }
        if (cx + 2 < params.width / 2) {
            uint uvIdx2 = uvY * params.nv12Pitch + (cx + 2) * 2;
            nv12[uvOffset + uvIdx2]     = uint8_t((uint(cb4) + uint(ncb4) + 1) >> 1);
            nv12[uvOffset + uvIdx2 + 1] = uint8_t((uint(cr4) + uint(ncr4) + 1) >> 1);
        }
    }
}

// NV12 -> V210 conversion kernel
kernel void nv12_to_v210(
    device uint32_t* v210                [[buffer(0)]],
    device const uint8_t* nv12           [[buffer(1)]],
    constant NV12ToV210Params& params    [[buffer(2)]],
    uint2 gid [[thread_position_in_grid]])
{
    uint groupX = gid.x;
    uint y = gid.y;
    uint groupsPerRow = params.width / 6;
    if (groupX >= groupsPerRow || y >= params.height) return;

    uint px = groupX * 6;

    uint32_t Y0 = uint32_t(nv12[y * params.nv12Pitch + px])     << 2;
    uint32_t Y1 = uint32_t(nv12[y * params.nv12Pitch + px + 1]) << 2;
    uint32_t Y2 = uint32_t(nv12[y * params.nv12Pitch + px + 2]) << 2;
    uint32_t Y3 = uint32_t(nv12[y * params.nv12Pitch + px + 3]) << 2;
    uint32_t Y4 = uint32_t(nv12[y * params.nv12Pitch + px + 4]) << 2;
    uint32_t Y5 = uint32_t(nv12[y * params.nv12Pitch + px + 5]) << 2;

    uint uvOffset = params.nv12Pitch * params.height;
    uint uvY = y / 2;
    uint cx = groupX * 3;

    uint32_t Cb0 = uint32_t(nv12[uvOffset + uvY * params.nv12Pitch + cx * 2])     << 2;
    uint32_t Cr0 = uint32_t(nv12[uvOffset + uvY * params.nv12Pitch + cx * 2 + 1]) << 2;
    uint32_t Cb2 = (cx + 1 < params.width / 2) ? uint32_t(nv12[uvOffset + uvY * params.nv12Pitch + (cx+1) * 2])     << 2 : Cb0;
    uint32_t Cr2 = (cx + 1 < params.width / 2) ? uint32_t(nv12[uvOffset + uvY * params.nv12Pitch + (cx+1) * 2 + 1]) << 2 : Cr0;
    uint32_t Cb4 = (cx + 2 < params.width / 2) ? uint32_t(nv12[uvOffset + uvY * params.nv12Pitch + (cx+2) * 2])     << 2 : Cb2;
    uint32_t Cr4 = (cx + 2 < params.width / 2) ? uint32_t(nv12[uvOffset + uvY * params.nv12Pitch + (cx+2) * 2 + 1]) << 2 : Cr2;

    uint32_t w0 = (Cb0 & 0x3FF) | ((Y0 & 0x3FF) << 10) | ((Cr0 & 0x3FF) << 20);
    uint32_t w1 = (Y1  & 0x3FF) | ((Cb2 & 0x3FF) << 10) | ((Y2 & 0x3FF) << 20);
    uint32_t w2 = (Cr2 & 0x3FF) | ((Y3  & 0x3FF) << 10) | ((Cb4 & 0x3FF) << 20);
    uint32_t w3 = (Y4  & 0x3FF) | ((Cr4 & 0x3FF) << 10) | ((Y5 & 0x3FF) << 20);

    uint v210Idx = y * params.v210Stride32 + groupX * 4;
    v210[v210Idx]     = w0;
    v210[v210Idx + 1] = w1;
    v210[v210Idx + 2] = w2;
    v210[v210Idx + 3] = w3;
}
