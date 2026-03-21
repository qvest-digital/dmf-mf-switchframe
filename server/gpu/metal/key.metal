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
