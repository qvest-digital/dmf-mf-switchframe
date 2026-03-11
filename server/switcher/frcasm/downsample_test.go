package frcasm

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// referenceDownsampleY2x computes exact 2× box-filter downsample: (a+b+c+d+2)/4.
func referenceDownsampleY2x(dst, src []byte, srcW, srcH int) {
	dstW := srcW / 2
	for row := 0; row < srcH/2; row++ {
		srcRow0 := row * 2 * srcW
		srcRow1 := srcRow0 + srcW
		dstOff := row * dstW
		for col := 0; col < dstW; col++ {
			sc := col * 2
			a := int(src[srcRow0+sc])
			b := int(src[srcRow0+sc+1])
			c := int(src[srcRow1+sc])
			d := int(src[srcRow1+sc+1])
			dst[dstOff+col] = byte((a + b + c + d + 2) / 4)
		}
	}
}

func TestDownsampleY2x_Uniform(t *testing.T) {
	// Uniform input: all pixels = 128, output should be exactly 128
	w, h := 64, 64
	src := make([]byte, w*h)
	for i := range src {
		src[i] = 128
	}
	dstW, dstH := w/2, h/2
	dst := make([]byte, dstW*dstH)

	DownsampleY2x(&dst[0], &src[0], w, h)

	for i, v := range dst {
		require.Equal(t, byte(128), v, "pixel %d should be 128", i)
	}
}

func TestDownsampleY2x_AllZero(t *testing.T) {
	w, h := 32, 32
	src := make([]byte, w*h)
	dst := make([]byte, (w/2)*(h/2))

	DownsampleY2x(&dst[0], &src[0], w, h)

	for i, v := range dst {
		require.Equal(t, byte(0), v, "pixel %d should be 0", i)
	}
}

func TestDownsampleY2x_AllMax(t *testing.T) {
	w, h := 32, 32
	src := make([]byte, w*h)
	for i := range src {
		src[i] = 255
	}
	dst := make([]byte, (w/2)*(h/2))

	DownsampleY2x(&dst[0], &src[0], w, h)

	for i, v := range dst {
		require.Equal(t, byte(255), v, "pixel %d should be 255", i)
	}
}

func TestDownsampleY2x_KnownPattern(t *testing.T) {
	// 4×2 source: [10, 20, 30, 40] / [50, 60, 70, 80]
	// Exact: pixel(0,0) = (10+20+50+60+2)/4 = 142/4 = 35
	// Exact: pixel(1,0) = (30+40+70+80+2)/4 = 222/4 = 55
	// PAVG cascaded:
	//   havg_r0_c0 = (10+20+1)>>1 = 15, havg_r1_c0 = (50+60+1)>>1 = 55
	//   final_c0 = (15+55+1)>>1 = 35  (matches exact for this case)
	//   havg_r0_c1 = (30+40+1)>>1 = 35, havg_r1_c1 = (70+80+1)>>1 = 75
	//   final_c1 = (35+75+1)>>1 = 55  (matches exact)
	w, h := 4, 2
	src := []byte{10, 20, 30, 40, 50, 60, 70, 80}
	dst := make([]byte, 2)

	DownsampleY2x(&dst[0], &src[0], w, h)

	require.Equal(t, byte(35), dst[0], "pixel (0,0)")
	require.Equal(t, byte(55), dst[1], "pixel (1,0)")
}

func TestDownsampleY2x_CrossValidate_1080p(t *testing.T) {
	w, h := 1920, 1080
	src := make([]byte, w*h)
	for i := range src {
		src[i] = byte((i*7 + 13) % 256)
	}

	dstW, dstH := w/2, h/2
	got := make([]byte, dstW*dstH)
	ref := make([]byte, dstW*dstH)

	DownsampleY2x(&got[0], &src[0], w, h)
	referenceDownsampleY2x(ref, src, w, h)

	// Allow ±1 tolerance for cascaded PAVG rounding vs exact (a+b+c+d+2)/4
	maxDiff := 0
	for i := range got {
		diff := int(got[i]) - int(ref[i])
		if diff < 0 {
			diff = -diff
		}
		if diff > maxDiff {
			maxDiff = diff
		}
		require.LessOrEqual(t, diff, 1,
			"pixel %d: got %d, ref %d (diff %d)", i, got[i], ref[i], diff)
	}
	t.Logf("max diff from exact: %d (expected ≤1 for cascaded PAVG rounding)", maxDiff)
}

