#include "common.cuh"

// Motion-compensated frame interpolation using optical flow vectors.
// Flow vectors are int16_t pairs (dx, dy) at 4x4 block granularity
// in NVOFA S10.5 format (5 fractional bits = divide by 32 for pixel displacement).
//
// Three quality improvements over naive bidirectional warp:
//   1. Flow magnitude clamping — prevents wild warps from extreme motion vectors
//   2. Cost-map confidence — NVOFA cost values modulate interpolation vs nearest-frame
//   3. Occlusion-aware blending — high-motion regions bias toward the temporally
//      closer source frame instead of equal blending

// Maximum flow displacement in pixels. Vectors exceeding this are clamped.
// 64px at 1080p30 ≈ 1920px/sec panning speed.
#define MAX_FLOW_MAG 64.0f

// Helper: read flow vector with magnitude clamping
__device__ __forceinline__ void read_flow_clamped(
    const int16_t* __restrict__ flowXY, int flowIdx,
    float* dx, float* dy, float* mag)
{
    float rawDx = (float)flowXY[flowIdx] * 0.03125f;      // S10.5 to pixel
    float rawDy = (float)flowXY[flowIdx + 1] * 0.03125f;
    float m = sqrtf(rawDx * rawDx + rawDy * rawDy);

    // Clamp magnitude to prevent extreme warps
    if (m > MAX_FLOW_MAG) {
        float scale = MAX_FLOW_MAG / m;
        rawDx *= scale;
        rawDy *= scale;
        m = MAX_FLOW_MAG;
    }
    *dx = rawDx;
    *dy = rawDy;
    *mag = m;
}

// Helper: compute occlusion-aware blend weight.
// For high-motion regions, bias toward the temporally closer frame
// to reduce ghosting at motion boundaries.
// Returns adjusted alpha (0=prev, 1=curr).
__device__ __forceinline__ float occlusion_aware_alpha(
    float alpha, float mag, float costVal)
{
    // costVal: 0.0 = high confidence, 1.0 = low confidence (likely occlusion)
    // At low confidence + high motion, snap toward the closer frame
    if (costVal > 0.3f && mag > 4.0f) {
        // Sharpen the alpha curve: push toward 0 or 1
        float sharpen = fminf(costVal * (mag / 16.0f), 0.8f);
        if (alpha < 0.5f) {
            alpha = fmaxf(0.0f, alpha - sharpen * alpha);
        } else {
            alpha = fminf(1.0f, alpha + sharpen * (1.0f - alpha));
        }
    }
    return alpha;
}

// Helper: bilinear sample from a Y plane
__device__ __forceinline__ float bilinear_y(
    const uint8_t* __restrict__ plane, int pitch,
    float fx, float fy, int width, int height)
{
    int x0 = (int)fx, y0 = (int)fy;
    int x1 = min(x0 + 1, width - 1), y1 = min(y0 + 1, height - 1);
    float wx = fx - x0, wy = fy - y0;
    return (plane[y0 * pitch + x0] * (1-wx) + plane[y0 * pitch + x1] * wx) * (1-wy)
         + (plane[y1 * pitch + x0] * (1-wx) + plane[y1 * pitch + x1] * wx) * wy;
}

