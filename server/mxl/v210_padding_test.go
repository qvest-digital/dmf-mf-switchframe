package mxl

import (
	"bytes"
	"fmt"
	"testing"
)

// The packers zero each line past its last group up to the 128-byte
// stride, as the CUDA packers do, so a buffer reused across frames carries
// nothing of what it held into a grain. 1278 and 1280 leave 48 and 32
// bytes of padding, 2 and 4 most of the line, 720, 1920 and 3840 none.
func TestV210_LinePaddingIsZero(t *testing.T) {
	const h = 4
	for _, w := range []int{2, 4, 10, 720, 1278, 1280, 1918, 1920, 2048, 3840} {
		used := (w + 5) / 6 * 16
		stride := V210LineStride(w)
		for name, pack := range map[string]func(out []byte) error{
			"8bit":  func(out []byte) error { return YUV420pToV210Into(yuv420pRamp(w, h), out, w, h) },
			"10bit": func(out []byte) error { return YUV422P10ToV210Into(yuv422p10Ramp(w, h), out, w, h) },
		} {
			t.Run(fmt.Sprintf("%s/%d", name, w), func(t *testing.T) {
				out := bytes.Repeat([]byte{0xEE}, stride*h)
				if err := pack(out); err != nil {
					t.Fatal(err)
				}
				for row := 0; row < h; row++ {
					pad := out[row*stride+used : (row+1)*stride]
					if !bytes.Equal(pad, make([]byte, len(pad))) {
						t.Fatalf("row %d: %d bytes of padding not zero", row, len(pad))
					}
				}
			})
		}
	}
}
