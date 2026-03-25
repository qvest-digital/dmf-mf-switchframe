package playout

import (
	"math"
	"testing"
)

// makeTestFrame creates a YUV420 frame with uniform Y, U, V values.
// For a w*h frame: Y plane = w*h bytes, U plane = w/2 * h/2 bytes, V plane = w/2 * h/2.
func makeTestFrame(w, h int, y, u, v byte, pts int64) *DecodedFrame {
	ySize := w * h
	uvSize := (w / 2) * (h / 2)
	yuv := make([]byte, ySize+2*uvSize)
	for i := 0; i < ySize; i++ {
		yuv[i] = y
	}
	for i := 0; i < uvSize; i++ {
		yuv[ySize+i] = u
	}
	for i := 0; i < uvSize; i++ {
		yuv[ySize+uvSize+i] = v
	}
	return &DecodedFrame{
		YUV:        yuv,
		Width:      w,
		Height:     h,
		PTS:        pts,
		IsKeyframe: false,
	}
}

// avgY returns the average luma value across the Y plane.
func avgY(frame *DecodedFrame) float64 {
	ySize := frame.Width * frame.Height
	if ySize == 0 {
		return 0
	}
	sum := 0
	for i := 0; i < ySize; i++ {
		sum += int(frame.YUV[i])
	}
	return float64(sum) / float64(ySize)
}

func TestCompositor_Cut_ReturnsIncoming(t *testing.T) {
	c := NewCompositor(4, 4, nil)
	b := makeTestFrame(4, 4, 16, 128, 128, 2000)

	result := c.Cut(b)

	// Cut should return b's data with b's PTS.
	if result.PTS != b.PTS {
		t.Errorf("Cut PTS = %d, want %d", result.PTS, b.PTS)
	}
	// The YUV pointer should be the same — no copy for a hard cut.
	if &result.YUV[0] != &b.YUV[0] {
		t.Error("Cut should return same YUV slice pointer as input b")
	}
}

func TestCompositor_Dissolve_AtZero(t *testing.T) {
	c := NewCompositor(4, 4, nil)
	a := makeTestFrame(4, 4, 200, 128, 128, 1000)
	b := makeTestFrame(4, 4, 16, 128, 128, 2000)

	result := c.Blend(a, b, 0.0, TransitionConfig{Type: "dissolve"})

	// At progress 0.0, result should be very close to frame A (Y=200).
	avg := avgY(result)
	if math.Abs(avg-200) > 2 {
		t.Errorf("dissolve at 0.0: avgY = %.1f, want ~200", avg)
	}
	// PTS should come from frame B.
	if result.PTS != b.PTS {
		t.Errorf("PTS = %d, want %d", result.PTS, b.PTS)
	}
}

func TestCompositor_Dissolve_AtOne(t *testing.T) {
	c := NewCompositor(4, 4, nil)
	a := makeTestFrame(4, 4, 200, 128, 128, 1000)
	b := makeTestFrame(4, 4, 16, 128, 128, 2000)

	result := c.Blend(a, b, 1.0, TransitionConfig{Type: "dissolve"})

	// At progress 1.0, result should be very close to frame B (Y=16).
	avg := avgY(result)
	if math.Abs(avg-16) > 2 {
		t.Errorf("dissolve at 1.0: avgY = %.1f, want ~16", avg)
	}
}

func TestCompositor_Dissolve_AtHalf(t *testing.T) {
	c := NewCompositor(4, 4, nil)
	a := makeTestFrame(4, 4, 200, 128, 128, 1000)
	b := makeTestFrame(4, 4, 16, 128, 128, 2000)

	result := c.Blend(a, b, 0.5, TransitionConfig{Type: "dissolve"})

	// At 0.5, result should be approximately midpoint: (200+16)/2 = 108.
	avg := avgY(result)
	expected := (200.0 + 16.0) / 2.0 // 108
	if math.Abs(avg-expected) > 3 {
		t.Errorf("dissolve at 0.5: avgY = %.1f, want ~%.0f", avg, expected)
	}
}

func TestCompositor_MixType(t *testing.T) {
	c := NewCompositor(4, 4, nil)
	a := makeTestFrame(4, 4, 200, 128, 128, 1000)
	b := makeTestFrame(4, 4, 16, 128, 128, 2000)

	result := c.Blend(a, b, 0.5, TransitionConfig{Type: "mix"})

	avg := avgY(result)
	expected := (200.0 + 16.0) / 2.0
	if math.Abs(avg-expected) > 3 {
		t.Errorf("mix at 0.5: avgY = %.1f, want ~%.0f", avg, expected)
	}
}

