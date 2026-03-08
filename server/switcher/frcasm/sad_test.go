package frcasm

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// --- Reference SAD implementation for cross-validation ---

func referenceSadBlock16x16(a, b []byte, aStride, bStride int) uint32 {
	var sad uint32
	for row := 0; row < 16; row++ {
		for col := 0; col < 16; col++ {
			av := int(a[row*aStride+col])
			bv := int(b[row*bStride+col])
			d := av - bv
			if d < 0 {
				d = -d
			}
			sad += uint32(d)
		}
	}
	return sad
}

func referenceSadRow(a, b []byte) uint64 {
	var sad uint64
	for i := range a {
		d := int(a[i]) - int(b[i])
		if d < 0 {
			d = -d
		}
		sad += uint64(d)
	}
	return sad
}

// --- SadBlock16x16 tests ---

func TestSadBlock16x16_Identical(t *testing.T) {
	// Same block should give SAD = 0
	block := make([]byte, 16*16)
	for i := range block {
		block[i] = byte(i % 256)
	}
	got := SadBlock16x16(&block[0], &block[0], 16, 16)
	require.Equal(t, uint32(0), got, "identical blocks should have SAD=0")
}

func TestSadBlock16x16_AllOnes(t *testing.T) {
	// Every byte differs by exactly 1 → SAD = 16*16*1 = 256
	a := make([]byte, 16*16)
	b := make([]byte, 16*16)
	for i := range a {
		a[i] = 100
		b[i] = 101
	}
	got := SadBlock16x16(&a[0], &b[0], 16, 16)
	require.Equal(t, uint32(256), got, "every pixel differs by 1")
}

func TestSadBlock16x16_MaxDiff(t *testing.T) {
	// A=0, B=255 → SAD = 16*16*255 = 65280
	a := make([]byte, 16*16)
	b := make([]byte, 16*16)
	for i := range b {
		b[i] = 255
	}
	got := SadBlock16x16(&a[0], &b[0], 16, 16)
	require.Equal(t, uint32(65280), got, "max difference: 0 vs 255")
}

func TestSadBlock16x16_KnownPattern(t *testing.T) {
	// Hand-calculated pattern: first row differs by 10, rest identical
	a := make([]byte, 16*16)
	b := make([]byte, 16*16)
	for i := range a {
		a[i] = 128
		b[i] = 128
	}
	// Make first row of b differ by 10
	for col := 0; col < 16; col++ {
		b[col] = 138
	}
	// Expected: 16 pixels * 10 diff = 160
	got := SadBlock16x16(&a[0], &b[0], 16, 16)
	expected := referenceSadBlock16x16(a, b, 16, 16)
	require.Equal(t, expected, got, "known pattern")
	require.Equal(t, uint32(160), got, "16 pixels * 10 diff = 160")
}

func TestSadBlock16x16_NonContiguous(t *testing.T) {
	// Blocks embedded in a larger frame (stride > 16)
	stride := 1920 // Full HD row width
	frameA := make([]byte, 16*stride)
	frameB := make([]byte, 16*stride)

	// Fill with known patterns — use values that won't overflow byte range
	for row := 0; row < 16; row++ {
		for col := 0; col < 16; col++ {
			frameA[row*stride+col] = 100
			frameB[row*stride+col] = 105 // differ by 5
		}
		// Fill some extra columns to verify stride handling
		for col := 16; col < 32; col++ {
			frameA[row*stride+col] = 200
			frameB[row*stride+col] = 0 // large diff that should NOT be included
		}
	}

	got := SadBlock16x16(&frameA[0], &frameB[0], stride, stride)
	expected := referenceSadBlock16x16(frameA, frameB, stride, stride)
	require.Equal(t, expected, got, "non-contiguous blocks with stride=%d", stride)
	require.Equal(t, uint32(16*16*5), got, "16x16 block with uniform diff of 5")
}

func TestSadBlock16x16_AsymmetricStrides(t *testing.T) {
	// Different strides for A and B
	aStride := 32
	bStride := 64
	frameA := make([]byte, 16*aStride)
	frameB := make([]byte, 16*bStride)

	for row := 0; row < 16; row++ {
		for col := 0; col < 16; col++ {
			frameA[row*aStride+col] = byte(row + col)
			frameB[row*bStride+col] = byte(row + col + 3)
		}
	}

	got := SadBlock16x16(&frameA[0], &frameB[0], aStride, bStride)
	expected := referenceSadBlock16x16(frameA, frameB, aStride, bStride)
	require.Equal(t, expected, got, "asymmetric strides A=%d B=%d", aStride, bStride)
}

