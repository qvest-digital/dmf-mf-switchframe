package graphics

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"math/rand"
	"testing"
)

// TestChromaKey10Row_DirectKernel tests the low-level chromaKey10Row kernel
// against the Go reference.
func TestChromaKey10Row_DirectKernel(t *testing.T) {
	t.Parallel()
	rng := rand.New(rand.NewSource(42))

	for _, count := range []int{1, 3, 4, 7, 8, 9, 15, 16, 17, 32, 64, 100} {
		cbData := make([]byte, count*2)
		crData := make([]byte, count*2)
		for i := 0; i < count; i++ {
			binary.LittleEndian.PutUint16(cbData[i*2:], uint16(rng.Intn(1024)))
			binary.LittleEndian.PutUint16(crData[i*2:], uint16(rng.Intn(1024)))
		}

		targetCb, targetCr := 512, 512
		simDistSq := 80 * 80
		totalDistSq := 120 * 120
		rangeSq := totalDistSq - simDistSq
		invRange := 0
		if rangeSq > 0 {
			invRange = 255 * 65536 / rangeSq
		}

		ref := make([]byte, count)
		got := make([]byte, count)

		chromaKey10RowGo(&ref[0], &cbData[0], &crData[0], count, targetCb, targetCr, simDistSq, totalDistSq, invRange)
		chromaKey10Row(&got[0], &cbData[0], &crData[0], count, targetCb, targetCr, simDistSq, totalDistSq, invRange)

		if !bytes.Equal(ref, got) {
			for i := 0; i < count; i++ {
				if ref[i] != got[i] {
					cb := binary.LittleEndian.Uint16(cbData[i*2:])
					cr := binary.LittleEndian.Uint16(crData[i*2:])
					t.Fatalf("count=%d sample %d: ref=%d, got=%d (cb=%d, cr=%d)",
						count, i, ref[i], got[i], cb, cr)
				}
			}
		}
	}
}

// TestChromaKey10Row_HardThreshold tests with no softness (hard edge).
func TestChromaKey10Row_HardThreshold(t *testing.T) {
	t.Parallel()
	count := 16
	cbData := make([]byte, count*2)
	crData := make([]byte, count*2)

	// Place some values at target (512, 512) and some far away
	for i := 0; i < count; i++ {
		if i%2 == 0 {
			binary.LittleEndian.PutUint16(cbData[i*2:], 512)
			binary.LittleEndian.PutUint16(crData[i*2:], 512)
		} else {
			binary.LittleEndian.PutUint16(cbData[i*2:], 0)
			binary.LittleEndian.PutUint16(crData[i*2:], 0)
		}
	}

	targetCb, targetCr := 512, 512
	simDistSq := 50 * 50
	totalDistSq := simDistSq // no softness
	invRange := 0

	ref := make([]byte, count)
	got := make([]byte, count)

	chromaKey10RowGo(&ref[0], &cbData[0], &crData[0], count, targetCb, targetCr, simDistSq, totalDistSq, invRange)
	chromaKey10Row(&got[0], &cbData[0], &crData[0], count, targetCb, targetCr, simDistSq, totalDistSq, invRange)

	if !bytes.Equal(ref, got) {
		t.Fatal("hard threshold mismatch")
	}
}

// TestChromaKey10Row_AllKeyed tests when all samples match the key.
func TestChromaKey10Row_AllKeyed(t *testing.T) {
	t.Parallel()
	count := 32
	cbData := make([]byte, count*2)
	crData := make([]byte, count*2)
	for i := 0; i < count; i++ {
		binary.LittleEndian.PutUint16(cbData[i*2:], 512)
		binary.LittleEndian.PutUint16(crData[i*2:], 512)
	}

	targetCb, targetCr := 512, 512
	simDistSq := 100 * 100
	totalDistSq := 150 * 150
	rangeSq := totalDistSq - simDistSq
	invRange := 255 * 65536 / rangeSq

	ref := make([]byte, count)
	got := make([]byte, count)

	chromaKey10RowGo(&ref[0], &cbData[0], &crData[0], count, targetCb, targetCr, simDistSq, totalDistSq, invRange)
	chromaKey10Row(&got[0], &cbData[0], &crData[0], count, targetCb, targetCr, simDistSq, totalDistSq, invRange)

	if !bytes.Equal(ref, got) {
		t.Fatal("all-keyed mismatch")
	}
	// All should be 255
	for i := 0; i < count; i++ {
		if got[i] != 255 {
			t.Fatalf("sample %d: expected 255, got %d", i, got[i])
		}
	}
}

// TestChromaKey10SIMD_MatchesReference verifies SIMD dispatch matches ChromaKey10.
func TestChromaKey10SIMD_MatchesReference(t *testing.T) {
	t.Parallel()
	w, h := 64, 32
	rng := rand.New(rand.NewSource(42))

	yuv := make([]byte, w*h*4)
	for i := 0; i < len(yuv)/2; i++ {
		binary.LittleEndian.PutUint16(yuv[i*2:], uint16(rng.Intn(1024)))
	}

	for _, tc := range []struct {
		tCb, tCr          uint16
		tolerance, softness int
	}{
		{512, 512, 80, 40},
		{300, 700, 50, 10},
		{512, 512, 100, 0},
		{512, 512, 0, 0},
	} {
		t.Run(fmt.Sprintf("t=%d,%d/tol=%d/soft=%d", tc.tCb, tc.tCr, tc.tolerance, tc.softness), func(t *testing.T) {
			ref := ChromaKey10(yuv, w, h, tc.tCb, tc.tCr, tc.tolerance, tc.softness)
			got := chromaKey10Dispatch(yuv, w, h, tc.tCb, tc.tCr, tc.tolerance, tc.softness)

			if !bytes.Equal(ref, got) {
				mismatches := 0
				for i := range ref {
					if ref[i] != got[i] {
						if mismatches == 0 {
							t.Errorf("first mismatch at %d: ref=%d, got=%d", i, ref[i], got[i])
						}
						mismatches++
					}
				}
				t.Errorf("total mismatches: %d of %d", mismatches, len(ref))
			}
		})
	}
}

func BenchmarkChromaKey10_Go_1080p(b *testing.B) {
	w, h := 1920, 1080
	yuv := make([]byte, w*h*4)
	rng := rand.New(rand.NewSource(42))
	for i := 0; i < len(yuv)/2; i++ {
		binary.LittleEndian.PutUint16(yuv[i*2:], uint16(rng.Intn(1024)))
	}
	b.SetBytes(int64(w * h * 4))
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		ChromaKey10(yuv, w, h, 512, 512, 80, 40)
	}
}

func BenchmarkChromaKey10_SIMD_1080p(b *testing.B) {
	w, h := 1920, 1080
	yuv := make([]byte, w*h*4)
	rng := rand.New(rand.NewSource(42))
	for i := 0; i < len(yuv)/2; i++ {
		binary.LittleEndian.PutUint16(yuv[i*2:], uint16(rng.Intn(1024)))
	}
	b.SetBytes(int64(w * h * 4))
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		chromaKey10Dispatch(yuv, w, h, 512, 512, 80, 40)
	}
}
