// ============================================================================
// dve3d.cu — 3D DVE CUDA kernel (plane + cube geometry)
// Port of dve3d.metal for NVIDIA GPUs
// ============================================================================

#include "common.cuh"
#include <math.h>

struct DVE3DParams {
    int dstW, dstH, dstPitch;
    int srcW, srcH, srcPitch;
    float invMVP[16];  // row-major 4x4 inverse MVP matrix
    int slotMinX, slotMinY, slotMaxX, slotMaxY;
    int alpha256;
    int shape;         // 0=plane, 1=cube, 2=page_turn
    int lightEnabled;
    float lightX, lightY, lightZ;
    float ambient, specular;
    float srcAspect;
    int reflectionEnabled;
    float reflectionOpacity, reflectionFade;
    // Page turn (shape=2)
    float curlProgress;
    float curlRadius;
};

struct DVE3DUVParams {
    int dstW, dstChromaH, dstPitch;
    int srcW, srcChromaH, srcPitch;
    float invMVP[16];
    int slotMinCX, slotMinCY, slotMaxCX, slotMaxCY;
    int alpha256;
    int shape;
    int lightEnabled;
    float lightX, lightY, lightZ;
    float ambient, specular;
    float srcAspect;
    int reflectionEnabled;
    float reflectionOpacity, reflectionFade;
    // Page turn (shape=2)
    float curlProgress;
    float curlRadius;
};

// --- Helpers ---

struct float3_t { float x, y, z; };
struct float4_t { float x, y, z, w; };

__device__ float4_t mat4_mul(const float* m, float4_t v) {
    return {
        m[0]*v.x + m[1]*v.y + m[2]*v.z + m[3]*v.w,
        m[4]*v.x + m[5]*v.y + m[6]*v.z + m[7]*v.w,
        m[8]*v.x + m[9]*v.y + m[10]*v.z + m[11]*v.w,
        m[12]*v.x + m[13]*v.y + m[14]*v.z + m[15]*v.w,
    };
}

__device__ float dot3(float3_t a, float3_t b) { return a.x*b.x + a.y*b.y + a.z*b.z; }
__device__ float len3(float3_t v) { return sqrtf(v.x*v.x + v.y*v.y + v.z*v.z); }
__device__ float3_t norm3(float3_t v) {
    float l = len3(v);
    if (l < 1e-8f) return {0, 0, 1};
    return {v.x/l, v.y/l, v.z/l};
}

struct HitResult {
    bool hit;
    float u, v;
    float3_t normal;
    float dist;
};

__device__ HitResult intersectPlane(float3_t ro, float3_t rd, float aspect) {
    HitResult r;
    r.hit = false;
    r.normal = {0, 0, 1};
    float halfW = aspect * 0.5f;
    float halfH = 0.5f;
    if (fabsf(rd.z) < 1e-8f) return r;
    float t = -ro.z / rd.z;
    if (t < 0) return r;
    float px = ro.x + rd.x * t;
    float py = ro.y + rd.y * t;
    if (px < -halfW || px > halfW || py < -halfH || py > halfH) return r;
    r.hit = true;
    r.u = (px + halfW) / (2.0f * halfW);
    r.v = (halfH - py) / (2.0f * halfH);
    r.dist = t;
    return r;
}

