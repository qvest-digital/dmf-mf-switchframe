//go:build !amd64 && !arm64

package graphics

// lumaKey10Row generates a luma key mask for count uint16 LE Y samples.
// For each sample: mask = 255 if y >= lowThresh && y <= highThresh, else 0.
// Generic Go fallback for platforms without SIMD acceleration.
func lumaKey10Row(mask *byte, y *byte, count int, lowThresh, highThresh int) {
	lumaKey10RowGo(mask, y, count, uint16(lowThresh), uint16(highThresh))
}
