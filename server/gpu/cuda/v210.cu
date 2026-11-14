#include "common.cuh"

// V210 packing (per 6 horizontal pixels = 4 x 32-bit words):
//   Word 0: bits [9:0]=Cb0, [19:10]=Y0, [29:20]=Cr0
//   Word 1: bits [9:0]=Y1,  [19:10]=Cb2, [29:20]=Y2
//   Word 2: bits [9:0]=Cr2, [19:10]=Y3,  [29:20]=Cb4
//   Word 3: bits [9:0]=Y4,  [19:10]=Cr4, [29:20]=Y5
//
// 10-bit to 8-bit: >> 2
// 4:2:2 to NV12: process two rows at a time, average chroma vertically

// A row whose width is not a multiple of six ends in a partial group of 2
// or 4 pixels. It still takes four words; the samples of the pixels past
// the width are zero (the layout ffmpeg's v210 encoder writes). The thread
// past the whole groups handles it, so the threads of the whole groups run
// the code they always ran. The packing kernels run one thread per group
// of the padded line, and the threads past the partial group write zeros,
// so the line padding never carries what the buffer held before. The
// helpers are host-callable too, so their layout can be checked without a
// GPU.

__host__ __device__ __forceinline__ void v210_unpack_group(
    const uint32_t* __restrict__ g, uint32_t ys[6], uint32_t cbs[3], uint32_t crs[3])
{
    uint32_t w0 = g[0], w1 = g[1], w2 = g[2], w3 = g[3];
    ys[0] = (w0 >> 10) & 0x3FF; ys[1] = w1 & 0x3FF; ys[2] = (w1 >> 20) & 0x3FF;
    ys[3] = (w2 >> 10) & 0x3FF; ys[4] = w3 & 0x3FF; ys[5] = (w3 >> 20) & 0x3FF;
    cbs[0] = w0 & 0x3FF; cbs[1] = (w1 >> 10) & 0x3FF; cbs[2] = (w2 >> 20) & 0x3FF;
    crs[0] = (w0 >> 20) & 0x3FF; crs[1] = w2 & 0x3FF; crs[2] = (w3 >> 10) & 0x3FF;
}

__host__ __device__ __forceinline__ void v210_pack_group(
    uint32_t* __restrict__ g, const uint32_t ys[6], const uint32_t cbs[3], const uint32_t crs[3])
{
    g[0] = (cbs[0] & 0x3FF) | ((ys[0] & 0x3FF) << 10) | ((crs[0] & 0x3FF) << 20);
    g[1] = (ys[1]  & 0x3FF) | ((cbs[1] & 0x3FF) << 10) | ((ys[2] & 0x3FF) << 20);
    g[2] = (crs[1] & 0x3FF) | ((ys[3]  & 0x3FF) << 10) | ((cbs[2] & 0x3FF) << 20);
    g[3] = (ys[4]  & 0x3FF) | ((crs[2] & 0x3FF) << 10) | ((ys[5] & 0x3FF) << 20);
}

// Partial group of a V210 row -> NV12, as v210_to_nv12_kernel does for a
// whole group: 8-bit by >> 2, chroma averaged with the next row on even rows.
__host__ __device__ void v210_to_nv12_tail(
    uint8_t* __restrict__ nv12, const uint32_t* __restrict__ v210,
    int height, int nv12Pitch, int v210Stride32, int y, int groupX, int n)
{
    uint32_t ys[6], cbs[3], crs[3];
    v210_unpack_group(v210 + y * v210Stride32 + groupX * 4, ys, cbs, crs);
    int px = groupX * 6;
    for (int i = 0; i < n; i++)
        nv12[y * nv12Pitch + px + i] = (uint8_t)(ys[i] >> 2);
    if ((y & 1) == 0 && y + 1 < height) {
        uint32_t nys[6], ncbs[3], ncrs[3];
        v210_unpack_group(v210 + (y + 1) * v210Stride32 + groupX * 4, nys, ncbs, ncrs);
        int uv = nv12Pitch * height + (y / 2) * nv12Pitch + groupX * 3 * 2;
        for (int i = 0; i < n / 2; i++) {
            nv12[uv + 2 * i]     = (uint8_t)(((int)(cbs[i] >> 2) + (int)(ncbs[i] >> 2) + 1) >> 1);
            nv12[uv + 2 * i + 1] = (uint8_t)(((int)(crs[i] >> 2) + (int)(ncrs[i] >> 2) + 1) >> 1);
        }
    }
}

