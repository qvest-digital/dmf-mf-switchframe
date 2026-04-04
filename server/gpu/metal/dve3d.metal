// ============================================================================
// dve3d.metal — 3D DVE compute shader (plane + cube geometry)
//
// For each output pixel, ray-casts from the camera through the pixel into the
// 3D scene, intersects with the geometry (plane or cube), samples the video
// texture at the intersection UV, applies optional lighting, and composites
// onto the destination.
// ============================================================================

#include <metal_stdlib>
using namespace metal;

struct DVE3DParams {
    uint dstW, dstH, dstPitch;
    uint srcW, srcH, srcPitch;

    // Inverse MVP matrix (flat array, row-major, matches C struct exactly)
    float invMVP[16];

    // Output placement (where to composite in the destination frame)
    int slotMinX, slotMinY, slotMaxX, slotMaxY;
    int alpha256;

    // Geometry type: 0=plane, 1=cube, 2=page_turn
    int shape;

    // Lighting
    int lightEnabled;
    float lightX, lightY, lightZ;
    float ambient;
    float specular;

    // Geometry
    float srcAspect;    // source width/height (e.g., 1.778 for 16:9)

    // Reflection
    int reflectionEnabled;
    float reflectionOpacity;
    float reflectionFade;

    // Page turn (shape=2)
    float curlProgress; // 0=flat, 1=fully turned
    float curlRadius;   // cylinder radius for curl
};

struct DVE3DUVParams {
    uint dstW, dstChromaH, dstPitch;
    uint srcW, srcChromaH, srcPitch;

    float invMVP[16];

    int slotMinCX, slotMinCY, slotMaxCX, slotMaxCY;
    int alpha256;

    int shape;

    int lightEnabled;
    float lightX, lightY, lightZ;
    float ambient;
    float specular;

    float srcAspect;

    int reflectionEnabled;
    float reflectionOpacity;
    float reflectionFade;

    // Page turn (shape=2)
    float curlProgress;
    float curlRadius;
};

// --- Helpers ---

inline float4 mat4x4_mul(const constant float* m, float4 v) {
    return float4(
        m[0]*v.x + m[1]*v.y + m[2]*v.z + m[3]*v.w,
        m[4]*v.x + m[5]*v.y + m[6]*v.z + m[7]*v.w,
        m[8]*v.x + m[9]*v.y + m[10]*v.z + m[11]*v.w,
        m[12]*v.x + m[13]*v.y + m[14]*v.z + m[15]*v.w
    );
}

// Ray-plane intersection: plane at z=0, normal=(0,0,1)
// Returns (hit, u, v, faceNormal_z_sign) where u,v ∈ [0,1]
struct HitResult {
    bool hit;
    float u, v;
    float3 normal;
    float dist;
};

inline HitResult intersectPlane(float3 rayOrigin, float3 rayDir, float aspect) {
    HitResult r;
    r.hit = false;
    r.normal = float3(0, 0, 1);

    // Plane at z=0, sized to match source aspect ratio
    float halfW = aspect * 0.5f;
    float halfH = 0.5f;

    if (abs(rayDir.z) < 1e-8f) return r;
    float t = -rayOrigin.z / rayDir.z;
    if (t < 0) return r;

    float3 p = rayOrigin + rayDir * t;
    if (p.x < -halfW || p.x > halfW || p.y < -halfH || p.y > halfH) return r;

    r.hit = true;
    r.u = (p.x + halfW) / (halfW * 2.0f);  // normalize to [0,1]
    r.v = (halfH - p.y) / (halfH * 2.0f);  // flip Y
    r.dist = t;
    return r;
}

