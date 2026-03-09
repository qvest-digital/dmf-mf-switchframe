//go:build amd64

package graphics

import "unsafe"

// expandChromaMaskRow reads chromaWidth bytes from src and writes chromaWidth*2
// bytes to dst, duplicating each byte.
// AMD64 scalar Go fallback.
func expandChromaMaskRow(dst *byte, src *byte, chromaWidth int) {
	if chromaWidth <= 0 {
		return
	}
	srcS := unsafe.Slice(src, chromaWidth)
	dstS := unsafe.Slice(dst, chromaWidth*2)

	for i := 0; i < chromaWidth; i++ {
		v := srcS[i]
		dstS[i*2] = v
		dstS[i*2+1] = v
	}
}
