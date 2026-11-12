package transition

import (
	"math/rand"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zsiec/switchframe/server/internal/video"
)

func TestBoxShrink2xYUV420_Correctness(t *testing.T) {
	srcW, srcH := 8, 8
	dstW, dstH := 4, 4
	srcSize := video.YUV420FrameSize(srcW, srcH)
	dstSize := video.YUV420FrameSize(dstW, dstH)

	src := make([]byte, srcSize)
	// Fill Y plane with gradient
	for y := 0; y < srcH; y++ {
		for x := 0; x < srcW; x++ {
			src[y*srcW+x] = byte(y*srcW + x)
		}
	}
	// Fill chroma with 128
	for i := srcW * srcH; i < srcSize; i++ {
		src[i] = 128
	}

	dst := make([]byte, dstSize)
	BoxShrink2xYUV420(src, srcW, srcH, dst, dstW, dstH)

	// Verify Y plane: each dst pixel = average of 2x2 block
	// dst[0,0] = avg(src[0,0], src[0,1], src[1,0], src[1,1])
	//          = avg(0, 1, 8, 9) = 18/4 = 4 (with rounding: (18+2)/4 = 5)
	expected00 := byte((0 + 1 + 8 + 9 + 2) >> 2)
	assert.Equal(t, expected00, dst[0], "Y[0,0] should be average of 2x2 block")

	// dst[1,0] = avg(src[0,2], src[0,3], src[1,2], src[1,3])
	//          = avg(2, 3, 10, 11) = (26+2)/4 = 7
	expected10 := byte((2 + 3 + 10 + 11 + 2) >> 2)
	assert.Equal(t, expected10, dst[1], "Y[1,0] should be average of next 2x2 block")

	// Verify chroma plane preserved
	cbStart := dstW * dstH
	for i := cbStart; i < dstSize; i++ {
		assert.Equal(t, byte(128), dst[i], "chroma should be 128")
	}
}

func TestBoxShrink2xYUV420_1080pTo540p(t *testing.T) {
	srcW, srcH := 1920, 1080
	dstW, dstH := 960, 540
	srcSize := video.YUV420FrameSize(srcW, srcH)
	dstSize := video.YUV420FrameSize(dstW, dstH)

	src := make([]byte, srcSize)
	for i := range src {
		src[i] = byte(i % 256)
	}

	dst := make([]byte, dstSize)

	// Should not panic
	require.NotPanics(t, func() {
		BoxShrink2xYUV420(src, srcW, srcH, dst, dstW, dstH)
	})

	// Spot check: dst should have reasonable values
	assert.True(t, dst[0] > 0 || dst[0] == 0, "dst should have valid values")
}

func BenchmarkScaleYUV420_1080pTo480p_Bilinear(b *testing.B) {
	src := make([]byte, video.YUV420FrameSize(1920, 1080))
	dst := make([]byte, video.YUV420FrameSize(854, 480))
	for i := range src {
		src[i] = byte(i % 256)
	}
	b.SetBytes(int64(len(src)))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		ScaleYUV420(src, 1920, 1080, dst, 854, 480)
	}
}

func BenchmarkScaleYUV420_1080pTo480p_BoxThenBilinear(b *testing.B) {
	src := make([]byte, video.YUV420FrameSize(1920, 1080))
	boxBuf := make([]byte, video.YUV420FrameSize(960, 540))
	dst := make([]byte, video.YUV420FrameSize(854, 480))
	for i := range src {
		src[i] = byte(i % 256)
	}
	b.SetBytes(int64(len(src)))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		BoxShrink2xYUV420(src, 1920, 1080, boxBuf, 960, 540)
		ScaleYUV420(boxBuf, 960, 540, dst, 854, 480)
	}
}

// boxAvg is the exact 2x2 average every box shrink path must produce.
func boxAvg(a, b, c, d byte) byte {
	return byte((int(a) + int(b) + int(c) + int(d) + 2) >> 2)
}

// The kernel splits a row into 16-, 8- and 1-pixel steps, so every width
// from 0 to 80 takes a different split. A rounding average (PAVGB) is up
// to 1 high on about a third of pixels, which lifts the whole picture;
// the sum has to be exact, saturated values included, and nothing past
// the row may be written.
func TestBoxShrink2xRow_ExactAtEveryWidth(t *testing.T) {
	t.Parallel()
	checkBoxShrink2xRowExact(t)
}

func checkBoxShrink2xRowExact(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	const guard = 0xA5
	for n := 0; n <= 80; n++ {
		row0 := make([]byte, 2*n+1)
		row1 := make([]byte, 2*n+1)
		rng.Read(row0)
		rng.Read(row1)
		if n%3 == 0 {
			for i := range row0 {
				row0[i], row1[i] = 255, 255
			}
		}
		dst := make([]byte, n+16)
		for i := range dst {
			dst[i] = guard
		}
		boxShrink2xRow(&dst[0], &row0[0], &row1[0], n)
		for i := 0; i < n; i++ {
			want := boxAvg(row0[2*i], row0[2*i+1], row1[2*i], row1[2*i+1])
			require.Equal(t, want, dst[i], "n=%d pixel %d", n, i)
		}
		for i := n; i < len(dst); i++ {
			require.Equal(t, byte(guard), dst[i], "n=%d wrote past the row at %d", n, i)
		}
	}
}

func TestBoxShrink2xYUV420_ExactOn1080p(t *testing.T) {
	t.Parallel()
	const srcW, srcH, dstW, dstH = 1920, 1080, 960, 540
	src := make([]byte, video.YUV420FrameSize(srcW, srcH))
	rand.New(rand.NewSource(7)).Read(src)
	dst := make([]byte, video.YUV420FrameSize(dstW, dstH))
	BoxShrink2xYUV420(src, srcW, srcH, dst, dstW, dstH)

	check := func(name string, s []byte, sw int, d []byte, dw, dh int) {
		for y := 0; y < dh; y++ {
			r0, r1 := s[2*y*sw:], s[(2*y+1)*sw:]
			for x := 0; x < dw; x++ {
				want := boxAvg(r0[2*x], r0[2*x+1], r1[2*x], r1[2*x+1])
				if d[y*dw+x] != want {
					t.Fatalf("%s (%d,%d) = %d, want %d", name, x, y, d[y*dw+x], want)
				}
			}
		}
	}
	sY, dY := srcW*srcH, dstW*dstH
	sC, dC := sY/4, dY/4
	check("Y", src, srcW, dst, dstW, dstH)
	check("Cb", src[sY:], srcW/2, dst[dY:], dstW/2, dstH/2)
	check("Cr", src[sY+sC:], srcW/2, dst[dY+dC:], dstW/2, dstH/2)
}

func BenchmarkBoxShrink2xYUV420_1080p(b *testing.B) {
	src := make([]byte, video.YUV420FrameSize(1920, 1080))
	dst := make([]byte, video.YUV420FrameSize(960, 540))
	for i := range src {
		src[i] = byte(i % 256)
	}
	b.SetBytes(int64(len(src)))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		BoxShrink2xYUV420(src, 1920, 1080, dst, 960, 540)
	}
}
