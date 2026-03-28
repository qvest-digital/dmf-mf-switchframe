package graphics

import (
	"testing"
)

func TestChromaKey10_ExactMatch(t *testing.T) {
	w, h := 16, 8
	yuv := make10bitGraphicsFrame(w, h, 500, 300, 700) // uniform chroma
	mask := ChromaKey10(yuv, w, h, 300, 700, 50, 10)   // target=300,700
	if len(mask) != w*h {
		t.Fatalf("mask length: got %d, want %d", len(mask), w*h)
	}
	// All pixels should be keyed (mask=255 = transparent in key space)
	if mask[0] != 255 {
		t.Errorf("exact chroma match: mask=%d, want 255", mask[0])
	}
	// Check a pixel in the middle too
	if mask[w*h/2] != 255 {
		t.Errorf("exact chroma match (mid): mask=%d, want 255", mask[w*h/2])
	}
}

func TestChromaKey10_NoMatch(t *testing.T) {
	w, h := 16, 8
	yuv := make10bitGraphicsFrame(w, h, 500, 300, 700)
	mask := ChromaKey10(yuv, w, h, 800, 200, 50, 10) // far from target
	if len(mask) != w*h {
		t.Fatalf("mask length: got %d, want %d", len(mask), w*h)
	}
	// No pixels should be keyed
	if mask[0] != 0 {
		t.Errorf("no match: mask=%d, want 0", mask[0])
	}
}

func TestChromaKey10_SoftnessGradient(t *testing.T) {
	w, h := 16, 8
	// Target at (512, 512), pixel at (512, 612) = distance 100
	yuv := make10bitGraphicsFrame(w, h, 500, 512, 612)
	// tolerance=80, softness=40 -> total distance = 120
	// pixel distance = 100, which is in the smooth zone (80..120)
	mask := ChromaKey10(yuv, w, h, 512, 512, 80, 40)
	if len(mask) != w*h {
		t.Fatalf("mask length: got %d, want %d", len(mask), w*h)
	}
	// Should be partially keyed (between 0 and 255)
	if mask[0] == 0 || mask[0] == 255 {
		t.Errorf("softness gradient: mask=%d, want partial (between 1 and 254)", mask[0])
	}
}

func TestChromaKey10_ZeroDimensions(t *testing.T) {
	mask := ChromaKey10(nil, 0, 0, 512, 512, 50, 10)
	if mask != nil {
		t.Errorf("zero dimensions should return nil, got len=%d", len(mask))
	}
}

func TestChromaKey10_OddWidth(t *testing.T) {
	// YUV422 requires even width, function should handle gracefully
	w, h := 15, 8
	yuv := make([]byte, w*h*4) // won't be a valid YUV422P10LE frame
	mask := ChromaKey10(yuv, w, h, 512, 512, 50, 10)
	// Should either return nil or a valid-length mask, not panic
	_ = mask
}

func TestLumaKey10_InRange(t *testing.T) {
	w, h := 16, 8
	yuv := make10bitGraphicsFrame(w, h, 500, 512, 512)
	mask := LumaKey10(yuv, w, h, 400, 600) // 500 is in [400,600]
	if len(mask) != w*h {
		t.Fatalf("mask length: got %d, want %d", len(mask), w*h)
	}
	if mask[0] != 255 {
		t.Errorf("in range: mask=%d, want 255", mask[0])
	}
}

func TestLumaKey10_OutOfRange_Below(t *testing.T) {
	w, h := 16, 8
	yuv := make10bitGraphicsFrame(w, h, 100, 512, 512)
	mask := LumaKey10(yuv, w, h, 400, 600) // 100 is below [400,600]
	if len(mask) != w*h {
		t.Fatalf("mask length: got %d, want %d", len(mask), w*h)
	}
	if mask[0] != 0 {
		t.Errorf("out of range below: mask=%d, want 0", mask[0])
	}
}

func TestLumaKey10_OutOfRange_Above(t *testing.T) {
	w, h := 16, 8
	yuv := make10bitGraphicsFrame(w, h, 900, 512, 512)
	mask := LumaKey10(yuv, w, h, 400, 600) // 900 is above [400,600]
	if len(mask) != w*h {
		t.Fatalf("mask length: got %d, want %d", len(mask), w*h)
	}
	if mask[0] != 0 {
		t.Errorf("out of range above: mask=%d, want 0", mask[0])
	}
}

func TestLumaKey10_AtBoundary(t *testing.T) {
	w, h := 16, 8
	// Exactly at the low threshold
	yuv := make10bitGraphicsFrame(w, h, 400, 512, 512)
	mask := LumaKey10(yuv, w, h, 400, 600)
	if len(mask) != w*h {
		t.Fatalf("mask length: got %d, want %d", len(mask), w*h)
	}
	// At boundary should be opaque (in range)
	if mask[0] != 255 {
		t.Errorf("at low boundary: mask=%d, want 255", mask[0])
	}

	// Exactly at the high threshold
	yuv = make10bitGraphicsFrame(w, h, 600, 512, 512)
	mask = LumaKey10(yuv, w, h, 400, 600)
	if mask[0] != 255 {
		t.Errorf("at high boundary: mask=%d, want 255", mask[0])
	}
}

func TestLumaKey10_FullRange(t *testing.T) {
	w, h := 16, 8
	// With thresholds 0 and 1023, all pixels should be opaque
	yuv := make10bitGraphicsFrame(w, h, 500, 512, 512)
	mask := LumaKey10(yuv, w, h, 0, 1023)
	if len(mask) != w*h {
		t.Fatalf("mask length: got %d, want %d", len(mask), w*h)
	}
	if mask[0] != 255 {
		t.Errorf("full range: mask=%d, want 255", mask[0])
	}
}

func TestLumaKey10_ZeroDimensions(t *testing.T) {
	mask := LumaKey10(nil, 0, 0, 400, 600)
	if mask != nil {
		t.Errorf("zero dimensions should return nil, got len=%d", len(mask))
	}
}

func TestLumaKey10_ChromaDataIgnored(t *testing.T) {
	w, h := 16, 8
	// Same Y value but different chroma should produce the same mask
	yuv1 := make10bitGraphicsFrame(w, h, 500, 300, 700)
	yuv2 := make10bitGraphicsFrame(w, h, 500, 700, 300)
	mask1 := LumaKey10(yuv1, w, h, 400, 600)
	mask2 := LumaKey10(yuv2, w, h, 400, 600)
	if len(mask1) != len(mask2) {
		t.Fatalf("mask lengths differ: %d vs %d", len(mask1), len(mask2))
	}
	for i := range mask1 {
		if mask1[i] != mask2[i] {
			t.Errorf("pixel %d: mask differs (%d vs %d), chroma should be ignored", i, mask1[i], mask2[i])
			break
		}
	}
}

func TestChromaKey10_MaskIsLumaResolution(t *testing.T) {
	// The mask should have one byte per luma pixel, not per chroma pixel
	w, h := 16, 8
	yuv := make10bitGraphicsFrame(w, h, 500, 300, 700)
	mask := ChromaKey10(yuv, w, h, 300, 700, 50, 10)
	if len(mask) != w*h {
		t.Errorf("mask should be luma resolution: got %d, want %d", len(mask), w*h)
	}
}
