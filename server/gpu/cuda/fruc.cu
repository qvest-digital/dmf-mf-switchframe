#include "common.cuh"

// Motion-compensated frame interpolation using optical flow vectors.
// Flow vectors are int16_t pairs (dx, dy) at 4x4 block granularity
// in NVOFA S10.5 format (5 fractional bits = divide by 32 for pixel displacement).
//
// For each output pixel, we look up the flow vector for its 4x4 block,
// compute the source position in both prev and curr frames at the
// interpolation position, then bilinear sample and blend.

// Y plane interpolation
__global__ void fruc_interpolate_y_kernel(
    uint8_t* __restrict__ dst, int dstPitch,
    const uint8_t* __restrict__ prev, int prevPitch,
    const uint8_t* __restrict__ curr, int currPitch,
    const int16_t* __restrict__ flowXY,  // interleaved (dx,dy) pairs
    int flowStride,  // stride in int16_t pairs (elements, not bytes)
    int width, int height,
    float alpha)  // temporal position: 0.0 = prev, 1.0 = curr
{
    int x = blockIdx.x * blockDim.x + threadIdx.x;
    int y = blockIdx.y * blockDim.y + threadIdx.y;
    if (x >= width || y >= height) return;

    // Look up flow vector for this pixel's 4x4 block
    int bx = x / 4;
    int by = y / 4;
    int flowIdx = by * flowStride + bx * 2;  // interleaved dx, dy
    float dx = (float)flowXY[flowIdx] * 0.03125f;      // S10.5 to pixel (1/32)
    float dy = (float)flowXY[flowIdx + 1] * 0.03125f;

    // Source positions in prev and curr (bidirectional interpolation)
    float prevX = (float)x - dx * alpha;
    float prevY = (float)y - dy * alpha;
    float currX = (float)x + dx * (1.0f - alpha);
    float currY = (float)y + dy * (1.0f - alpha);

    // Clamp to frame bounds
    prevX = fmaxf(0.0f, fminf(prevX, (float)(width - 1)));
    prevY = fmaxf(0.0f, fminf(prevY, (float)(height - 1)));
    currX = fmaxf(0.0f, fminf(currX, (float)(width - 1)));
    currY = fmaxf(0.0f, fminf(currY, (float)(height - 1)));

    // Bilinear sample from prev
    int px0 = (int)prevX, py0 = (int)prevY;
    int px1 = min(px0 + 1, width - 1), py1 = min(py0 + 1, height - 1);
    float fx = prevX - px0, fy = prevY - py0;
    float pVal = (prev[py0 * prevPitch + px0] * (1-fx) + prev[py0 * prevPitch + px1] * fx) * (1-fy)
               + (prev[py1 * prevPitch + px0] * (1-fx) + prev[py1 * prevPitch + px1] * fx) * fy;

    // Bilinear sample from curr
    int cx0 = (int)currX, cy0 = (int)currY;
    int cx1 = min(cx0 + 1, width - 1), cy1 = min(cy0 + 1, height - 1);
    fx = currX - cx0; fy = currY - cy0;
    float cVal = (curr[cy0 * currPitch + cx0] * (1-fx) + curr[cy0 * currPitch + cx1] * fx) * (1-fy)
               + (curr[cy1 * currPitch + cx0] * (1-fx) + curr[cy1 * currPitch + cx1] * fx) * fy;

    // Blend prev and curr contributions
    float result = pVal * (1.0f - alpha) + cVal * alpha;
    dst[y * dstPitch + x] = (uint8_t)fminf(fmaxf(result + 0.5f, 0.0f), 255.0f);
}