// Ray-cube intersection: aspect-correct cube [-halfW, halfW] × [-0.5, 0.5] × [-0.5, 0.5]
// Front/back faces are wider to match video aspect ratio.
inline HitResult intersectCube(float3 rayOrigin, float3 rayDir, float aspect) {
    HitResult best;
    best.hit = false;
    best.dist = 1e20f;

    float halfW = aspect * 0.5f;
    float halfH = 0.5f;
    float halfD = 0.5f;

    // 6 faces: front(+Z), back(-Z), right(+X), left(-X), top(+Y), bottom(-Y)
    float3 normals[6] = {
        float3(0, 0, 1), float3(0, 0, -1),
        float3(1, 0, 0), float3(-1, 0, 0),
        float3(0, 1, 0), float3(0, -1, 0),
    };
    float faceOffsets[6] = { halfD, halfD, halfW, halfW, halfH, halfH };

    for (int face = 0; face < 6; face++) {
        float3 n = normals[face];
        float denom = dot(n, rayDir);
        if (abs(denom) < 1e-8f) continue;

        float t = (faceOffsets[face] - dot(n, rayOrigin)) / denom;
        if (t < 0 || t >= best.dist) continue;

        float3 p = rayOrigin + rayDir * t;

        float uc, vc;
        bool inside = false;

        if (face == 0) { // front (+Z)
            inside = (p.x >= -halfW && p.x <= halfW && p.y >= -halfH && p.y <= halfH);
            uc = (p.x + halfW) / (2.0f * halfW);
            vc = (halfH - p.y) / (2.0f * halfH);
        } else if (face == 1) { // back (-Z)
            inside = (p.x >= -halfW && p.x <= halfW && p.y >= -halfH && p.y <= halfH);
            uc = (halfW - p.x) / (2.0f * halfW);
            vc = (halfH - p.y) / (2.0f * halfH);
        } else if (face == 2) { // right (+X)
            inside = (p.z >= -halfD && p.z <= halfD && p.y >= -halfH && p.y <= halfH);
            uc = (halfD - p.z) / (2.0f * halfD);
            vc = (halfH - p.y) / (2.0f * halfH);
        } else if (face == 3) { // left (-X)
            inside = (p.z >= -halfD && p.z <= halfD && p.y >= -halfH && p.y <= halfH);
            uc = (p.z + halfD) / (2.0f * halfD);
            vc = (halfH - p.y) / (2.0f * halfH);
        } else if (face == 4) { // top (+Y)
            inside = (p.x >= -halfW && p.x <= halfW && p.z >= -halfD && p.z <= halfD);
            uc = (p.x + halfW) / (2.0f * halfW);
            vc = (p.z + halfD) / (2.0f * halfD);
        } else { // bottom (-Y)
            inside = (p.x >= -halfW && p.x <= halfW && p.z >= -halfD && p.z <= halfD);
            uc = (p.x + halfW) / (2.0f * halfW);
            vc = (halfD - p.z) / (2.0f * halfD);
        }

        if (inside) {
            best.hit = true;
            best.u = uc;
            best.v = vc;
            best.normal = n;
            best.dist = t;
        }
    }
    return best;
}

inline float bilinearY(device const uint8_t* src, float u, float v,
                        uint srcW, uint srcH, uint srcPitch) {
    float sx = u * float(srcW - 1);
    float sy = v * float(srcH - 1);
    sx = clamp(sx, 0.0f, float(srcW - 1));
    sy = clamp(sy, 0.0f, float(srcH - 1));

    int ix = int(sx);
    int iy = int(sy);
    float fx = sx - float(ix);
    float fy = sy - float(iy);

    int ix1 = min(ix + 1, int(srcW - 1));
    int iy1 = min(iy + 1, int(srcH - 1));

    float v00 = float(src[iy * srcPitch + ix]);
    float v10 = float(src[iy * srcPitch + ix1]);
    float v01 = float(src[iy1 * srcPitch + ix]);
    float v11 = float(src[iy1 * srcPitch + ix1]);

    float top = v00 + (v10 - v00) * fx;
    float bot = v01 + (v11 - v01) * fx;
    return top + (bot - top) * fy;
}

inline float computeLighting(float3 normal, float3 lightDir, float ambient, float specular) {
    float diffuse = max(dot(normalize(normal), normalize(lightDir)), 0.0f);
    float spec = 0.0f;
    if (specular > 0.0f) {
        float3 viewDir = float3(0, 0, 1); // camera looks down -Z, so view = +Z in object space
        float3 halfDir = normalize(lightDir + viewDir);
        spec = pow(max(dot(normalize(normal), halfDir), 0.0f), 32.0f) * specular;
    }
    return clamp(ambient + diffuse * (1.0f - ambient) + spec, 0.0f, 1.5f);
}

// --- Y Plane Kernel ---