// Partial group of an NV12 row -> V210, as nv12_to_v210_kernel does for a
// whole group: 10-bit by << 2, 4:2:0 chroma repeated for both rows.
__host__ __device__ void nv12_to_v210_tail(
    uint32_t* __restrict__ v210, const uint8_t* __restrict__ nv12,
    int height, int nv12Pitch, int v210Stride32, int y, int groupX, int n)
{
    uint32_t ys[6] = {0, 0, 0, 0, 0, 0}, cbs[3] = {0, 0, 0}, crs[3] = {0, 0, 0};
    int px = groupX * 6;
    for (int i = 0; i < n; i++)
        ys[i] = (uint32_t)nv12[y * nv12Pitch + px + i] << 2;
    int uv = nv12Pitch * height + (y / 2) * nv12Pitch + groupX * 3 * 2;
    for (int i = 0; i < n / 2; i++) {
        cbs[i] = (uint32_t)nv12[uv + 2 * i] << 2;
        crs[i] = (uint32_t)nv12[uv + 2 * i + 1] << 2;
    }
    v210_pack_group(v210 + y * v210Stride32 + groupX * 4, ys, cbs, crs);
}

// Partial group of a V210 row -> P210, lossless.
__host__ __device__ void v210_to_p210_tail(
    uint16_t* __restrict__ p210, const uint32_t* __restrict__ v210,
    int height, int p210Pitch16, int v210Stride32, int y, int groupX, int n)
{
    uint32_t ys[6], cbs[3], crs[3];
    v210_unpack_group(v210 + y * v210Stride32 + groupX * 4, ys, cbs, crs);
    int px = groupX * 6;
    for (int i = 0; i < n; i++)
        p210[y * p210Pitch16 + px + i] = (uint16_t)ys[i];
    int uv = p210Pitch16 * height + y * p210Pitch16 + groupX * 3 * 2;
    for (int i = 0; i < n / 2; i++) {
        p210[uv + 2 * i]     = (uint16_t)cbs[i];
        p210[uv + 2 * i + 1] = (uint16_t)crs[i];
    }
}

// Partial group of a P210 row -> V210, lossless.
__host__ __device__ void p210_to_v210_tail(
    uint32_t* __restrict__ v210, const uint16_t* __restrict__ p210,
    int height, int p210Pitch16, int v210Stride32, int y, int groupX, int n)
{
    uint32_t ys[6] = {0, 0, 0, 0, 0, 0}, cbs[3] = {0, 0, 0}, crs[3] = {0, 0, 0};
    int px = groupX * 6;
    for (int i = 0; i < n; i++)
        ys[i] = p210[y * p210Pitch16 + px + i];
    int uv = p210Pitch16 * height + y * p210Pitch16 + groupX * 3 * 2;
    for (int i = 0; i < n / 2; i++) {
        cbs[i] = p210[uv + 2 * i];
        crs[i] = p210[uv + 2 * i + 1];
    }
    v210_pack_group(v210 + y * v210Stride32 + groupX * 4, ys, cbs, crs);
}

// V210 → NV12 conversion kernel
// Each thread processes one 6-pixel V210 group for one row.
// Produces: 6 Y values in Y plane, and (for even rows only) 3 UV pairs in UV plane.
// For 4:2:0 downsample, we accumulate chroma across two rows using atomicAdd
// on a temporary buffer, then divide by 2.
//
// Simpler approach: two-pass. Pass 1 extracts Y + 4:2:2 chroma per row.
// Pass 2 averages adjacent chroma rows for 4:2:0.
// We use a single-pass approach with shared memory for the chroma averaging.

