//go:build arm64

package stmap

// warpBilinearRow processes n output pixels using precomputed 16.16
// fixed-point LUT coordinates (int32). Uses software prefetching via
// PRFM to hide cache miss latency from the random source access pattern.
//
//go:noescape
func warpBilinearRow(dst, src *byte, srcW, srcH, n int, lutX, lutY *int32)