// UV plane interpolation (NV12 interleaved, half resolution)
__global__ void fruc_interpolate_uv_kernel(
    uint8_t* __restrict__ dstUV, int dstPitch,
    const uint8_t* __restrict__ prevUV, int prevPitch,
    const uint8_t* __restrict__ currUV, int currPitch,
    const int16_t* __restrict__ flowXY,
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
    int flowW = flowStride;

    float dx = 0, dy = 0;
    int count = 0;
    // Sample up to 4 flow blocks
    int bxs[2] = {bx0, bx1};
    int bys[2] = {by0, by1};
    for (int j = 0; j < 2; j++) {
        for (int i = 0; i < 2; i++) {
            int idx = bys[j] * flowW + bxs[i] * 2;
            dx += (float)flowXY[idx] * 0.03125f;      // S10.5 to pixel (1/32)
            dy += (float)flowXY[idx + 1] * 0.03125f;
            count++;
        }
    }
    dx /= count;
    dy /= count;

    // Scale displacement to chroma resolution
    float cdx = dx * 0.5f;
    float cdy = dy * 0.5f;

    float prevCX = (float)cx - cdx * alpha;
    float prevCY = (float)cy - cdy * alpha;
    float currCX = (float)cx + cdx * (1.0f - alpha);
    float currCY = (float)cy + cdy * (1.0f - alpha);

    prevCX = fmaxf(0.0f, fminf(prevCX, (float)(chromaW - 1)));
    prevCY = fmaxf(0.0f, fminf(prevCY, (float)(chromaH - 1)));
    currCX = fmaxf(0.0f, fminf(currCX, (float)(chromaW - 1)));
    currCY = fmaxf(0.0f, fminf(currCY, (float)(chromaH - 1)));

    // Nearest-neighbor for chroma (bilinear on interleaved pairs is complex)
    int psx = (int)(prevCX + 0.5f), psy = (int)(prevCY + 0.5f);
    int csx = (int)(currCX + 0.5f), csy = (int)(currCY + 0.5f);
    psx = min(max(psx, 0), chromaW - 1);
    psy = min(max(psy, 0), chromaH - 1);
    csx = min(max(csx, 0), chromaW - 1);
    csy = min(max(csy, 0), chromaH - 1);

    float invA = 1.0f - alpha;
    int dstIdx = cy * dstPitch + cx * 2;

    // U channel
    float pU = (float)prevUV[psy * prevPitch + psx * 2];
    float cU = (float)currUV[csy * currPitch + csx * 2];
    dstUV[dstIdx] = (uint8_t)(pU * invA + cU * alpha + 0.5f);

    // V channel
    float pV = (float)prevUV[psy * prevPitch + psx * 2 + 1];
    float cV = (float)currUV[csy * currPitch + csx * 2 + 1];
    dstUV[dstIdx + 1] = (uint8_t)(pV * invA + cV * alpha + 0.5f);
}

// Adaptive motion-blur Y plane kernel.
// Traces per-pixel motion trajectories over a configurable shutter window,
// accumulating multiple samples along the motion path for realistic motion blur.
// Sample count adapts to motion magnitude to avoid wasting work on static regions.
__global__ void fruc_motionblur_y_kernel(
    uint8_t* __restrict__ dst, int dstPitch,
    const uint8_t* __restrict__ prev, int prevPitch,
    const uint8_t* __restrict__ curr, int currPitch,
    const int16_t* __restrict__ flowXY,
    int flowStride,
    int width, int height,
    float alpha, float shutterAngle)
{
    int x = blockIdx.x * blockDim.x + threadIdx.x;
    int y = blockIdx.y * blockDim.y + threadIdx.y;
    if (x >= width || y >= height) return;

    // Look up flow vector for this pixel's 4x4 block
    int bx = x / 4;
    int by = y / 4;
    int flowIdx = by * flowStride + bx * 2;
    float dx = (float)flowXY[flowIdx] * 0.03125f;
    float dy = (float)flowXY[flowIdx + 1] * 0.03125f;

    // Compute motion magnitude for adaptive sample count
    float mag = sqrtf(dx * dx + dy * dy);

    int N;
    if (mag < 1.0f)       N = 2;
    else if (mag < 4.0f)  N = 4;
    else if (mag < 8.0f)  N = 8;
    else                   N = 16;

    // Shutter window: [alpha - shutterAngle/2, alpha + shutterAngle/2] clamped to [0,1]
    float halfShutter = shutterAngle * 0.5f;
    float winStart = fmaxf(0.0f, alpha - halfShutter);
    float winEnd   = fminf(1.0f, alpha + halfShutter);

    // Edge case: if shutter window collapses, use single sample at alpha
    if (winEnd - winStart < 1e-6f || N <= 1) {
        N = 1;
        winStart = alpha;
        winEnd = alpha;
    }

    float sum = 0.0f;
    float fW = (float)(width - 1);
    float fH = (float)(height - 1);

    for (int s = 0; s < N; s++) {
        // Evenly space samples within the shutter window
        float t = (N == 1) ? winStart : winStart + (winEnd - winStart) * (float)s / (float)(N - 1);

        // Source positions in prev and curr
        float prevX = fmaxf(0.0f, fminf((float)x - dx * t, fW));
        float prevY = fmaxf(0.0f, fminf((float)y - dy * t, fH));
        float currX = fmaxf(0.0f, fminf((float)x + dx * (1.0f - t), fW));
        float currY = fmaxf(0.0f, fminf((float)y + dy * (1.0f - t), fH));

        // Bilinear sample from prev
        int px0 = (int)prevX, py0 = (int)prevY;
        int px1 = min(px0 + 1, width - 1), py1 = min(py0 + 1, height - 1);
        float fx = prevX - px0, fy = prevY - py0;
        float pVal = (prev[py0 * prevPitch + px0] * (1-fx) + prev[py0 * prevPitch + px1] * fx) * (1-fy)
                   + (prev[py1 * prevPitch + px0] * (1-fx) + prev[py1 * prevPitch + px1] * fx) * fy;

        // Bilinear sample from curr
        int cx0 = (int)currX, cy0 = (int)currY;
        int cx1 = min(cx0 + 1, width - 1), cy1 = min(cy0 + 1, height - 1);
        fx = currX - cx0; fy = currY - cy0;
        float cVal = (curr[cy0 * currPitch + cx0] * (1-fx) + curr[cy0 * currPitch + cx1] * fx) * (1-fy)
                   + (curr[cy1 * currPitch + cx0] * (1-fx) + curr[cy1 * currPitch + cx1] * fx) * fy;

        // Blend prev and curr at this temporal position
        sum += pVal * (1.0f - t) + cVal * t;
    }

    dst[y * dstPitch + x] = (uint8_t)fminf(fmaxf(sum / (float)N + 0.5f, 0.0f), 255.0f);
}