kernel void dve3d_composite_y(
    device uint8_t* dst           [[buffer(0)]],
    device const uint8_t* src     [[buffer(1)]],
    constant DVE3DParams& params  [[buffer(2)]],
    uint2 gid [[thread_position_in_grid]])
{
    if (gid.x >= params.dstW || gid.y >= params.dstH) return;

    // AABB early-out
    int px = int(gid.x);
    int py = int(gid.y);
    if (px < params.slotMinX || px > params.slotMaxX ||
        py < params.slotMinY || py > params.slotMaxY) return;

    // Convert pixel to NDC [-1, 1]
    float ndcX = (float(px - params.slotMinX) / float(max(params.slotMaxX - params.slotMinX, 1))) * 2.0f - 1.0f;
    float ndcY = 1.0f - (float(py - params.slotMinY) / float(max(params.slotMaxY - params.slotMinY, 1))) * 2.0f;

    // Unproject: NDC → world space ray
    float4 nearPoint = mat4x4_mul(params.invMVP, float4(ndcX, ndcY, -1.0f, 1.0f));
    float4 farPoint = mat4x4_mul(params.invMVP, float4(ndcX, ndcY, 1.0f, 1.0f));

    if (abs(nearPoint.w) < 1e-8f || abs(farPoint.w) < 1e-8f) return;

    float3 near3 = nearPoint.xyz / nearPoint.w;
    float3 far3 = farPoint.xyz / farPoint.w;
    float3 rayDir = normalize(far3 - near3);

    // Intersect geometry
    HitResult hit;
    if (params.shape == 1) {
        hit = intersectCube(near3, rayDir, params.srcAspect);
    } else if (params.shape == 2) {
        // Page turn: use plane intersection then apply curl deformation to UV
        hit = intersectPlane(near3, rayDir, params.srcAspect);
    } else {
        hit = intersectPlane(near3, rayDir, params.srcAspect);
    }

    if (!hit.hit) return;

    // Page turn curl deformation (shape=2)
    float pageTurnLightScale = 1.0f;
    if (params.shape == 2 && params.curlRadius > 0.001f) {
        // curlX is the boundary between flat and curled portions, moving from right (1) to left (0)
        float curlX = 1.0f - params.curlProgress;
        if (hit.u > curlX) {
            float angle = (hit.u - curlX) / params.curlRadius;
            // Curl the page: map flat u to cylinder surface
            hit.u = curlX + params.curlRadius * sin(angle);
            // Depth offset for shadow effect
            float depth = params.curlRadius * (1.0f - cos(angle));
            // Darken the curled portion for visual depth
            pageTurnLightScale = 1.0f - depth * 0.3f;
            // Clamp u to valid range
            hit.u = clamp(hit.u, 0.0f, 1.0f);
        }
    }

    // Sample video at UV
    float sampledY = bilinearY(src, hit.u, hit.v, params.srcW, params.srcH, params.srcPitch);

    // Apply lighting
    float lightScale = pageTurnLightScale;
    if (params.lightEnabled) {
        float3 lightDir = normalize(float3(params.lightX, params.lightY, params.lightZ));
        float light = computeLighting(hit.normal, lightDir, params.ambient, params.specular);
        lightScale *= light;
    }
    if (lightScale != 1.0f) {
        // Apply combined light scale to luma: scale around BT.709 mid-gray
        sampledY = 16.0f + (sampledY - 16.0f) * lightScale;
        sampledY = clamp(sampledY, 16.0f, 235.0f);
    }

    // Composite with alpha
    uint dstIdx = gid.y * params.dstPitch + gid.x;
    int existing = int(dst[dstIdx]);
    int val = int(sampledY + 0.5f);
    int a = params.alpha256;
    int inv = 256 - a;
    dst[dstIdx] = uint8_t(clamp((existing * inv + val * a + 128) >> 8, 0, 255));
}

// --- UV Plane Kernel ---

