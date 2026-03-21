//go:build amd64

package stmap

// warpBilinearRow processes one band of pixels using precomputed 16.16
// fixed-point LUT coordinates. For each output pixel i, reads lutX[i] and
// lutY[i], samples 4 source pixels from src (full plane, srcW×srcH), and
// writes the bilinear-interpolated result to dst[i].
//
// This eliminates Go bounds checks on the src array access and uses
// register-pinned loop variables for ~2x speedup over the Go fallback.
//
//go:noescape
func warpBilinearRow(dst, src *byte, srcW, srcH, n int, lutX, lutY *int64)