__global__ void v210_to_nv12_kernel(
    uint8_t* __restrict__ nv12,
    const uint32_t* __restrict__ v210,
    int width, int height, int nv12Pitch, int v210Stride32)
{
    int groupX = blockIdx.x * blockDim.x + threadIdx.x;
    int y = blockIdx.y * blockDim.y + threadIdx.y;
    int wholeGroups = width / 6;
    if (groupX > wholeGroups || y >= height) return;
    if (groupX == wholeGroups) {
        if (width % 6 != 0)
            v210_to_nv12_tail(nv12, v210, height, nv12Pitch, v210Stride32, y, groupX, width - groupX * 6);
        return;
    }

    // Read 4 words for this 6-pixel group
    int v210Idx = y * v210Stride32 + groupX * 4;
    uint32_t w0 = v210[v210Idx];
    uint32_t w1 = v210[v210Idx + 1];
    uint32_t w2 = v210[v210Idx + 2];
    uint32_t w3 = v210[v210Idx + 3];

    // Extract 10-bit luma values, convert to 8-bit
    int px = groupX * 6;
    uint8_t y0 = (uint8_t)(((w0 >> 10) & 0x3FF) >> 2);
    uint8_t y1 = (uint8_t)(((w1)       & 0x3FF) >> 2);
    uint8_t y2 = (uint8_t)(((w1 >> 20) & 0x3FF) >> 2);
    uint8_t y3 = (uint8_t)(((w2 >> 10) & 0x3FF) >> 2);
    uint8_t y4 = (uint8_t)(((w3)       & 0x3FF) >> 2);
    uint8_t y5 = (uint8_t)(((w3 >> 20) & 0x3FF) >> 2);

    // Write Y plane
    nv12[y * nv12Pitch + px]     = y0;
    nv12[y * nv12Pitch + px + 1] = y1;
    nv12[y * nv12Pitch + px + 2] = y2;
    nv12[y * nv12Pitch + px + 3] = y3;
    nv12[y * nv12Pitch + px + 4] = y4;
    nv12[y * nv12Pitch + px + 5] = y5;

    // Extract 10-bit chroma values (3 Cb + 3 Cr per 6-pixel group in 4:2:2)
    // V210 chroma is co-sited at even pixel positions: Cb0,Cr0 for pixels 0-1,
    // Cb2,Cr2 for pixels 2-3, Cb4,Cr4 for pixels 4-5.
    uint8_t cb0 = (uint8_t)(((w0)       & 0x3FF) >> 2);
    uint8_t cr0 = (uint8_t)(((w0 >> 20) & 0x3FF) >> 2);
    uint8_t cb2 = (uint8_t)(((w1 >> 10) & 0x3FF) >> 2);
    uint8_t cr2 = (uint8_t)(((w2)       & 0x3FF) >> 2);
    uint8_t cb4 = (uint8_t)(((w2 >> 20) & 0x3FF) >> 2);
    uint8_t cr4 = (uint8_t)(((w3 >> 10) & 0x3FF) >> 2);

    // Write NV12 UV plane: interleaved Cb,Cr pairs at half horizontal resolution.
    // For 4:2:0 vertical downsampling, we only write on even rows.
    // For odd rows, we average with the previous row's chroma.
    // Simple approach: even rows write chroma directly; a second pass could
    // average, but for V210 (broadcast SDI), chroma doesn't vary much
    // vertically so even-row sampling is acceptable.
    // Better approach: even rows write, odd rows blend with atomics.
    // Best approach: write 4:2:2 to temp, then downsample.
    // For simplicity and correctness: only process even rows, average with next row.
    if ((y & 1) == 0 && y + 1 < height) {
        // Read next row's chroma for vertical averaging
        int v210IdxNext = (y + 1) * v210Stride32 + groupX * 4;
        uint32_t nw0 = v210[v210IdxNext];
        uint32_t nw1 = v210[v210IdxNext + 1];
        uint32_t nw2 = v210[v210IdxNext + 2];
        uint32_t nw3 = v210[v210IdxNext + 3];

        uint8_t ncb0 = (uint8_t)(((nw0)       & 0x3FF) >> 2);
        uint8_t ncr0 = (uint8_t)(((nw0 >> 20) & 0x3FF) >> 2);
        uint8_t ncb2 = (uint8_t)(((nw1 >> 10) & 0x3FF) >> 2);
        uint8_t ncr2 = (uint8_t)(((nw2)       & 0x3FF) >> 2);
        uint8_t ncb4 = (uint8_t)(((nw2 >> 20) & 0x3FF) >> 2);
        uint8_t ncr4 = (uint8_t)(((nw3 >> 10) & 0x3FF) >> 2);

        int uvOffset = nv12Pitch * height;
        int uvY = y / 2;
        int cx = groupX * 3; // 3 chroma pairs per V210 group

        // Average top+bottom row chroma for 4:2:0
        int uvIdx0 = uvY * nv12Pitch + cx * 2;
        nv12[uvOffset + uvIdx0]     = (uint8_t)(((int)cb0 + ncb0 + 1) >> 1);
        nv12[uvOffset + uvIdx0 + 1] = (uint8_t)(((int)cr0 + ncr0 + 1) >> 1);

        if (cx + 1 < width / 2) {
            int uvIdx1 = uvY * nv12Pitch + (cx + 1) * 2;
            nv12[uvOffset + uvIdx1]     = (uint8_t)(((int)cb2 + ncb2 + 1) >> 1);
            nv12[uvOffset + uvIdx1 + 1] = (uint8_t)(((int)cr2 + ncr2 + 1) >> 1);
        }
        if (cx + 2 < width / 2) {
            int uvIdx2 = uvY * nv12Pitch + (cx + 2) * 2;
            nv12[uvOffset + uvIdx2]     = (uint8_t)(((int)cb4 + ncb4 + 1) >> 1);
            nv12[uvOffset + uvIdx2 + 1] = (uint8_t)(((int)cr4 + ncr4 + 1) >> 1);
        }
    }
}

