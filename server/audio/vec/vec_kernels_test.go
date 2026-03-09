package vec

import (
	"math"
	"testing"

	"github.com/stretchr/testify/require"
)

// --- AddFloat32 tests ---

func TestAddFloat32_Basic(t *testing.T) {
	t.Parallel()
	dst := []float32{1.0, 2.0, 3.0, 4.0}
	src := []float32{0.5, 1.5, 2.5, 3.5}
	AddFloat32(&dst[0], &src[0], 4)
	require.Equal(t, float32(1.5), dst[0])
	require.Equal(t, float32(3.5), dst[1])
	require.Equal(t, float32(5.5), dst[2])
	require.Equal(t, float32(7.5), dst[3])
}

func TestAddFloat32_ZeroLength(t *testing.T) {
	t.Parallel()
	dst := []float32{1.0, 2.0}
	src := []float32{0.5, 1.5}
	AddFloat32(&dst[0], &src[0], 0) // no-op
	require.Equal(t, float32(1.0), dst[0])
	require.Equal(t, float32(2.0), dst[1])
}

func TestAddFloat32_OddSizes(t *testing.T) {
	t.Parallel()
	for _, n := range []int{1, 3, 7, 15, 17, 31, 33} {
		dst := make([]float32, n)
		src := make([]float32, n)
		dstCopy := make([]float32, n)
		for i := 0; i < n; i++ {
			dst[i] = float32(i) * 0.1
			src[i] = float32(i) * 0.2
			dstCopy[i] = dst[i]
		}
		AddFloat32(&dst[0], &src[0], n)
		for i := 0; i < n; i++ {
			expected := dstCopy[i] + src[i]
			require.Equal(t, expected, dst[i], "n=%d, index %d", n, i)
		}
	}
}

func TestAddFloat32_LargeN(t *testing.T) {
	t.Parallel()
	n := 2048
	dst := make([]float32, n)
	src := make([]float32, n)
	expected := make([]float32, n)
	for i := 0; i < n; i++ {
		dst[i] = float32(math.Sin(float64(i) * 0.01))
		src[i] = float32(math.Cos(float64(i) * 0.01))
		expected[i] = dst[i] + src[i]
	}
	AddFloat32(&dst[0], &src[0], n)
	for i := 0; i < n; i++ {
		require.Equal(t, expected[i], dst[i], "index %d", i)
	}
}

// --- ScaleFloat32 tests ---

func TestScaleFloat32_Basic(t *testing.T) {
	t.Parallel()
	dst := []float32{2.0, 4.0, 6.0, 8.0}
	ScaleFloat32(&dst[0], 0.5, 4)
	require.Equal(t, float32(1.0), dst[0])
	require.Equal(t, float32(2.0), dst[1])
	require.Equal(t, float32(3.0), dst[2])
	require.Equal(t, float32(4.0), dst[3])
}

func TestScaleFloat32_Unity(t *testing.T) {
	t.Parallel()
	dst := []float32{1.0, 2.0, 3.0, 4.0}
	original := make([]float32, 4)
	copy(original, dst)
	ScaleFloat32(&dst[0], 1.0, 4)
	for i := range dst {
		require.Equal(t, original[i], dst[i], "index %d", i)
	}
}

func TestScaleFloat32_Zero(t *testing.T) {
	t.Parallel()
	dst := []float32{1.0, 2.0, 3.0, 4.0}
	ScaleFloat32(&dst[0], 0.0, 4)
	for i := range dst {
		require.Equal(t, float32(0.0), dst[i], "index %d", i)
	}
}

func TestScaleFloat32_ZeroLength(t *testing.T) {
	t.Parallel()
	dst := []float32{1.0, 2.0}
	ScaleFloat32(&dst[0], 0.5, 0) // no-op
	require.Equal(t, float32(1.0), dst[0])
	require.Equal(t, float32(2.0), dst[1])
}

