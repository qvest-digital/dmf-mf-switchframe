package frcasm

import (
	"encoding/binary"
	"testing"

	"github.com/stretchr/testify/require"
)

// referenceDownsampleY10_2x computes the exact 2x box-filter downsample for 10-bit Y plane.
// Two-step: avg 2x2 block of uint16 → 10-bit average, then >>2 for 10→8 bit conversion.
func referenceDownsampleY10_2x(dst, src []byte, srcStride, dstW, dstH int) {
	for row := 0; row < dstH; row++ {
		srcRow0 := row * 2 * srcStride
		srcRow1 := srcRow0 + srcStride
		dstOff := row * dstW
		for col := 0; col < dstW; col++ {
			sc := col * 2 * 2 // 2 pixels × 2 bytes
			p00 := int(binary.LittleEndian.Uint16(src[srcRow0+sc:]))
			p10 := int(binary.LittleEndian.Uint16(src[srcRow0+sc+2:]))
			p01 := int(binary.LittleEndian.Uint16(src[srcRow1+sc:]))
			p11 := int(binary.LittleEndian.Uint16(src[srcRow1+sc+2:]))
			avg := (p00 + p10 + p01 + p11 + 2) >> 2
			dst[dstOff+col] = byte(avg >> 2)
		}
	}
}

func TestDownsampleY10_2x_Uniform(t *testing.T) {
	w, h := 64, 64
	srcStride := w * 2
	src := make([]byte, srcStride*h)
	for i := 0; i < w*h; i++ {
		binary.LittleEndian.PutUint16(src[i*2:], 512) // mid-range 10-bit
	}
	dstW, dstH := w/2, h/2
	dst := make([]byte, dstW*dstH)

	DownsampleY10_2x(&dst[0], &src[0], srcStride, dstW, dstH)

	expected := byte(512 >> 2) // 128
	for i, v := range dst {
		require.Equal(t, expected, v, "pixel %d should be %d", i, expected)
	}
}

func TestDownsampleY10_2x_AllZero(t *testing.T) {
	w, h := 32, 32
	srcStride := w * 2
	src := make([]byte, srcStride*h)
	dstW, dstH := w/2, h/2
	dst := make([]byte, dstW*dstH)

	DownsampleY10_2x(&dst[0], &src[0], srcStride, dstW, dstH)

	for i, v := range dst {
		require.Equal(t, byte(0), v, "pixel %d should be 0", i)
	}
}

func TestDownsampleY10_2x_AllMax(t *testing.T) {
	w, h := 32, 32
	srcStride := w * 2
	src := make([]byte, srcStride*h)
	for i := 0; i < w*h; i++ {
		binary.LittleEndian.PutUint16(src[i*2:], 1023)
	}
	dstW, dstH := w/2, h/2
	dst := make([]byte, dstW*dstH)

	DownsampleY10_2x(&dst[0], &src[0], srcStride, dstW, dstH)

	// 1023 >> 2 = 255
	for i, v := range dst {
		require.Equal(t, byte(255), v, "pixel %d should be 255", i)
	}
}

func TestDownsampleY10_2x_KnownPattern(t *testing.T) {
	// 4×2 source (uint16): [100, 200, 300, 400] / [500, 600, 700, 800]
	// Block 0: (100+200+500+600+2)/4 = 1402/4 = 350 → 350>>2 = 87
	// Block 1: (300+400+700+800+2)/4 = 2202/4 = 550 → 550>>2 = 137
	w, h := 4, 2
	srcStride := w * 2
	src := make([]byte, srcStride*h)
	vals := []uint16{100, 200, 300, 400, 500, 600, 700, 800}
	for i, v := range vals {
		binary.LittleEndian.PutUint16(src[i*2:], v)
	}

	dstW, dstH := 2, 1
	dst := make([]byte, dstW*dstH)
	DownsampleY10_2x(&dst[0], &src[0], srcStride, dstW, dstH)

	require.Equal(t, byte(87), dst[0], "pixel (0,0)")
	require.Equal(t, byte(137), dst[1], "pixel (1,0)")
}

func TestDownsampleY10_2x_MatchesReference(t *testing.T) {
	w, h := 1920, 1080
	srcStride := w * 2
	src := make([]byte, srcStride*h)
	for i := 0; i < w*h; i++ {
		binary.LittleEndian.PutUint16(src[i*2:], uint16((i*7+13)%1024))
	}

	dstW, dstH := w/2, h/2
	got := make([]byte, dstW*dstH)
	ref := make([]byte, dstW*dstH)

	DownsampleY10_2x(&got[0], &src[0], srcStride, dstW, dstH)
	referenceDownsampleY10_2x(ref, src, srcStride, dstW, dstH)

	for i := range got {
		require.Equal(t, ref[i], got[i],
			"pixel %d: got %d, ref %d", i, got[i], ref[i])
	}
}

func TestDownsampleY10_2x_OddTailWidth(t *testing.T) {
	// dstW=17, not a multiple of 8. Tests scalar tail handling.
	w, h := 34, 4
	srcStride := w * 2
	src := make([]byte, srcStride*h)
	for i := 0; i < w*h; i++ {
		binary.LittleEndian.PutUint16(src[i*2:], uint16((i*3+7)%1024))
	}

	dstW, dstH := w/2, h/2
	got := make([]byte, dstW*dstH)
	ref := make([]byte, dstW*dstH)

	DownsampleY10_2x(&got[0], &src[0], srcStride, dstW, dstH)
	referenceDownsampleY10_2x(ref, src, srcStride, dstW, dstH)

	for i := range got {
		require.Equal(t, ref[i], got[i],
			"pixel %d: got %d, ref %d", i, got[i], ref[i])
	}
}

func TestDownsampleY10_2x_SmallWidths(t *testing.T) {
	widths := []int{2, 4, 6, 8, 10, 14, 16, 18, 30}
	for _, w := range widths {
		h := 4
		srcStride := w * 2
		src := make([]byte, srcStride*h)
		for i := 0; i < w*h; i++ {
			binary.LittleEndian.PutUint16(src[i*2:], uint16((i*7+3)%1024))
		}
		dstW, dstH := w/2, h/2
		got := make([]byte, dstW*dstH)
		ref := make([]byte, dstW*dstH)

		DownsampleY10_2x(&got[0], &src[0], srcStride, dstW, dstH)
		referenceDownsampleY10_2x(ref, src, srcStride, dstW, dstH)

		for i := range got {
			require.Equal(t, ref[i], got[i],
				"w=%d pixel %d: got %d, ref %d", w, i, got[i], ref[i])
		}
	}
}

func BenchmarkDownsampleY10_2x_1080p(b *testing.B) {
	w, h := 1920, 1080
	srcStride := w * 2
	src := make([]byte, srcStride*h)
	for i := 0; i < w*h; i++ {
		binary.LittleEndian.PutUint16(src[i*2:], uint16(i%1024))
	}
	dstW, dstH := w/2, h/2
	dst := make([]byte, dstW*dstH)

	b.SetBytes(int64(w * h * 2))
	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		DownsampleY10_2x(&dst[0], &src[0], srcStride, dstW, dstH)
	}
}
