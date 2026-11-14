package mxl

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"testing"
)

// v210 carries any even width: a row ends in a partial 6-pixel group when
// the width is not a multiple of six, padded like every row to 128 bytes.

var v210Widths = []int{2, 4, 6, 8, 10, 14, 16, 22, 1280, 1918, 1920, 3840}

// yuv422p10Ramp is a YUV422P10LE frame whose every sample differs from its
// neighbours, so a sample written to the wrong place shows.
func yuv422p10Ramp(w, h int) []byte {
	out := make([]byte, w*h*4)
	put := func(i, v int) { binary.LittleEndian.PutUint16(out[2*i:], uint16(v&0x3FF)) }
	for i := 0; i < w*h; i++ {
		put(i, 64+i*7)
	}
	for i := 0; i < w/2*h; i++ {
		put(w*h+i, 200+i*5)
		put(w*h+w/2*h+i, 700+i*3)
	}
	return out
}

// yuv420pRamp is the same for 8-bit 4:2:0.
func yuv420pRamp(w, h int) []byte {
	out := make([]byte, w*h+2*(w/2)*(h/2))
	for i := range out {
		out[i] = byte(16 + i*7)
	}
	return out
}

// The layout of a partial group, as ffmpeg's v210 encoder writes it from
// a YUV422P10LE frame whose row n holds Y 100+i, Cb 300+i and Cr 600+i:
// the samples of the pixels past the width are zero.
func TestV210_PartialGroupLayout(t *testing.T) {
	word := func(a, b, c uint32) uint32 { return a | b<<10 | c<<20 }
	for _, tc := range []struct {
		width int
		want  []uint32
	}{
		{8, []uint32{word(300, 100, 600), word(101, 301, 102), word(601, 103, 302), word(104, 602, 105),
			word(303, 106, 603), word(107, 0, 0), 0, 0}},
		{10, []uint32{word(300, 100, 600), word(101, 301, 102), word(601, 103, 302), word(104, 602, 105),
			word(303, 106, 603), word(107, 304, 108), word(604, 109, 0), 0}},
	} {
		t.Run(fmt.Sprint(tc.width), func(t *testing.T) {
			w, h := tc.width, 2
			yuv := make([]byte, w*h*4)
			put := func(i, v int) { binary.LittleEndian.PutUint16(yuv[2*i:], uint16(v)) }
			for row := 0; row < h; row++ {
				for i := 0; i < w; i++ {
					put(row*w+i, 100+i)
				}
				for i := 0; i < w/2; i++ {
					put(w*h+row*w/2+i, 300+i)
					put(w*h+w/2*h+row*w/2+i, 600+i)
				}
			}
			for name, pack := range map[string]func([]byte, int, int) ([]byte, error){
				"go": YUV422P10ToV210, "simd": yuv422P10ToV210SIMD,
			} {
				v210, err := pack(yuv, w, h)
				if err != nil {
					t.Fatal(err)
				}
				for row := 0; row < h; row++ {
					line := v210[row*V210LineStride(w) : (row+1)*V210LineStride(w)]
					for i, want := range tc.want {
						if got := binary.LittleEndian.Uint32(line[4*i:]); got != want {
							t.Fatalf("%s: row %d word %d = %#x, want %#x", name, row, i, got, want)
						}
					}
					if !bytes.Equal(line[4*len(tc.want):], make([]byte, len(line)-4*len(tc.want))) {
						t.Fatalf("%s: row %d padding is not zero", name, row)
					}
				}
			}
		})
	}
}