// NV12 → V210 conversion kernel
// Each thread processes one 6-pixel group for one row.
// Reads 6 Y from Y plane + 3 UV pairs from UV plane, packs into 4 x 32-bit words.
// NV12 4:2:0 chroma is upsampled to 4:2:2 by duplicating vertically.
__global__ void nv12_to_v210_kernel(
    uint32_t* __restrict__ v210,
    const uint8_t* __restrict__ nv12,
    int width, int height, int nv12Pitch, int v210Stride32)
{
    int groupX = blockIdx.x * blockDim.x + threadIdx.x;
    int y = blockIdx.y * blockDim.y + threadIdx.y;
    int wholeGroups = width / 6;
    if (groupX >= v210Stride32 / 4 || y >= height) return;
    if (groupX >= wholeGroups) {
        // The partial group, then zeros up to the end of the line.
        nv12_to_v210_tail(v210, nv12, height, nv12Pitch, v210Stride32, y, groupX, max(width - groupX * 6, 0));
        return;
    }

    int px = groupX * 6;

    // Read 6 Y values, shift to 10-bit
    uint32_t Y0 = (uint32_t)nv12[y * nv12Pitch + px]     << 2;
    uint32_t Y1 = (uint32_t)nv12[y * nv12Pitch + px + 1] << 2;
    uint32_t Y2 = (uint32_t)nv12[y * nv12Pitch + px + 2] << 2;
    uint32_t Y3 = (uint32_t)nv12[y * nv12Pitch + px + 3] << 2;
    uint32_t Y4 = (uint32_t)nv12[y * nv12Pitch + px + 4] << 2;
    uint32_t Y5 = (uint32_t)nv12[y * nv12Pitch + px + 5] << 2;

    // Read chroma from NV12 UV plane (4:2:0 → duplicate for 4:2:2)
    int uvOffset = nv12Pitch * height;
    int uvY = y / 2;
    int cx = groupX * 3;

    uint32_t Cb0 = (uint32_t)nv12[uvOffset + uvY * nv12Pitch + cx * 2]     << 2;
    uint32_t Cr0 = (uint32_t)nv12[uvOffset + uvY * nv12Pitch + cx * 2 + 1] << 2;
    uint32_t Cb2 = (cx + 1 < width / 2) ? (uint32_t)nv12[uvOffset + uvY * nv12Pitch + (cx+1) * 2]     << 2 : Cb0;
    uint32_t Cr2 = (cx + 1 < width / 2) ? (uint32_t)nv12[uvOffset + uvY * nv12Pitch + (cx+1) * 2 + 1] << 2 : Cr0;
    uint32_t Cb4 = (cx + 2 < width / 2) ? (uint32_t)nv12[uvOffset + uvY * nv12Pitch + (cx+2) * 2]     << 2 : Cb2;
    uint32_t Cr4 = (cx + 2 < width / 2) ? (uint32_t)nv12[uvOffset + uvY * nv12Pitch + (cx+2) * 2 + 1] << 2 : Cr2;

    // Pack into V210 words
    uint32_t w0 = (Cb0 & 0x3FF) | ((Y0 & 0x3FF) << 10) | ((Cr0 & 0x3FF) << 20);
    uint32_t w1 = (Y1  & 0x3FF) | ((Cb2 & 0x3FF) << 10) | ((Y2 & 0x3FF) << 20);
    uint32_t w2 = (Cr2 & 0x3FF) | ((Y3  & 0x3FF) << 10) | ((Cb4 & 0x3FF) << 20);
    uint32_t w3 = (Y4  & 0x3FF) | ((Cr4 & 0x3FF) << 10) | ((Y5 & 0x3FF) << 20);

    int v210Idx = y * v210Stride32 + groupX * 4;
    v210[v210Idx]     = w0;
    v210[v210Idx + 1] = w1;
    v210[v210Idx + 2] = w2;
    v210[v210Idx + 3] = w3;
}

