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

// referenceHpelH computes horizontal half-pel SAD using PAVGB/URHADD rounding.
func referenceHpelH(cur, ref []byte, curStride, refStride int) uint32 {
	var sad uint32
	for row := 0; row < 16; row++ {
		for col := 0; col < 16; col++ {
			a := int(ref[row*refStride+col])
			b := int(ref[row*refStride+col+1])
			interp := (a + b + 1) >> 1
			d := int(cur[row*curStride+col]) - interp
			if d < 0 {
				d = -d
			}
			sad += uint32(d)
		}
	}
	return sad
}

// referenceHpelV computes vertical half-pel SAD using PAVGB/URHADD rounding.
func referenceHpelV(cur, ref []byte, curStride, refStride int) uint32 {
	var sad uint32
	for row := 0; row < 16; row++ {
		for col := 0; col < 16; col++ {
			a := int(ref[row*refStride+col])
			b := int(ref[(row+1)*refStride+col])
			interp := (a + b + 1) >> 1
			d := int(cur[row*curStride+col]) - interp
			if d < 0 {
				d = -d
			}
			sad += uint32(d)
		}
	}
	return sad
}

// referenceHpelD computes diagonal half-pel SAD using cascaded PAVGB/URHADD rounding.
// Uses avg(avg(a,b), avg(c,d)) to match hardware behavior.
func referenceHpelD(cur, ref []byte, curStride, refStride int) uint32 {
	var sad uint32
	for row := 0; row < 16; row++ {
		for col := 0; col < 16; col++ {
			a := int(ref[row*refStride+col])
			b := int(ref[row*refStride+col+1])
			c := int(ref[(row+1)*refStride+col])
			d := int(ref[(row+1)*refStride+col+1])
			avgTop := (a + b + 1) >> 1
			avgBot := (c + d + 1) >> 1
			interp := (avgTop + avgBot + 1) >> 1
			diff := int(cur[row*curStride+col]) - interp
			if diff < 0 {
				diff = -diff
			}
			sad += uint32(diff)
		}
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

// --- SadBlock16x16HpelH tests ---

func TestHpelH_Identical(t *testing.T) {
	// ref is uniform → avg(val, val) = val → SAD vs identical cur = 0
	stride := 32 // need 17 bytes per row, use 32 for alignment
	cur := make([]byte, 16*stride)
	ref := make([]byte, 16*stride)
	for row := 0; row < 16; row++ {
		for col := 0; col < 17; col++ {
			cur[row*stride+col] = 128
			ref[row*stride+col] = 128
		}
	}
	got := SadBlock16x16HpelH(&cur[0], &ref[0], stride, stride)
	require.Equal(t, uint32(0), got, "identical uniform blocks should give SAD=0")
}

func TestHpelH_KnownPattern(t *testing.T) {
	// ref[x]=100, ref[x+1]=200 → interp = (100+200+1)>>1 = 150
	// cur = 150 → SAD = 0
	stride := 32
	cur := make([]byte, 16*stride)
	ref := make([]byte, 16*stride)
	for row := 0; row < 16; row++ {
		for col := 0; col < 16; col++ {
			ref[row*stride+col] = 100
			ref[row*stride+col+1] = 200 // will be overwritten for col < 15, that's fine
			cur[row*stride+col] = 150
		}
		// Fix: ref needs alternating 100, 200 for the whole row
		for col := 0; col < 17; col++ {
			if col%2 == 0 {
				ref[row*stride+col] = 100
			} else {
				ref[row*stride+col] = 200
			}
		}
	}
	got := SadBlock16x16HpelH(&cur[0], &ref[0], stride, stride)
	expected := referenceHpelH(cur, ref, stride, stride)
	require.Equal(t, expected, got, "known horizontal half-pel pattern")
}

func TestHpelH_CrossValidate(t *testing.T) {
	stride := 1920
	cur := make([]byte, 16*stride)
	ref := make([]byte, 16*stride)
	for i := range cur {
		cur[i] = byte((i*7 + 13) % 256)
		ref[i] = byte((i*11 + 37) % 256)
	}
	got := SadBlock16x16HpelH(&cur[0], &ref[0], stride, stride)
	expected := referenceHpelH(cur, ref, stride, stride)
	require.Equal(t, expected, got, "HpelH cross-validation with pseudo-random data")
}

func TestHpelH_NonContiguous(t *testing.T) {
	stride := 1920
	cur := make([]byte, 16*stride)
	ref := make([]byte, 16*stride)
	for row := 0; row < 16; row++ {
		for col := 0; col < 17; col++ {
			cur[row*stride+col] = byte(row*16 + col)
			ref[row*stride+col] = byte((row*16 + col + 5) % 256)
		}
		// Poison bytes beyond block that should not be touched
		for col := 18; col < 32; col++ {
			cur[row*stride+col] = 255
			ref[row*stride+col] = 0
		}
	}
	got := SadBlock16x16HpelH(&cur[0], &ref[0], stride, stride)
	expected := referenceHpelH(cur, ref, stride, stride)
	require.Equal(t, expected, got, "HpelH non-contiguous stride=%d", stride)
}

// --- SadBlock16x16HpelV tests ---

func TestHpelV_Identical(t *testing.T) {
	stride := 32
	cur := make([]byte, 17*stride) // 17 rows for ref
	ref := make([]byte, 17*stride)
	for row := 0; row < 17; row++ {
		for col := 0; col < 16; col++ {
			if row < 16 {
				cur[row*stride+col] = 128
			}
			ref[row*stride+col] = 128
		}
	}
	got := SadBlock16x16HpelV(&cur[0], &ref[0], stride, stride)
	require.Equal(t, uint32(0), got, "identical uniform blocks should give SAD=0")
}

func TestHpelV_KnownPattern(t *testing.T) {
	// ref row Y = 100, ref row Y+1 = 200 → interp = 150
	stride := 32
	cur := make([]byte, 17*stride)
	ref := make([]byte, 17*stride)
	for row := 0; row < 17; row++ {
		for col := 0; col < 16; col++ {
			if row%2 == 0 {
				ref[row*stride+col] = 100
			} else {
				ref[row*stride+col] = 200
			}
			if row < 16 {
				cur[row*stride+col] = 150
			}
		}
	}
	got := SadBlock16x16HpelV(&cur[0], &ref[0], stride, stride)
	expected := referenceHpelV(cur, ref, stride, stride)
	require.Equal(t, expected, got, "known vertical half-pel pattern")
}

func TestHpelV_CrossValidate(t *testing.T) {
	stride := 1920
	cur := make([]byte, 17*stride)
	ref := make([]byte, 17*stride)
	for i := range cur {
		cur[i] = byte((i*7 + 13) % 256)
	}
	for i := range ref {
		ref[i] = byte((i*11 + 37) % 256)
	}
	got := SadBlock16x16HpelV(&cur[0], &ref[0], stride, stride)
	expected := referenceHpelV(cur, ref, stride, stride)
	require.Equal(t, expected, got, "HpelV cross-validation with pseudo-random data")
}

func TestHpelV_NonContiguous(t *testing.T) {
	stride := 1920
	cur := make([]byte, 17*stride)
	ref := make([]byte, 17*stride)
	for row := 0; row < 17; row++ {
		for col := 0; col < 16; col++ {
			ref[row*stride+col] = byte((row*16 + col + 5) % 256)
			if row < 16 {
				cur[row*stride+col] = byte(row*16 + col)
			}
		}
	}
	got := SadBlock16x16HpelV(&cur[0], &ref[0], stride, stride)
	expected := referenceHpelV(cur, ref, stride, stride)
	require.Equal(t, expected, got, "HpelV non-contiguous stride=%d", stride)
}

// --- SadBlock16x16HpelD tests ---

func TestHpelD_Identical(t *testing.T) {
	stride := 32
	cur := make([]byte, 17*stride)
	ref := make([]byte, 17*stride)
	for row := 0; row < 17; row++ {
		for col := 0; col < 17; col++ {
			ref[row*stride+col] = 128
			if row < 16 && col < 16 {
				cur[row*stride+col] = 128
			}
		}
	}
	got := SadBlock16x16HpelD(&cur[0], &ref[0], stride, stride)
	require.Equal(t, uint32(0), got, "identical uniform blocks should give SAD=0")
}

func TestHpelD_CrossValidate(t *testing.T) {
	stride := 1920
	cur := make([]byte, 17*stride)
	ref := make([]byte, 17*stride)
	for i := range cur {
		cur[i] = byte((i*7 + 13) % 256)
	}
	for i := range ref {
		ref[i] = byte((i*11 + 37) % 256)
	}
	got := SadBlock16x16HpelD(&cur[0], &ref[0], stride, stride)
	expected := referenceHpelD(cur, ref, stride, stride)
	require.Equal(t, expected, got, "HpelD cross-validation with pseudo-random data")
}

func TestHpelD_NonContiguous(t *testing.T) {
	stride := 1920
	cur := make([]byte, 17*stride)
	ref := make([]byte, 17*stride)
	for row := 0; row < 17; row++ {
		for col := 0; col < 17; col++ {
			ref[row*stride+col] = byte((row*17 + col + 3) % 256)
			if row < 16 && col < 16 {
				cur[row*stride+col] = byte(row*16 + col)
			}
		}
	}
	got := SadBlock16x16HpelD(&cur[0], &ref[0], stride, stride)
	expected := referenceHpelD(cur, ref, stride, stride)
	require.Equal(t, expected, got, "HpelD non-contiguous stride=%d", stride)
}

func TestHpelD_KnownCorners(t *testing.T) {
	// All ref pixels = 0, cur = 0 → SAD = 0
	stride := 32
	cur := make([]byte, 17*stride)
	ref := make([]byte, 17*stride)
	got := SadBlock16x16HpelD(&cur[0], &ref[0], stride, stride)
	require.Equal(t, uint32(0), got, "all-zero blocks")

	// All ref pixels = 255, cur = 255 → interp = avg(avg(255,255),avg(255,255)) = 255 → SAD = 0
	for i := range cur {
		cur[i] = 255
	}
	for i := range ref {
		ref[i] = 255
	}
	got = SadBlock16x16HpelD(&cur[0], &ref[0], stride, stride)
	require.Equal(t, uint32(0), got, "all-255 blocks")
}

func TestHpelD_Asymmetric(t *testing.T) {
	// Test with different cur/ref strides
	curStride := 32
	refStride := 64
	cur := make([]byte, 16*curStride)
	ref := make([]byte, 17*refStride)
	for row := 0; row < 17; row++ {
		for col := 0; col < 17; col++ {
			ref[row*refStride+col] = byte((row*17 + col*3) % 256)
			if row < 16 && col < 16 {
				cur[row*curStride+col] = byte((row*16 + col*5) % 256)
			}
		}
	}
	got := SadBlock16x16HpelD(&cur[0], &ref[0], curStride, refStride)
	expected := referenceHpelD(cur, ref, curStride, refStride)
	require.Equal(t, expected, got, "HpelD asymmetric strides cur=%d ref=%d", curStride, refStride)
}

// --- Half-pel benchmarks ---

func BenchmarkSadBlock16x16HpelH(b *testing.B) {
	stride := 1920
	cur := make([]byte, 16*stride)
	ref := make([]byte, 16*stride)
	for i := range cur {
		cur[i] = byte(i % 256)
		ref[i] = byte((i * 3) % 256)
	}
	b.SetBytes(16 * 16 * 2)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		SadBlock16x16HpelH(&cur[0], &ref[0], stride, stride)
	}
}

func BenchmarkSadBlock16x16HpelV(b *testing.B) {
	stride := 1920
	cur := make([]byte, 17*stride)
	ref := make([]byte, 17*stride)
	for i := range cur {
		cur[i] = byte(i % 256)
	}
	for i := range ref {
		ref[i] = byte((i * 3) % 256)
	}
	b.SetBytes(16 * 16 * 2)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		SadBlock16x16HpelV(&cur[0], &ref[0], stride, stride)
	}
}

