//go:build amd64

package stmap

// warpBilinearRow processes n output pixels using precomputed 16.16
// fixed-point LUT coordinates (int32). For each pixel, reads source
// coordinates from lutX/lutY, samples 4 source pixels with bilinear
// interpolation, and writes the result to dst.
//
// Uses software prefetching to hide memory latency from the random
// source pixel access pattern (each pixel reads from a different
// source location, causing L2/L3 cache misses).
//
//go:noescape
func warpBilinearRow(dst, src *byte, srcW, srcH, n int, lutX, lutY *int32)
