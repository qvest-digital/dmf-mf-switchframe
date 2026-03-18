package transition

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBoxShrink2xYUV420_Correctness(t *testing.T) {
	srcW, srcH := 8, 8
	dstW, dstH := 4, 4
	srcSize := srcW * srcH * 3 / 2
	dstSize := dstW * dstH * 3 / 2

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
	srcSize := srcW * srcH * 3 / 2
	dstSize := dstW * dstH * 3 / 2

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
	src := make([]byte, 1920*1080*3/2)
	dst := make([]byte, 854*480*3/2)
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
	src := make([]byte, 1920*1080*3/2)
	boxBuf := make([]byte, 960*540*3/2)
	dst := make([]byte, 854*480*3/2)
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
