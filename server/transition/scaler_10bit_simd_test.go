package transition

import (
	"bytes"
	"encoding/binary"
	"math/rand"
	"testing"
)

// TestScaleBilinear10Dispatch_MatchesReference verifies the dispatch path
// produces identical output to the direct ScaleYUV422P10 function.
func TestScaleBilinear10Dispatch_MatchesReference(t *testing.T) {
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

	if err := ScaleYUV422P10(src, srcW, srcH, ref, dstW, dstH); err != nil {
		t.Fatal(err)
	}
	if err := scaleBilinear10Dispatch(src, srcW, srcH, got, dstW, dstH); err != nil {
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

// TestScaleBilinear10Dispatch_Upscale tests upscaling path.
func TestScaleBilinear10Dispatch_Upscale(t *testing.T) {
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

	if err := ScaleYUV422P10(src, srcW, srcH, ref, dstW, dstH); err != nil {
		t.Fatal(err)
	}
	if err := scaleBilinear10Dispatch(src, srcW, srcH, got, dstW, dstH); err != nil {
		t.Fatal(err)
	}

	if !bytes.Equal(ref, got) {
		t.Fatal("upscale dispatch mismatch")
	}
}
