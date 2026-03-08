package transition

import (
	"math/rand"
	"testing"

	"github.com/stretchr/testify/require"
)

// --- downsampleAlpha2x2 kernel tests ---

func TestDownsampleAlpha2x2_Basic(t *testing.T) {
	t.Parallel()
	// 4 input bytes per row (2 pairs), known values.
	// pair 0: row0[0]=10, row0[1]=20, row1[0]=30, row1[1]=40 → (10+20+30+40+2)/4 = 102/4 = 25
	// pair 1: row0[2]=100, row0[3]=200, row1[2]=50, row1[3]=150 → (100+200+50+150+2)/4 = 502/4 = 125
	row0 := []byte{10, 20, 100, 200}
	row1 := []byte{30, 40, 50, 150}
	dst := make([]byte, 2)
	downsampleAlpha2x2(&dst[0], &row0[0], &row1[0], 2)
	require.Equal(t, byte(25), dst[0], "pair 0")
	require.Equal(t, byte(125), dst[1], "pair 1")
}

func TestDownsampleAlpha2x2_AllZero(t *testing.T) {
	t.Parallel()
	row0 := make([]byte, 8)
	row1 := make([]byte, 8)
	dst := make([]byte, 4)
	downsampleAlpha2x2(&dst[0], &row0[0], &row1[0], 4)
	for i := range dst {
		require.Equal(t, byte(0), dst[i], "pixel %d", i)
	}
}

func TestDownsampleAlpha2x2_AllMax(t *testing.T) {
	t.Parallel()
	row0 := make([]byte, 8)
	row1 := make([]byte, 8)
	for i := range row0 {
		row0[i] = 255
		row1[i] = 255
	}
	dst := make([]byte, 4)
	downsampleAlpha2x2(&dst[0], &row0[0], &row1[0], 4)
	for i := range dst {
		require.Equal(t, byte(255), dst[i], "pixel %d", i)
	}
}

func TestDownsampleAlpha2x2_CrossValidation(t *testing.T) {
	t.Parallel()
	// Compare against the original downsampleAlphaToChroma scalar loop
	// at 1080p resolution. Allow +/-1 tolerance since SIMD VPAVGB/URHADD
	// rounding may differ slightly from exact (a+b+c+d+2)/4.
	w := 1920
	h := 1080
	chromaW := w / 2
	chromaH := h / 2

	rng := rand.New(rand.NewSource(42))
	alpha := make([]byte, w*h)
	for i := range alpha {
		alpha[i] = byte(rng.Intn(256))
	}

	// Reference: exact scalar computation
	ref := make([]byte, chromaW*chromaH)
	for cy := 0; cy < chromaH; cy++ {
		for cx := 0; cx < chromaW; cx++ {
			ly := cy * 2
			lx := cx * 2
			a00 := int(alpha[ly*w+lx])
			a10 := int(alpha[ly*w+lx+1])
			a01 := int(alpha[(ly+1)*w+lx])
			a11 := int(alpha[(ly+1)*w+lx+1])
			ref[cy*chromaW+cx] = byte((a00 + a10 + a01 + a11 + 2) / 4)
		}
	}

	// SIMD kernel: process row by row (same as the modified downsampleAlphaToChroma)
	simd := make([]byte, chromaW*chromaH)
	for cy := 0; cy < chromaH; cy++ {
		ly := cy * 2
		row0 := alpha[ly*w:]
		row1 := alpha[(ly+1)*w:]
		downsampleAlpha2x2(&simd[cy*chromaW], &row0[0], &row1[0], chromaW)
	}

	mismatches := 0
	for i := range ref {
		diff := int(ref[i]) - int(simd[i])
		if diff < -1 || diff > 1 {
			mismatches++
			if mismatches <= 10 {
				t.Errorf("pixel %d: ref=%d, simd=%d (diff=%d)", i, ref[i], simd[i], diff)
			}
		}
	}
	if mismatches > 0 {
		t.Errorf("total mismatches (>1 tolerance): %d / %d", mismatches, len(ref))
	}
}

func TestDownsampleAlpha2x2_OddPairs(t *testing.T) {
	t.Parallel()
	// Test with pairs=1,3,7,15,17 to exercise tail handling.
	pairCounts := []int{1, 3, 7, 15, 17}
	for _, pairs := range pairCounts {
		inputSize := pairs * 2
		row0 := make([]byte, inputSize)
		row1 := make([]byte, inputSize)
		for i := range row0 {
			row0[i] = byte((i*13 + 7) % 256)
			row1[i] = byte((i*17 + 3) % 256)
		}
		dst := make([]byte, pairs)
		downsampleAlpha2x2(&dst[0], &row0[0], &row1[0], pairs)

		for i := 0; i < pairs; i++ {
			a00 := int(row0[2*i])
			a10 := int(row0[2*i+1])
			a01 := int(row1[2*i])
			a11 := int(row1[2*i+1])
			exact := byte((a00 + a10 + a01 + a11 + 2) / 4)
			diff := int(dst[i]) - int(exact)
			if diff < -1 || diff > 1 {
				t.Errorf("pairs=%d pixel=%d: got=%d, want=%d (diff=%d)", pairs, i, dst[i], exact, diff)
			}
		}
	}
}

func TestDownsampleAlpha2x2_ZeroPairs(t *testing.T) {
	t.Parallel()
	// Should not panic with zero pairs.
	downsampleAlpha2x2(nil, nil, nil, 0)
}

func TestDownsampleAlpha2x2_ExactScalarMatch(t *testing.T) {
	t.Parallel()
	// Verify that the scalar tail path matches exact arithmetic.
	// Use values where VPAVGB rounding matches exact: all four values equal.
	pairs := 5
	row0 := make([]byte, pairs*2)
	row1 := make([]byte, pairs*2)
	for i := 0; i < pairs; i++ {
		v := byte(i * 50)
		row0[2*i] = v
		row0[2*i+1] = v
		row1[2*i] = v
		row1[2*i+1] = v
	}
	dst := make([]byte, pairs)
	downsampleAlpha2x2(&dst[0], &row0[0], &row1[0], pairs)
	for i := 0; i < pairs; i++ {
		expected := byte(i * 50) // avg of 4 identical values = the value itself
		require.Equal(t, expected, dst[i], "pair %d", i)
	}
}

// --- Benchmark ---

func BenchmarkDownsampleAlpha2x2_1080p(b *testing.B) {
	pairs := 960 // 1920/2
	row0 := make([]byte, pairs*2)
	row1 := make([]byte, pairs*2)
	dst := make([]byte, pairs)
	fillTestPattern(row0)
	fillTestPattern(row1)

	b.SetBytes(int64(pairs))
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		downsampleAlpha2x2(&dst[0], &row0[0], &row1[0], pairs)
	}
}

func BenchmarkDownsampleAlphaToChroma_1080p(b *testing.B) {
	w := 1920
	h := 1080
	alpha := make([]byte, w*h)
	dst := make([]byte, (w/2)*(h/2))
	fillTestPattern(alpha)

	b.SetBytes(int64(w * h))
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		downsampleAlphaToChroma(alpha, w, h, dst)
	}
}
