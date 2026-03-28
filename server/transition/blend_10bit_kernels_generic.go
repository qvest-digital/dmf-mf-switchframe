//go:build !amd64 && !arm64

package transition

// blendMix10Row blends count uint16 LE samples: dst[i] = clamp10((a[i]*(256-pos) + b[i]*pos + 128) >> 8).
// Generic Go fallback for platforms without SIMD acceleration.
func blendMix10Row(dst, a, b *byte, count, pos256 int) {
	blendMix10RowGo(dst, a, b, count, pos256)
}
