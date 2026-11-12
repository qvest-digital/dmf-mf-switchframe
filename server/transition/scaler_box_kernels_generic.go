//go:build !amd64

package transition

import "unsafe"

// boxShrink2xRow averages the 2x2 blocks of one source row pair into n
// destination pixels: dst[i] = (row0[2i] + row0[2i+1] + row1[2i] +
// row1[2i+1] + 2) >> 2, reading 2n bytes from each row.
func boxShrink2xRow(dst, row0, row1 *byte, n int) {
	if n <= 0 {
		return
	}
	d := unsafe.Slice(dst, n)
	r0 := unsafe.Slice(row0, 2*n)
	r1 := unsafe.Slice(row1, 2*n)
	for i := range d {
		sum := int(r0[2*i]) + int(r0[2*i+1]) + int(r1[2*i]) + int(r1[2*i+1])
		d[i] = byte((sum + 2) >> 2)
	}
}