// ============================================================================
// V210 <-> P210 conversion kernels (LOSSLESS 10-bit — no bit shift)
// ============================================================================
// V210 packing: 6 pixels per 4 x 32-bit words (same as above).
// P210: uint16 semi-planar (Y plane + interleaved UV plane, full height).
// V210 is 4:2:2 and P210 is 4:2:2, so NO vertical chroma downsampling.

// V210 -> P210: extract 10-bit samples directly (no >> 2 bit loss)
__global__ void v210_to_p210_kernel(
    uint16_t* __restrict__ p210,
    const uint32_t* __restrict__ v210,
    int width, int height, int p210Pitch16, int v210Stride32)
{
    int groupX = blockIdx.x * blockDim.x + threadIdx.x;
    int y = blockIdx.y * blockDim.y + threadIdx.y;
    int wholeGroups = width / 6;
    if (groupX > wholeGroups || y >= height) return;
    if (groupX == wholeGroups) {
        if (width % 6 != 0)
            v210_to_p210_tail(p210, v210, height, p210Pitch16, v210Stride32, y, groupX, width - groupX * 6);
        return;
    }

    // Read 4 words for this 6-pixel group
    int v210Idx = y * v210Stride32 + groupX * 4;
    uint32_t w0 = v210[v210Idx];
    uint32_t w1 = v210[v210Idx + 1];
    uint32_t w2 = v210[v210Idx + 2];
    uint32_t w3 = v210[v210Idx + 3];

    // Extract 10-bit luma values (NO bit shift — lossless)
    int px = groupX * 6;
    uint16_t y0 = (uint16_t)((w0 >> 10) & 0x3FF);
    uint16_t y1 = (uint16_t)((w1)       & 0x3FF);
    uint16_t y2 = (uint16_t)((w1 >> 20) & 0x3FF);
    uint16_t y3 = (uint16_t)((w2 >> 10) & 0x3FF);
    uint16_t y4 = (uint16_t)((w3)       & 0x3FF);
    uint16_t y5 = (uint16_t)((w3 >> 20) & 0x3FF);

    // Write Y plane (uint16 per sample)
    p210[y * p210Pitch16 + px]     = y0;
    p210[y * p210Pitch16 + px + 1] = y1;
    p210[y * p210Pitch16 + px + 2] = y2;
    p210[y * p210Pitch16 + px + 3] = y3;
    p210[y * p210Pitch16 + px + 4] = y4;
    p210[y * p210Pitch16 + px + 5] = y5;

    // Extract 10-bit chroma values (3 Cb + 3 Cr per 6-pixel group)
    uint16_t cb0 = (uint16_t)((w0)       & 0x3FF);
    uint16_t cr0 = (uint16_t)((w0 >> 20) & 0x3FF);
    uint16_t cb2 = (uint16_t)((w1 >> 10) & 0x3FF);
    uint16_t cr2 = (uint16_t)((w2)       & 0x3FF);
    uint16_t cb4 = (uint16_t)((w2 >> 20) & 0x3FF);
    uint16_t cr4 = (uint16_t)((w3 >> 10) & 0x3FF);

    // Write UV plane (interleaved CbCr uint16 pairs at half horizontal resolution)
    // NO vertical downsampling — write every row (4:2:2)
    int uvOffset = p210Pitch16 * height;
    int cx = groupX * 3;  // 3 chroma pairs per V210 group

    int uvIdx0 = y * p210Pitch16 + cx * 2;
    p210[uvOffset + uvIdx0]     = cb0;
    p210[uvOffset + uvIdx0 + 1] = cr0;

    if (cx + 1 < width / 2) {
        int uvIdx1 = y * p210Pitch16 + (cx + 1) * 2;
        p210[uvOffset + uvIdx1]     = cb2;
        p210[uvOffset + uvIdx1 + 1] = cr2;
    }
    if (cx + 2 < width / 2) {
        int uvIdx2 = y * p210Pitch16 + (cx + 2) * 2;
        p210[uvOffset + uvIdx2]     = cb4;
        p210[uvOffset + uvIdx2 + 1] = cr4;
    }
}

