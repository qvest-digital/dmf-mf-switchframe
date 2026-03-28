//go:build !amd64 && !arm64

package graphics

// expandChromaMask10Row duplicates each input byte to two output bytes for
// 4:2:2 chroma→luma mask expansion. count = number of INPUT samples.
// Generic Go fallback for platforms without SIMD acceleration.
func expandChromaMask10Row(dst, src *byte, count int) {
	expandChromaMask10RowGo(dst, src, count)
}