// Adaptive motion-blur UV plane kernel (NV12 interleaved, half resolution).
// Same algorithm as Y kernel but averages flow from 2x2 luma blocks,
// scales displacement to chroma resolution, and uses nearest-neighbor
// sampling for interleaved UV pairs.
__global__ void fruc_motionblur_uv_kernel(
    uint8_t* __restrict__ dstUV, int dstPitch,
    const uint8_t* __restrict__ prevUV, int prevPitch,
    const uint8_t* __restrict__ currUV, int currPitch,
    const int16_t* __restrict__ flowXY,
    int flowStride,
    int chromaW, int chromaH, int lumaW, int lumaH,
    float alpha, float shutterAngle)
{
    int cx = blockIdx.x * blockDim.x + threadIdx.x;
    int cy = blockIdx.y * blockDim.y + threadIdx.y;
    if (cx >= chromaW || cy >= chromaH) return;

    // Average flow from 2x2 luma blocks (same pattern as interpolate UV kernel)
    int lx = cx * 2;
    int ly = cy * 2;
    int bx0 = lx / 4, by0 = ly / 4;
    int bx1 = min(lx + 1, lumaW - 1) / 4, by1 = min(ly + 1, lumaH - 1) / 4;
    int flowW = flowStride;

    float dx = 0, dy = 0;
    int count = 0;
    int bxs[2] = {bx0, bx1};
    int bys[2] = {by0, by1};
    for (int j = 0; j < 2; j++) {
        for (int i = 0; i < 2; i++) {
            int idx = bys[j] * flowW + bxs[i] * 2;
            dx += (float)flowXY[idx] * 0.03125f;
            dy += (float)flowXY[idx + 1] * 0.03125f;
            count++;
        }
    }
    dx /= count;
    dy /= count;

    // Scale displacement to chroma resolution
    float cdx = dx * 0.5f;
    float cdy = dy * 0.5f;

    // Compute motion magnitude for adaptive sample count
    float mag = sqrtf(dx * dx + dy * dy);

    int N;
    if (mag < 1.0f)       N = 2;
    else if (mag < 4.0f)  N = 4;
    else if (mag < 8.0f)  N = 8;
    else                   N = 16;

    // Shutter window
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

        // Chroma source positions
        float prevCX = fmaxf(0.0f, fminf((float)cx - cdx * t, fCW));
        float prevCY = fmaxf(0.0f, fminf((float)cy - cdy * t, fCH));
        float currCX = fmaxf(0.0f, fminf((float)cx + cdx * (1.0f - t), fCW));
        float currCY = fmaxf(0.0f, fminf((float)cy + cdy * (1.0f - t), fCH));

        // Nearest-neighbor for interleaved UV pairs
        int psx = (int)(prevCX + 0.5f), psy = (int)(prevCY + 0.5f);
        int csx = (int)(currCX + 0.5f), csy = (int)(currCY + 0.5f);
        psx = min(max(psx, 0), chromaW - 1);
        psy = min(max(psy, 0), chromaH - 1);
        csx = min(max(csx, 0), chromaW - 1);
        csy = min(max(csy, 0), chromaH - 1);

        float invT = 1.0f - t;

        // U channel
        float pU = (float)prevUV[psy * prevPitch + psx * 2];
        float cU = (float)currUV[csy * currPitch + csx * 2];
        sumU += pU * invT + cU * t;

        // V channel
        float pV = (float)prevUV[psy * prevPitch + psx * 2 + 1];
        float cV = (float)currUV[csy * currPitch + csx * 2 + 1];
        sumV += pV * invT + cV * t;
    }

    int dstIdx = cy * dstPitch + cx * 2;
    dstUV[dstIdx]     = (uint8_t)fminf(fmaxf(sumU / (float)N + 0.5f, 0.0f), 255.0f);
    dstUV[dstIdx + 1] = (uint8_t)fminf(fmaxf(sumV / (float)N + 0.5f, 0.0f), 255.0f);
}

