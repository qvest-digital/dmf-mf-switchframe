//go:build arm64

package stmap

// warpBilinearRow10 processes n output pixels of a 10-bit plane using
// precomputed 16.16 fixed-point LUT coordinates (int32). Each output
// sample is a uint16 LE value (2 bytes). Uses software prefetching via
// PRFM to hide cache miss latency from the random source access pattern.
//
// The bilinear interpolation uses int64 for the vertical accumulation
// to avoid overflow (10-bit sample * 65536 fractional * 65536 fractional
// exceeds int32 range).
//
//go:noescape
func warpBilinearRow10(dst, src *byte, srcW, srcH, n int, lutX, lutY *int32)
