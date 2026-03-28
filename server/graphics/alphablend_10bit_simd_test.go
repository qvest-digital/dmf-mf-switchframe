package graphics

import (
	"bytes"
	"encoding/binary"
	"math/rand"
	"testing"
)

// TestAlphaBlendRGBA10Dispatch_MatchesReference verifies the dispatch path
// produces identical output to the direct AlphaBlendRGBA10 function.
func TestAlphaBlendRGBA10Dispatch_MatchesReference(t *testing.T) {
	t.Parallel()
	w, h := 32, 16
	rng := rand.New(rand.NewSource(42))

	// Create random YUV frame
	yuv := make([]byte, w*h*4)
	for i := 0; i < len(yuv)/2; i++ {
		binary.LittleEndian.PutUint16(yuv[i*2:], uint16(rng.Intn(1024)))
	}

	// Create random RGBA overlay
	rgba := make([]byte, w*h*4)
	for i := range rgba {
		rgba[i] = byte(rng.Intn(256))
	}

	// Reference
	refYUV := make([]byte, len(yuv))
	copy(refYUV, yuv)
	if err := AlphaBlendRGBA10(refYUV, rgba, w, h); err != nil {
		t.Fatal(err)
	}

	// Dispatch
	gotYUV := make([]byte, len(yuv))
	copy(gotYUV, yuv)
	if err := alphaBlendRGBA10Dispatch(gotYUV, rgba, w, h); err != nil {
		t.Fatal(err)
	}

	if !bytes.Equal(refYUV, gotYUV) {
		for i := 0; i < len(refYUV); i += 2 {
			rv := binary.LittleEndian.Uint16(refYUV[i:])
			gv := binary.LittleEndian.Uint16(gotYUV[i:])
			if rv != gv {
				t.Fatalf("mismatch at byte %d (sample %d): ref=%d, got=%d", i, i/2, rv, gv)
			}
		}
	}
}

// TestAlphaBlendRGBA10Dispatch_TransparentOverlay verifies no change with alpha=0.
func TestAlphaBlendRGBA10Dispatch_TransparentOverlay(t *testing.T) {
	t.Parallel()
	w, h := 16, 8
	yuv := make([]byte, w*h*4)
	for i := 0; i < len(yuv)/2; i++ {
		binary.LittleEndian.PutUint16(yuv[i*2:], 500)
	}

	// All transparent
	rgba := make([]byte, w*h*4)
	for i := 0; i < w*h; i++ {
		rgba[i*4+0] = 255 // R
		rgba[i*4+1] = 0   // G
		rgba[i*4+2] = 0   // B
		rgba[i*4+3] = 0   // A=0 → transparent
	}

	origYUV := make([]byte, len(yuv))
	copy(origYUV, yuv)

	if err := alphaBlendRGBA10Dispatch(yuv, rgba, w, h); err != nil {
		t.Fatal(err)
	}

	if !bytes.Equal(origYUV, yuv) {
		t.Fatal("transparent overlay should not modify YUV")
	}
}
