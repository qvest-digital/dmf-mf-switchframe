package graphics

import (
	"bytes"
	"encoding/binary"
	"math/rand"
	"testing"
)

// TestChromaKey10Dispatch_MatchesReference verifies dispatch returns same output.
func TestChromaKey10Dispatch_MatchesReference(t *testing.T) {
	t.Parallel()
	w, h := 32, 16
	rng := rand.New(rand.NewSource(42))

	yuv := make([]byte, w*h*4)
	for i := 0; i < len(yuv)/2; i++ {
		binary.LittleEndian.PutUint16(yuv[i*2:], uint16(rng.Intn(1024)))
	}

	ref := ChromaKey10(yuv, w, h, 512, 512, 100, 50)
	got := chromaKey10Dispatch(yuv, w, h, 512, 512, 100, 50)

	if !bytes.Equal(ref, got) {
		mismatches := 0
		for i := range ref {
			if ref[i] != got[i] {
				mismatches++
				if mismatches == 1 {
					t.Errorf("first mismatch at %d: ref=%d, got=%d", i, ref[i], got[i])
				}
			}
		}
		t.Errorf("total mismatches: %d of %d", mismatches, len(ref))
	}
}

// TestChromaKey10Dispatch_NoSoftness verifies hard-edge chroma key.
func TestChromaKey10Dispatch_NoSoftness(t *testing.T) {
	t.Parallel()
	w, h := 16, 8

	// All chroma at target → fully keyed
	yuv := make([]byte, w*h*4)
	ySize := w * h
	chromaW := w / 2
	chromaSize := chromaW * h
	cbOff := ySize * 2
	crOff := cbOff + chromaSize*2

	for i := 0; i < ySize; i++ {
		binary.LittleEndian.PutUint16(yuv[i*2:], 500)
	}
	for i := 0; i < chromaSize; i++ {
		binary.LittleEndian.PutUint16(yuv[cbOff+i*2:], 512)
		binary.LittleEndian.PutUint16(yuv[crOff+i*2:], 512)
	}

	ref := ChromaKey10(yuv, w, h, 512, 512, 200, 0)
	got := chromaKey10Dispatch(yuv, w, h, 512, 512, 200, 0)

	if !bytes.Equal(ref, got) {
		t.Fatal("hard-edge chroma key dispatch mismatch")
	}
	// All pixels should be keyed (255)
	for i, v := range ref {
		if v != 255 {
			t.Fatalf("pixel %d: expected 255 (keyed), got %d", i, v)
		}
	}
}
