//go:build arm64

package graphics

// expandChromaMaskRow reads chromaWidth bytes from src and writes chromaWidth*2
// bytes to dst, duplicating each byte. ARM64 NEON uses ZIP1/ZIP2 to interleave
// each byte with its duplicate, processing 16 input bytes → 32 output bytes
// per iteration.
//
//go:noescape
func expandChromaMaskRow(dst *byte, src *byte, chromaWidth int)