func TestDownsampleY2x_CrossValidate_Sizes(t *testing.T) {
	sizes := [][2]int{
		{32, 32},    // small, dstW=16 (exactly 1 NEON iteration)
		{64, 48},    // small, dstW=32 (2 iterations)
		{640, 480},  // 480p
		{1280, 720}, // 720p
		{1920, 1080}, // 1080p
	}
	for _, sz := range sizes {
		w, h := sz[0], sz[1]
		t.Run("", func(t *testing.T) {
			src := make([]byte, w*h)
			for i := range src {
				src[i] = byte((i*11 + 37) % 256)
			}
			dstW, dstH := w/2, h/2
			got := make([]byte, dstW*dstH)
			ref := make([]byte, dstW*dstH)

			DownsampleY2x(&got[0], &src[0], w, h)
			referenceDownsampleY2x(ref, src, w, h)

			for i := range got {
				diff := int(got[i]) - int(ref[i])
				if diff < 0 {
					diff = -diff
				}
				require.LessOrEqual(t, diff, 1,
					"%dx%d pixel %d: got %d, ref %d", w, h, i, got[i], ref[i])
			}
		})
	}
}

func TestDownsampleY2x_OddTailWidth(t *testing.T) {
	// dstW=17, not a multiple of 16. Tests scalar tail handling.
	w, h := 34, 4
	src := make([]byte, w*h)
	for i := range src {
		src[i] = byte((i*3 + 7) % 256)
	}
	dstW, dstH := w/2, h/2
	got := make([]byte, dstW*dstH)
	ref := make([]byte, dstW*dstH)

	DownsampleY2x(&got[0], &src[0], w, h)
	referenceDownsampleY2x(ref, src, w, h)

	for i := range got {
		diff := int(got[i]) - int(ref[i])
		if diff < 0 {
			diff = -diff
		}
		require.LessOrEqual(t, diff, 1,
			"pixel %d: got %d, ref %d", i, got[i], ref[i])
	}
}

func TestDownsampleY2x_SmallWidths(t *testing.T) {
	// Widths smaller than SIMD register width (all scalar tail)
	widths := []int{2, 4, 6, 8, 10, 14, 16, 18, 30}
	for _, w := range widths {
		h := 4
		t.Run("", func(t *testing.T) {
			src := make([]byte, w*h)
			for i := range src {
				src[i] = byte((i*7 + 3) % 256)
			}
			dstW, dstH := w/2, h/2
			got := make([]byte, dstW*dstH)
			ref := make([]byte, dstW*dstH)

			DownsampleY2x(&got[0], &src[0], w, h)
			referenceDownsampleY2x(ref, src, w, h)

			for i := range got {
				diff := int(got[i]) - int(ref[i])
				if diff < 0 {
					diff = -diff
				}
				require.LessOrEqual(t, diff, 1,
					"w=%d pixel %d: got %d, ref %d", w, i, got[i], ref[i])
			}
		})
	}
}

func BenchmarkDownsampleY2x_1080p(b *testing.B) {
	w, h := 1920, 1080
	src := make([]byte, w*h)
	for i := range src {
		src[i] = byte(i % 256)
	}
	dst := make([]byte, (w/2)*(h/2))

	b.SetBytes(int64(w * h))
	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		DownsampleY2x(&dst[0], &src[0], w, h)
	}
}

func BenchmarkDownsampleY2x_720p(b *testing.B) {
	w, h := 1280, 720
	src := make([]byte, w*h)
	for i := range src {
		src[i] = byte(i % 256)
	}
	dst := make([]byte, (w/2)*(h/2))

	b.SetBytes(int64(w * h))
	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		DownsampleY2x(&dst[0], &src[0], w, h)
	}
}
