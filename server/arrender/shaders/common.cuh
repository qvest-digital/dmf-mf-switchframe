// common.cuh — Shared types for Switchframe OptiX PBR shaders.
// These structures are shared between all shader programs (raygen,
// closest-hit, miss, shadow) and must match the host-side
// optix_launch_params_t layout in optix_wrapper.h.
//
// IMPORTANT: LaunchParams layout must stay in sync with
// optix_launch_params_t in optix_wrapper.h. If you add or reorder
// fields here, update the C struct there (and vice-versa).

#pragma once
#include <optix.h>
#include <cuda_runtime.h>

// Pi constant — defined early so all inline functions can use it.
#ifndef M_PI
#define M_PI 3.14159265358979323846f
#endif

// ---------- Light ----------
// Scene light source for direct illumination.
// Matches optix_light_t from optix_wrapper.h.
struct Light {
    int type;           // 0 = point, 1 = directional, 2 = area
    float3 position;    // world-space position (point/area)
    float3 direction;   // light direction (directional/area)
    float3 color;       // linear RGB color
    float intensity;    // radiance multiplier
};

// ---------- Launch parameters ----------
// Passed to all programs via SBT or optixGetSbtDataPointer().
// Must match optix_launch_params_t from optix_wrapper.h.
struct LaunchParams {
    float4* output_buffer;          // float4 RGBA render target (device pointer)
    int width;                      // render width in pixels
    int height;                     // render height in pixels
    OptixTraversableHandle traversable; // top-level IAS handle
    float3 camera_pos;              // camera world position
    float3 camera_u;                // camera right vector (screen-space X)
    float3 camera_v;                // camera up vector (screen-space Y)
    float3 camera_w;                // camera forward vector (into screen)
    int spp;                        // samples per pixel
    int frame_number;               // for RNG seeding / temporal accumulation
    Light* lights;                  // device pointer to light buffer (nullable)
    int num_lights;                 // number of lights in buffer (0 = use fallback)
};

// ---------- Material ----------
// PBR material parameters for Cook-Torrance shading.
struct Material {
    float3 base_color;              // albedo (linear RGB)
    float metallic;                 // 0 = dielectric, 1 = metal
    float roughness;                // 0 = mirror, 1 = rough
    float3 emission;                // emissive color (linear RGB, HDR)
};

// ---------- Hit record ----------
// Per-intersection data populated by closest-hit programs.
struct HitRecord {
    float3 position;                // world-space hit point
    float3 normal;                  // world-space shading normal
    float2 texcoord;                // UV coordinates
    int material_id;                // index into material buffer
};

// ---------- Per-ray payload ----------
// Carried through the trace call and populated by hit/miss programs.
struct RayPayload {
    float3 color;                   // accumulated radiance
    float alpha;                    // opacity (0 = transparent, 1 = opaque)
    float t_hit;                    // hit distance (FLT_MAX if miss)
    int depth;                      // current bounce depth
    unsigned int seed;              // per-ray RNG state (xorshift)
};

// ---------- SBT record data ----------
// Shader binding table record for closest-hit programs.
struct HitGroupData {
    float3* vertices;               // device pointer to vertex positions
    float3* normals;                // device pointer to vertex normals
    float2* texcoords;              // device pointer to UVs (nullable)
    uint3* indices;                 // device pointer to triangle indices
    Material material;              // material parameters
    unsigned long long base_color_tex; // cudaTextureObject_t (0 = no texture)
};

// ---------- float3 vector math ----------
// CUDA float3 has no built-in operators. These are standard helpers
// (equivalent to OptiX SDK sutil/vec_math.h).

