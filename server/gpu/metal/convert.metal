#include <metal_stdlib>
using namespace metal;

// Params struct passed as constant buffer
struct ConvertParams {
    uint width;
    uint height;
    uint nv12Pitch;
    uint srcStride;
};

struct FillParams {
    uint width;
    uint height;
    uint pitch;
    uint8_t yVal;
    uint8_t cbVal;
    uint8_t crVal;
};

struct FillP210Params {
    uint width;
    uint height;
    uint pitch;      // in bytes
    ushort yVal;     // 10-bit Y (e.g., 64 for black)
    ushort cbVal;    // 10-bit Cb (e.g., 512 for neutral)
    ushort crVal;    // 10-bit Cr (e.g., 512 for neutral)
};

// Convert YUV420p (3 separate planes) to NV12 (Y + interleaved UV)
kernel void yuv420p_to_nv12(
    device const uint8_t* y_src   [[buffer(0)]],
    device const uint8_t* cb_src  [[buffer(1)]],
    device const uint8_t* cr_src  [[buffer(2)]],
    device uint8_t* nv12          [[buffer(3)]],
    constant ConvertParams& params [[buffer(4)]],
    uint2 gid [[thread_position_in_grid]])
{
    // Copy Y plane
    if (gid.x < params.width && gid.y < params.height) {
        nv12[gid.y * params.nv12Pitch + gid.x] = y_src[gid.y * params.srcStride + gid.x];
    }

    // Interleave UV (one thread per chroma pair, half resolution)
    uint chromaW = params.width / 2;
    uint chromaH = params.height / 2;
    if (gid.x < chromaW && gid.y < chromaH) {
        uint uvOffset = params.nv12Pitch * params.height;
        uint uvIdx = gid.y * params.nv12Pitch + gid.x * 2;
        uint srcIdx = gid.y * (params.srcStride / 2) + gid.x;
        nv12[uvOffset + uvIdx]     = cb_src[srcIdx];
        nv12[uvOffset + uvIdx + 1] = cr_src[srcIdx];
    }
}

// Convert NV12 to YUV420p
kernel void nv12_to_yuv420p(
    device uint8_t* y_dst         [[buffer(0)]],
    device uint8_t* cb_dst        [[buffer(1)]],
    device uint8_t* cr_dst        [[buffer(2)]],
    device const uint8_t* nv12    [[buffer(3)]],
    constant ConvertParams& params [[buffer(4)]],
    uint2 gid [[thread_position_in_grid]])
{
    // Copy Y plane
    if (gid.x < params.width && gid.y < params.height) {
        y_dst[gid.y * params.srcStride + gid.x] = nv12[gid.y * params.nv12Pitch + gid.x];
    }

    // De-interleave UV
    uint chromaW = params.width / 2;
    uint chromaH = params.height / 2;
    if (gid.x < chromaW && gid.y < chromaH) {
        uint uvOffset = params.nv12Pitch * params.height;
        uint uvIdx = gid.y * params.nv12Pitch + gid.x * 2;
        uint dstIdx = gid.y * (params.srcStride / 2) + gid.x;
        cb_dst[dstIdx] = nv12[uvOffset + uvIdx];
        cr_dst[dstIdx] = nv12[uvOffset + uvIdx + 1];
    }
}

// Fill P210 frame with constant 10-bit color.
// Y plane: width uint16 samples per row, height rows.
// UV plane: width/2 interleaved CbCr uint16 pairs per row, height rows (full height 4:2:2).
kernel void p210_fill(
    device uchar* p210            [[buffer(0)]],
    constant FillP210Params& params [[buffer(1)]],
    uint2 gid [[thread_position_in_grid]])
{
    uint samplesPerRow = params.pitch / 2;

    // Fill Y plane
    if (gid.x < params.width && gid.y < params.height) {
        ((device ushort*)p210)[gid.y * samplesPerRow + gid.x] = params.yVal;
    }

    // Fill UV plane (interleaved CbCr, full height for 4:2:2)
    uint chromaW = params.width / 2;
    if (gid.x < chromaW && gid.y < params.height) {
        uint uvOffset = samplesPerRow * params.height; // in uint16 elements
        uint uvIdx = gid.y * samplesPerRow + gid.x * 2;
        ((device ushort*)p210)[uvOffset + uvIdx]     = params.cbVal;
        ((device ushort*)p210)[uvOffset + uvIdx + 1] = params.crVal;
    }
}

// Fill NV12 frame with constant color (black = Y:16, UV:128 for limited range)
kernel void nv12_fill(
    device uint8_t* nv12          [[buffer(0)]],
    constant FillParams& params   [[buffer(1)]],
    uint2 gid [[thread_position_in_grid]])
{
    // Fill Y plane
    if (gid.x < params.width && gid.y < params.height) {
        nv12[gid.y * params.pitch + gid.x] = params.yVal;
    }

    // Fill UV plane (interleaved)
    uint chromaW = params.width / 2;
    uint chromaH = params.height / 2;
    if (gid.x < chromaW && gid.y < chromaH) {
        uint uvOffset = params.pitch * params.height;
        uint uvIdx = gid.y * params.pitch + gid.x * 2;
        nv12[uvOffset + uvIdx]     = params.cbVal;
        nv12[uvOffset + uvIdx + 1] = params.crVal;
    }
}
