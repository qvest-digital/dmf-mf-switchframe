#include <metal_stdlib>
using namespace metal;

struct FRUCBlendParams {
    uint width;
    uint height;
    uint dstPitch;
    uint prevPitch;
    uint currPitch;
    float alpha;   // temporal position: 0.0 = prev, 1.0 = curr
};

struct FRUCInterpolateParams {
    uint width;
    uint height;
    uint dstPitch;
    uint prevPitch;
    uint currPitch;
    int flowStride;   // stride in int16_t pairs
    float alpha;
};

struct FRUCInterpolateUVParams {
    uint chromaW;
    uint chromaH;
    uint lumaW;
    uint lumaH;
    uint dstPitch;
    uint prevPitch;
    uint currPitch;
    int flowStride;
    float alpha;
};

// Simple linear blend between two frames (fallback when optical flow unavailable)
kernel void fruc_blend(
    device uint8_t* dst           [[buffer(0)]],
    device const uint8_t* prev    [[buffer(1)]],
    device const uint8_t* curr    [[buffer(2)]],
    constant FRUCBlendParams& params [[buffer(3)]],
    uint2 gid [[thread_position_in_grid]])
{
    if (gid.x >= params.width || gid.y >= params.height) return;

    uint dstIdx = gid.y * params.dstPitch + gid.x;
    uint prevIdx = gid.y * params.prevPitch + gid.x;
    uint currIdx = gid.y * params.currPitch + gid.x;

    float pVal = float(prev[prevIdx]);
    float cVal = float(curr[currIdx]);
    dst[dstIdx] = uint8_t(pVal * (1.0f - params.alpha) + cVal * params.alpha + 0.5f);
}

// Motion-compensated Y plane interpolation
kernel void fruc_interpolate_y(
    device uint8_t* dst           [[buffer(0)]],
    device const uint8_t* prev    [[buffer(1)]],
    device const uint8_t* curr    [[buffer(2)]],
    device const short* flowXY    [[buffer(3)]],
    constant FRUCInterpolateParams& params [[buffer(4)]],
    uint2 gid [[thread_position_in_grid]])
{
    if (gid.x >= params.width || gid.y >= params.height) return;

    // Look up flow vector for this pixel's 4x4 block
    int bx = int(gid.x) / 4;
    int by = int(gid.y) / 4;
    int flowIdx = by * params.flowStride + bx * 2;
    float dx = float(flowXY[flowIdx]) * 0.03125f;
    float dy = float(flowXY[flowIdx + 1]) * 0.03125f;

    // Source positions in prev and curr (bidirectional interpolation)
    float prevX = float(gid.x) - dx * params.alpha;
    float prevY = float(gid.y) - dy * params.alpha;
    float currX = float(gid.x) + dx * (1.0f - params.alpha);
    float currY = float(gid.y) + dy * (1.0f - params.alpha);

    prevX = max(0.0f, min(prevX, float(params.width - 1)));
    prevY = max(0.0f, min(prevY, float(params.height - 1)));
    currX = max(0.0f, min(currX, float(params.width - 1)));
    currY = max(0.0f, min(currY, float(params.height - 1)));

    // Bilinear sample from prev
    int px0 = int(prevX), py0 = int(prevY);
    int px1 = min(px0 + 1, int(params.width) - 1);
    int py1 = min(py0 + 1, int(params.height) - 1);
    float fx = prevX - float(px0), fy = prevY - float(py0);
    float pVal = (float(prev[uint(py0) * params.prevPitch + uint(px0)]) * (1.0f-fx) + float(prev[uint(py0) * params.prevPitch + uint(px1)]) * fx) * (1.0f-fy)
               + (float(prev[uint(py1) * params.prevPitch + uint(px0)]) * (1.0f-fx) + float(prev[uint(py1) * params.prevPitch + uint(px1)]) * fx) * fy;

    // Bilinear sample from curr
    int cx0 = int(currX), cy0 = int(currY);
    int cx1 = min(cx0 + 1, int(params.width) - 1);
    int cy1 = min(cy0 + 1, int(params.height) - 1);
    fx = currX - float(cx0); fy = currY - float(cy0);
    float cVal = (float(curr[uint(cy0) * params.currPitch + uint(cx0)]) * (1.0f-fx) + float(curr[uint(cy0) * params.currPitch + uint(cx1)]) * fx) * (1.0f-fy)
               + (float(curr[uint(cy1) * params.currPitch + uint(cx0)]) * (1.0f-fx) + float(curr[uint(cy1) * params.currPitch + uint(cx1)]) * fx) * fy;

    float result = pVal * (1.0f - params.alpha) + cVal * params.alpha;
    dst[gid.y * params.dstPitch + gid.x] = uint8_t(min(max(result + 0.5f, 0.0f), 255.0f));
}

