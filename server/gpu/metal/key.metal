#include <metal_stdlib>
using namespace metal;

struct ChromaKeyParams {
    uint width;
    uint height;
    uint pitch;
    uint8_t keyCb;
    uint8_t keyCr;
    int simDistSq;
    int totalDistSq;
    float spillSuppress;
    uint8_t spillReplaceCb;
    uint8_t spillReplaceCr;
};

struct LumaKeyParams {
    uint width;
    uint height;
    uint pitch;
};

// Fused chroma key: compute mask from NV12 UV plane + optional spill suppression
// Each thread handles one luma pixel. Chroma is read at half-resolution from NV12 UV plane.
kernel void chroma_key_nv12(
    device uint8_t* nv12          [[buffer(0)]],
    device uint8_t* mask          [[buffer(1)]],
    constant ChromaKeyParams& params [[buffer(2)]],
    uint2 gid [[thread_position_in_grid]])
{
    if (gid.x >= params.width || gid.y >= params.height) return;

    // Read chroma for this 2x2 block from NV12 UV plane
    uint cx = gid.x / 2;
    uint cy = gid.y / 2;
    uint uvOffset = params.pitch * params.height;
    uint uvIdx = cy * params.pitch + cx * 2;
    int cb = int(nv12[uvOffset + uvIdx]);
    int cr = int(nv12[uvOffset + uvIdx + 1]);

    // Squared distance from key color in Cb/Cr space
    int dcb = cb - int(params.keyCb);
    int dcr = cr - int(params.keyCr);
    int distSq = dcb * dcb + dcr * dcr;

    // Generate alpha mask
    uint8_t alpha;
    if (distSq <= params.simDistSq) {
        alpha = 0;  // fully keyed (transparent)
    } else if (distSq >= params.totalDistSq) {
        alpha = 255;  // fully opaque
    } else {
        float t = float(distSq - params.simDistSq) / float(params.totalDistSq - params.simDistSq);
        alpha = uint8_t(sqrt(t) * 255.0f);
    }
    mask[gid.y * params.pitch + gid.x] = alpha;

    // Spill suppression: desaturate chroma near key color
    // Only modify chroma once per 2x2 block (top-left pixel of the block)
    if (params.spillSuppress > 0.0f && (gid.x & 1) == 0 && (gid.y & 1) == 0 && distSq < params.totalDistSq) {
        float amount = 1.0f - sqrt(float(distSq) / float(params.totalDistSq));
        amount *= params.spillSuppress;
        int newCb = cb + int(float(int(params.spillReplaceCb) - cb) * amount);
        int newCr = cr + int(float(int(params.spillReplaceCr) - cr) * amount);
        nv12[uvOffset + uvIdx]     = uint8_t(min(max(newCb, 16), 240));
        nv12[uvOffset + uvIdx + 1] = uint8_t(min(max(newCr, 16), 240));
    }
}

// Luma key: threshold-based with precomputed LUT passed as buffer
kernel void luma_key_nv12(
    device const uint8_t* nv12    [[buffer(0)]],
    device uint8_t* mask          [[buffer(1)]],
    device const uint8_t* lut     [[buffer(2)]],
    constant LumaKeyParams& params [[buffer(3)]],
    uint2 gid [[thread_position_in_grid]])
{
    if (gid.x >= params.width || gid.y >= params.height) return;

    mask[gid.y * params.pitch + gid.x] = lut[nv12[gid.y * params.pitch + gid.x]];
}

// ============================================================================
// P210 (10-bit 4:2:2) chroma + luma key kernels
// ============================================================================
//
// P210 layout (planar, written byte-by-byte but consumed as uint16):
//   Y plane: pitch * height bytes, samplesPerRow = pitch / 2
//   UV plane (interleaved CbCrCbCr): starts at pitch * height bytes;
//     full luma height (4:2:2), half luma width; samplesPerRow = pitch / 2
// 10-bit values use the low 10 bits of each uint16 (range 0-1023).
//
// The mask buffer is allocated from the same P210 pool as the program
// frame, so its raw byte stride (Pitch) is the P210 pitch. We write the
// alpha as 1 byte per pixel using maskPitch as the row stride; the
// downstream blend_alpha_p210 kernel reads it the same way.