extern "C" {

cudaError_t fruc_motionblur_nv12(
    uint8_t* dst, int dstPitch,
    const uint8_t* prev, int prevPitch,
    const uint8_t* curr, int currPitch,
    const int16_t* flowXY, int flowStride,
    int width, int height,
    float alpha, float shutterAngle,
    cudaStream_t stream)
{
    dim3 block(BLOCK_DIM_X, BLOCK_DIM_Y);

    // Y plane
    dim3 gridY((width + block.x - 1) / block.x, (height + block.y - 1) / block.y);
    fruc_motionblur_y_kernel<<<gridY, block, 0, stream>>>(
        dst, dstPitch, prev, prevPitch, curr, currPitch,
        flowXY, flowStride, width, height, alpha, shutterAngle);

    // UV plane
    int chromaW = width / 2;
    int chromaH = height / 2;
    dim3 gridUV((chromaW + block.x - 1) / block.x, (chromaH + block.y - 1) / block.y);
    fruc_motionblur_uv_kernel<<<gridUV, block, 0, stream>>>(
        dst + dstPitch * height, dstPitch,
        prev + prevPitch * height, prevPitch,
        curr + currPitch * height, currPitch,
        flowXY, flowStride,
        chromaW, chromaH, width, height, alpha, shutterAngle);

    return cudaGetLastError();
}

cudaError_t fruc_interpolate_nv12(
    uint8_t* dst, int dstPitch,
    const uint8_t* prev, int prevPitch,
    const uint8_t* curr, int currPitch,
    const int16_t* flowXY, int flowStride,
    int width, int height, float alpha,
    cudaStream_t stream)
{
    dim3 block(BLOCK_DIM_X, BLOCK_DIM_Y);

    // Y plane
    dim3 gridY((width + block.x - 1) / block.x, (height + block.y - 1) / block.y);
    fruc_interpolate_y_kernel<<<gridY, block, 0, stream>>>(
        dst, dstPitch, prev, prevPitch, curr, currPitch,
        flowXY, flowStride, width, height, alpha);

    // UV plane
    int chromaW = width / 2;
    int chromaH = height / 2;
    dim3 gridUV((chromaW + block.x - 1) / block.x, (chromaH + block.y - 1) / block.y);
    fruc_interpolate_uv_kernel<<<gridUV, block, 0, stream>>>(
        dst + dstPitch * height, dstPitch,
        prev + prevPitch * height, prevPitch,
        curr + currPitch * height, currPitch,
        flowXY, flowStride,
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
    // Reuse blend_uniform_kernel for both Y and UV planes
    extern cudaError_t blend_uniform_nv12(
        uint8_t* dst, const uint8_t* a, const uint8_t* b,
        int pos256, int width, int height, int pitch, cudaStream_t stream);

    int pos256 = (int)(alpha * 256.0f);
    if (pos256 < 0) pos256 = 0;
    if (pos256 > 256) pos256 = 256;

    return blend_uniform_nv12(dst, prev, curr, pos256, width, height, dstPitch, stream);
}

} // extern "C"