func BenchmarkSadBlock16x16HpelD(b *testing.B) {
	stride := 1920
	cur := make([]byte, 17*stride)
	ref := make([]byte, 17*stride)
	for i := range cur {
		cur[i] = byte(i % 256)
	}
	for i := range ref {
		ref[i] = byte((i * 3) % 256)
	}
	b.SetBytes(16 * 16 * 2)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		SadBlock16x16HpelD(&cur[0], &ref[0], stride, stride)
	}
}

// --- SadBlock16x16x4 tests ---

// referenceSadBlock16x16x4 computes 4 SADs independently using the scalar reference.
func referenceSadBlock16x16x4(cur []byte, refs [4][]byte, curStride, refStride int) [4]uint32 {
	var result [4]uint32
	for i := 0; i < 4; i++ {
		result[i] = referenceSadBlock16x16(cur, refs[i], curStride, refStride)
	}
	return result
}

func TestSadBlock16x16x4_Identical(t *testing.T) {
	block := make([]byte, 16*16)
	for i := range block {
		block[i] = byte(i % 256)
	}
	refs := [4]*byte{&block[0], &block[0], &block[0], &block[0]}
	got := SadBlock16x16x4(&block[0], refs, 16, 16)
	require.Equal(t, [4]uint32{0, 0, 0, 0}, got, "all refs identical to cur should give all zeros")
}

