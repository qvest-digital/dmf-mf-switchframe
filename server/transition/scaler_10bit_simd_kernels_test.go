package transition

import (
	"bytes"
	"encoding/binary"
	"math/rand"
	"testing"
)

// TestScaleBilinear10RowKernels_MatchesReference verifies the SIMD row-based
// bilinear scaler produces bit-identical results to the Go reference for
// a variety of scale factors and random input data.
func TestScaleBilinear10RowKernels_MatchesReference(t *testing.T) {
	t.Parallel()
	rng := rand.New(rand.NewSource(42))

	testCases := []struct {
		srcW, srcH, dstW, dstH int
	}{
		{64, 48, 32, 24},   // downsample 2:1
		{32, 24, 64, 48},   // upsample 1:2
		{64, 48, 48, 36},   // fractional downsample
		{30, 20, 60, 40},   // upsample non-power-of-2
		{1920, 1080, 960, 540}, // 1080p → 540p
		{960, 540, 1920, 1080}, // 540p → 1080p
		{10, 10, 6, 6},     // small frame
		{6, 4, 16, 12},     // small upsample
	}

	for _, tc := range testCases {
		src := make([]byte, tc.srcW*tc.srcH*4)
		for i := 0; i < len(src)/2; i++ {
			binary.LittleEndian.PutUint16(src[i*2:], uint16(rng.Intn(1024)))
		}

		ref := make([]byte, tc.dstW*tc.dstH*4)
		got := make([]byte, tc.dstW*tc.dstH*4)

		if err := ScaleYUV422P10(src, tc.srcW, tc.srcH, ref, tc.dstW, tc.dstH); err != nil {
			t.Fatalf("%dx%d→%dx%d: ref error: %v", tc.srcW, tc.srcH, tc.dstW, tc.dstH, err)
		}
		if err := scaleBilinear10Dispatch(src, tc.srcW, tc.srcH, got, tc.dstW, tc.dstH); err != nil {
			t.Fatalf("%dx%d→%dx%d: dispatch error: %v", tc.srcW, tc.srcH, tc.dstW, tc.dstH, err)
		}

		if !bytes.Equal(ref, got) {
			for i := 0; i < len(ref); i += 2 {
				rv := binary.LittleEndian.Uint16(ref[i:])
				gv := binary.LittleEndian.Uint16(got[i:])
				if rv != gv {
					t.Fatalf("%dx%d→%dx%d: mismatch at byte %d (sample %d): ref=%d, got=%d",
						tc.srcW, tc.srcH, tc.dstW, tc.dstH, i, i/2, rv, gv)
				}
			}
		}
	}
}

// TestBlendRows10_DirectKernel tests the vertical bilinear blend row kernel.
func TestBlendRows10_DirectKernel(t *testing.T) {
	t.Parallel()
	rng := rand.New(rand.NewSource(77))

	for _, count := range []int{1, 3, 7, 8, 9, 15, 16, 17, 31, 32, 33, 64, 100, 960} {
		for _, fy := range []int{0, 1, 128, 255, 32768, 65535} {
			topRow := make([]byte, count*2)
			botRow := make([]byte, count*2)
			for i := 0; i < count; i++ {
				binary.LittleEndian.PutUint16(topRow[i*2:], uint16(rng.Intn(1024)))
				binary.LittleEndian.PutUint16(botRow[i*2:], uint16(rng.Intn(1024)))
			}

			ref := make([]byte, count*2)
			got := make([]byte, count*2)

			blendRows10Go(&ref[0], &topRow[0], &botRow[0], fy, count)
			blendRows10(&got[0], &topRow[0], &botRow[0], fy, count)

			if !bytes.Equal(ref, got) {
				for i := 0; i < count; i++ {
					rv := binary.LittleEndian.Uint16(ref[i*2:])
					gv := binary.LittleEndian.Uint16(got[i*2:])
					if rv != gv {
						t.Fatalf("count=%d fy=%d sample %d: ref=%d, got=%d (top=%d, bot=%d)",
							count, fy, i, rv, gv,
							binary.LittleEndian.Uint16(topRow[i*2:]),
							binary.LittleEndian.Uint16(botRow[i*2:]))
					}
				}
			}
		}
	}
}

// TestBlendRows10_MaxValues tests the vertical blend kernel with maximum values.
func TestBlendRows10_MaxValues(t *testing.T) {
	t.Parallel()
	count := 16
	topRow := make([]byte, count*2)
	botRow := make([]byte, count*2)
	for i := 0; i < count; i++ {
		binary.LittleEndian.PutUint16(topRow[i*2:], 1023)
		binary.LittleEndian.PutUint16(botRow[i*2:], 1023)
	}

	for _, fy := range []int{0, 32768, 65535} {
		ref := make([]byte, count*2)
		got := make([]byte, count*2)
		blendRows10Go(&ref[0], &topRow[0], &botRow[0], fy, count)
		blendRows10(&got[0], &topRow[0], &botRow[0], fy, count)

		if !bytes.Equal(ref, got) {
			t.Fatalf("max values fy=%d mismatch", fy)
		}
	}
}

func BenchmarkScaleBilinear10_Go_1080p_to_540p(b *testing.B) {
	srcW, srcH := 1920, 1080
	dstW, dstH := 960, 540
	src := make([]byte, srcW*srcH*4)
	dst := make([]byte, dstW*dstH*4)
	rng := rand.New(rand.NewSource(42))
	for i := 0; i < len(src)/2; i++ {
		binary.LittleEndian.PutUint16(src[i*2:], uint16(rng.Intn(1024)))
	}
	b.SetBytes(int64(dstW * dstH * 4))
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		ScaleYUV422P10(src, srcW, srcH, dst, dstW, dstH)
	}
}

func BenchmarkScaleBilinear10_SIMD_1080p_to_540p(b *testing.B) {
	srcW, srcH := 1920, 1080
	dstW, dstH := 960, 540
	src := make([]byte, srcW*srcH*4)
	dst := make([]byte, dstW*dstH*4)
	rng := rand.New(rand.NewSource(42))
	for i := 0; i < len(src)/2; i++ {
		binary.LittleEndian.PutUint16(src[i*2:], uint16(rng.Intn(1024)))
	}
	b.SetBytes(int64(dstW * dstH * 4))
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		scaleBilinear10Dispatch(src, srcW, srcH, dst, dstW, dstH)
	}
}
