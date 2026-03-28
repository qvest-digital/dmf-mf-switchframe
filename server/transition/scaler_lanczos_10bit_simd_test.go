package transition

import (
	"bytes"
	"encoding/binary"
	"math/rand"
	"testing"
)

// TestScaleLanczos10_Basic verifies basic downscale produces valid output.
func TestScaleLanczos10_Basic(t *testing.T) {
	t.Parallel()
	srcW, srcH := 64, 48
	dstW, dstH := 32, 24

	src := make([]byte, srcW*srcH*4)
	rng := rand.New(rand.NewSource(42))
	for i := 0; i < len(src)/2; i++ {
		binary.LittleEndian.PutUint16(src[i*2:], uint16(rng.Intn(1024)))
	}

	dst := make([]byte, dstW*dstH*4)
	if err := ScaleYUV422P10Lanczos(src, srcW, srcH, dst, dstW, dstH); err != nil {
		t.Fatal(err)
	}

	// Verify output is in valid range
	for i := 0; i < len(dst)/2; i++ {
		v := binary.LittleEndian.Uint16(dst[i*2:])
		if v > 1023 {
			t.Fatalf("sample %d: value %d exceeds 10-bit range", i, v)
		}
	}
}

// TestScaleLanczos10_Identity verifies same dimensions returns identical output.
func TestScaleLanczos10_Identity(t *testing.T) {
	t.Parallel()
	w, h := 32, 24
	src := make([]byte, w*h*4)
	rng := rand.New(rand.NewSource(99))
	for i := 0; i < len(src)/2; i++ {
		binary.LittleEndian.PutUint16(src[i*2:], uint16(rng.Intn(1024)))
	}

	dst := make([]byte, w*h*4)
	if err := ScaleYUV422P10Lanczos(src, w, h, dst, w, h); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(src, dst) {
		t.Fatal("identity scale should produce identical output")
	}
}

// TestScaleLanczos10_Upscale verifies upscaling works.
func TestScaleLanczos10_Upscale(t *testing.T) {
	t.Parallel()
	srcW, srcH := 32, 24
	dstW, dstH := 64, 48

	src := make([]byte, srcW*srcH*4)
	rng := rand.New(rand.NewSource(77))
	for i := 0; i < len(src)/2; i++ {
		binary.LittleEndian.PutUint16(src[i*2:], uint16(rng.Intn(1024)))
	}

	dst := make([]byte, dstW*dstH*4)
	if err := ScaleYUV422P10Lanczos(src, srcW, srcH, dst, dstW, dstH); err != nil {
		t.Fatal(err)
	}

	// Verify valid range
	for i := 0; i < len(dst)/2; i++ {
		v := binary.LittleEndian.Uint16(dst[i*2:])
		if v > 1023 {
			t.Fatalf("upscale sample %d: value %d exceeds 10-bit range", i, v)
		}
	}
}

// TestScaleLanczos10_WithQuality tests the quality dispatch path.
func TestScaleLanczos10_WithQuality(t *testing.T) {
	t.Parallel()
	srcW, srcH := 64, 48
	dstW, dstH := 32, 24

	src := make([]byte, srcW*srcH*4)
	rng := rand.New(rand.NewSource(42))
	for i := 0; i < len(src)/2; i++ {
		binary.LittleEndian.PutUint16(src[i*2:], uint16(rng.Intn(1024)))
	}

	ref := make([]byte, dstW*dstH*4)
	got := make([]byte, dstW*dstH*4)

	if err := ScaleYUV422P10Lanczos(src, srcW, srcH, ref, dstW, dstH); err != nil {
		t.Fatal(err)
	}
	if err := ScaleYUV422P10WithQuality(src, srcW, srcH, got, dstW, dstH, ScaleQualityHigh); err != nil {
		t.Fatal(err)
	}

	if !bytes.Equal(ref, got) {
		t.Fatal("quality dispatch mismatch for Lanczos")
	}
}

// TestScaleLanczos10_OddWidth verifies odd width is rejected.
func TestScaleLanczos10_OddWidth(t *testing.T) {
	t.Parallel()
	err := ScaleYUV422P10Lanczos(nil, 15, 8, nil, 8, 4)
	if err == nil {
		t.Fatal("should reject odd src width")
	}
	err = ScaleYUV422P10Lanczos(nil, 16, 8, nil, 9, 4)
	if err == nil {
		t.Fatal("should reject odd dst width")
	}
}
