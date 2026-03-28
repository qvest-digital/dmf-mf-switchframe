package stmap

import "fmt"

// Warp10Dispatch applies the ST map warp to a YUV422P10LE frame using
// the platform-optimized dispatch path. This is identical to Warp10 since
// Warp10 itself now uses the optimized assembly kernels.
func (p *Processor) Warp10Dispatch(dst, src []byte, w, h int) error {
	return p.Warp10(dst, src, w, h)
}

// warpPlane10Dispatch dispatches to the platform-optimized 10-bit bilinear
// warp kernel. On arm64 and amd64, this calls an assembly implementation
// with software prefetching and optimized register usage. On other
// platforms, falls back to a Go implementation matching the warpPlane10
// reference algorithm.
func warpPlane10Dispatch(dst, src []byte, w, h int, lutX, lutY []int32) {
	n := w * h
	if n == 0 {
		return
	}

	// Validate LUT size.
	if len(lutX) < n || len(lutY) < n {
		panic(fmt.Sprintf("stmap: LUT too small: need %d, got lutX=%d lutY=%d", n, len(lutX), len(lutY)))
	}

	// Dispatch to platform-specific kernel.
	warpBilinearRow10(&dst[0], &src[0], w, h, n, &lutX[0], &lutY[0])
}
