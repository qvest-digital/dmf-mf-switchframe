package transition

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"math/rand"
	"testing"
)

// blendMix10GoRef computes the reference blend using pure Go (no SIMD).
// Used by tests to verify SIMD output is bit-identical.
func blendMix10GoRef(dst, a, b []byte, width, height, pos256 int) {
	if pos256 < 0 {
		pos256 = 0
	}
	if pos256 > 256 {
		pos256 = 256
	}

	ySize := width * height
	chromaW := width / 2
	chromaSize := chromaW * height

	blendMix10RowGo(&dst[0], &a[0], &b[0], ySize, pos256)
	cbOff := ySize * 2
	blendMix10RowGo(&dst[cbOff], &a[cbOff], &b[cbOff], chromaSize, pos256)
	crOff := cbOff + chromaSize*2
	blendMix10RowGo(&dst[crOff], &a[crOff], &b[crOff], chromaSize, pos256)
}

// TestBlendMix10SIMD_MatchesReference verifies the SIMD-accelerated blendMix10Row
// produces bit-identical results to the Go reference across a range of
// blend positions and random input data.
func TestBlendMix10SIMD_MatchesReference(t *testing.T) {
	w, h := 64, 32
	rng := rand.New(rand.NewSource(42))

	a := make([]byte, w*h*4)
	b := make([]byte, w*h*4)
	for i := 0; i < len(a); i += 2 {
		binary.LittleEndian.PutUint16(a[i:], uint16(rng.Intn(1024)))
		binary.LittleEndian.PutUint16(b[i:], uint16(rng.Intn(1024)))
	}

	for _, pos := range []int{0, 1, 64, 128, 192, 255, 256} {
		t.Run(fmt.Sprintf("pos=%d", pos), func(t *testing.T) {
			// Pure Go reference
			ref := make([]byte, w*h*4)
			blendMix10GoRef(ref, a, b, w, h, pos)

			// SIMD path (via BlendMix10 which uses dispatch)
			got := make([]byte, w*h*4)
			if err := BlendMix10(got, a, b, w, h, pos); err != nil {
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
		})
	}
}

// TestBlendMix10SIMD_SmallBuffer tests SIMD with widths not aligned to SIMD width.
func TestBlendMix10SIMD_SmallBuffer(t *testing.T) {
	w, h := 6, 2 // 6 is not a multiple of 8 or 16
	a := make10bitFrame(w, h, 800, 400, 600)
	b := make10bitFrame(w, h, 200, 600, 400)

	ref := make([]byte, w*h*4)
	blendMix10GoRef(ref, a, b, w, h, 128)

	got := make([]byte, w*h*4)
	if err := BlendMix10(got, a, b, w, h, 128); err != nil {
		t.Fatal(err)
	}

	if !bytes.Equal(ref, got) {
		for i := 0; i < len(ref); i += 2 {
			rv := binary.LittleEndian.Uint16(ref[i:])
			gv := binary.LittleEndian.Uint16(got[i:])
			if rv != gv {
				t.Fatalf("small buffer mismatch at byte %d (sample %d): ref=%d, got=%d", i, i/2, rv, gv)
			}
		}
	}
}

// TestBlendMix10SIMD_AllPositions exhaustively tests every blend position 0-256.
func TestBlendMix10SIMD_AllPositions(t *testing.T) {
	w, h := 16, 4
	rng := rand.New(rand.NewSource(99))

	a := make([]byte, w*h*4)
	b := make([]byte, w*h*4)
	for i := 0; i < len(a); i += 2 {
		binary.LittleEndian.PutUint16(a[i:], uint16(rng.Intn(1024)))
		binary.LittleEndian.PutUint16(b[i:], uint16(rng.Intn(1024)))
	}

	for pos := 0; pos <= 256; pos++ {
		ref := make([]byte, w*h*4)
		got := make([]byte, w*h*4)
		blendMix10GoRef(ref, a, b, w, h, pos)
		_ = BlendMix10(got, a, b, w, h, pos)

		if !bytes.Equal(ref, got) {
			for i := 0; i < len(ref); i += 2 {
				rv := binary.LittleEndian.Uint16(ref[i:])
				gv := binary.LittleEndian.Uint16(got[i:])
				if rv != gv {
					t.Fatalf("pos=%d mismatch at byte %d (sample %d): ref=%d, got=%d", pos, i, i/2, rv, gv)
				}
			}
		}
	}
}

// TestBlendMix10SIMD_MaxValues tests blending at maximum 10-bit values
// to ensure no overflow in SIMD intermediate math.
func TestBlendMix10SIMD_MaxValues(t *testing.T) {
	w, h := 16, 4
	a := make10bitFrame(w, h, 1023, 1023, 1023)
	b := make10bitFrame(w, h, 1023, 1023, 1023)

	for _, pos := range []int{0, 128, 256} {
		ref := make([]byte, w*h*4)
		got := make([]byte, w*h*4)
		blendMix10GoRef(ref, a, b, w, h, pos)
		_ = BlendMix10(got, a, b, w, h, pos)

		if !bytes.Equal(ref, got) {
			t.Fatalf("max values mismatch at pos=%d", pos)
		}
	}
}

// TestBlendMix10SIMD_ZeroValues tests blending at zero values.
func TestBlendMix10SIMD_ZeroValues(t *testing.T) {
	w, h := 16, 4
	a := make10bitFrame(w, h, 0, 0, 0)
	b := make10bitFrame(w, h, 0, 0, 0)

	for _, pos := range []int{0, 128, 256} {
		ref := make([]byte, w*h*4)
		got := make([]byte, w*h*4)
		blendMix10GoRef(ref, a, b, w, h, pos)
		_ = BlendMix10(got, a, b, w, h, pos)

		if !bytes.Equal(ref, got) {
			t.Fatalf("zero values mismatch at pos=%d", pos)
		}
	}
}

// TestBlendMix10SIMD_OddWidth tests that odd width is rejected.
func TestBlendMix10SIMD_OddWidth(t *testing.T) {
	err := BlendMix10(nil, nil, nil, 15, 8, 128)
	if err == nil {
		t.Fatal("should reject odd width")
	}
}

// TestBlendMix10SIMD_LargeFrame tests with 1080p-sized frames.
func TestBlendMix10SIMD_LargeFrame(t *testing.T) {
	w, h := 1920, 1080
	rng := rand.New(rand.NewSource(7))

	a := make([]byte, w*h*4)
	b := make([]byte, w*h*4)
	for i := 0; i < len(a); i += 2 {
		binary.LittleEndian.PutUint16(a[i:], uint16(rng.Intn(1024)))
		binary.LittleEndian.PutUint16(b[i:], uint16(rng.Intn(1024)))
	}

	ref := make([]byte, w*h*4)
	got := make([]byte, w*h*4)
	blendMix10GoRef(ref, a, b, w, h, 128)
	_ = BlendMix10(got, a, b, w, h, 128)

	if !bytes.Equal(ref, got) {
		for i := 0; i < len(ref); i += 2 {
			rv := binary.LittleEndian.Uint16(ref[i:])
			gv := binary.LittleEndian.Uint16(got[i:])
			if rv != gv {
				t.Fatalf("1080p mismatch at byte %d (sample %d): ref=%d, got=%d", i, i/2, rv, gv)
			}
		}
	}
}

// TestBlendMix10Row_DirectKernel tests the low-level blendMix10Row kernel directly.
func TestBlendMix10Row_DirectKernel(t *testing.T) {
	rng := rand.New(rand.NewSource(77))

	for _, count := range []int{1, 3, 7, 8, 9, 15, 16, 17, 31, 32, 33, 64, 100} {
		for _, pos := range []int{0, 1, 64, 128, 192, 255, 256} {
			t.Run(fmt.Sprintf("count=%d/pos=%d", count, pos), func(t *testing.T) {
				a := make([]byte, count*2)
				b := make([]byte, count*2)
				for i := 0; i < count; i++ {
					binary.LittleEndian.PutUint16(a[i*2:], uint16(rng.Intn(1024)))
					binary.LittleEndian.PutUint16(b[i*2:], uint16(rng.Intn(1024)))
				}

				// Compute reference using Go
				ref := make([]byte, count*2)
				blendMix10RowGo(&ref[0], &a[0], &b[0], count, pos)

				// SIMD kernel
				got := make([]byte, count*2)
				blendMix10Row(&got[0], &a[0], &b[0], count, pos)

				if !bytes.Equal(ref, got) {
					for i := 0; i < count; i++ {
						rv := binary.LittleEndian.Uint16(ref[i*2:])
						gv := binary.LittleEndian.Uint16(got[i*2:])
						if rv != gv {
							t.Fatalf("sample %d: ref=%d, got=%d (a=%d, b=%d, pos=%d)",
								i, rv, gv,
								binary.LittleEndian.Uint16(a[i*2:]),
								binary.LittleEndian.Uint16(b[i*2:]),
								pos)
						}
					}
				}
			})
		}
	}
}

func BenchmarkBlendMix10_Go_1080p(b *testing.B) {
	w, h := 1920, 1080
	a := make10bitFrame(w, h, 500, 300, 700)
	bb := make10bitFrame(w, h, 800, 600, 400)
	dst := make([]byte, w*h*4)
	b.SetBytes(int64(w * h * 4 * 3))
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		blendMix10GoRef(dst, a, bb, w, h, 128)
	}
}

func BenchmarkBlendMix10_SIMD_1080p(b *testing.B) {
	w, h := 1920, 1080
	a := make10bitFrame(w, h, 500, 300, 700)
	bb := make10bitFrame(w, h, 800, 600, 400)
	dst := make([]byte, w*h*4)
	b.SetBytes(int64(w * h * 4 * 3))
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		BlendMix10(dst, a, bb, w, h, 128)
	}
}

func BenchmarkBlendMix10_Go_4K(b *testing.B) {
	w, h := 3840, 2160
	a := make10bitFrame(w, h, 500, 300, 700)
	bb := make10bitFrame(w, h, 800, 600, 400)
	dst := make([]byte, w*h*4)
	b.SetBytes(int64(w * h * 4 * 3))
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		blendMix10GoRef(dst, a, bb, w, h, 128)
	}
}

func BenchmarkBlendMix10_SIMD_4K(b *testing.B) {
	w, h := 3840, 2160
	a := make10bitFrame(w, h, 500, 300, 700)
	bb := make10bitFrame(w, h, 800, 600, 400)
	dst := make([]byte, w*h*4)
	b.SetBytes(int64(w * h * 4 * 3))
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		BlendMix10(dst, a, bb, w, h, 128)
	}
}
