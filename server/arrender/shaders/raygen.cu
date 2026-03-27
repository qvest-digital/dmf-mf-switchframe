// raygen.cu — Camera ray generation for Switchframe OptiX renderer.
// Generates primary rays from the camera through each pixel with
// optional multi-sample anti-aliasing (jittered sub-pixel offsets).
//
// Payload convention (2 registers):
//   p0: packed RGB (R in bits 0-9, G in bits 10-19, B in bits 20-29)
//   p1: alpha as float bits (__float_as_uint)

#include "common.cuh"

extern "C" {
    __constant__ LaunchParams params;
}

// Pack linear RGB [0,1] into a single 30-bit uint (10 bits per channel).
__device__ __forceinline__ unsigned int pack_rgb(float r, float g, float b) {
    unsigned int ri = min((unsigned int)(r * 1023.0f + 0.5f), 1023u);
    unsigned int gi = min((unsigned int)(g * 1023.0f + 0.5f), 1023u);
    unsigned int bi = min((unsigned int)(b * 1023.0f + 0.5f), 1023u);
    return ri | (gi << 10) | (bi << 20);
}

// Unpack 30-bit uint back to linear RGB.
__device__ __forceinline__ float3 unpack_rgb(unsigned int packed) {
    float r = float(packed & 0x3FF) / 1023.0f;
    float g = float((packed >> 10) & 0x3FF) / 1023.0f;
    float b = float((packed >> 20) & 0x3FF) / 1023.0f;
    return make_float3(r, g, b);
}

extern "C" __global__ void __raygen__render() {
    const uint3 idx = optixGetLaunchIndex();
    const uint3 dim = optixGetLaunchDimensions();

    if (idx.x >= dim.x || idx.y >= dim.y) return;

    // Initialize per-pixel RNG seed from pixel coordinates and frame number.
    unsigned int seed = (idx.y * dim.x + idx.x) * 1973 + params.frame_number * 6133;
    seed = rng_next(seed);

    float3 accumulated_color = make_float3(0.0f, 0.0f, 0.0f);
    float accumulated_alpha = 0.0f;

    const int spp = max(params.spp, 1);

    for (int s = 0; s < spp; s++) {
        // Sub-pixel jitter for anti-aliasing.
        float jx = (spp > 1) ? rng_float(seed) : 0.5f;
        float jy = (spp > 1) ? rng_float(seed) : 0.5f;

        // Normalized device coordinates [-1, 1].
        float u = 2.0f * (float(idx.x) + jx) / float(dim.x) - 1.0f;
        float v = 2.0f * (float(idx.y) + jy) / float(dim.y) - 1.0f;

        float3 origin = params.camera_pos;
        float3 direction = normalize(u * params.camera_u + v * params.camera_v + params.camera_w);

        // Payload: p0 = packed RGB, p1 = alpha bits
        unsigned int p0 = 0, p1 = 0;

        optixTrace(
            params.traversable,
            origin,
            direction,
            0.001f,             // tmin
            1e16f,              // tmax
            0.0f,               // rayTime
            OptixVisibilityMask(0xFF),
            OPTIX_RAY_FLAG_NONE,
            0,                  // SBT offset (primary ray)
            1,                  // SBT stride
            0,                  // miss SBT index
            p0, p1);

        float3 color = unpack_rgb(p0);
        float alpha = __uint_as_float(p1);

        accumulated_color = accumulated_color + color;
        accumulated_alpha += alpha;
    }

    // Average samples.
    float inv_spp = 1.0f / float(spp);
    float4 result = make_float4(
        accumulated_color.x * inv_spp,
        accumulated_color.y * inv_spp,
        accumulated_color.z * inv_spp,
        accumulated_alpha * inv_spp);

    // Write to output buffer.
    params.output_buffer[idx.y * params.width + idx.x] = result;
}
