//go:build amd64

package transition

// downsampleAlpha10Row averages pairs of adjacent uint8 alpha values for 4:2:2 chroma downsampling.
// dst[i] = (src[2*i] + src[2*i+1] + 1) >> 1
// count = number of OUTPUT samples (= srcWidth/2).
//
// AMD64 AVX2 implementation processes 16 output samples per iteration using:
//   - VPAND/VPSRLW to separate even/odd bytes
//   - VPAVGB for unsigned rounding average
//
// SSE2 fallback processes 8 output samples per iteration.
//
//go:noescape
func downsampleAlpha10Row(dst, src *byte, count int)
