#include <metal_stdlib>
using namespace metal;

struct BlendUniformParams {
    uint width;
    uint height;
    uint pitch;
    int pos256;  // 0-256 fixed-point blend position
};

struct BlendFadeConstParams {
    uint width;
    uint height;
    uint pitch;
    int pos256;
    uint8_t constY;
    uint8_t constUV;
};

struct BlendAlphaParams {
    uint width;
    uint height;
    uint pitch;
    uint alphaPitch;
};

struct WipeMaskParams {
    uint width;
    uint height;
    uint pitch;
    float position;
    int direction;   // 0=h-left, 1=h-right, 2=v-top, 3=v-bottom, 4=box-center, 5=box-edges
    int softEdge;
};

struct DownsampleAlphaParams {
    uint chromaW;
    uint chromaH;
    uint srcPitch;
    uint dstPitch;
};

// Uniform blend: dst = (a * inv + b * pos + 128) >> 8
// Works for both Y and UV planes (NV12)
kernel void blend_uniform(
    device uint8_t* dst           [[buffer(0)]],
    device const uint8_t* a       [[buffer(1)]],
    device const uint8_t* b       [[buffer(2)]],
    constant BlendUniformParams& params [[buffer(3)]],
    uint2 gid [[thread_position_in_grid]])
{
    if (gid.x >= params.width || gid.y >= params.height) return;

    uint idx = gid.y * params.pitch + gid.x;
    int inv = 256 - params.pos256;
    dst[idx] = (uint8_t)((int(a[idx]) * inv + int(b[idx]) * params.pos256 + 128) >> 8);
}

// Fade to/from constant value (FTB, dip phase)
kernel void blend_fade_const(
    device uint8_t* dst           [[buffer(0)]],
    device const uint8_t* src     [[buffer(1)]],
    constant BlendFadeConstParams& params [[buffer(2)]],
    uint2 gid [[thread_position_in_grid]])
{
    if (gid.x >= params.width || gid.y >= params.height) return;

    uint idx = gid.y * params.pitch + gid.x;
    int inv = 256 - params.pos256;
    // For Y plane, use constY; for UV plane, use constUV
    // This kernel is dispatched separately for Y and UV with different params
    dst[idx] = (uint8_t)((int(src[idx]) * inv + int(params.constY) * params.pos256 + 128) >> 8);
}

// Per-pixel alpha blend (wipes, stingers)
kernel void blend_alpha(
    device uint8_t* dst           [[buffer(0)]],
    device const uint8_t* a       [[buffer(1)]],
    device const uint8_t* b       [[buffer(2)]],
    device const uint8_t* alpha   [[buffer(3)]],
    constant BlendAlphaParams& params [[buffer(4)]],
    uint2 gid [[thread_position_in_grid]])
{
    if (gid.x >= params.width || gid.y >= params.height) return;

    uint idx = gid.y * params.pitch + gid.x;
    uint aidx = gid.y * params.alphaPitch + gid.x;
    int al = int(alpha[aidx]) + (int(alpha[aidx]) >> 7); // match CPU: 0-256 range
    int inv = 256 - al;
    dst[idx] = (uint8_t)((int(a[idx]) * inv + int(b[idx]) * al + 128) >> 8);
}

// Generate wipe alpha mask
kernel void wipe_mask_generate(
    device uint8_t* mask          [[buffer(0)]],
    constant WipeMaskParams& params [[buffer(1)]],
    uint2 gid [[thread_position_in_grid]])
{
    if (gid.x >= params.width || gid.y >= params.height) return;

    float threshold;
    switch (params.direction) {
    case 0: threshold = float(gid.x) / float(params.width); break;
    case 1: threshold = 1.0f - float(gid.x) / float(params.width); break;
    case 2: threshold = float(gid.y) / float(params.height); break;
    case 3: threshold = 1.0f - float(gid.y) / float(params.height); break;
    case 4: {
        float cx = abs(float(gid.x) / float(params.width) - 0.5f) * 2.0f;
        float cy = abs(float(gid.y) / float(params.height) - 0.5f) * 2.0f;
        threshold = max(cx, cy);
        break;
    }
    case 5: {
        float cx = abs(float(gid.x) / float(params.width) - 0.5f) * 2.0f;
        float cy = abs(float(gid.y) / float(params.height) - 0.5f) * 2.0f;
        threshold = 1.0f - max(cx, cy);
        break;
    }
    default: threshold = float(gid.x) / float(params.width); break;
    }

    float dim = (params.direction <= 1) ? float(params.width) : float(params.height);
    float edgeF = float(params.softEdge) / dim;
    float alpha;
    if (edgeF < 0.001f) {
        alpha = (params.position >= threshold) ? 1.0f : 0.0f;
    } else if (params.position <= threshold - edgeF) {
        alpha = 0.0f;
    } else if (params.position >= threshold + edgeF) {
        alpha = 1.0f;
    } else {
        alpha = (params.position - threshold + edgeF) / (2.0f * edgeF);
    }

    mask[gid.y * params.pitch + gid.x] = uint8_t(alpha * 255.0f + 0.5f);
}

// Downsample luma-resolution alpha to NV12 UV-plane width.
// Each chroma alpha is written to both Cb (2*cx) and Cr (2*cx+1) positions.
kernel void downsample_alpha_to_nv12_uv(
    device uint8_t* dst           [[buffer(0)]],
    device const uint8_t* src     [[buffer(1)]],
    constant DownsampleAlphaParams& params [[buffer(2)]],
    uint2 gid [[thread_position_in_grid]])
{
    if (gid.x >= params.chromaW || gid.y >= params.chromaH) return;

    uint lx = gid.x * 2;
    uint ly = gid.y * 2;
    int avg = (int(src[ly * params.srcPitch + lx]) +
               int(src[ly * params.srcPitch + lx + 1]) +
               int(src[(ly+1) * params.srcPitch + lx]) +
               int(src[(ly+1) * params.srcPitch + lx + 1]) + 2) >> 2;
    dst[gid.y * params.dstPitch + gid.x * 2]     = uint8_t(avg);
    dst[gid.y * params.dstPitch + gid.x * 2 + 1] = uint8_t(avg);
}