func TestScaleFloat32_OddSizes(t *testing.T) {
	t.Parallel()
	scale := float32(0.75)
	for _, n := range []int{1, 3, 7, 15, 17, 31, 33} {
		dst := make([]float32, n)
		expected := make([]float32, n)
		for i := 0; i < n; i++ {
			dst[i] = float32(i) * 0.3
			expected[i] = dst[i] * scale
		}
		ScaleFloat32(&dst[0], scale, n)
		for i := 0; i < n; i++ {
			require.Equal(t, expected[i], dst[i], "n=%d, index %d", n, i)
		}
	}
}

func TestScaleFloat32_LargeN(t *testing.T) {
	t.Parallel()
	n := 2048
	scale := float32(0.891) // -1 dBFS
	dst := make([]float32, n)
	expected := make([]float32, n)
	for i := 0; i < n; i++ {
		dst[i] = float32(math.Sin(float64(i) * 0.01))
		expected[i] = dst[i] * scale
	}
	ScaleFloat32(&dst[0], scale, n)
	for i := 0; i < n; i++ {
		require.Equal(t, expected[i], dst[i], "index %d", i)
	}
}

func TestScaleFloat32_Negative(t *testing.T) {
	t.Parallel()
	dst := []float32{1.0, -2.0, 3.0, -4.0}
	ScaleFloat32(&dst[0], -1.0, 4)
	require.Equal(t, float32(-1.0), dst[0])
	require.Equal(t, float32(2.0), dst[1])
	require.Equal(t, float32(-3.0), dst[2])
	require.Equal(t, float32(4.0), dst[3])
}

// --- MulAddFloat32 tests ---

func TestMulAddFloat32_Basic(t *testing.T) {
	t.Parallel()
	a := []float32{1.0, 2.0, 3.0, 4.0}
	x := []float32{0.5, 0.5, 0.5, 0.5}
	b := []float32{2.0, 3.0, 4.0, 5.0}
	y := []float32{0.25, 0.25, 0.25, 0.25}
	dst := make([]float32, 4)
	MulAddFloat32(&dst[0], &a[0], &x[0], &b[0], &y[0], 4)
	// dst[0] = 1.0*0.5 + 2.0*0.25 = 0.5 + 0.5 = 1.0
	require.Equal(t, float32(1.0), dst[0])
	// dst[1] = 2.0*0.5 + 3.0*0.25 = 1.0 + 0.75 = 1.75
	require.Equal(t, float32(1.75), dst[1])
	// dst[2] = 3.0*0.5 + 4.0*0.25 = 1.5 + 1.0 = 2.5
	require.Equal(t, float32(2.5), dst[2])
	// dst[3] = 4.0*0.5 + 5.0*0.25 = 2.0 + 1.25 = 3.25
	require.Equal(t, float32(3.25), dst[3])
}

func TestMulAddFloat32_ZeroLength(t *testing.T) {
	t.Parallel()
	dst := []float32{99.0, 99.0}
	a := []float32{1.0, 2.0}
	x := []float32{0.5, 0.5}
	b := []float32{2.0, 3.0}
	y := []float32{0.25, 0.25}
	MulAddFloat32(&dst[0], &a[0], &x[0], &b[0], &y[0], 0) // no-op
	require.Equal(t, float32(99.0), dst[0])
	require.Equal(t, float32(99.0), dst[1])
}

func TestMulAddFloat32_OddSizes(t *testing.T) {
	t.Parallel()
	for _, n := range []int{1, 3, 7, 15, 17, 31, 33} {
		a := make([]float32, n)
		x := make([]float32, n)
		b := make([]float32, n)
		y := make([]float32, n)
		dst := make([]float32, n)
		expected := make([]float32, n)
		for i := 0; i < n; i++ {
			a[i] = float32(i) * 0.1
			x[i] = float32(i) * 0.2
			b[i] = float32(i) * 0.3
			y[i] = float32(i) * 0.4
			expected[i] = a[i]*x[i] + b[i]*y[i]
		}
		MulAddFloat32(&dst[0], &a[0], &x[0], &b[0], &y[0], n)
		for i := 0; i < n; i++ {
			require.InDelta(t, float64(expected[i]), float64(dst[i]), 1e-5, "n=%d, index %d", n, i)
		}
	}
}

