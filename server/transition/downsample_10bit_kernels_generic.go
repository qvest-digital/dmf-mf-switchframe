//go:build !amd64 && !arm64

package transition

// downsampleAlpha10Row averages pairs of adjacent uint8 alpha values for 4:2:2 chroma downsampling.
// dst[i] = (src[2*i] + src[2*i+1] + 1) >> 1
// count = number of OUTPUT samples (= srcWidth/2).
// Generic Go fallback for platforms without SIMD acceleration.
func downsampleAlpha10Row(dst, src *byte, count int) {
	downsampleAlpha10RowGo(dst, src, count)
}
