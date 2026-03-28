//go:build arm64

package graphics

// expandChromaMask10Row duplicates each input byte to two output bytes for
// 4:2:2 chroma→luma mask expansion. count = number of INPUT samples.
//
// ARM64 NEON implementation processes 16 input bytes per iteration using
// ZIP1/ZIP2 to interleave each byte with its duplicate.
//
//go:noescape
func expandChromaMask10Row(dst, src *byte, count int)
