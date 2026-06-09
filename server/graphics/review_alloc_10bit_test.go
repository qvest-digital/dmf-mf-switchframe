package graphics

import (
	"bytes"
	"image"
	"sync"
	"testing"
)

// makeVaryingRGBA builds an RGBA overlay whose pixels vary with position so that
// reused-buffer bugs (stale data leaking through) are detectable.
func makeVaryingRGBA(w, h int) []byte {
	rgba := make([]byte, w*h*4)
	for i := 0; i < w*h; i++ {
		rgba[i*4+0] = byte((i*7 + 13) & 0xff)
		rgba[i*4+1] = byte((i*3 + 29) & 0xff)
		rgba[i*4+2] = byte((i*5 + 71) & 0xff)
		// Vary alpha across the full range, including 0 (transparent) holes.
		rgba[i*4+3] = byte((i * 11) & 0xff)
	}
	return rgba
}

// TestAlphaBlendRGBA10Scaled_IdentityAtFullAlpha pins that the scaled blend at
// alphaScale==1.0 is byte-identical to AlphaBlendRGBA10. This guards the fade
// fold-in that replaced applyFadeAlpha's per-frame overlay copy in ProcessYUV10.
func TestAlphaBlendRGBA10Scaled_IdentityAtFullAlpha(t *testing.T) {
	w, h := 16, 8
	rgba := makeVaryingRGBA(w, h)

	ref := make10bitGraphicsFrame(w, h, 500, 400, 600)
	got := make10bitGraphicsFrame(w, h, 500, 400, 600)

	if err := AlphaBlendRGBA10(ref, rgba, w, h); err != nil {
		t.Fatalf("AlphaBlendRGBA10: %v", err)
	}
	if err := AlphaBlendRGBA10Scaled(got, rgba, w, h, 1.0); err != nil {
		t.Fatalf("AlphaBlendRGBA10Scaled: %v", err)
	}
	if !bytes.Equal(ref, got) {
		t.Fatal("AlphaBlendRGBA10Scaled(scale=1.0) must be byte-identical to AlphaBlendRGBA10")
	}
}

// TestAlphaBlendRGBA10Scaled_MatchesPreScaledAlpha pins that folding the fade
// into the kernel is equivalent (within fixed-point rounding) to pre-scaling the
// overlay alpha and blending at full alpha — the behavior the ProcessYUV10
// full-frame fade path replaced (the old applyFadeAlpha copy).
//
// The kernel maps alpha exactly to 0-256 (A + A>>7, matching the 8-bit path)
// before applying the fade, while the pre-scaled reference must round the
// scaled alpha back to an 8-bit byte first. The double rounding diverges the
// effective alpha weight by at most a couple of /256 steps, which bounds the
// per-sample difference to a few 10-bit LSBs.
func TestAlphaBlendRGBA10Scaled_MatchesPreScaledAlpha(t *testing.T) {
	w, h := 16, 8
	rgba := makeVaryingRGBA(w, h)

	for _, fade := range []float64{0.0, 0.25, 0.5, 0.75, 0.9} {
		alphaScale256 := int32(fade*256 + 0.5)

		// Reference: copy overlay, pre-scale alpha with the same fixed-point
		// factor the kernel folds in, then blend at full alpha.
		preScaled := make([]byte, len(rgba))
		copy(preScaled, rgba)
		for i := 0; i < w*h; i++ {
			a := int32(preScaled[i*4+3])
			a = (a*alphaScale256 + 128) >> 8
			if a > 255 {
				a = 255
			}
			preScaled[i*4+3] = byte(a)
		}
		want := make10bitGraphicsFrame(w, h, 512, 480, 540)
		if err := AlphaBlendRGBA10(want, preScaled, w, h); err != nil {
			t.Fatalf("AlphaBlendRGBA10: %v", err)
		}

		// Kernel: fold the same fade into the blend.
		got := make10bitGraphicsFrame(w, h, 512, 480, 540)
		if err := AlphaBlendRGBA10Scaled(got, rgba, w, h, fade); err != nil {
			t.Fatalf("AlphaBlendRGBA10Scaled: %v", err)
		}

		// Double rounding of the pre-scaled 8-bit alpha allows a small divergence.
		assert10bitClose(t, want, got, w, h, 8, fade)
	}
}

