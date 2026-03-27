// closest_hit.cu — PBR closest-hit shader for Switchframe OptiX renderer.
// Implements Cook-Torrance microfacet BRDF with direct lighting.
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
    unsigned int ri = min((unsigned int)(saturate(r) * 1023.0f + 0.5f), 1023u);
    unsigned int gi = min((unsigned int)(saturate(g) * 1023.0f + 0.5f), 1023u);
    unsigned int bi = min((unsigned int)(saturate(b) * 1023.0f + 0.5f), 1023u);
    return ri | (gi << 10) | (bi << 20);
}

// ---------- BRDF functions ----------

// GGX (Trowbridge-Reitz) normal distribution function.
__device__ __forceinline__ float D_GGX(float NdotH, float roughness) {
    float a = roughness * roughness;
    float a2 = a * a;
    float denom = NdotH * NdotH * (a2 - 1.0f) + 1.0f;
    return a2 / fmaxf(M_PI * denom * denom, 1e-7f);
}

// Smith geometry function (combined G1 terms for view and light).
__device__ __forceinline__ float G_Smith(float NdotV, float NdotL, float roughness) {
    float r = roughness + 1.0f;
    float k = (r * r) / 8.0f;
    float G1V = NdotV / (NdotV * (1.0f - k) + k);
    float G1L = NdotL / (NdotL * (1.0f - k) + k);
    return G1V * G1L;
}

// Fresnel-Schlick approximation.
__device__ __forceinline__ float3 F_Schlick(float cosTheta, const float3& F0) {
    float t = 1.0f - cosTheta;
    float t2 = t * t;
    float t5 = t2 * t2 * t;
    return F0 + (make_float3(1.0f, 1.0f, 1.0f) - F0) * t5;
}

// ---------- Shadow test ----------

__device__ __forceinline__ bool is_shadowed(
    const float3& origin, const float3& direction, float max_dist)
{
    unsigned int occluded = 0;
    optixTrace(
        params.traversable,
        origin,
        direction,
        0.001f,
        max_dist - 0.001f,
        0.0f,
        OptixVisibilityMask(0xFF),
        OPTIX_RAY_FLAG_TERMINATE_ON_FIRST_HIT | OPTIX_RAY_FLAG_DISABLE_CLOSESTHIT,
        0,                  // SBT offset
        1,                  // SBT stride
        1,                  // miss SBT index (shadow miss)
        occluded);
    return occluded != 0;
}

// ---------- Closest-hit program ----------