// Y plane interpolation with cost map + magnitude clamping + occlusion-aware blend
__global__ void fruc_interpolate_y_kernel(
    uint8_t* __restrict__ dst, int dstPitch,
    const uint8_t* __restrict__ prev, int prevPitch,
    const uint8_t* __restrict__ curr, int currPitch,
    const int16_t* __restrict__ flowXY,
    const uint32_t* __restrict__ costMap,  // per-block cost (NULL = no cost map)
    int flowStride,
    int width, int height,
    float alpha)
{
    int x = blockIdx.x * blockDim.x + threadIdx.x;
    int y = blockIdx.y * blockDim.y + threadIdx.y;
    if (x >= width || y >= height) return;

    int bx = x / 4;
    int by = y / 4;
    int flowIdx = by * flowStride + bx * 2;

    float dx, dy, mag;
    read_flow_clamped(flowXY, flowIdx, &dx, &dy, &mag);

    // Read cost value (normalized to 0-1 range, NVOFA cost is 0-65535)
    float costVal = 0.0f;
    if (costMap) {
        int costIdx = by * (flowStride / 2) + bx;
        costVal = fminf((float)costMap[costIdx] / 65535.0f, 1.0f);
    }

    // Occlusion-aware alpha adjustment
    float adjAlpha = occlusion_aware_alpha(alpha, mag, costVal);

    // Source positions in prev and curr
    float prevX = fmaxf(0.0f, fminf((float)x - dx * adjAlpha, (float)(width - 1)));
    float prevY = fmaxf(0.0f, fminf((float)y - dy * adjAlpha, (float)(height - 1)));
    float currX = fmaxf(0.0f, fminf((float)x + dx * (1.0f - adjAlpha), (float)(width - 1)));
    float currY = fmaxf(0.0f, fminf((float)y + dy * (1.0f - adjAlpha), (float)(height - 1)));

    float pVal = bilinear_y(prev, prevPitch, prevX, prevY, width, height);
    float cVal = bilinear_y(curr, currPitch, currX, currY, width, height);

    // High-cost regions blend toward nearest frame to reduce artifacts
    float blendAlpha = adjAlpha;
    if (costVal > 0.5f) {
        // For very uncertain flow, lerp toward simple temporal nearest
        float nearestVal = (alpha < 0.5f) ? prev[y * prevPitch + x] : curr[y * currPitch + x];
        float flowVal = pVal * (1.0f - blendAlpha) + cVal * blendAlpha;
        float costBlend = fminf((costVal - 0.5f) * 2.0f, 1.0f);  // 0 at cost=0.5, 1 at cost=1.0
        float result = flowVal * (1.0f - costBlend) + nearestVal * costBlend;
        dst[y * dstPitch + x] = (uint8_t)fminf(fmaxf(result + 0.5f, 0.0f), 255.0f);
        return;
    }

    float result = pVal * (1.0f - blendAlpha) + cVal * blendAlpha;
    dst[y * dstPitch + x] = (uint8_t)fminf(fmaxf(result + 0.5f, 0.0f), 255.0f);
}

// UV plane interpolation with cost map + magnitude clamping + occlusion-aware blend
__global__ void fruc_interpolate_uv_kernel(
    uint8_t* __restrict__ dstUV, int dstPitch,
    const uint8_t* __restrict__ prevUV, int prevPitch,
    const uint8_t* __restrict__ currUV, int currPitch,
    const int16_t* __restrict__ flowXY,
    const uint32_t* __restrict__ costMap,
    int flowStride,
    int chromaW, int chromaH, int lumaW, int lumaH,
    float alpha)
{
    int cx = blockIdx.x * blockDim.x + threadIdx.x;
    int cy = blockIdx.y * blockDim.y + threadIdx.y;
    if (cx >= chromaW || cy >= chromaH) return;

    // Average flow from 2x2 luma blocks
    int lx = cx * 2;
    int ly = cy * 2;
    int bx0 = lx / 4, by0 = ly / 4;
    int bx1 = min(lx + 1, lumaW - 1) / 4, by1 = min(ly + 1, lumaH - 1) / 4;

    float dx = 0, dy = 0, maxMag = 0, maxCost = 0;
    int bxs[2] = {bx0, bx1};
    int bys[2] = {by0, by1};
    for (int j = 0; j < 2; j++) {
        for (int i = 0; i < 2; i++) {
            int idx = bys[j] * flowStride + bxs[i] * 2;
            float bdx, bdy, bmag;
            read_flow_clamped(flowXY, idx, &bdx, &bdy, &bmag);
            dx += bdx;
            dy += bdy;
            if (bmag > maxMag) maxMag = bmag;
            if (costMap) {
                int costIdx = bys[j] * (flowStride / 2) + bxs[i];
                float c = fminf((float)costMap[costIdx] / 65535.0f, 1.0f);
                if (c > maxCost) maxCost = c;
            }
        }
    }
    dx *= 0.25f;
    dy *= 0.25f;

    float cdx = dx * 0.5f;
    float cdy = dy * 0.5f;

    float adjAlpha = occlusion_aware_alpha(alpha, maxMag, maxCost);

    float prevCX = fmaxf(0.0f, fminf((float)cx - cdx * adjAlpha, (float)(chromaW - 1)));
    float prevCY = fmaxf(0.0f, fminf((float)cy - cdy * adjAlpha, (float)(chromaH - 1)));
    float currCX = fmaxf(0.0f, fminf((float)cx + cdx * (1.0f - adjAlpha), (float)(chromaW - 1)));
    float currCY = fmaxf(0.0f, fminf((float)cy + cdy * (1.0f - adjAlpha), (float)(chromaH - 1)));

    int psx = min(max((int)(prevCX + 0.5f), 0), chromaW - 1);
    int psy = min(max((int)(prevCY + 0.5f), 0), chromaH - 1);
    int csx = min(max((int)(currCX + 0.5f), 0), chromaW - 1);
    int csy = min(max((int)(currCY + 0.5f), 0), chromaH - 1);

    int dstIdx = cy * dstPitch + cx * 2;
    float invA = 1.0f - adjAlpha;

    // For high-cost regions, snap to nearest frame
    if (maxCost > 0.5f) {
        float costBlend = fminf((maxCost - 0.5f) * 2.0f, 1.0f);
        int nearIdx = (alpha < 0.5f) ? (cy * prevPitch + cx * 2) : (cy * currPitch + cx * 2);
        const uint8_t* nearUV = (alpha < 0.5f) ? prevUV : currUV;

        float flowU = (float)prevUV[psy * prevPitch + psx * 2] * invA
                    + (float)currUV[csy * currPitch + csx * 2] * adjAlpha;
        float flowV = (float)prevUV[psy * prevPitch + psx * 2 + 1] * invA
                    + (float)currUV[csy * currPitch + csx * 2 + 1] * adjAlpha;

        dstUV[dstIdx]     = (uint8_t)(flowU * (1-costBlend) + nearUV[nearIdx] * costBlend + 0.5f);
        dstUV[dstIdx + 1] = (uint8_t)(flowV * (1-costBlend) + nearUV[nearIdx+1] * costBlend + 0.5f);
        return;
    }

    float pU = (float)prevUV[psy * prevPitch + psx * 2];
    float cU = (float)currUV[csy * currPitch + csx * 2];
    dstUV[dstIdx] = (uint8_t)(pU * invA + cU * adjAlpha + 0.5f);

    float pV = (float)prevUV[psy * prevPitch + psx * 2 + 1];
    float cV = (float)currUV[csy * currPitch + csx * 2 + 1];
    dstUV[dstIdx + 1] = (uint8_t)(pV * invA + cV * adjAlpha + 0.5f);
}