func TestMulAddFloat32_LargeN(t *testing.T) {
	t.Parallel()
	n := 2048
	a := make([]float32, n)
	x := make([]float32, n)
	b := make([]float32, n)
	y := make([]float32, n)
	dst := make([]float32, n)
	expected := make([]float32, n)
	for i := 0; i < n; i++ {
		a[i] = float32(math.Sin(float64(i) * 0.01))
		x[i] = float32(math.Cos(float64(i) * 0.02))
		b[i] = float32(math.Sin(float64(i) * 0.03))
		y[i] = float32(math.Cos(float64(i) * 0.04))
		expected[i] = a[i]*x[i] + b[i]*y[i]
	}
	MulAddFloat32(&dst[0], &a[0], &x[0], &b[0], &y[0], n)
	for i := 0; i < n; i++ {
		require.InDelta(t, float64(expected[i]), float64(dst[i]), 1e-6, "index %d", i)
	}
}

func TestMulAddFloat32_CrossfadeValidation(t *testing.T) {
	t.Parallel()
	// Simulate a crossfade: dst[i] = old[i]*cosGain[i] + new[i]*sinGain[i]
	n := 1024
	oldPCM := make([]float32, n)
	newPCM := make([]float32, n)
	cosGains := make([]float32, n)
	sinGains := make([]float32, n)
	for i := 0; i < n; i++ {
		oldPCM[i] = float32(math.Sin(float64(i) * 0.1))
		newPCM[i] = float32(math.Cos(float64(i) * 0.1))
		tNorm := float64(i) / float64(n-1)
		cosGains[i] = float32(math.Cos(tNorm * math.Pi / 2))
		sinGains[i] = float32(math.Sin(tNorm * math.Pi / 2))
	}

	// Compute expected with Go loop
	expected := make([]float32, n)
	for i := 0; i < n; i++ {
		expected[i] = oldPCM[i]*cosGains[i] + newPCM[i]*sinGains[i]
	}

	// Compute with kernel
	dst := make([]float32, n)
	MulAddFloat32(&dst[0], &oldPCM[0], &cosGains[0], &newPCM[0], &sinGains[0], n)

	for i := 0; i < n; i++ {
		require.InDelta(t, float64(expected[i]), float64(dst[i]), 1e-6,
			"crossfade sample %d mismatch", i)
	}
}

// --- PeakAbsFloat32 tests ---

func TestPeakAbsFloat32_Basic(t *testing.T) {
	t.Parallel()
	data := []float32{-0.5, 0.3, -0.8, 0.1}
	got := PeakAbsFloat32(&data[0], 4)
	require.Equal(t, float32(0.8), got)
}

func TestPeakAbsFloat32_AllPositive(t *testing.T) {
	t.Parallel()
	data := []float32{0.1, 0.5, 0.3, 0.9, 0.2}
	got := PeakAbsFloat32(&data[0], 5)
	require.Equal(t, float32(0.9), got)
}

func TestPeakAbsFloat32_AllNegative(t *testing.T) {
	t.Parallel()
	data := []float32{-0.1, -0.5, -0.3, -0.9, -0.2}
	got := PeakAbsFloat32(&data[0], 5)
	require.Equal(t, float32(0.9), got)
}

func TestPeakAbsFloat32_AllZero(t *testing.T) {
	t.Parallel()
	data := make([]float32, 16)
	got := PeakAbsFloat32(&data[0], 16)
	require.Equal(t, float32(0.0), got)
}