func TestV210_RoundTrip10bitAnyWidth(t *testing.T) {
	const h = 4
	for _, w := range v210Widths {
		t.Run(fmt.Sprint(w), func(t *testing.T) {
			in := yuv422p10Ramp(w, h)
			v210, err := YUV422P10ToV210(in, w, h)
			if err != nil {
				t.Fatal(err)
			}
			simd, err := yuv422P10ToV210SIMD(in, w, h)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(v210, simd) {
				t.Fatal("SIMD pack differs from the Go pack")
			}
			out, err := V210ToYUV422P10(v210, V210LineStride(w), w, h)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(out, in) {
				t.Fatal("10-bit round trip changed the frame")
			}
			outSIMD, err := v210ToYUV422P10SIMD(v210, V210LineStride(w), w, h)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(outSIMD, in) {
				t.Fatal("SIMD unpack differs from the frame")
			}
		})
	}
}

func TestV210_RoundTrip8bitAnyWidth(t *testing.T) {
	const h = 4
	for _, w := range v210Widths {
		t.Run(fmt.Sprint(w), func(t *testing.T) {
			in := yuv420pRamp(w, h)
			v210, err := YUV420pToV210(in, w, h)
			if err != nil {
				t.Fatal(err)
			}
			// The 8-bit pack widens as the 10-bit one stores: both
			// packers agree on the frame the 8-bit one stands for.
			p10 := make([]byte, w*h*4)
			for i := 0; i < w*h; i++ {
				binary.LittleEndian.PutUint16(p10[2*i:], uint16(expand8to10(in[i])))
			}
			cw := w / 2
			for row := 0; row < h; row++ {
				for i := 0; i < cw; i++ {
					cb, cr := in[w*h+(row/2)*cw+i], in[w*h+cw*(h/2)+(row/2)*cw+i]
					binary.LittleEndian.PutUint16(p10[2*(w*h+row*cw+i):], uint16(expand8to10(cb)))
					binary.LittleEndian.PutUint16(p10[2*(w*h+cw*h+row*cw+i):], uint16(expand8to10(cr)))
				}
			}
			want, err := YUV422P10ToV210(p10, w, h)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(v210, want) {
				t.Fatal("8-bit pack differs from the 10-bit pack of the same frame")
			}
			out, err := V210ToYUV420p(v210, w, h)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(out, in) {
				t.Fatal("8-bit round trip changed the frame")
			}
		})
	}
}

// A row's whole groups are packed by the row kernels whatever follows
// them: the 1918-pixel frame's first 319 groups are the 1920-pixel
// frame's.
func TestV210_WholeGroupsUnchangedByATail(t *testing.T) {
	const h = 2
	full := yuv420pRamp(1920, h)
	crop := make([]byte, 0, 1918*h+2*959*(h/2))
	for row := 0; row < h; row++ {
		crop = append(crop, full[row*1920:row*1920+1918]...)
	}
	for _, plane := range []int{1920 * h, 1920*h + 960*(h/2)} {
		for row := 0; row < h/2; row++ {
			crop = append(crop, full[plane+row*960:plane+row*960+959]...)
		}
	}
	a, err := YUV420pToV210(full, 1920, h)
	if err != nil {
		t.Fatal(err)
	}
	b, err := YUV420pToV210(crop, 1918, h)
	if err != nil {
		t.Fatal(err)
	}
	stride := V210LineStride(1920)
	for row := 0; row < h; row++ {
		if !bytes.Equal(a[row*stride:row*stride+319*16], b[row*stride:row*stride+319*16]) {
			t.Fatalf("row %d: whole groups differ", row)
		}
	}
}

func TestV210_RejectsAnOddWidth(t *testing.T) {
	for _, w := range []int{0, 1, 7, 1919} {
		if _, err := YUV420pToV210(make([]byte, 1<<16), w, 2); err == nil {
			t.Errorf("YUV420pToV210 width %d: no error", w)
		}
		if _, err := V210ToYUV420p(make([]byte, 1<<16), w, 2); err == nil {
			t.Errorf("V210ToYUV420p width %d: no error", w)
		}
		if _, err := YUV422P10ToV210(make([]byte, 1<<16), w, 2); err == nil {
			t.Errorf("YUV422P10ToV210 width %d: no error", w)
		}
	}
}