// Adaptive motion-blur Y plane kernel with cost map + clamping + occlusion awareness.
__global__ void fruc_motionblur_y_kernel(
    uint8_t* __restrict__ dst, int dstPitch,
    const uint8_t* __restrict__ prev, int prevPitch,
    const uint8_t* __restrict__ curr, int currPitch,
    const int16_t* __restrict__ flowXY,
    const uint32_t* __restrict__ costMap,
    int flowStride,
    int width, int height,
    float alpha, float shutterAngle)
{
    int x = blockIdx.x * blockDim.x + threadIdx.x;
    int y = blockIdx.y * blockDim.y + threadIdx.y;
    if (x >= width || y >= height) return;

    int bx = x / 4;
    int by = y / 4;
    int flowIdx = by * flowStride + bx * 2;

    float dx, dy, mag;
    read_flow_clamped(flowXY, flowIdx, &dx, &dy, &mag);

    float costVal = 0.0f;
    if (costMap) {
        int costIdx = by * (flowStride / 2) + bx;
        costVal = fminf((float)costMap[costIdx] / 65535.0f, 1.0f);
    }

    // Adaptive sample count
    int N;
    if (mag < 1.0f)       N = 2;
    else if (mag < 4.0f)  N = 4;
    else if (mag < 8.0f)  N = 8;
    else                   N = 16;

    // For high-cost regions, reduce sample count (we'll blend toward nearest anyway)
    if (costVal > 0.5f) N = min(N, 4);

    float halfShutter = shutterAngle * 0.5f;
    float winStart = fmaxf(0.0f, alpha - halfShutter);
    float winEnd   = fminf(1.0f, alpha + halfShutter);

    if (winEnd - winStart < 1e-6f || N <= 1) {
        N = 1;
        winStart = alpha;
        winEnd = alpha;
    }

    float sum = 0.0f;
    float fW = (float)(width - 1);
    float fH = (float)(height - 1);

    for (int s = 0; s < N; s++) {
        float t = (N == 1) ? winStart : winStart + (winEnd - winStart) * (float)s / (float)(N - 1);
        float adjT = occlusion_aware_alpha(t, mag, costVal);

        float prevX = fmaxf(0.0f, fminf((float)x - dx * adjT, fW));
        float prevY = fmaxf(0.0f, fminf((float)y - dy * adjT, fH));
        float currX = fmaxf(0.0f, fminf((float)x + dx * (1.0f - adjT), fW));
        float currY = fmaxf(0.0f, fminf((float)y + dy * (1.0f - adjT), fH));

        float pVal = bilinear_y(prev, prevPitch, prevX, prevY, width, height);
        float cVal = bilinear_y(curr, currPitch, currX, currY, width, height);

        sum += pVal * (1.0f - adjT) + cVal * adjT;
    }

    float flowResult = sum / (float)N;

    // High-cost fallback: blend toward nearest frame
    if (costVal > 0.5f) {
        float nearVal = (alpha < 0.5f) ? (float)prev[y * prevPitch + x] : (float)curr[y * currPitch + x];
        float costBlend = fminf((costVal - 0.5f) * 2.0f, 1.0f);
        flowResult = flowResult * (1.0f - costBlend) + nearVal * costBlend;
    }

    dst[y * dstPitch + x] = (uint8_t)fminf(fmaxf(flowResult + 0.5f, 0.0f), 255.0f);
}

