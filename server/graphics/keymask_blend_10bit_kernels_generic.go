//go:build !amd64 && !arm64

package graphics

// keyMaskBlend10Row blends fill onto bg using mask as per-pixel alpha for 10-bit samples.
// Generic Go fallback for platforms without SIMD acceleration.
func keyMaskBlend10Row(bg, fill, mask *byte, count int) {
	keyMaskBlend10RowGo(bg, fill, mask, count)
}
