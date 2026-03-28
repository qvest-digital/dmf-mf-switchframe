package transition

import (
	"bytes"
	"encoding/binary"
	"math/rand"
	"testing"
)

// TestLanczosVert10Row_DirectKernel tests the low-level lanczosVert10Row kernel
// against the Go reference across various sizes and tap configurations.
func TestLanczosVert10Row_DirectKernel(t *testing.T) {
	t.Parallel()
	rng := rand.New(rand.NewSource(42))

	for _, dstW := range []int{1, 3, 4, 7, 8, 9, 15, 16, 17, 32, 64, 100, 960} {
		for _, maxTaps := range []int{3, 6, 7, 8} {
			t.Run("", func(t *testing.T) {
				srcH := maxTaps + 5
				// Fill temp buffer (float32 intermediate, dstW * srcH)
				temp := make([]float32, dstW*srcH)
				for i := range temp {
					temp[i] = float32(rng.Intn(1024))
				}

				// Create weights (sum to ~1.0 for realistic test)
				weights := make([]float32, maxTaps)
				var wSum float32
				for t := range weights {
					weights[t] = rng.Float32()
					wSum += weights[t]
				}
				for t := range weights {
					weights[t] /= wSum
				}

				startRow := int32(2)

				ref := make([]byte, dstW*2)
				got := make([]byte, dstW*2)

				lanczosVert10RowGo(ref, temp, dstW, startRow, weights, maxTaps)
				lanczosVert10Row(got, temp, dstW, startRow, weights, maxTaps)

				if !bytes.Equal(ref, got) {
					for i := 0; i < dstW; i++ {
						rv := binary.LittleEndian.Uint16(ref[i*2:])
						gv := binary.LittleEndian.Uint16(got[i*2:])
						if rv != gv {
							t.Fatalf("dstW=%d taps=%d col %d: ref=%d, got=%d",
								dstW, maxTaps, i, rv, gv)
						}
					}
				}
			})
		}
	}
}

// TestLanczosVert10Row_MaxValues tests with max 10-bit values.
func TestLanczosVert10Row_MaxValues(t *testing.T) {
	t.Parallel()
	dstW := 16
	maxTaps := 6
	srcH := 10

	temp := make([]float32, dstW*srcH)
	for i := range temp {
		temp[i] = 1023.0
	}

	// Unity weight on one tap
	weights := make([]float32, maxTaps)
	weights[0] = 1.0

	ref := make([]byte, dstW*2)
	got := make([]byte, dstW*2)

	lanczosVert10RowGo(ref, temp, dstW, 0, weights, maxTaps)
	lanczosVert10Row(got, temp, dstW, 0, weights, maxTaps)

	if !bytes.Equal(ref, got) {
		t.Fatal("max values mismatch")
	}
	// Verify output is 1023
	for i := 0; i < dstW; i++ {
		v := binary.LittleEndian.Uint16(got[i*2:])
		if v != 1023 {
			t.Fatalf("col %d: expected 1023, got %d", i, v)
		}
	}
}

// TestLanczosVert10Row_ZeroWeights tests that zero weights produce zero output.
func TestLanczosVert10Row_ZeroWeights(t *testing.T) {
	t.Parallel()
	dstW := 16
	maxTaps := 6
	srcH := 10

	temp := make([]float32, dstW*srcH)
	for i := range temp {
		temp[i] = 500.0
	}

	weights := make([]float32, maxTaps) // all zero

	ref := make([]byte, dstW*2)
	got := make([]byte, dstW*2)

	lanczosVert10RowGo(ref, temp, dstW, 0, weights, maxTaps)
	lanczosVert10Row(got, temp, dstW, 0, weights, maxTaps)

	if !bytes.Equal(ref, got) {
		t.Fatal("zero weights mismatch")
	}
}