func TestSadBlock16x16x4_KnownDiffs(t *testing.T) {
	cur := make([]byte, 16*16)
	for i := range cur {
		cur[i] = 100
	}
	// 4 reference blocks with different per-pixel offsets
	refs := [4][]byte{
		make([]byte, 16*16), // diff=1 → SAD=256
		make([]byte, 16*16), // diff=5 → SAD=1280
		make([]byte, 16*16), // diff=10 → SAD=2560
		make([]byte, 16*16), // diff=50 → SAD=12800
	}
	for i := range refs[0] {
		refs[0][i] = 101
		refs[1][i] = 105
		refs[2][i] = 110
		refs[3][i] = 150
	}
	refPtrs := [4]*byte{&refs[0][0], &refs[1][0], &refs[2][0], &refs[3][0]}
	got := SadBlock16x16x4(&cur[0], refPtrs, 16, 16)
	require.Equal(t, [4]uint32{256, 1280, 2560, 12800}, got)
}

func TestSadBlock16x16x4_NonContiguous(t *testing.T) {
	stride := 1920
	frame := make([]byte, 16*stride)
	for i := range frame {
		frame[i] = 100
	}
	// 4 reference blocks at different offsets within a frame-sized buffer
	refFrames := [4][]byte{
		make([]byte, 16*stride),
		make([]byte, 16*stride),
		make([]byte, 16*stride),
		make([]byte, 16*stride),
	}
	diffs := [4]byte{3, 7, 15, 30}
	for r := 0; r < 4; r++ {
		for row := 0; row < 16; row++ {
			for col := 0; col < 16; col++ {
				refFrames[r][row*stride+col] = 100 + diffs[r]
			}
		}
	}
	refPtrs := [4]*byte{&refFrames[0][0], &refFrames[1][0], &refFrames[2][0], &refFrames[3][0]}
	got := SadBlock16x16x4(&frame[0], refPtrs, stride, stride)
	expected := [4]uint32{
		16 * 16 * uint32(diffs[0]),
		16 * 16 * uint32(diffs[1]),
		16 * 16 * uint32(diffs[2]),
		16 * 16 * uint32(diffs[3]),
	}
	require.Equal(t, expected, got, "non-contiguous with stride=%d", stride)
}