extern "C" __global__ void __closesthit__pbr() {
    // Get hit group data from SBT.
    const HitGroupData* data = (const HitGroupData*)optixGetSbtDataPointer();

    // Triangle barycentric coordinates.
    const float2 bary = optixGetTriangleBarycentrics();
    const float b0 = 1.0f - bary.x - bary.y;
    const float b1 = bary.x;
    const float b2 = bary.y;

    // Primitive index.
    const int prim_idx = optixGetPrimitiveIndex();
    const uint3 tri = data->indices[prim_idx];

    // Interpolate position.
    const float3 p0v = data->vertices[tri.x];
    const float3 p1v = data->vertices[tri.y];
    const float3 p2v = data->vertices[tri.z];
    float3 hit_pos = make_float3(
        b0 * p0v.x + b1 * p1v.x + b2 * p2v.x,
        b0 * p0v.y + b1 * p1v.y + b2 * p2v.y,
        b0 * p0v.z + b1 * p1v.z + b2 * p2v.z);

    // Interpolate normal.
    float3 N;
    if (data->normals) {
        const float3 n0 = data->normals[tri.x];
        const float3 n1 = data->normals[tri.y];
        const float3 n2 = data->normals[tri.z];
        N = normalize(make_float3(
            b0 * n0.x + b1 * n1.x + b2 * n2.x,
            b0 * n0.y + b1 * n1.y + b2 * n2.y,
            b0 * n0.z + b1 * n1.z + b2 * n2.z));
    } else {
        // Flat shading: compute face normal from triangle edges.
        float3 e1 = p1v - p0v;
        float3 e2 = p2v - p0v;
        N = normalize(cross(e1, e2));
    }

    // Transform to world space.
    float3 world_pos = optixTransformPointFromObjectToWorldSpace(hit_pos);
    float3 world_normal = normalize(optixTransformNormalFromObjectToWorldSpace(N));

    // View direction (from hit point to camera).
    float3 V = normalize(params.camera_pos - world_pos);

    // Material properties.
    const Material& mat = data->material;
    float3 albedo = mat.base_color;
    float metallic = mat.metallic;
    float roughness = fmaxf(mat.roughness, 0.04f); // clamp to avoid singularity

    // Fresnel reflectance at normal incidence (F0).
    float3 F0 = make_float3(0.04f, 0.04f, 0.04f);
    F0 = F0 + (albedo - F0) * metallic; // lerp toward albedo for metals

    // Accumulate direct lighting.
    float3 Lo = make_float3(0.0f, 0.0f, 0.0f);

    if (params.num_lights > 0 && params.lights != nullptr) {
        // Use scene lights from the light buffer.
        for (int i = 0; i < params.num_lights; i++) {
            const Light& light = params.lights[i];
            float3 L;
            float attenuation = light.intensity;

            if (light.type == 1) {
                // Directional light.
                L = normalize(make_float3(-light.direction.x, -light.direction.y, -light.direction.z));
            } else {
                // Point/area light.
                float3 to_light = light.position - world_pos;
                float dist2 = dot(to_light, to_light);
                L = to_light / sqrtf(fmaxf(dist2, 1e-6f));
                attenuation *= 1.0f / fmaxf(dist2, 1e-6f);
            }

            float3 radiance = light.color * attenuation;
            float NdotL = fmaxf(dot(world_normal, L), 0.0f);
            if (NdotL <= 0.0f) continue;

            // Half vector.
            float3 H = normalize(V + L);
            float NdotH = fmaxf(dot(world_normal, H), 0.0f);
            float NdotV = fmaxf(dot(world_normal, V), 0.001f);
            float HdotV = fmaxf(dot(H, V), 0.0f);

            // Cook-Torrance specular BRDF.
            float D = D_GGX(NdotH, roughness);
            float G = G_Smith(NdotV, NdotL, roughness);
            float3 F = F_Schlick(HdotV, F0);

            float3 numerator = F * (D * G);
            float denominator = 4.0f * NdotV * NdotL + 0.0001f;
            float3 specular = numerator / denominator;

            // Diffuse (Lambert) — metals have no diffuse.
            float3 kD = (make_float3(1.0f, 1.0f, 1.0f) - F) * (1.0f - metallic);
            float3 diffuse = kD * albedo / M_PI;

            Lo = Lo + (diffuse + specular) * radiance * NdotL;
        }
    } else {
        // Fallback: hardcoded key + fill lights for when no scene lights are set.
        // Key light from upper-right.
        float3 key_dir = normalize(make_float3(1.0f, 2.0f, 1.0f));
        float key_NdotL = fmaxf(dot(world_normal, key_dir), 0.0f);
        if (key_NdotL > 0.0f) {
            float3 H = normalize(V + key_dir);
            float NdotH = fmaxf(dot(world_normal, H), 0.0f);
            float NdotV = fmaxf(dot(world_normal, V), 0.001f);
            float HdotV = fmaxf(dot(H, V), 0.0f);
            float D = D_GGX(NdotH, roughness);
            float G = G_Smith(NdotV, key_NdotL, roughness);
            float3 F = F_Schlick(HdotV, F0);
            float3 spec = F * (D * G) / (4.0f * NdotV * key_NdotL + 0.0001f);
            float3 kD = (make_float3(1.0f, 1.0f, 1.0f) - F) * (1.0f - metallic);
            Lo = Lo + (kD * albedo / M_PI + spec) * make_float3(1.0f, 0.95f, 0.9f) * key_NdotL;
        }
        // Fill light from lower-left.
        float3 fill_dir = normalize(make_float3(-1.0f, 0.5f, -0.5f));
        float fill_NdotL = fmaxf(dot(world_normal, fill_dir), 0.0f);
        Lo = Lo + albedo * make_float3(0.3f, 0.3f, 0.35f) * fill_NdotL;
    }

    // Ambient term.
    float3 ambient = make_float3(0.03f, 0.03f, 0.03f) * albedo;

    // Emissive.
    float3 emissive = mat.emission;

    // Final color.
    float3 color = ambient + Lo + emissive;

    // Set payload: packed RGB + alpha.
    optixSetPayload_0(pack_rgb(color.x, color.y, color.z));
    optixSetPayload_1(__float_as_uint(1.0f)); // fully opaque hit
}