func TestPeakAbsFloat32_ZeroLength(t *testing.T) {
	t.Parallel()
	data := []float32{1.0}
	got := PeakAbsFloat32(&data[0], 0)
	require.Equal(t, float32(0.0), got)
}

func TestPeakAbsFloat32_SingleElement(t *testing.T) {
	t.Parallel()
	data := []float32{-0.75}
	got := PeakAbsFloat32(&data[0], 1)
	require.Equal(t, float32(0.75), got)
}

func TestPeakAbsFloat32_OddSizes(t *testing.T) {
	t.Parallel()
	for _, n := range []int{1, 3, 5, 7, 13, 15, 17, 31, 33} {
		data := make([]float32, n)
		var expected float32
		for i := 0; i < n; i++ {
			v := float32(math.Sin(float64(i)*0.7)) * float32(i) / float32(n)
			data[i] = v
			if v < 0 {
				v = -v
			}
			if v > expected {
				expected = v
			}
		}
		got := PeakAbsFloat32(&data[0], n)
		require.InDelta(t, float64(expected), float64(got), 1e-6, "n=%d", n)
	}
}

func TestPeakAbsFloat32_LargeN(t *testing.T) {
	t.Parallel()
	n := 2048
	data := make([]float32, n)
	var expected float32
	for i := 0; i < n; i++ {
		v := float32(math.Sin(float64(i) * 0.01))
		data[i] = v
		if v < 0 {
			v = -v
		}
		if v > expected {
			expected = v
		}
	}
	got := PeakAbsFloat32(&data[0], n)
	require.InDelta(t, float64(expected), float64(got), 1e-6)
}

func TestPeakAbsFloat32_PeakAtEnd(t *testing.T) {
	t.Parallel()
	// Ensure the peak at the very end (scalar tail) is found
	n := 17 // 16 NEON + 1 scalar
	data := make([]float32, n)
	for i := 0; i < n-1; i++ {
		data[i] = 0.1
	}
	data[n-1] = -0.99
	got := PeakAbsFloat32(&data[0], n)
	require.Equal(t, float32(0.99), got)
}

// --- PeakAbsStereoFloat32 tests ---

func TestPeakAbsStereoFloat32_Basic(t *testing.T) {
	t.Parallel()
	// [L0=-0.5, R0=0.3, L1=0.8, R1=-0.9]
	data := []float32{-0.5, 0.3, 0.8, -0.9}
	l, r := PeakAbsStereoFloat32(&data[0], 4)
	require.Equal(t, float32(0.8), l)
	require.Equal(t, float32(0.9), r)
}

func TestPeakAbsStereoFloat32_AllZero(t *testing.T) {
	t.Parallel()
	data := make([]float32, 16)
	l, r := PeakAbsStereoFloat32(&data[0], 16)
	require.Equal(t, float32(0.0), l)
	require.Equal(t, float32(0.0), r)
}

func TestPeakAbsStereoFloat32_LessThan2(t *testing.T) {
	t.Parallel()
	data := []float32{1.0}
	l, r := PeakAbsStereoFloat32(&data[0], 0)
	require.Equal(t, float32(0.0), l)
	require.Equal(t, float32(0.0), r)
}

func TestPeakAbsStereoFloat32_OddSizes(t *testing.T) {
	t.Parallel()
	for _, pairs := range []int{1, 2, 3, 5, 7, 15, 17, 31} {
		n := pairs * 2
		data := make([]float32, n)
		var expectedL, expectedR float32
		for i := 0; i < pairs; i++ {
			lv := float32(math.Sin(float64(i)*0.7)) * float32(i) / float32(pairs)
			rv := float32(math.Cos(float64(i)*0.7)) * float32(i) / float32(pairs)
			data[i*2] = lv
			data[i*2+1] = rv
			if lv < 0 {
				lv = -lv
			}
			if lv > expectedL {
				expectedL = lv
			}
			if rv < 0 {
				rv = -rv
			}
			if rv > expectedR {
				expectedR = rv
			}
		}
		gotL, gotR := PeakAbsStereoFloat32(&data[0], n)
		require.InDelta(t, float64(expectedL), float64(gotL), 1e-6, "pairs=%d left", pairs)
		require.InDelta(t, float64(expectedR), float64(gotR), 1e-6, "pairs=%d right", pairs)
	}
}