__device__ HitResult intersectCube(float3_t ro, float3_t rd, float aspect) {
    HitResult best;
    best.hit = false;
    best.dist = 1e20f;

    float halfW = aspect * 0.5f;
    float halfH = 0.5f;
    float halfD = 0.5f;

    float3_t normals[6] = {{0,0,1},{0,0,-1},{1,0,0},{-1,0,0},{0,1,0},{0,-1,0}};
    float offsets[6] = {halfD, halfD, halfW, halfW, halfH, halfH};

    for (int face = 0; face < 6; face++) {
        float3_t n = normals[face];
        float denom = dot3(n, rd);
        if (fabsf(denom) < 1e-8f) continue;
        float t = (offsets[face] - dot3(n, ro)) / denom;
        if (t < 0 || t >= best.dist) continue;

        float3_t p = {ro.x+rd.x*t, ro.y+rd.y*t, ro.z+rd.z*t};
        float uc, vc;
        bool inside = false;

        if (face==0) { inside=(p.x>=-halfW&&p.x<=halfW&&p.y>=-halfH&&p.y<=halfH); uc=(p.x+halfW)/(2*halfW); vc=(halfH-p.y)/(2*halfH); }
        else if (face==1) { inside=(p.x>=-halfW&&p.x<=halfW&&p.y>=-halfH&&p.y<=halfH); uc=(halfW-p.x)/(2*halfW); vc=(halfH-p.y)/(2*halfH); }
        else if (face==2) { inside=(p.z>=-halfD&&p.z<=halfD&&p.y>=-halfH&&p.y<=halfH); uc=(halfD-p.z)/(2*halfD); vc=(halfH-p.y)/(2*halfH); }
        else if (face==3) { inside=(p.z>=-halfD&&p.z<=halfD&&p.y>=-halfH&&p.y<=halfH); uc=(p.z+halfD)/(2*halfD); vc=(halfH-p.y)/(2*halfH); }
        else if (face==4) { inside=(p.x>=-halfW&&p.x<=halfW&&p.z>=-halfD&&p.z<=halfD); uc=(p.x+halfW)/(2*halfW); vc=(p.z+halfD)/(2*halfD); }
        else { inside=(p.x>=-halfW&&p.x<=halfW&&p.z>=-halfD&&p.z<=halfD); uc=(p.x+halfW)/(2*halfW); vc=(halfD-p.z)/(2*halfD); }

        if (inside) { best.hit=true; best.u=uc; best.v=vc; best.normal=n; best.dist=t; }
    }
    return best;
}

__device__ float bilinearY(const uint8_t* src, float u, float v, int w, int h, int pitch) {
    float sx = fminf(fmaxf(u * (w - 1), 0.0f), (float)(w - 1));
    float sy = fminf(fmaxf(v * (h - 1), 0.0f), (float)(h - 1));
    int ix = (int)sx, iy = (int)sy;
    float fx = sx - ix, fy = sy - iy;
    int ix1 = min(ix+1, w-1), iy1 = min(iy+1, h-1);
    float v00 = src[iy*pitch+ix], v10 = src[iy*pitch+ix1];
    float v01 = src[iy1*pitch+ix], v11 = src[iy1*pitch+ix1];
    float top = v00 + (v10-v00)*fx, bot = v01 + (v11-v01)*fx;
    return top + (bot-top)*fy;
}

__device__ float computeLighting(float3_t normal, float3_t lightDir, float ambient, float specular) {
    float3_t n = norm3(normal);
    float3_t l = norm3(lightDir);
    float diff = fmaxf(dot3(n, l), 0.0f);
    float spec = 0.0f;
    if (specular > 0.0f) {
        float3_t v = {0, 0, 1};
        float3_t h = norm3({l.x+v.x, l.y+v.y, l.z+v.z});
        spec = powf(fmaxf(dot3(n, h), 0.0f), 32.0f) * specular;
    }
    return fminf(fmaxf(ambient + diff * (1.0f - ambient) + spec, 0.0f), 1.5f);
}

// --- Y Kernel ---

