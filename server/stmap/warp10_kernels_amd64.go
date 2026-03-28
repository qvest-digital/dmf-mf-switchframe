//go:build amd64

package stmap

// warpBilinearRow10 processes n output pixels of a 10-bit plane using
// precomputed 16.16 fixed-point LUT coordinates (int32). Each output
// sample is a uint16 LE value (2 bytes). Uses software prefetching via
// PREFETCHT0 to hide cache miss latency from the random source access pattern.
//
// The bilinear interpolation uses 64-bit multiply for the vertical
// accumulation to avoid overflow.
//
//go:noescape
func warpBilinearRow10(dst, src *byte, srcW, srcH, n int, lutX, lutY *int32)