// assert10bitClose checks every uint16 sample in two YUV422P10LE buffers agrees
// within tol.
func assert10bitClose(t *testing.T, a, b []byte, w, h int, tol int, ctx float64) {
	t.Helper()
	if len(a) != len(b) {
		t.Fatalf("length mismatch: %d vs %d", len(a), len(b))
	}
	for i := 0; i+1 < len(a); i += 2 {
		va := int(a[i]) | int(a[i+1])<<8
		vb := int(b[i]) | int(b[i+1])<<8
		d := va - vb
		if d < 0 {
			d = -d
		}
		if d > tol {
			t.Fatalf("fade=%.2f: sample %d differs by %d (want %d, got %d), tol=%d", ctx, i/2, d, va, vb, tol)
		}
	}
}

// TestChromaKey10Into_ReusedDirtyBuffer verifies ChromaKey10Into returns the
// same mask as ChromaKey10 even when the provided buffer is pre-filled with
// stale data — the dirty-buffer safety the bridge's reuse depends on.
func TestChromaKey10Into_ReusedDirtyBuffer(t *testing.T) {
	w, h := 32, 16
	yuv := make10bitGraphicsFrame(w, h, 500, 512, 612) // partial chroma match
	want := ChromaKey10(yuv, w, h, 512, 512, 80, 40)
	if want == nil {
		t.Fatal("ChromaKey10 returned nil")
	}

	// Pre-fill the scratch with 0xAA so any unwritten pixel would be wrong.
	scratch := make([]byte, w*h)
	for i := range scratch {
		scratch[i] = 0xAA
	}
	got := ChromaKey10Into(scratch, yuv, w, h, 512, 512, 80, 40)
	if !bytes.Equal(want, got) {
		t.Fatal("ChromaKey10Into with dirty reused buffer must match ChromaKey10")
	}
	// Confirm the buffer was actually reused (no allocation).
	if &got[0] != &scratch[0] {
		t.Fatal("ChromaKey10Into should reuse the provided buffer when large enough")
	}
}

// TestLumaKey10Into_ReusedDirtyBuffer verifies LumaKey10Into clears out-of-range
// pixels in a reused buffer (LumaKey10 relies on zero-init for keyed pixels).
func TestLumaKey10Into_ReusedDirtyBuffer(t *testing.T) {
	w, h := 32, 16
	// Y=900 is out of the [200,600] range → all pixels keyed (mask=0).
	yuv := make10bitGraphicsFrame(w, h, 900, 512, 512)
	want := LumaKey10(yuv, w, h, 200, 600)
	if want == nil {
		t.Fatal("LumaKey10 returned nil")
	}

	scratch := make([]byte, w*h)
	for i := range scratch {
		scratch[i] = 0xFF // stale "opaque" that must be overwritten to 0
	}
	got := LumaKey10Into(scratch, yuv, w, h, 200, 600)
	if !bytes.Equal(want, got) {
		t.Fatal("LumaKey10Into with dirty reused buffer must match LumaKey10 (stale bytes not cleared)")
	}
	if &got[0] != &scratch[0] {
		t.Fatal("LumaKey10Into should reuse the provided buffer when large enough")
	}
}