// Motion-compensated UV plane interpolation (NV12 interleaved, half resolution)
kernel void fruc_interpolate_uv(
    device uint8_t* dstUV         [[buffer(0)]],
    device const uint8_t* prevUV  [[buffer(1)]],
    device const uint8_t* currUV  [[buffer(2)]],
    device const short* flowXY    [[buffer(3)]],
    constant FRUCInterpolateUVParams& params [[buffer(4)]],
    uint2 gid [[thread_position_in_grid]])
{
    if (gid.x >= params.chromaW || gid.y >= params.chromaH) return;

    int lx = int(gid.x) * 2;
    int ly = int(gid.y) * 2;
    int bx0 = lx / 4, by0 = ly / 4;
    int bx1 = min(lx + 1, int(params.lumaW) - 1) / 4;
    int by1 = min(ly + 1, int(params.lumaH) - 1) / 4;

    float dx = 0, dy = 0;
    int bxs[2] = {bx0, bx1};
    int bys[2] = {by0, by1};
    for (int j = 0; j < 2; j++) {
        for (int i = 0; i < 2; i++) {
            int idx = bys[j] * params.flowStride + bxs[i] * 2;
            dx += float(flowXY[idx]) * 0.03125f;
            dy += float(flowXY[idx + 1]) * 0.03125f;
        }
    }
    dx *= 0.25f;
    dy *= 0.25f;

    float cdx = dx * 0.5f;
    float cdy = dy * 0.5f;

    float prevCX = float(gid.x) - cdx * params.alpha;
    float prevCY = float(gid.y) - cdy * params.alpha;
    float currCX = float(gid.x) + cdx * (1.0f - params.alpha);
    float currCY = float(gid.y) + cdy * (1.0f - params.alpha);

    prevCX = max(0.0f, min(prevCX, float(params.chromaW - 1)));
    prevCY = max(0.0f, min(prevCY, float(params.chromaH - 1)));
    currCX = max(0.0f, min(currCX, float(params.chromaW - 1)));
    currCY = max(0.0f, min(currCY, float(params.chromaH - 1)));

    int psx = min(max(int(prevCX + 0.5f), 0), int(params.chromaW) - 1);
    int psy = min(max(int(prevCY + 0.5f), 0), int(params.chromaH) - 1);
    int csx = min(max(int(currCX + 0.5f), 0), int(params.chromaW) - 1);
    int csy = min(max(int(currCY + 0.5f), 0), int(params.chromaH) - 1);

    float invA = 1.0f - params.alpha;
    uint dstIdx = gid.y * params.dstPitch + gid.x * 2;

    float pU = float(prevUV[uint(psy) * params.prevPitch + uint(psx) * 2]);
    float cU = float(currUV[uint(csy) * params.currPitch + uint(csx) * 2]);
    dstUV[dstIdx] = uint8_t(pU * invA + cU * params.alpha + 0.5f);

    float pV = float(prevUV[uint(psy) * params.prevPitch + uint(psx) * 2 + 1]);
    float cV = float(currUV[uint(csy) * params.currPitch + uint(csx) * 2 + 1]);
    dstUV[dstIdx + 1] = uint8_t(pV * invA + cV * params.alpha + 0.5f);
}