// P210 -> V210: pack uint16 samples into V210 32-bit words (lossless)
__global__ void p210_to_v210_kernel(
    uint32_t* __restrict__ v210,
    const uint16_t* __restrict__ p210,
    int width, int height, int p210Pitch16, int v210Stride32)
{
    int groupX = blockIdx.x * blockDim.x + threadIdx.x;
    int y = blockIdx.y * blockDim.y + threadIdx.y;
    int wholeGroups = width / 6;
    if (groupX >= v210Stride32 / 4 || y >= height) return;
    if (groupX >= wholeGroups) {
        // The partial group, then zeros up to the end of the line.
        p210_to_v210_tail(v210, p210, height, p210Pitch16, v210Stride32, y, groupX, max(width - groupX * 6, 0));
        return;
    }

    int px = groupX * 6;

    // Read 6 Y values (uint16, already 10-bit)
    uint32_t Y0 = (uint32_t)p210[y * p210Pitch16 + px];
    uint32_t Y1 = (uint32_t)p210[y * p210Pitch16 + px + 1];
    uint32_t Y2 = (uint32_t)p210[y * p210Pitch16 + px + 2];
    uint32_t Y3 = (uint32_t)p210[y * p210Pitch16 + px + 3];
    uint32_t Y4 = (uint32_t)p210[y * p210Pitch16 + px + 4];
    uint32_t Y5 = (uint32_t)p210[y * p210Pitch16 + px + 5];

    // Read chroma from UV plane (4:2:2 — every row)
    int uvOffset = p210Pitch16 * height;
    int cx = groupX * 3;

    uint32_t Cb0 = (uint32_t)p210[uvOffset + y * p210Pitch16 + cx * 2];
    uint32_t Cr0 = (uint32_t)p210[uvOffset + y * p210Pitch16 + cx * 2 + 1];
    uint32_t Cb2 = (cx + 1 < width / 2) ? (uint32_t)p210[uvOffset + y * p210Pitch16 + (cx+1) * 2]     : Cb0;
    uint32_t Cr2 = (cx + 1 < width / 2) ? (uint32_t)p210[uvOffset + y * p210Pitch16 + (cx+1) * 2 + 1] : Cr0;
    uint32_t Cb4 = (cx + 2 < width / 2) ? (uint32_t)p210[uvOffset + y * p210Pitch16 + (cx+2) * 2]     : Cb2;
    uint32_t Cr4 = (cx + 2 < width / 2) ? (uint32_t)p210[uvOffset + y * p210Pitch16 + (cx+2) * 2 + 1] : Cr2;

    // Pack into V210 words (no bit shift — lossless)
    uint32_t w0 = (Cb0 & 0x3FF) | ((Y0 & 0x3FF) << 10) | ((Cr0 & 0x3FF) << 20);
    uint32_t w1 = (Y1  & 0x3FF) | ((Cb2 & 0x3FF) << 10) | ((Y2 & 0x3FF) << 20);
    uint32_t w2 = (Cr2 & 0x3FF) | ((Y3  & 0x3FF) << 10) | ((Cb4 & 0x3FF) << 20);
    uint32_t w3 = (Y4  & 0x3FF) | ((Cr4 & 0x3FF) << 10) | ((Y5 & 0x3FF) << 20);

    int v210Idx = y * v210Stride32 + groupX * 4;
    v210[v210Idx]     = w0;
    v210[v210Idx + 1] = w1;
    v210[v210Idx + 2] = w2;
    v210[v210Idx + 3] = w3;
}