// Adaptive motion-blur UV plane kernel with cost map + clamping + occlusion awareness.
__global__ void fruc_motionblur_uv_kernel(
    uint8_t* __restrict__ dstUV, int dstPitch,
    const uint8_t* __restrict__ prevUV, int prevPitch,
    const uint8_t* __restrict__ currUV, int currPitch,
    const int16_t* __restrict__ flowXY,
    const uint32_t* __restrict__ costMap,
    int flowStride,
    int chromaW, int chromaH, int lumaW, int lumaH,
    float alpha, float shutterAngle)
{
    int cx = blockIdx.x * blockDim.x + threadIdx.x;
    int cy = blockIdx.y * blockDim.y + threadIdx.y;
    if (cx >= chromaW || cy >= chromaH) return;

    int lx = cx * 2;
    int ly = cy * 2;
    int bx0 = lx / 4, by0 = ly / 4;
    int bx1 = min(lx + 1, lumaW - 1) / 4, by1 = min(ly + 1, lumaH - 1) / 4;

    float dx = 0, dy = 0, maxMag = 0, maxCost = 0;
    int bxs[2] = {bx0, bx1};
    int bys[2] = {by0, by1};
    for (int j = 0; j < 2; j++) {
        for (int i = 0; i < 2; i++) {
            int idx = bys[j] * flowStride + bxs[i] * 2;
            float bdx, bdy, bmag;
            read_flow_clamped(flowXY, idx, &bdx, &bdy, &bmag);
            dx += bdx;
            dy += bdy;
            if (bmag > maxMag) maxMag = bmag;
            if (costMap) {
                int costIdx = bys[j] * (flowStride / 2) + bxs[i];
                float c = fminf((float)costMap[costIdx] / 65535.0f, 1.0f);
                if (c > maxCost) maxCost = c;
            }
        }
    }
    dx *= 0.25f;
    dy *= 0.25f;

    float cdx = dx * 0.5f;
    float cdy = dy * 0.5f;

    int N;
    if (maxMag < 1.0f)       N = 2;
    else if (maxMag < 4.0f)  N = 4;
    else if (maxMag < 8.0f)  N = 8;
    else                      N = 16;

    if (maxCost > 0.5f) N = min(N, 4);

    float halfShutter = shutterAngle * 0.5f;
    float winStart = fmaxf(0.0f, alpha - halfShutter);
    float winEnd   = fminf(1.0f, alpha + halfShutter);

    if (winEnd - winStart < 1e-6f || N <= 1) {
        N = 1;
        winStart = alpha;
        winEnd = alpha;
    }

    float sumU = 0.0f, sumV = 0.0f;
    float fCW = (float)(chromaW - 1);
    float fCH = (float)(chromaH - 1);

    for (int s = 0; s < N; s++) {
        float t = (N == 1) ? winStart : winStart + (winEnd - winStart) * (float)s / (float)(N - 1);
        float adjT = occlusion_aware_alpha(t, maxMag, maxCost);

        float prevCX = fmaxf(0.0f, fminf((float)cx - cdx * adjT, fCW));
        float prevCY = fmaxf(0.0f, fminf((float)cy - cdy * adjT, fCH));
        float currCX = fmaxf(0.0f, fminf((float)cx + cdx * (1.0f - adjT), fCW));
        float currCY = fmaxf(0.0f, fminf((float)cy + cdy * (1.0f - adjT), fCH));

        int psx = min(max((int)(prevCX + 0.5f), 0), chromaW - 1);
        int psy = min(max((int)(prevCY + 0.5f), 0), chromaH - 1);
        int csx = min(max((int)(currCX + 0.5f), 0), chromaW - 1);
        int csy = min(max((int)(currCY + 0.5f), 0), chromaH - 1);

        float invT = 1.0f - adjT;
        sumU += (float)prevUV[psy * prevPitch + psx * 2] * invT
              + (float)currUV[csy * currPitch + csx * 2] * adjT;
        sumV += (float)prevUV[psy * prevPitch + psx * 2 + 1] * invT
              + (float)currUV[csy * currPitch + csx * 2 + 1] * adjT;
    }

    int dstIdx = cy * dstPitch + cx * 2;
    float flowU = sumU / (float)N;
    float flowV = sumV / (float)N;

    if (maxCost > 0.5f) {
        float costBlend = fminf((maxCost - 0.5f) * 2.0f, 1.0f);
        int nearIdx = (alpha < 0.5f) ? (cy * prevPitch + cx * 2) : (cy * currPitch + cx * 2);
        const uint8_t* nearUV = (alpha < 0.5f) ? prevUV : currUV;
        flowU = flowU * (1-costBlend) + nearUV[nearIdx] * costBlend;
        flowV = flowV * (1-costBlend) + nearUV[nearIdx+1] * costBlend;
    }

    dstUV[dstIdx]     = (uint8_t)fminf(fmaxf(flowU + 0.5f, 0.0f), 255.0f);
    dstUV[dstIdx + 1] = (uint8_t)fminf(fmaxf(flowV + 0.5f, 0.0f), 255.0f);
}

