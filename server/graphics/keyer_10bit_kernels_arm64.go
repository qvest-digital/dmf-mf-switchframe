//go:build arm64

package graphics

// lumaKey10Row generates a luma key mask for count uint16 LE Y samples.
// For each sample: mask = 255 if y >= lowThresh && y <= highThresh, else 0.
//
// ARM64 NEON implementation processes 8 samples per iteration using:
//   - CMHS (unsigned compare higher-or-same) for threshold checks
//   - AND to combine both conditions
//   - XTN to narrow 16-bit mask to 8-bit output
//
// lowThresh and highThresh are passed as int but must be in [0, 1023].
//
//go:noescape
func lumaKey10Row(mask *byte, y *byte, count int, lowThresh, highThresh int)