extern "C" {

cudaError_t v210_to_nv12(
    uint8_t* nv12, const uint32_t* v210,
    int width, int height, int nv12Pitch, int v210StrideBytes,
    cudaStream_t stream)
{
    int groupsPerRow = (width + 5) / 6; // the last one partial when width % 6 != 0
    int v210Stride32 = v210StrideBytes / 4;
    dim3 block(32, 8);
    dim3 grid((groupsPerRow + block.x - 1) / block.x, (height + block.y - 1) / block.y);
    v210_to_nv12_kernel<<<grid, block, 0, stream>>>(
        nv12, v210, width, height, nv12Pitch, v210Stride32);
    return cudaGetLastError();
}

cudaError_t nv12_to_v210(
    uint32_t* v210, const uint8_t* nv12,
    int width, int height, int nv12Pitch, int v210StrideBytes,
    cudaStream_t stream)
{
    int groupsPerRow = v210StrideBytes / 16; // every group of the padded line
    int v210Stride32 = v210StrideBytes / 4;
    dim3 block(32, 8);
    dim3 grid((groupsPerRow + block.x - 1) / block.x, (height + block.y - 1) / block.y);
    nv12_to_v210_kernel<<<grid, block, 0, stream>>>(
        v210, nv12, width, height, nv12Pitch, v210Stride32);
    return cudaGetLastError();
}

// --- V210 <-> P210 wrappers ---

cudaError_t v210_to_p210(
    uint8_t* p210, const uint32_t* v210,
    int width, int height, int p210Pitch, int v210StrideBytes,
    cudaStream_t stream)
{
    int groupsPerRow = (width + 5) / 6; // the last one partial when width % 6 != 0
    int v210Stride32 = v210StrideBytes / 4;
    int p210Pitch16 = p210Pitch / 2;
    dim3 block(32, 8);
    dim3 grid((groupsPerRow + block.x - 1) / block.x, (height + block.y - 1) / block.y);
    v210_to_p210_kernel<<<grid, block, 0, stream>>>(
        (uint16_t*)p210, v210, width, height, p210Pitch16, v210Stride32);
    return cudaGetLastError();
}

cudaError_t p210_to_v210(
    uint32_t* v210, const uint8_t* p210,
    int width, int height, int p210Pitch, int v210StrideBytes,
    cudaStream_t stream)
{
    int groupsPerRow = v210StrideBytes / 16; // every group of the padded line
    int v210Stride32 = v210StrideBytes / 4;
    int p210Pitch16 = p210Pitch / 2;
    dim3 block(32, 8);
    dim3 grid((groupsPerRow + block.x - 1) / block.x, (height + block.y - 1) / block.y);
    p210_to_v210_kernel<<<grid, block, 0, stream>>>(
        v210, (const uint16_t*)p210, width, height, p210Pitch16, v210Stride32);
    return cudaGetLastError();
}

} // extern "C"
