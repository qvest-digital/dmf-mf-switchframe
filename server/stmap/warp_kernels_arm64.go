//go:build arm64

package stmap

// warpBilinearRow processes one band of pixels using precomputed 16.16
// fixed-point LUT coordinates. See warp_kernels_amd64.go for documentation.
//
//go:noescape
func warpBilinearRow(dst, src *byte, srcW, srcH, n int, lutX, lutY *int64)