__global__ void dve3d_composite_y_kernel(uint8_t* dst, const uint8_t* src, DVE3DParams p) {
    int ox = blockIdx.x * blockDim.x + threadIdx.x;
    int oy = blockIdx.y * blockDim.y + threadIdx.y;
    if (ox >= p.dstW || oy >= p.dstH) return;
    if (ox < p.slotMinX || ox > p.slotMaxX || oy < p.slotMinY || oy > p.slotMaxY) return;

    float ndcX = (float(ox - p.slotMinX) / fmaxf(float(p.slotMaxX - p.slotMinX), 1.0f)) * 2.0f - 1.0f;
    float ndcY = 1.0f - (float(oy - p.slotMinY) / fmaxf(float(p.slotMaxY - p.slotMinY), 1.0f)) * 2.0f;

    float4_t nearPt = mat4_mul(p.invMVP, {ndcX, ndcY, -1.0f, 1.0f});
    float4_t farPt = mat4_mul(p.invMVP, {ndcX, ndcY, 1.0f, 1.0f});
    if (fabsf(nearPt.w) < 1e-8f || fabsf(farPt.w) < 1e-8f) return;

    float3_t near3 = {nearPt.x/nearPt.w, nearPt.y/nearPt.w, nearPt.z/nearPt.w};
    float3_t far3 = {farPt.x/farPt.w, farPt.y/farPt.w, farPt.z/farPt.w};
    float3_t rayDir = norm3({far3.x-near3.x, far3.y-near3.y, far3.z-near3.z});

    HitResult hit;
    if (p.shape == 1) hit = intersectCube(near3, rayDir, p.srcAspect);
    else if (p.shape == 2) hit = intersectPlane(near3, rayDir, p.srcAspect);
    else hit = intersectPlane(near3, rayDir, p.srcAspect);
    if (!hit.hit) return;

    // Page turn curl deformation (shape=2)
    float pageTurnLightScale = 1.0f;
    if (p.shape == 2 && p.curlRadius > 0.001f) {
        float curlX = 1.0f - p.curlProgress;
        if (hit.u > curlX) {
            float angle = (hit.u - curlX) / p.curlRadius;
            hit.u = curlX + p.curlRadius * sinf(angle);
            float depth = p.curlRadius * (1.0f - cosf(angle));
            pageTurnLightScale = 1.0f - depth * 0.3f;
            hit.u = fminf(fmaxf(hit.u, 0.0f), 1.0f);
        }
    }

    float sampledY = bilinearY(src, hit.u, hit.v, p.srcW, p.srcH, p.srcPitch);

    float lightScale = pageTurnLightScale;
    if (p.lightEnabled) {
        float light = computeLighting(hit.normal, {p.lightX, p.lightY, p.lightZ}, p.ambient, p.specular);
        lightScale *= light;
    }
    if (lightScale != 1.0f) {
        sampledY = 16.0f + (sampledY - 16.0f) * lightScale;
        sampledY = fminf(fmaxf(sampledY, 16.0f), 235.0f);
    }

    int dstIdx = oy * p.dstPitch + ox;
    int existing = dst[dstIdx];
    int val = (int)(sampledY + 0.5f);
    int a = p.alpha256, inv = 256 - a;
    dst[dstIdx] = (uint8_t)min(max((existing*inv + val*a + 128) >> 8, 0), 255);
}

// --- UV Kernel ---