func TestSadBlock16x16_CrossValidate(t *testing.T) {
	// Cross-validate assembly against reference with pseudo-random data
	a := make([]byte, 16*16)
	b := make([]byte, 16*16)

	// Use a deterministic pattern that exercises various byte values
	for i := range a {
		a[i] = byte((i*7 + 13) % 256)
		b[i] = byte((i*11 + 37) % 256)
	}

	got := SadBlock16x16(&a[0], &b[0], 16, 16)
	expected := referenceSadBlock16x16(a, b, 16, 16)
	require.Equal(t, expected, got, "cross-validation with pseudo-random data")
}

// --- SadRow tests ---

func TestSadRow_Identical(t *testing.T) {
	a := make([]byte, 100)
	for i := range a {
		a[i] = byte(i)
	}
	got := SadRow(&a[0], &a[0], 100)
	require.Equal(t, uint64(0), got, "identical rows should have SAD=0")
}

func TestSadRow_KnownDelta(t *testing.T) {
	// Every byte differs by 10, n=100 → SAD=1000
	a := make([]byte, 100)
	b := make([]byte, 100)
	for i := range a {
		a[i] = 50
		b[i] = 60
	}
	got := SadRow(&a[0], &b[0], 100)
	require.Equal(t, uint64(1000), got, "100 bytes each differing by 10")
}

func TestSadRow_VariousSizes(t *testing.T) {
	sizes := []int{1, 15, 16, 17, 31, 32, 33, 63, 64, 1920}
	for _, n := range sizes {
		t.Run("", func(t *testing.T) {
			a := make([]byte, n)
			b := make([]byte, n)
			for i := 0; i < n; i++ {
				a[i] = byte((i * 3) % 256)
				b[i] = byte((i * 5) % 256)
			}
			got := SadRow(&a[0], &b[0], n)
			expected := referenceSadRow(a, b)
			require.Equal(t, expected, got, "n=%d", n)
		})
	}
}

func TestSadRow_MaxDiff(t *testing.T) {
	// A=0, B=255, n=1920 → SAD = 1920*255 = 489600
	n := 1920
	a := make([]byte, n)
	b := make([]byte, n)
	for i := range b {
		b[i] = 255
	}
	got := SadRow(&a[0], &b[0], n)
	require.Equal(t, uint64(489600), got, "max diff: 0 vs 255, n=1920")
}

func TestSadRow_ReverseDiff(t *testing.T) {
	// Ensure |a-b| works both directions
	a := make([]byte, 32)
	b := make([]byte, 32)
	for i := range a {
		a[i] = 200
		b[i] = 50
	}
	got1 := SadRow(&a[0], &b[0], 32)
	got2 := SadRow(&b[0], &a[0], 32)
	require.Equal(t, got1, got2, "SAD should be symmetric")
	require.Equal(t, uint64(32*150), got1, "32 bytes each differing by 150")
}

func TestSadRow_LargeRow(t *testing.T) {
	// Test with a full 4K row width to verify no overflow
	n := 3840
	a := make([]byte, n)
	b := make([]byte, n)
	for i := 0; i < n; i++ {
		a[i] = byte((i * 7) % 256)
		b[i] = byte((i * 13) % 256)
	}
	got := SadRow(&a[0], &b[0], n)
	expected := referenceSadRow(a, b)
	require.Equal(t, expected, got, "4K row cross-validation")
}

// --- Benchmarks ---

func BenchmarkSadBlock16x16(b *testing.B) {
	blockA := make([]byte, 16*16)
	blockB := make([]byte, 16*16)
	for i := range blockA {
		blockA[i] = byte(i % 256)
		blockB[i] = byte((i * 3) % 256)
	}
	b.SetBytes(16 * 16 * 2) // bytes read from both blocks
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		SadBlock16x16(&blockA[0], &blockB[0], 16, 16)
	}
}

func BenchmarkSadRow_1920(b *testing.B) {
	n := 1920
	a := make([]byte, n)
	bb := make([]byte, n)
	for i := 0; i < n; i++ {
		a[i] = byte(i % 256)
		bb[i] = byte((i * 3) % 256)
	}
	b.SetBytes(int64(n * 2))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		SadRow(&a[0], &bb[0], n)
	}
}

func BenchmarkSadRow_960(b *testing.B) {
	n := 960
	a := make([]byte, n)
	bb := make([]byte, n)
	for i := 0; i < n; i++ {
		a[i] = byte(i % 256)
		bb[i] = byte((i * 3) % 256)
	}
	b.SetBytes(int64(n * 2))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		SadRow(&a[0], &bb[0], n)
	}
}