// TestLanczosHoriz10Row_DirectKernel tests the horizontal Lanczos kernel for 10-bit.
func TestLanczosHoriz10Row_DirectKernel(t *testing.T) {
	t.Parallel()
	rng := rand.New(rand.NewSource(99))

	for _, dstW := range []int{1, 4, 8, 16, 32, 64, 100} {
		for _, maxTaps := range []int{3, 6, 7} {
			srcW := dstW * 2
			src := make([]byte, srcW*2)
			for i := 0; i < srcW; i++ {
				binary.LittleEndian.PutUint16(src[i*2:], uint16(rng.Intn(1024)))
			}

			offsets := make([]int32, dstW)
			weights := make([]float32, dstW*maxTaps)
			for d := 0; d < dstW; d++ {
				off := d * 2
				if off+maxTaps > srcW {
					off = srcW - maxTaps
				}
				if off < 0 {
					off = 0
				}
				offsets[d] = int32(off)
				var wSum float32
				for tt := 0; tt < maxTaps; tt++ {
					weights[d*maxTaps+tt] = rng.Float32()
					wSum += weights[d*maxTaps+tt]
				}
				for tt := 0; tt < maxTaps; tt++ {
					weights[d*maxTaps+tt] /= wSum
				}
			}

			ref := make([]float32, dstW)
			got := make([]float32, dstW)

			lanczosHoriz10RowGo(ref, src, srcW, offsets, weights, maxTaps)
			lanczosHoriz10Row(got, src, srcW, offsets, weights, maxTaps)

			for i := 0; i < dstW; i++ {
				if ref[i] != got[i] {
					t.Fatalf("dstW=%d taps=%d col %d: ref=%f, got=%f",
						dstW, maxTaps, i, ref[i], got[i])
				}
			}
		}
	}
}

// TestScaleLanczos10Dispatch_MatchesReference verifies the full dispatch
// produces identical output to the existing ScaleYUV422P10Lanczos.
func TestScaleLanczos10Dispatch_MatchesReference(t *testing.T) {
	t.Parallel()
	srcW, srcH := 64, 48
	dstW, dstH := 32, 24
	rng := rand.New(rand.NewSource(42))

	src := make([]byte, srcW*srcH*4)
	for i := 0; i < len(src)/2; i++ {
		binary.LittleEndian.PutUint16(src[i*2:], uint16(rng.Intn(1024)))
	}

	ref := make([]byte, dstW*dstH*4)
	got := make([]byte, dstW*dstH*4)

	if err := ScaleYUV422P10Lanczos(src, srcW, srcH, ref, dstW, dstH); err != nil {
		t.Fatal(err)
	}
	if err := scaleLanczos10Dispatch(src, srcW, srcH, got, dstW, dstH); err != nil {
		t.Fatal(err)
	}

	if !bytes.Equal(ref, got) {
		for i := 0; i < len(ref); i += 2 {
			rv := binary.LittleEndian.Uint16(ref[i:])
			gv := binary.LittleEndian.Uint16(got[i:])
			if rv != gv {
				t.Fatalf("mismatch at byte %d (sample %d): ref=%d, got=%d", i, i/2, rv, gv)
			}
		}
	}
}

// TestScaleLanczos10Dispatch_Upscale tests upscaling via dispatch.
func TestScaleLanczos10Dispatch_Upscale(t *testing.T) {
	t.Parallel()
	srcW, srcH := 32, 24
	dstW, dstH := 64, 48
	rng := rand.New(rand.NewSource(99))

	src := make([]byte, srcW*srcH*4)
	for i := 0; i < len(src)/2; i++ {
		binary.LittleEndian.PutUint16(src[i*2:], uint16(rng.Intn(1024)))
	}

	ref := make([]byte, dstW*dstH*4)
	got := make([]byte, dstW*dstH*4)

	if err := ScaleYUV422P10Lanczos(src, srcW, srcH, ref, dstW, dstH); err != nil {
		t.Fatal(err)
	}
	if err := scaleLanczos10Dispatch(src, srcW, srcH, got, dstW, dstH); err != nil {
		t.Fatal(err)
	}

	if !bytes.Equal(ref, got) {
		t.Fatal("upscale lanczos10 dispatch mismatch")
	}
}

func BenchmarkScaleLanczos10_Go_1080p_to_540p(b *testing.B) {
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
		ScaleYUV422P10Lanczos(src, srcW, srcH, dst, dstW, dstH)
	}
}

func BenchmarkScaleLanczos10_SIMD_1080p_to_540p(b *testing.B) {
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
		scaleLanczos10Dispatch(src, srcW, srcH, dst, dstW, dstH)
	}
}