__global__ void dve3d_composite_uv_kernel(uint8_t* dstUV, const uint8_t* srcUV, DVE3DUVParams p) {
    int cx = blockIdx.x * blockDim.x + threadIdx.x;
    int cy = blockIdx.y * blockDim.y + threadIdx.y;
    int chromaW = p.dstW / 2;
    if (cx >= chromaW || cy >= p.dstChromaH) return;
    if (cx < p.slotMinCX || cx > p.slotMaxCX || cy < p.slotMinCY || cy > p.slotMaxCY) return;

    int lumaX = cx * 2;
    int lumaY = cy * 2;
    float ndcX = (float(lumaX + 1 - p.slotMinCX * 2) / fmaxf(float(p.slotMaxCX*2 - p.slotMinCX*2), 1.0f)) * 2.0f - 1.0f;
    float ndcY = 1.0f - (float(lumaY + 1 - p.slotMinCY * 2) / fmaxf(float(p.slotMaxCY*2 - p.slotMinCY*2), 1.0f)) * 2.0f;

    float4_t nearPt = mat4_mul(p.invMVP, {ndcX, ndcY, -1.0f, 1.0f});
    float4_t farPt = mat4_mul(p.invMVP, {ndcX, ndcY, 1.0f, 1.0f});
    if (fabsf(nearPt.w) < 1e-8f || fabsf(farPt.w) < 1e-8f) return;

    float3_t near3 = {nearPt.x/nearPt.w, nearPt.y/nearPt.w, nearPt.z/nearPt.w};
    float3_t far3 = {farPt.x/farPt.w, farPt.y/farPt.w, farPt.z/farPt.w};
    float3_t rayDir = norm3({far3.x-near3.x, far3.y-near3.y, far3.z-near3.z});

    HitResult hit;
    if (p.shape == 1) hit = intersectCube(near3, rayDir, p.srcAspect);
    else if (p.shape == 2) hit = intersectPlane(near3, rayDir, p.srcAspect);
    else hit = intersectPlane(near3, rayDir, p.srcAspect);
    if (!hit.hit) return;

    // Page turn curl deformation (shape=2)
    if (p.shape == 2 && p.curlRadius > 0.001f) {
        float curlX = 1.0f - p.curlProgress;
        if (hit.u > curlX) {
            float angle = (hit.u - curlX) / p.curlRadius;
            hit.u = curlX + p.curlRadius * sinf(angle);
            hit.u = fminf(fmaxf(hit.u, 0.0f), 1.0f);
        }
    }

    int srcCW = p.srcW / 2;
    float scx = fminf(fmaxf(hit.u * (srcCW-1), 0.0f), (float)(srcCW-1));
    float scy = fminf(fmaxf(hit.v * (p.srcChromaH-1), 0.0f), (float)(p.srcChromaH-1));
    int ix = (int)scx, iy = (int)scy;
    float fx = scx-ix, fy = scy-iy;
    int ix1 = min(ix+1, srcCW-1), iy1 = min(iy+1, p.srcChromaH-1);

    // Bilinear Cb
    float cb00 = srcUV[iy*p.srcPitch+ix*2], cb10 = srcUV[iy*p.srcPitch+ix1*2];
    float cb01 = srcUV[iy1*p.srcPitch+ix*2], cb11 = srcUV[iy1*p.srcPitch+ix1*2];
    float cb = cb00+(cb10-cb00)*fx; cb += ((cb01+(cb11-cb01)*fx)-cb)*fy;

    // Bilinear Cr
    float cr00 = srcUV[iy*p.srcPitch+ix*2+1], cr10 = srcUV[iy*p.srcPitch+ix1*2+1];
    float cr01 = srcUV[iy1*p.srcPitch+ix*2+1], cr11 = srcUV[iy1*p.srcPitch+ix1*2+1];
    float cr = cr00+(cr10-cr00)*fx; cr += ((cr01+(cr11-cr01)*fx)-cr)*fy;

    int dstIdx = cy * p.dstPitch + cx * 2;
    int a = p.alpha256, inv = 256 - a;
    dstUV[dstIdx]   = (uint8_t)min(max(((int)dstUV[dstIdx]*inv + (int)(cb+0.5f)*a + 128)>>8, 0), 255);
    dstUV[dstIdx+1] = (uint8_t)min(max(((int)dstUV[dstIdx+1]*inv + (int)(cr+0.5f)*a + 128)>>8, 0), 255);
}

// --- Extern C wrappers ---

extern "C" {

cudaError_t dve3d_composite_y(uint8_t* dst, const uint8_t* src, DVE3DParams params, cudaStream_t stream) {
    dim3 block(BLOCK_DIM_X, BLOCK_DIM_Y);
    dim3 grid((params.dstW + block.x - 1) / block.x, (params.dstH + block.y - 1) / block.y);
    dve3d_composite_y_kernel<<<grid, block, 0, stream>>>(dst, src, params);
    return cudaGetLastError();
}

cudaError_t dve3d_composite_uv(uint8_t* dstUV, const uint8_t* srcUV, DVE3DUVParams params, cudaStream_t stream) {
    int chromaW = params.dstW / 2;
    dim3 block(BLOCK_DIM_X, BLOCK_DIM_Y);
    dim3 grid((chromaW + block.x - 1) / block.x, (params.dstChromaH + block.y - 1) / block.y);
    dve3d_composite_uv_kernel<<<grid, block, 0, stream>>>(dstUV, srcUV, params);
    return cudaGetLastError();
}

} // extern "C"