kernel void dve3d_composite_uv(
    device uint8_t* dstUV         [[buffer(0)]],
    device const uint8_t* srcUV   [[buffer(1)]],
    constant DVE3DUVParams& params [[buffer(2)]],
    uint2 gid [[thread_position_in_grid]])
{
    uint cx = gid.x;
    uint cy = gid.y;
    uint chromaW = params.dstW / 2;
    if (cx >= chromaW || cy >= params.dstChromaH) return;

    // AABB check in chroma coordinates
    if (int(cx) < params.slotMinCX || int(cx) > params.slotMaxCX ||
        int(cy) < params.slotMinCY || int(cy) > params.slotMaxCY) return;

    // Map chroma pixel to luma coordinates (center of 2x2 block)
    int lumaX = int(cx) * 2;
    int lumaY = int(cy) * 2;

    // Use center of 2x2 block for ray computation
    float ndcX = (float(lumaX + 1 - params.slotMinCX * 2) / float(max(params.slotMaxCX * 2 - params.slotMinCX * 2, 1))) * 2.0f - 1.0f;
    float ndcY = 1.0f - (float(lumaY + 1 - params.slotMinCY * 2) / float(max(params.slotMaxCY * 2 - params.slotMinCY * 2, 1))) * 2.0f;

    // Unproject
    float4 nearPoint = mat4x4_mul(params.invMVP, float4(ndcX, ndcY, -1.0f, 1.0f));
    float4 farPoint = mat4x4_mul(params.invMVP, float4(ndcX, ndcY, 1.0f, 1.0f));

    if (abs(nearPoint.w) < 1e-8f || abs(farPoint.w) < 1e-8f) return;

    float3 near3 = nearPoint.xyz / nearPoint.w;
    float3 far3 = farPoint.xyz / farPoint.w;
    float3 rayDir = normalize(far3 - near3);

    HitResult hit;
    if (params.shape == 1) {
        hit = intersectCube(near3, rayDir, params.srcAspect);
    } else if (params.shape == 2) {
        hit = intersectPlane(near3, rayDir, params.srcAspect);
    } else {
        hit = intersectPlane(near3, rayDir, params.srcAspect);
    }

    if (!hit.hit) return;

    // Page turn curl deformation (shape=2)
    if (params.shape == 2 && params.curlRadius > 0.001f) {
        float curlX = 1.0f - params.curlProgress;
        if (hit.u > curlX) {
            float angle = (hit.u - curlX) / params.curlRadius;
            hit.u = curlX + params.curlRadius * sin(angle);
            hit.u = clamp(hit.u, 0.0f, 1.0f);
        }
    }

    // Sample chroma at UV
    uint srcChromaW = params.srcW / 2;
    float srcCX = hit.u * float(srcChromaW - 1);
    float srcCY = hit.v * float(params.srcChromaH - 1);
    srcCX = clamp(srcCX, 0.0f, float(srcChromaW - 1));
    srcCY = clamp(srcCY, 0.0f, float(params.srcChromaH - 1));

    int ix = int(srcCX);
    int iy = int(srcCY);
    float fx = srcCX - float(ix);
    float fy = srcCY - float(iy);
    int ix1 = min(ix + 1, int(srcChromaW - 1));
    int iy1 = min(iy + 1, int(params.srcChromaH - 1));

    // Bilinear sample Cb and Cr (interleaved NV12)
    auto sampleUV = [&](int channel) -> float {
        float v00 = float(srcUV[iy * params.srcPitch + ix * 2 + channel]);
        float v10 = float(srcUV[iy * params.srcPitch + ix1 * 2 + channel]);
        float v01 = float(srcUV[iy1 * params.srcPitch + ix * 2 + channel]);
        float v11 = float(srcUV[iy1 * params.srcPitch + ix1 * 2 + channel]);
        float top = v00 + (v10 - v00) * fx;
        float bot = v01 + (v11 - v01) * fx;
        return top + (bot - top) * fy;
    };

    float sampledCb = sampleUV(0);
    float sampledCr = sampleUV(1);

    // Composite
    uint dstIdx = cy * params.dstPitch + cx * 2;
    int a = params.alpha256;
    int inv = 256 - a;

    int existCb = int(dstUV[dstIdx]);
    int existCr = int(dstUV[dstIdx + 1]);
    dstUV[dstIdx]     = uint8_t(clamp((existCb * inv + int(sampledCb + 0.5f) * a + 128) >> 8, 0, 255));
    dstUV[dstIdx + 1] = uint8_t(clamp((existCr * inv + int(sampledCr + 0.5f) * a + 128) >> 8, 0, 255));
}