func TestSadBlock16x16x4_AsymmetricStrides(t *testing.T) {
	curStride := 32
	refStride := 64
	cur := make([]byte, 16*curStride)
	refs := [4][]byte{
		make([]byte, 16*refStride),
		make([]byte, 16*refStride),
		make([]byte, 16*refStride),
		make([]byte, 16*refStride),
	}
	for row := 0; row < 16; row++ {
		for col := 0; col < 16; col++ {
			cur[row*curStride+col] = byte(row + col)
		}
	}
	for r := 0; r < 4; r++ {
		for row := 0; row < 16; row++ {
			for col := 0; col < 16; col++ {
				refs[r][row*refStride+col] = byte(row + col + r + 1)
			}
		}
	}
	refPtrs := [4]*byte{&refs[0][0], &refs[1][0], &refs[2][0], &refs[3][0]}
	got := SadBlock16x16x4(&cur[0], refPtrs, curStride, refStride)
	expected := referenceSadBlock16x16x4(cur, refs, curStride, refStride)
	require.Equal(t, expected, got, "asymmetric strides cur=%d ref=%d", curStride, refStride)
}

func TestSadBlock16x16x4_CrossValidate(t *testing.T) {
	// Cross-validate batched x4 against 4 individual SadBlock16x16 calls
	stride := 48
	cur := make([]byte, 16*stride)
	refs := [4][]byte{
		make([]byte, 16*stride),
		make([]byte, 16*stride),
		make([]byte, 16*stride),
		make([]byte, 16*stride),
	}
	for i := range cur {
		cur[i] = byte((i*7 + 13) % 256)
	}
	for r := 0; r < 4; r++ {
		for i := range refs[r] {
			refs[r][i] = byte((i*(11+r*3) + 37 + r*17) % 256)
		}
	}

	refPtrs := [4]*byte{&refs[0][0], &refs[1][0], &refs[2][0], &refs[3][0]}
	got := SadBlock16x16x4(&cur[0], refPtrs, stride, stride)

	// Compare against individual calls
	for i := 0; i < 4; i++ {
		individual := SadBlock16x16(&cur[0], refPtrs[i], stride, stride)
		require.Equal(t, individual, got[i], "x4[%d] should match individual SadBlock16x16", i)
	}
}