func TestPeakAbsStereoFloat32_LargeN(t *testing.T) {
	t.Parallel()
	n := 2048
	data := make([]float32, n)
	var expectedL, expectedR float32
	for i := 0; i < n/2; i++ {
		lv := float32(math.Sin(float64(i) * 0.01))
		rv := float32(math.Cos(float64(i) * 0.01))
		data[i*2] = lv
		data[i*2+1] = rv
		if lv < 0 {
			lv = -lv
		}
		if lv > expectedL {
			expectedL = lv
		}
		if rv < 0 {
			rv = -rv
		}
		if rv > expectedR {
			expectedR = rv
		}
	}
	gotL, gotR := PeakAbsStereoFloat32(&data[0], n)
	require.InDelta(t, float64(expectedL), float64(gotL), 1e-6)
	require.InDelta(t, float64(expectedR), float64(gotR), 1e-6)
}

func TestPeakAbsStereoFloat32_PeakAtEnd(t *testing.T) {
	t.Parallel()
	n := 18 // 16 NEON + 2 scalar
	data := make([]float32, n)
	for i := range data {
		data[i] = 0.1
	}
	data[n-2] = -0.99 // left peak
	data[n-1] = 0.88  // right peak
	l, r := PeakAbsStereoFloat32(&data[0], n)
	require.Equal(t, float32(0.99), l)
	require.Equal(t, float32(0.88), r)
}

// --- Benchmarks ---

func BenchmarkAddFloat32_2048(b *testing.B) {
	n := 2048
	dst := make([]float32, n)
	src := make([]float32, n)
	for i := range dst {
		dst[i] = float32(i%256) * 0.01
		src[i] = float32(i%256) * 0.02
	}
	b.SetBytes(int64(n * 4))
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		AddFloat32(&dst[0], &src[0], n)
	}
}

func BenchmarkScaleFloat32_2048(b *testing.B) {
	n := 2048
	dst := make([]float32, n)
	for i := range dst {
		dst[i] = float32(i%256) * 0.01
	}
	b.SetBytes(int64(n * 4))
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		ScaleFloat32(&dst[0], 0.891, n)
	}
}

func BenchmarkPeakAbsFloat32_2048(b *testing.B) {
	n := 2048
	data := make([]float32, n)
	for i := range data {
		data[i] = float32(math.Sin(float64(i) * 0.01))
	}
	b.SetBytes(int64(n * 4))
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		PeakAbsFloat32(&data[0], n)
	}
}

func BenchmarkPeakAbsFloat32_1024(b *testing.B) {
	n := 1024
	data := make([]float32, n)
	for i := range data {
		data[i] = float32(math.Sin(float64(i) * 0.01))
	}
	b.SetBytes(int64(n * 4))
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		PeakAbsFloat32(&data[0], n)
	}
}

func BenchmarkMulAddFloat32_2048(b *testing.B) {
	n := 2048
	a := make([]float32, n)
	x := make([]float32, n)
	bArr := make([]float32, n)
	y := make([]float32, n)
	dst := make([]float32, n)
	for i := range a {
		a[i] = float32(i%256) * 0.01
		x[i] = float32(i%256) * 0.02
		bArr[i] = float32(i%256) * 0.03
		y[i] = float32(i%256) * 0.04
	}
	b.SetBytes(int64(n * 4))
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		MulAddFloat32(&dst[0], &a[0], &x[0], &bArr[0], &y[0], n)
	}
}