__device__ __forceinline__ float3 operator+(const float3& a, const float3& b) {
    return make_float3(a.x + b.x, a.y + b.y, a.z + b.z);
}
__device__ __forceinline__ float3 operator-(const float3& a, const float3& b) {
    return make_float3(a.x - b.x, a.y - b.y, a.z - b.z);
}
__device__ __forceinline__ float3 operator*(float s, const float3& v) {
    return make_float3(s * v.x, s * v.y, s * v.z);
}
__device__ __forceinline__ float3 operator*(const float3& v, float s) {
    return make_float3(v.x * s, v.y * s, v.z * s);
}
__device__ __forceinline__ float3 operator*(const float3& a, const float3& b) {
    return make_float3(a.x * b.x, a.y * b.y, a.z * b.z);
}
__device__ __forceinline__ float3 operator/(const float3& v, float s) {
    float inv = 1.0f / s;
    return make_float3(v.x * inv, v.y * inv, v.z * inv);
}
__device__ __forceinline__ float3 operator-(const float3& v) {
    return make_float3(-v.x, -v.y, -v.z);
}
__device__ __forceinline__ float dot(const float3& a, const float3& b) {
    return a.x * b.x + a.y * b.y + a.z * b.z;
}
__device__ __forceinline__ float length(const float3& v) {
    return sqrtf(dot(v, v));
}
__device__ __forceinline__ float3 normalize(const float3& v) {
    float inv_len = rsqrtf(fmaxf(dot(v, v), 1e-30f));
    return v * inv_len;
}
__device__ __forceinline__ float3 cross(const float3& a, const float3& b) {
    return make_float3(a.y * b.z - a.z * b.y,
                       a.z * b.x - a.x * b.z,
                       a.x * b.y - a.y * b.x);
}
__device__ __forceinline__ float3 reflect(const float3& i, const float3& n) {
    return i - 2.0f * dot(n, i) * n;
}
__device__ __forceinline__ float3 fminf3(const float3& a, const float3& b) {
    return make_float3(fminf(a.x, b.x), fminf(a.y, b.y), fminf(a.z, b.z));
}
__device__ __forceinline__ float3 fmaxf3(const float3& a, const float3& b) {
    return make_float3(fmaxf(a.x, b.x), fmaxf(a.y, b.y), fmaxf(a.z, b.z));
}

// ---------- Utility functions ----------

// Simple xorshift RNG for stratified sampling.
__device__ __forceinline__ unsigned int rng_next(unsigned int& state) {
    state ^= state << 13;
    state ^= state >> 17;
    state ^= state << 5;
    return state;
}

// Random float in [0, 1) from RNG state.
__device__ __forceinline__ float rng_float(unsigned int& state) {
    return rng_next(state) * 2.3283064365386963e-10f; // 1/2^32
}

// Construct an orthonormal basis from a normal vector (Frisvad's method).
__device__ __forceinline__ void make_orthonormal_basis(
    const float3& n, float3& t, float3& b)
{
    if (n.z < -0.9999999f) {
        t = make_float3(0.0f, -1.0f, 0.0f);
        b = make_float3(-1.0f, 0.0f, 0.0f);
    } else {
        float a = 1.0f / (1.0f + n.z);
        float d = -n.x * n.y * a;
        t = make_float3(1.0f - n.x * n.x * a, d, -n.x);
        b = make_float3(d, 1.0f - n.y * n.y * a, -n.y);
    }
}

// Cosine-weighted hemisphere sampling.
__device__ __forceinline__ float3 cosine_hemisphere_sample(
    const float3& n, unsigned int& seed)
{
    float u1 = rng_float(seed);
    float u2 = rng_float(seed);
    float r = sqrtf(u1);
    float phi = 2.0f * M_PI * u2;
    float x = r * cosf(phi);
    float y = r * sinf(phi);
    float z = sqrtf(fmaxf(0.0f, 1.0f - u1));

    float3 t, b;
    make_orthonormal_basis(n, t, b);
    return normalize(x * t + y * b + z * n);
}

// Clamp a float to [0, 1].
__device__ __forceinline__ float saturate(float x) {
    return fminf(1.0f, fmaxf(0.0f, x));
}