func TestCompositor_DipBlack_AtHalf(t *testing.T) {
	c := NewCompositor(4, 4, nil)
	a := makeTestFrame(4, 4, 200, 128, 128, 1000)
	b := makeTestFrame(4, 4, 200, 128, 128, 2000)

	result := c.Blend(a, b, 0.5, TransitionConfig{Type: "dip_black"})

	// At the midpoint of a dip-to-black, the frame should be fully black.
	// Limited-range black Y=16.
	avg := avgY(result)
	if math.Abs(avg-16) > 2 {
		t.Errorf("dip_black at 0.5: avgY = %.1f, want ~16 (limited-range black)", avg)
	}
}

func TestCompositor_DipBlack_AtZero(t *testing.T) {
	c := NewCompositor(4, 4, nil)
	a := makeTestFrame(4, 4, 200, 128, 128, 1000)
	b := makeTestFrame(4, 4, 50, 128, 128, 2000)

	result := c.Blend(a, b, 0.0, TransitionConfig{Type: "dip_black"})

	// At progress 0.0, dip should show frame A.
	avg := avgY(result)
	if math.Abs(avg-200) > 2 {
		t.Errorf("dip_black at 0.0: avgY = %.1f, want ~200", avg)
	}
}

func TestCompositor_DipBlack_AtOne(t *testing.T) {
	c := NewCompositor(4, 4, nil)
	a := makeTestFrame(4, 4, 200, 128, 128, 1000)
	b := makeTestFrame(4, 4, 50, 128, 128, 2000)

	result := c.Blend(a, b, 1.0, TransitionConfig{Type: "dip_black"})

	// At progress 1.0, dip should show frame B.
	avg := avgY(result)
	if math.Abs(avg-50) > 2 {
		t.Errorf("dip_black at 1.0: avgY = %.1f, want ~50", avg)
	}
}

func TestCompositor_Wipe_AtHalf(t *testing.T) {
	// Wipe at 0.5 means roughly half the pixels are A, half are B.
	c := NewCompositor(8, 8, nil)
	a := makeTestFrame(8, 8, 200, 128, 128, 1000)
	b := makeTestFrame(8, 8, 16, 128, 128, 2000)

	result := c.Blend(a, b, 0.5, TransitionConfig{Type: "wipe"})

	// Average Y should be roughly midpoint between 200 and 16.
	avg := avgY(result)
	expected := (200.0 + 16.0) / 2.0
	if math.Abs(avg-expected) > 20 {
		t.Errorf("wipe at 0.5: avgY = %.1f, want ~%.0f (±20 tolerance for soft edge)", avg, expected)
	}
}

func TestCompositor_UnknownType_FallsToCut(t *testing.T) {
	c := NewCompositor(4, 4, nil)
	a := makeTestFrame(4, 4, 200, 128, 128, 1000)
	b := makeTestFrame(4, 4, 16, 128, 128, 2000)

	result := c.Blend(a, b, 0.5, TransitionConfig{Type: "unknown_fancy"})

	// Unknown type should fall back to hard cut (returns B).
	if result.PTS != b.PTS {
		t.Errorf("unknown type PTS = %d, want %d", result.PTS, b.PTS)
	}
	if &result.YUV[0] != &b.YUV[0] {
		t.Error("unknown type should return same YUV slice pointer as b (hard cut)")
	}
}

func TestCompositor_NilGPU_UsesCPU(t *testing.T) {
	// Passing nil gpuBlend should work without panic.
	c := NewCompositor(4, 4, nil)
	a := makeTestFrame(4, 4, 200, 128, 128, 1000)
	b := makeTestFrame(4, 4, 16, 128, 128, 2000)

	// Should not panic.
	result := c.Blend(a, b, 0.5, TransitionConfig{Type: "dissolve"})
	if result == nil {
		t.Fatal("Blend returned nil with nil GPU func")
	}
	if result.Width != 4 || result.Height != 4 {
		t.Errorf("dimensions = %dx%d, want 4x4", result.Width, result.Height)
	}
}