func TestSadBlock16x16x4_MatchesReference(t *testing.T) {
	// Cross-validate against pure Go reference implementation
	cur := make([]byte, 16*16)
	refs := [4][]byte{
		make([]byte, 16*16),
		make([]byte, 16*16),
		make([]byte, 16*16),
		make([]byte, 16*16),
	}
	for i := range cur {
		cur[i] = byte((i*13 + 7) % 256)
	}
	for r := 0; r < 4; r++ {
		for i := range refs[r] {
			refs[r][i] = byte((i*(17+r*5) + 41 + r*23) % 256)
		}
	}
	refPtrs := [4]*byte{&refs[0][0], &refs[1][0], &refs[2][0], &refs[3][0]}
	got := SadBlock16x16x4(&cur[0], refPtrs, 16, 16)
	expected := referenceSadBlock16x16x4(cur, refs, 16, 16)
	require.Equal(t, expected, got, "batched should match reference")
}

func BenchmarkSadBlock16x16x4(b *testing.B) {
	stride := 1920
	cur := make([]byte, 16*stride)
	refBufs := [4][]byte{
		make([]byte, 16*stride),
		make([]byte, 16*stride),
		make([]byte, 16*stride),
		make([]byte, 16*stride),
	}
	for i := range cur {
		cur[i] = byte(i % 256)
	}
	for r := 0; r < 4; r++ {
		for i := range refBufs[r] {
			refBufs[r][i] = byte((i*(3+r) + r*17) % 256)
		}
	}
	refs := [4]*byte{&refBufs[0][0], &refBufs[1][0], &refBufs[2][0], &refBufs[3][0]}
	b.SetBytes(16 * 16 * 5) // cur + 4 refs
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		SadBlock16x16x4(&cur[0], refs, stride, stride)
	}
}

func BenchmarkSadBlock16x16x4_vs_4xSingle(b *testing.B) {
	// Baseline: 4 individual SadBlock16x16 calls (what batching replaces)
	stride := 1920
	cur := make([]byte, 16*stride)
	refBufs := [4][]byte{
		make([]byte, 16*stride),
		make([]byte, 16*stride),
		make([]byte, 16*stride),
		make([]byte, 16*stride),
	}
	for i := range cur {
		cur[i] = byte(i % 256)
	}
	for r := 0; r < 4; r++ {
		for i := range refBufs[r] {
			refBufs[r][i] = byte((i*(3+r) + r*17) % 256)
		}
	}
	refs := [4]*byte{&refBufs[0][0], &refBufs[1][0], &refBufs[2][0], &refBufs[3][0]}
	b.SetBytes(16 * 16 * 5)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		SadBlock16x16(&cur[0], refs[0], stride, stride)
		SadBlock16x16(&cur[0], refs[1], stride, stride)
		SadBlock16x16(&cur[0], refs[2], stride, stride)
		SadBlock16x16(&cur[0], refs[3], stride, stride)
	}
}