struct ChromaKeyP210Params {
    uint width;          // luma width (samples)
    uint height;         // luma height (samples)
    uint pitch;          // P210 pitch in bytes (luma + UV use the same pitch)
    uint maskPitch;      // alpha mask pitch in bytes
    uint uvOffsetU16;    // start of UV plane, expressed in uint16 indices
    ushort keyCb;        // key chroma in 10-bit (0-1023)
    ushort keyCr;
    int simDistSq;       // 10-bit chroma distance squared, fully-keyed threshold
    int totalDistSq;     // 10-bit chroma distance squared, fully-opaque threshold
    float spillSuppress;
    ushort spillReplaceCb; // 10-bit
    ushort spillReplaceCr; // 10-bit
};

struct LumaKeyP210Params {
    uint width;
    uint height;
    uint pitch;       // P210 pitch in bytes
    uint maskPitch;   // alpha mask pitch in bytes
};

kernel void chroma_key_p210(
    device uchar* p210            [[buffer(0)]],
    device uint8_t* mask          [[buffer(1)]],
    constant ChromaKeyP210Params& params [[buffer(2)]],
    uint2 gid [[thread_position_in_grid]])
{
    if (gid.x >= params.width || gid.y >= params.height) return;

    uint cx = gid.x / 2;
    uint cy = gid.y; // P210 chroma is full height (4:2:2)
    uint samplesPerRow = params.pitch / 2;
    device ushort* p210U16 = (device ushort*)p210;

    uint cbIdx = params.uvOffsetU16 + cy * samplesPerRow + cx * 2;
    int cb = int(p210U16[cbIdx]);
    int cr = int(p210U16[cbIdx + 1]);

    int dcb = cb - int(params.keyCb);
    int dcr = cr - int(params.keyCr);
    int distSq = dcb * dcb + dcr * dcr;

    uint8_t alpha;
    if (distSq <= params.simDistSq) {
        alpha = 0;
    } else if (distSq >= params.totalDistSq) {
        alpha = 255;
    } else {
        float t = float(distSq - params.simDistSq) / float(params.totalDistSq - params.simDistSq);
        alpha = uint8_t(sqrt(t) * 255.0f);
    }
    mask[gid.y * params.maskPitch + gid.x] = alpha;

    // Spill suppression: write each 1x2 chroma block once, from the left
    // luma pixel of the pair. 10-bit limited-range chroma clamps to [64, 960].
    if (params.spillSuppress > 0.0f && (gid.x & 1) == 0 && distSq < params.totalDistSq) {
        float amount = 1.0f - sqrt(float(distSq) / float(params.totalDistSq));
        amount *= params.spillSuppress;
        int newCb = cb + int(float(int(params.spillReplaceCb) - cb) * amount);
        int newCr = cr + int(float(int(params.spillReplaceCr) - cr) * amount);
        p210U16[cbIdx]     = ushort(clamp(newCb, 64, 960));
        p210U16[cbIdx + 1] = ushort(clamp(newCr, 64, 960));
    }
}

// Luma key for P210: each luma sample is uint16 in the low 10 bits.
// We index the same 256-entry LUT used by NV12 by shifting the 10-bit
// sample down to 8 bits (>> 2). That's lossless w.r.t. the LUT's own
// 8-bit input quantisation and avoids needing a 1024-entry LUT.
kernel void luma_key_p210(
    device const uchar* p210      [[buffer(0)]],
    device uint8_t* mask          [[buffer(1)]],
    device const uint8_t* lut     [[buffer(2)]],
    constant LumaKeyP210Params& params [[buffer(3)]],
    uint2 gid [[thread_position_in_grid]])
{
    if (gid.x >= params.width || gid.y >= params.height) return;
    uint samplesPerRow = params.pitch / 2;
    device const ushort* yU16 = (device const ushort*)p210;
    ushort y10 = yU16[gid.y * samplesPerRow + gid.x];
    mask[gid.y * params.maskPitch + gid.x] = lut[y10 >> 2];
}