func TestCompositor_GPUFunc_CalledWhenProvided(t *testing.T) {
	called := false
	var capturedProgress float32
	var capturedType string

	gpuFn := func(a, b []byte, dst []byte, w, h int, progress float32, blendType string) {
		called = true
		capturedProgress = progress
		capturedType = blendType
		// Fill dst with a known value so we can verify.
		for i := range dst {
			dst[i] = 42
		}
	}

	c := NewCompositor(4, 4, gpuFn)
	frameA := makeTestFrame(4, 4, 200, 128, 128, 1000)
	frameB := makeTestFrame(4, 4, 16, 128, 128, 2000)

	result := c.Blend(frameA, frameB, 0.75, TransitionConfig{Type: "dissolve"})

	if !called {
		t.Fatal("GPU blend function was not called")
	}
	if math.Abs(float64(capturedProgress)-0.75) > 0.001 {
		t.Errorf("GPU progress = %f, want 0.75", capturedProgress)
	}
	if capturedType != "dissolve" {
		t.Errorf("GPU blend type = %q, want %q", capturedType, "dissolve")
	}
	if result.YUV[0] != 42 {
		t.Errorf("GPU output not used; first byte = %d, want 42", result.YUV[0])
	}
	if result.PTS != frameB.PTS {
		t.Errorf("PTS = %d, want %d", result.PTS, frameB.PTS)
	}
}

func TestCompositor_Resize(t *testing.T) {
	c := NewCompositor(4, 4, nil)

	if c.width != 4 || c.height != 4 {
		t.Fatalf("initial dimensions = %dx%d, want 4x4", c.width, c.height)
	}

	c.Resize(8, 8)

	if c.width != 8 || c.height != 8 {
		t.Errorf("after Resize: dimensions = %dx%d, want 8x8", c.width, c.height)
	}

	// Verify the blender works at the new size by running a blend.
	a := makeTestFrame(8, 8, 200, 128, 128, 1000)
	b := makeTestFrame(8, 8, 16, 128, 128, 2000)
	result := c.Blend(a, b, 0.5, TransitionConfig{Type: "dissolve"})
	if result == nil {
		t.Fatal("Blend after Resize returned nil")
	}
	if result.Width != 8 || result.Height != 8 {
		t.Errorf("result dimensions = %dx%d, want 8x8", result.Width, result.Height)
	}
}

func TestCompositor_Blend_CopiesOutput(t *testing.T) {
	// The FrameBlender reuses its internal buffer. Verify that Compositor
	// copies the output so successive calls don't clobber previous results.
	c := NewCompositor(4, 4, nil)
	a := makeTestFrame(4, 4, 200, 128, 128, 1000)
	b := makeTestFrame(4, 4, 16, 128, 128, 2000)

	result1 := c.Blend(a, b, 0.25, TransitionConfig{Type: "dissolve"})
	y1 := result1.YUV[0] // capture first byte

	// Second blend should NOT corrupt result1.
	_ = c.Blend(a, b, 0.75, TransitionConfig{Type: "dissolve"})

	if result1.YUV[0] != y1 {
		t.Errorf("result1 was corrupted by second Blend: first byte = %d, was %d", result1.YUV[0], y1)
	}
}

func TestCompositor_Blend_ProgressClamping(t *testing.T) {
	c := NewCompositor(4, 4, nil)
	a := makeTestFrame(4, 4, 200, 128, 128, 1000)
	b := makeTestFrame(4, 4, 16, 128, 128, 2000)

	// Progress below 0.0 should clamp (no panic, result close to A).
	r1 := c.Blend(a, b, -0.5, TransitionConfig{Type: "dissolve"})
	if r1 == nil {
		t.Fatal("Blend with negative progress returned nil")
	}
	avg1 := avgY(r1)
	if math.Abs(avg1-200) > 2 {
		t.Errorf("progress=-0.5: avgY = %.1f, want ~200", avg1)
	}

	// Progress above 1.0 should clamp (no panic, result close to B).
	r2 := c.Blend(a, b, 1.5, TransitionConfig{Type: "dissolve"})
	if r2 == nil {
		t.Fatal("Blend with progress >1.0 returned nil")
	}
	avg2 := avgY(r2)
	if math.Abs(avg2-16) > 2 {
		t.Errorf("progress=1.5: avgY = %.1f, want ~16", avg2)
	}
}
