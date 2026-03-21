package stmap

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestProcessor_Identity_NoChange(t *testing.T) {
	const w, h = 8, 8
	m := Identity(w, h)
	p := NewProcessor(m)

	require.True(t, p.Active())

	frameSize := w * h * 3 / 2
	src := make([]byte, frameSize)
	dst := make([]byte, frameSize)

	// Fill Y plane with a gradient pattern.
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			src[y*w+x] = byte(y*w + x)
		}
	}
	// Fill Cb/Cr planes with distinct values.
	cbOff := w * h
	crOff := cbOff + (w/2)*(h/2)
	cw, ch := w/2, h/2
	for y := 0; y < ch; y++ {
		for x := 0; x < cw; x++ {
			src[cbOff+y*cw+x] = byte(100 + y*cw + x)
			src[crOff+y*cw+x] = byte(200 + y*cw + x)
		}
	}

	p.ProcessYUV(dst, src, w, h)

	// Identity map should preserve Y values within ±1 (bilinear rounding).
	for i := 0; i < w*h; i++ {
		diff := int(dst[i]) - int(src[i])
		if diff < -1 || diff > 1 {
			t.Errorf("Y[%d]: got %d, want %d (±1)", i, dst[i], src[i])
		}
	}

	// Cb plane: within ±1.
	for i := 0; i < cw*ch; i++ {
		diff := int(dst[cbOff+i]) - int(src[cbOff+i])
		if diff < -1 || diff > 1 {
			t.Errorf("Cb[%d]: got %d, want %d (±1)", i, dst[cbOff+i], src[cbOff+i])
		}
	}

	// Cr plane: within ±1.
	for i := 0; i < cw*ch; i++ {
		diff := int(dst[crOff+i]) - int(src[crOff+i])
		if diff < -1 || diff > 1 {
			t.Errorf("Cr[%d]: got %d, want %d (±1)", i, dst[crOff+i], src[crOff+i])
		}
	}
}

func TestProcessor_HorizontalFlip(t *testing.T) {
	const w, h = 8, 4
	m := Identity(w, h)

	// Reverse S coordinates so each pixel samples from the opposite side.
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			idx := y*w + x
			// Map pixel x to (w-1-x+0.5)/w = pixel-center of the mirrored position.
			m.S[idx] = (float32(w-1-x) + 0.5) / float32(w)
		}
	}

	p := NewProcessor(m)

	frameSize := w * h * 3 / 2
	src := make([]byte, frameSize)
	dst := make([]byte, frameSize)

	// Fill Y with column index so horizontal flip is visible.
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			src[y*w+x] = byte(x * 30) // 0,30,60,...,210
		}
	}

	p.ProcessYUV(dst, src, w, h)

	// Y plane should be horizontally flipped within ±1.
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			expected := int(src[y*w+(w-1-x)])
			got := int(dst[y*w+x])
			diff := got - expected
			if diff < -1 || diff > 1 {
				t.Errorf("Y[%d,%d]: got %d, want %d (±1)", x, y, got, expected)
			}
		}
	}
}

func TestProcessor_NilMap_NoOp(t *testing.T) {
	p := NewProcessor(nil)
	require.False(t, p.Active())

	// ProcessYUV should be a safe no-op.
	src := make([]byte, 48) // 8x4 * 3/2
	dst := make([]byte, 48)
	for i := range src {
		src[i] = byte(i)
	}

	p.ProcessYUV(dst, src, 8, 4)

	// dst should remain all zeros — no-op.
	for i := range dst {
		require.Zero(t, dst[i], "dst[%d] should be zero for nil map", i)
	}
}

func TestProcessor_ChromaPlanes(t *testing.T) {
	const w, h = 8, 8
	cw, ch := w/2, h/2

	// Create an ST map that shifts everything down by half the image.
	// T maps y -> (y + h/2) mod h, pixel-center.
	m := Identity(w, h)
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			idx := y*w + x
			shifted := (y + h/2) % h
			m.T[idx] = (float32(shifted) + 0.5) / float32(h)
		}
	}

	p := NewProcessor(m)

	frameSize := w * h * 3 / 2
	src := make([]byte, frameSize)
	dst := make([]byte, frameSize)

	// Fill Cb with row-varying values.
	cbOff := w * h
	for y := 0; y < ch; y++ {
		for x := 0; x < cw; x++ {
			src[cbOff+y*cw+x] = byte(y * 50)
		}
	}

	p.ProcessYUV(dst, src, w, h)

	// Cb should be vertically shifted by ch/2.
	for y := 0; y < ch; y++ {
		for x := 0; x < cw; x++ {
			srcY := (y + ch/2) % ch
			expected := int(src[cbOff+srcY*cw+x])
			got := int(dst[cbOff+y*cw+x])
			diff := got - expected
			if diff < -1 || diff > 1 {
				t.Errorf("Cb[%d,%d]: got %d, want %d (±1)", x, y, got, expected)
			}
		}
	}
}

func TestProcessor_OutOfBounds_Clamps(t *testing.T) {
	const w, h = 4, 4

	// Create an ST map with values well outside 0-1.
	m, err := NewSTMap("oob", w, h)
	require.NoError(t, err)
	for i := range m.S {
		m.S[i] = -1.0 // Far left of source
		m.T[i] = 2.0  // Far below source
	}

	p := NewProcessor(m)

	frameSize := w * h * 3 / 2
	src := make([]byte, frameSize)
	dst := make([]byte, frameSize)

	// Fill Y with distinct values at edges.
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			src[y*w+x] = byte(10*(y+1) + x)
		}
	}

	// Should not panic.
	p.ProcessYUV(dst, src, w, h)

	// All Y pixels should map to the clamped edge: bottom-left corner.
	// With S=-1.0, clamped to x=0; T=2.0, clamped to y=h-1.
	// Bottom-left pixel is src[(h-1)*w + 0].
	expected := int(src[(h-1)*w+0])
	for i := 0; i < w*h; i++ {
		got := int(dst[i])
		diff := got - expected
		if diff < -1 || diff > 1 {
			t.Errorf("Y[%d]: got %d, want %d (±1)", i, got, expected)
		}
	}
}

func TestProcessor_LargerFrame(t *testing.T) {
	// Test with a more realistic resolution to verify LUT correctness.
	const w, h = 64, 48
	m := Identity(w, h)
	p := NewProcessor(m)

	frameSize := w * h * 3 / 2
	src := make([]byte, frameSize)
	dst := make([]byte, frameSize)

	// Fill with pseudo-random pattern.
	for i := range src {
		src[i] = byte((i * 137 + 43) & 0xFF)
	}

	p.ProcessYUV(dst, src, w, h)

	// Identity should preserve within ±1.
	for i := 0; i < frameSize; i++ {
		diff := int(dst[i]) - int(src[i])
		if diff < -1 || diff > 1 {
			t.Errorf("pixel[%d]: got %d, want %d (±1)", i, dst[i], src[i])
			if i > 10 {
				t.Fatal("too many errors, stopping")
			}
		}
	}
}

func BenchmarkProcessor_1080p(b *testing.B) {
	const w, h = 1920, 1080
	m := Identity(w, h)
	p := NewProcessor(m)

	frameSize := w * h * 3 / 2
	src := make([]byte, frameSize)
	dst := make([]byte, frameSize)
	for i := range src {
		src[i] = byte(i)
	}

	b.SetBytes(int64(frameSize))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		p.ProcessYUV(dst, src, w, h)
	}
}
