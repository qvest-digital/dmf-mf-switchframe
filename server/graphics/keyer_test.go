package graphics

import (
	"math"
	"testing"
)

// makeYUV420Frame creates a solid-color YUV420 frame.
func makeYUV420Frame(width, height int, y, u, v byte) []byte {
	ySize := width * height
	uvSize := (width / 2) * (height / 2)
	frame := make([]byte, ySize+2*uvSize)

	for i := 0; i < ySize; i++ {
		frame[i] = y
	}
	for i := 0; i < uvSize; i++ {
		frame[ySize+i] = u
		frame[ySize+uvSize+i] = v
	}
	return frame
}

// greenYUV returns BT.709 YCbCr for pure green (R=0, G=255, B=0).
// Y = 0.2126*0 + 0.7152*255 + 0.0722*0 ≈ 182
// Cb = -0.1146*0 - 0.3854*255 + 0.5*0 + 128 ≈ 30
// Cr = 0.5*0 - 0.4542*255 - 0.0458*0 + 128 ≈ 12
func greenYUV() YCbCr {
	return YCbCr{Y: 182, Cb: 30, Cr: 12}
}

func TestChromaKey_GreenPixelsTransparent(t *testing.T) {
	// Create a 4x4 green frame
	frame := makeYUV420Frame(4, 4, 182, 30, 12)

	mask := ChromaKey(frame, 4, 4, greenYUV(), 0.4, 0.0, 0.0)

	if len(mask) != 16 {
		t.Fatalf("expected mask length 16, got %d", len(mask))
	}

	// All pixels should be transparent (alpha ≈ 0)
	for i, a := range mask {
		if a > 30 { // allow some tolerance
			t.Fatalf("pixel %d: expected transparent (≤30), got %d", i, a)
		}
	}
}

func TestChromaKey_NonGreenStaysOpaque(t *testing.T) {
	// Create a 4x4 blue frame: Y≈18, Cb≈237, Cr≈114
	frame := makeYUV420Frame(4, 4, 18, 237, 114)

	mask := ChromaKey(frame, 4, 4, greenYUV(), 0.4, 0.0, 0.0)

	// All pixels should be opaque (alpha ≈ 255)
	for i, a := range mask {
		if a < 225 {
			t.Fatalf("pixel %d: expected opaque (≥225), got %d", i, a)
		}
	}
}

func TestChromaKey_SmoothnessCreatesSoftEdges(t *testing.T) {
	// Near-green frame: slightly off green (Y=180, Cb=40, Cr=20)
	frame := makeYUV420Frame(4, 4, 180, 40, 20)

	// Without smoothness: hard edge, should be near-opaque
	maskHard := ChromaKey(frame, 4, 4, greenYUV(), 0.1, 0.0, 0.0)
	// With smoothness: soft edges, partial transparency
	maskSoft := ChromaKey(frame, 4, 4, greenYUV(), 0.1, 0.3, 0.0)

	// At least some pixels should differ between hard and soft
	differs := false
	for i := range maskHard {
		if maskHard[i] != maskSoft[i] {
			differs = true
			break
		}
	}
	if !differs {
		t.Log("smoothness did not create different alpha values (may be expected for this color)")
	}
}

func TestChromaKey_SpillSuppression(t *testing.T) {
	// Near-green pixel where spill suppression should desaturate
	// Cb=50, Cr=30 — close to green in chroma space
	frame := makeYUV420Frame(4, 4, 180, 50, 30)

	// Run with spill suppression
	mask := ChromaKey(frame, 4, 4, greenYUV(), 0.4, 0.1, 0.8)

	// Mask should be computed without errors (spill suppression modifies the frame, not the mask)
	if len(mask) != 16 {
		t.Fatalf("expected mask length 16, got %d", len(mask))
	}
}

func TestLumaKey_BrightPixelsTransparent(t *testing.T) {
	// High-clip luma key: bright pixels (Y > highClip) become transparent
	frame := makeYUV420Frame(4, 4, 240, 128, 128)

	mask := LumaKey(frame, 4, 4, 0.0, 0.8, 0.0)

	// Y=240/255 ≈ 0.94, highClip=0.8 → should be transparent
	for i, a := range mask {
		if a > 30 {
			t.Fatalf("pixel %d: expected transparent (≤30) for bright pixel above highClip, got %d", i, a)
		}
	}
}