extern "C" {

cudaError_t fruc_motionblur_nv12(
    uint8_t* dst, int dstPitch,
    const uint8_t* prev, int prevPitch,
    const uint8_t* curr, int currPitch,
    const int16_t* flowXY, const uint32_t* costMap, int flowStride,
    int width, int height,
    float alpha, float shutterAngle,
    cudaStream_t stream)
{
    dim3 block(BLOCK_DIM_X, BLOCK_DIM_Y);

    dim3 gridY((width + block.x - 1) / block.x, (height + block.y - 1) / block.y);
    fruc_motionblur_y_kernel<<<gridY, block, 0, stream>>>(
        dst, dstPitch, prev, prevPitch, curr, currPitch,
        flowXY, costMap, flowStride, width, height, alpha, shutterAngle);

    int chromaW = width / 2;
    int chromaH = height / 2;
    dim3 gridUV((chromaW + block.x - 1) / block.x, (chromaH + block.y - 1) / block.y);
    fruc_motionblur_uv_kernel<<<gridUV, block, 0, stream>>>(
        dst + dstPitch * height, dstPitch,
        prev + prevPitch * height, prevPitch,
        curr + currPitch * height, currPitch,
        flowXY, costMap, flowStride,
        chromaW, chromaH, width, height, alpha, shutterAngle);

    return cudaGetLastError();
}

cudaError_t fruc_interpolate_nv12(
    uint8_t* dst, int dstPitch,
    const uint8_t* prev, int prevPitch,
    const uint8_t* curr, int currPitch,
    const int16_t* flowXY, const uint32_t* costMap, int flowStride,
    int width, int height, float alpha,
    cudaStream_t stream)
{
    dim3 block(BLOCK_DIM_X, BLOCK_DIM_Y);

    dim3 gridY((width + block.x - 1) / block.x, (height + block.y - 1) / block.y);
    fruc_interpolate_y_kernel<<<gridY, block, 0, stream>>>(
        dst, dstPitch, prev, prevPitch, curr, currPitch,
        flowXY, costMap, flowStride, width, height, alpha);

    int chromaW = width / 2;
    int chromaH = height / 2;
    dim3 gridUV((chromaW + block.x - 1) / block.x, (chromaH + block.y - 1) / block.y);
    fruc_interpolate_uv_kernel<<<gridUV, block, 0, stream>>>(
        dst + dstPitch * height, dstPitch,
        prev + prevPitch * height, prevPitch,
        curr + currPitch * height, currPitch,
        flowXY, costMap, flowStride,
        chromaW, chromaH, width, height, alpha);

    return cudaGetLastError();
}

// Simple linear blend fallback (used when optical flow is unavailable)
cudaError_t fruc_blend_nv12(
    uint8_t* dst, int dstPitch,
    const uint8_t* prev, int prevPitch,
    const uint8_t* curr, int currPitch,
    int width, int height, float alpha,
    cudaStream_t stream)
{
    extern cudaError_t blend_uniform_nv12(
        uint8_t* dst, const uint8_t* a, const uint8_t* b,
        int pos256, int width, int height, int pitch, cudaStream_t stream);

    int pos256 = (int)(alpha * 256.0f);
    if (pos256 < 0) pos256 = 0;
    if (pos256 > 256) pos256 = 256;

    return blend_uniform_nv12(dst, prev, curr, pos256, width, height, dstPitch, stream);
}

} // extern "C"
