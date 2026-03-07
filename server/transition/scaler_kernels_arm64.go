//go:build arm64

package transition

// scaleBilinearRow performs bilinear interpolation for one destination row.
// NEON implementation uses scalar gather with SIMD interpolation math.
// Processes 4 destination pixels per iteration.
//
//go:noescape
func scaleBilinearRow(dst, row0, row1 *byte, srcW, dstW int, xCoords *int64, fy int)