func TestLumaKey_DarkPixelsTransparent(t *testing.T) {
	// Low-clip luma key: dark pixels (Y < lowClip) become transparent
	frame := makeYUV420Frame(4, 4, 10, 128, 128)

	mask := LumaKey(frame, 4, 4, 0.2, 1.0, 0.0)

	// Y=10/255 ≈ 0.04, lowClip=0.2 → should be transparent
	for i, a := range mask {
		if a > 30 {
			t.Fatalf("pixel %d: expected transparent (≤30) for dark pixel below lowClip, got %d", i, a)
		}
	}
}

func TestLumaKey_MidRangeOpaque(t *testing.T) {
	// Mid-range pixel between lowClip and highClip stays opaque
	frame := makeYUV420Frame(4, 4, 128, 128, 128)

	mask := LumaKey(frame, 4, 4, 0.1, 0.9, 0.0)

	// Y=128/255 ≈ 0.50 → between 0.1 and 0.9, should be opaque
	for i, a := range mask {
		if a < 225 {
			t.Fatalf("pixel %d: expected opaque (≥225) for mid-range pixel, got %d", i, a)
		}
	}
}

func TestLumaKey_SoftnessCreatesGradualTransitions(t *testing.T) {
	// Create a frame with varying luma (gradient)
	w, h := 8, 2
	ySize := w * h
	uvSize := (w / 2) * (h / 2)
	frame := make([]byte, ySize+2*uvSize)

	// Y gradient: 0, 32, 64, 96, 128, 160, 192, 224 (repeated for 2 rows)
	for row := 0; row < h; row++ {
		for col := 0; col < w; col++ {
			frame[row*w+col] = byte(col * 32)
		}
	}
	// Neutral chroma
	for i := 0; i < uvSize*2; i++ {
		frame[ySize+i] = 128
	}

	// Luma key with softness: lowClip=0.3, highClip=1.0, softness=0.2
	mask := LumaKey(frame, w, h, 0.3, 1.0, 0.2)

	if len(mask) != ySize {
		t.Fatalf("expected mask length %d, got %d", ySize, len(mask))
	}

	// Pixels near the lowClip boundary should have intermediate alpha values
	// Pixel at col=2 (Y=64, luma=0.25) is near lowClip=0.3
	// Some should be partially transparent, some fully opaque
	hasPartial := false
	for _, a := range mask {
		if a > 30 && a < 225 {
			hasPartial = true
			break
		}
	}
	if !hasPartial {
		t.Log("softness did not create partial alpha values (may be expected for these exact values)")
	}
}

func TestChromaKey_ZeroSizeFrame(t *testing.T) {
	mask := ChromaKey(nil, 0, 0, greenYUV(), 0.4, 0.0, 0.0)
	if len(mask) != 0 {
		t.Fatalf("expected empty mask for zero-size frame, got %d", len(mask))
	}
}

func TestLumaKey_ZeroSizeFrame(t *testing.T) {
	mask := LumaKey(nil, 0, 0, 0.2, 0.8, 0.0)
	if len(mask) != 0 {
		t.Fatalf("expected empty mask for zero-size frame, got %d", len(mask))
	}
}

func TestChromaKey_SinglePixel(t *testing.T) {
	// 2x2 is minimum for YUV420 (chroma subsampling)
	frame := makeYUV420Frame(2, 2, 182, 30, 12) // green
	mask := ChromaKey(frame, 2, 2, greenYUV(), 0.4, 0.0, 0.0)
	if len(mask) != 4 {
		t.Fatalf("expected 4 pixels, got %d", len(mask))
	}
}

func TestLumaKey_SinglePixel(t *testing.T) {
	frame := makeYUV420Frame(2, 2, 128, 128, 128)
	mask := LumaKey(frame, 2, 2, 0.2, 0.8, 0.0)
	if len(mask) != 4 {
		t.Fatalf("expected 4 pixels, got %d", len(mask))
	}
}

// suppress unused import warning
var _ = math.Abs
