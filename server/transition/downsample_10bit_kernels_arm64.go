//go:build arm64

package transition

// downsampleAlpha10Row averages pairs of adjacent uint8 alpha values for 4:2:2 chroma downsampling.
// dst[i] = (src[2*i] + src[2*i+1] + 1) >> 1
// count = number of OUTPUT samples (= srcWidth/2).
//
// ARM64 NEON implementation processes 16 output samples per iteration using:
//   - LD2 to deinterleave even/odd bytes
//   - URHADD for unsigned rounding halving add
//
//go:noescape
func downsampleAlpha10Row(dst, src *byte, count int)