// TestAlphaBlendRGBARect10Into_ReusedBufferMatches verifies the Into variant
// produces identical output to the convenience wrapper across two consecutive
// calls that reuse the returned xCoords buffer, including at a mismatched
// resolution where xCoords entries are non-trivial.
func TestAlphaBlendRGBARect10Into_ReusedBufferMatches(t *testing.T) {
	frameW, frameH := 64, 32
	overlayW, overlayH := 20, 10 // resolution mismatch → real bilinear X map
	rgba := makeVaryingRGBA(overlayW, overlayH)
	rect := image.Rect(8, 4, 40, 24)

	want := make10bitGraphicsFrame(frameW, frameH, 512, 500, 524)
	if err := AlphaBlendRGBARect10(want, rgba, frameW, frameH, overlayW, overlayH, rect, 0.8); err != nil {
		t.Fatalf("AlphaBlendRGBARect10: %v", err)
	}

	// First Into call allocates the buffer; second reuses it.
	gotA := make10bitGraphicsFrame(frameW, frameH, 512, 500, 524)
	xc, err := AlphaBlendRGBARect10Into(gotA, rgba, frameW, frameH, overlayW, overlayH, rect, 0.8, nil)
	if err != nil {
		t.Fatalf("AlphaBlendRGBARect10Into (alloc): %v", err)
	}
	if !bytes.Equal(want, gotA) {
		t.Fatal("AlphaBlendRGBARect10Into (allocating) must match AlphaBlendRGBARect10")
	}

	gotB := make10bitGraphicsFrame(frameW, frameH, 512, 500, 524)
	xc2, err := AlphaBlendRGBARect10Into(gotB, rgba, frameW, frameH, overlayW, overlayH, rect, 0.8, xc)
	if err != nil {
		t.Fatalf("AlphaBlendRGBARect10Into (reuse): %v", err)
	}
	if !bytes.Equal(want, gotB) {
		t.Fatal("AlphaBlendRGBARect10Into (reused buffer) must match AlphaBlendRGBARect10")
	}
	if cap(xc2) < rect.Dx() {
		t.Fatalf("returned xCoords too small: cap=%d, need>=%d", cap(xc2), rect.Dx())
	}
}

// TestKeyProcessorBridge_ProcessYUV10_StableAcrossReusedBuffers calls ProcessYUV10
// twice on identical inputs and requires identical output. Because the bridge now
// reuses persistent mask buffers across calls, a dirty-buffer bug would surface
// as a difference between the first (clean) and second (reused) call.
func TestKeyProcessorBridge_ProcessYUV10_StableAcrossReusedBuffers(t *testing.T) {
	for _, kc := range []KeyConfig{
		{Type: KeyTypeChroma, Enabled: true, KeyColorCb: 30, KeyColorCr: 12, Similarity: 0.4, Smoothness: 0.3},
		{Type: KeyTypeLuma, Enabled: true, LowClip: 0.5, HighClip: 1.0},
	} {
		kp := NewKeyProcessor()
		kp.SetKey("cam1", kc)
		bridge := NewKeyProcessorBridge(kp)

		w, h := 32, 16
		fill := make10bitGraphicsFrame(w, h, 728, 120, 48)
		bridge.IngestFillYUV10("cam1", fill, w, h)

		program := make10bitGraphicsFrame(w, h, 512, 512, 512)

		first := make([]byte, len(program))
		copy(first, program)
		_ = bridge.ProcessYUV10(first, w, h)

		second := make([]byte, len(program))
		copy(second, program)
		_ = bridge.ProcessYUV10(second, w, h)

		if !bytes.Equal(first, second) {
			t.Fatalf("ProcessYUV10 output differs across reused-buffer calls for key type %v", kc.Type)
		}
	}
}

// TestCompositorClose_ConcurrentMutators is a race-detector regression for the
// Close() map-iteration-while-lock-dropped finding. Close snapshots the layer
// pointers before cancelling, so concurrent AddLayer/RemoveLayer that race the
// closed flag must not trigger "concurrent map iteration and map write".
func TestCompositorClose_ConcurrentMutators(t *testing.T) {
	for iter := 0; iter < 20; iter++ {
		c := NewCompositor()
		ids := make([]int, 0, 8)
		for i := 0; i < 6; i++ {
			id, err := c.AddLayer()
			if err != nil {
				t.Fatalf("AddLayer: %v", err)
			}
			ids = append(ids, id)
		}

		var wg sync.WaitGroup
		wg.Add(2)
		go func() {
			defer wg.Done()
			for _, id := range ids {
				_ = c.RemoveLayer(id)
			}
			_, _ = c.AddLayer()
		}()
		go func() {
			defer wg.Done()
			c.Close()
		}()
		wg.Wait()
	}
}
