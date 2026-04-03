package stmap

import (
	"encoding/binary"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/zsiec/switchframe/server/internal/video"
)

// make10Frame creates a YUV422P10LE frame filled with uniform Y/Cb/Cr values.
func make10Frame(w, h int, yVal, cbVal, crVal uint16) []byte {
	buf := make([]byte, video.YUV422_10bitFrameSize(w, h))
	ySize := w * h
	chromaW := w / 2
	chromaSize := chromaW * h

	// Y plane
	for i := 0; i < ySize; i++ {
		binary.LittleEndian.PutUint16(buf[i*2:], yVal)
	}
	// Cb plane
	cbOff := ySize * 2
	for i := 0; i < chromaSize; i++ {
		binary.LittleEndian.PutUint16(buf[cbOff+i*2:], cbVal)
	}
	// Cr plane
	crOff := cbOff + chromaSize*2
	for i := 0; i < chromaSize; i++ {
		binary.LittleEndian.PutUint16(buf[crOff+i*2:], crVal)
	}
	return buf
}

// read10Y reads the Y sample at pixel (x, y) from a YUV422P10LE buffer.
func read10Y(buf []byte, x, y, w int) uint16 {
	return binary.LittleEndian.Uint16(buf[(y*w+x)*2:])
}

// read10Cb reads the Cb sample at chroma position (cx, cy) from a YUV422P10LE buffer.
func read10Cb(buf []byte, cx, cy, w, h int) uint16 {
	ySize := w * h * 2
	chromaW := w / 2
	return binary.LittleEndian.Uint16(buf[ySize+(cy*chromaW+cx)*2:])
}

// read10Cr reads the Cr sample at chroma position (cx, cy) from a YUV422P10LE buffer.
func read10Cr(buf []byte, cx, cy, w, h int) uint16 {
	ySize := w * h * 2
	chromaW := w / 2
	chromaPlaneSize := chromaW * h * 2
	return binary.LittleEndian.Uint16(buf[ySize+chromaPlaneSize+(cy*chromaW+cx)*2:])
}

func TestWarp10_Identity(t *testing.T) {
	const w, h = 16, 8
	m := Identity(w, h)
	p := NewProcessor(m)

	src := make10Frame(w, h, 500, 300, 700)
	dst := make([]byte, video.YUV422_10bitFrameSize(w, h))

	err := p.Warp10(dst, src, w, h)
	require.NoError(t, err)

	// Identity should preserve Y values within ±1 (bilinear rounding).
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			got := read10Y(dst, x, y, w)
			diff := int(got) - 500
			if diff < -1 || diff > 1 {
				t.Errorf("Y[%d,%d]: got %d, want 500 (±1)", x, y, got)
			}
		}
	}

	// Cb within ±1
	chromaW := w / 2
	for cy := 0; cy < h; cy++ {
		for cx := 0; cx < chromaW; cx++ {
			got := read10Cb(dst, cx, cy, w, h)
			diff := int(got) - 300
			if diff < -1 || diff > 1 {
				t.Errorf("Cb[%d,%d]: got %d, want 300 (±1)", cx, cy, got)
			}
		}
	}

	// Cr within ±1
	for cy := 0; cy < h; cy++ {
		for cx := 0; cx < chromaW; cx++ {
			got := read10Cr(dst, cx, cy, w, h)
			diff := int(got) - 700
			if diff < -1 || diff > 1 {
				t.Errorf("Cr[%d,%d]: got %d, want 700 (±1)", cx, cy, got)
			}
		}
	}
}

func TestWarp10_IdentityGradient(t *testing.T) {
	// Fill Y with a gradient to test bilinear interpolation precision.
	const w, h = 16, 8
	m := Identity(w, h)
	p := NewProcessor(m)

	src := make([]byte, video.YUV422_10bitFrameSize(w, h))
	dst := make([]byte, video.YUV422_10bitFrameSize(w, h))

	// Y plane: gradient from 64 to 940
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			val := uint16(64 + (940-64)*x/(w-1))
			binary.LittleEndian.PutUint16(src[(y*w+x)*2:], val)
		}
	}
	// Fill chroma with mid-gray
	ySize := w * h * 2
	chromaW := w / 2
	chromaSize := chromaW * h
	for i := 0; i < chromaSize; i++ {
		binary.LittleEndian.PutUint16(src[ySize+i*2:], 512)
		binary.LittleEndian.PutUint16(src[ySize+chromaSize*2+i*2:], 512)
	}

	err := p.Warp10(dst, src, w, h)
	require.NoError(t, err)

	// Identity should preserve gradient within ±1.
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			srcVal := read10Y(src, x, y, w)
			dstVal := read10Y(dst, x, y, w)
			diff := int(dstVal) - int(srcVal)
			if diff < -1 || diff > 1 {
				t.Errorf("Y[%d,%d]: got %d, want %d (±1)", x, y, dstVal, srcVal)
			}
		}
	}
}

func TestWarp10_ChromaFullHeight(t *testing.T) {
	// Verify that chroma is warped at full height (4:2:2, not 4:2:0).
	const w, h = 16, 8
	m := Identity(w, h)
	p := NewProcessor(m)

	src := make([]byte, video.YUV422_10bitFrameSize(w, h))
	dst := make([]byte, video.YUV422_10bitFrameSize(w, h))

	// Fill Cb with row-varying values so we can verify full-height treatment.
	ySize := w * h * 2
	chromaW := w / 2
	for cy := 0; cy < h; cy++ {
		for cx := 0; cx < chromaW; cx++ {
			val := uint16(100 + cy*80) // 100, 180, 260, ... 660
			binary.LittleEndian.PutUint16(src[ySize+(cy*chromaW+cx)*2:], val)
		}
	}
	// Fill Cr with a different pattern
	crOff := ySize + chromaW*h*2
	for cy := 0; cy < h; cy++ {
		for cx := 0; cx < chromaW; cx++ {
			val := uint16(200 + cy*50)
			binary.LittleEndian.PutUint16(src[crOff+(cy*chromaW+cx)*2:], val)
		}
	}
	// Y plane: uniform
	for i := 0; i < w*h; i++ {
		binary.LittleEndian.PutUint16(src[i*2:], 500)
	}

	err := p.Warp10(dst, src, w, h)
	require.NoError(t, err)

	// Check Cb at every row (full-height chroma should preserve per-row values).
	for cy := 0; cy < h; cy++ {
		got := read10Cb(dst, 0, cy, w, h)
		expected := uint16(100 + cy*80)
		diff := int(got) - int(expected)
		if diff < -1 || diff > 1 {
			t.Errorf("Cb row %d: got %d, want %d (±1)", cy, got, expected)
		}
	}

	// Check Cr at last row specifically (this is the row that would be
	// missing if we accidentally used 4:2:0 chroma).
	gotCr := read10Cr(dst, 0, h-1, w, h)
	expectedCr := uint16(200 + (h-1)*50)
	diff := int(gotCr) - int(expectedCr)
	if diff < -1 || diff > 1 {
		t.Errorf("Cr last row: got %d, want %d (±1)", gotCr, expectedCr)
	}
}

func TestWarp10_HorizontalFlip(t *testing.T) {
	const w, h = 16, 8

	// Create an ST map that flips horizontally.
	m := Identity(w, h)
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			idx := y*w + x
			m.S[idx] = (float32(w-1-x) + 0.5) / float32(w)
		}
	}
	p := NewProcessor(m)

	src := make([]byte, video.YUV422_10bitFrameSize(w, h))
	dst := make([]byte, video.YUV422_10bitFrameSize(w, h))

	// Fill Y with column index gradient.
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			binary.LittleEndian.PutUint16(src[(y*w+x)*2:], uint16(x*60))
		}
	}

	err := p.Warp10(dst, src, w, h)
	require.NoError(t, err)

	// Y plane should be horizontally flipped within ±1.
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			expected := int(read10Y(src, w-1-x, y, w))
			got := int(read10Y(dst, x, y, w))
			diff := got - expected
			if diff < -1 || diff > 1 {
				t.Errorf("Y[%d,%d]: got %d, want %d (±1)", x, y, got, expected)
			}
		}
	}
}

func TestWarp10_OutOfBounds_Clamps(t *testing.T) {
	const w, h = 8, 8

	// ST map with values well outside 0-1.
	m, err := NewSTMap("oob", w, h)
	require.NoError(t, err)
	for i := range m.S {
		m.S[i] = -1.0 // Far left
		m.T[i] = 2.0  // Far below
	}
	p := NewProcessor(m)

	src := make10Frame(w, h, 0, 0, 0)
	dst := make([]byte, video.YUV422_10bitFrameSize(w, h))

	// Set bottom-left Y pixel to a known value.
	binary.LittleEndian.PutUint16(src[((h-1)*w+0)*2:], 777)

	// Should not panic.
	err = p.Warp10(dst, src, w, h)
	require.NoError(t, err)

	// All Y pixels should map to bottom-left corner.
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			got := read10Y(dst, x, y, w)
			diff := int(got) - 777
			if diff < -1 || diff > 1 {
				t.Errorf("Y[%d,%d]: got %d, want 777 (±1)", x, y, got)
			}
		}
	}
}

func TestWarp10_BufferValidation(t *testing.T) {
	const w, h = 16, 8
	m := Identity(w, h)
	p := NewProcessor(m)

	validSize := video.YUV422_10bitFrameSize(w, h)
	validSrc := make([]byte, validSize)

	// Undersized dst
	err := p.Warp10(make([]byte, 10), validSrc, w, h)
	require.Error(t, err, "should reject undersized dst")

	// Undersized src
	err = p.Warp10(make([]byte, validSize), make([]byte, 10), w, h)
	require.Error(t, err, "should reject undersized src")

	// Odd width
	err = p.Warp10(make([]byte, validSize), validSrc, 15, 8)
	require.Error(t, err, "should reject odd width")

	// Zero dimensions
	err = p.Warp10(make([]byte, validSize), validSrc, 0, 8)
	require.Error(t, err, "should reject zero width")
}

func TestWarp10_InactiveProcessor(t *testing.T) {
	p := NewProcessor(nil)
	require.False(t, p.Active())

	// Warp10 on inactive processor should return error.
	err := p.Warp10(make([]byte, 100), make([]byte, 100), 4, 4)
	require.Error(t, err)
}

func TestWarp10_Clamp1023(t *testing.T) {
	// Ensure output values are clamped to [0, 1023].
	const w, h = 8, 4
	m := Identity(w, h)
	p := NewProcessor(m)

	src := make10Frame(w, h, 1023, 1023, 1023) // max 10-bit
	dst := make([]byte, video.YUV422_10bitFrameSize(w, h))

	err := p.Warp10(dst, src, w, h)
	require.NoError(t, err)

	// All Y values should be <= 1023.
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			got := read10Y(dst, x, y, w)
			if got > 1023 {
				t.Errorf("Y[%d,%d]: got %d, want <= 1023", x, y, got)
			}
		}
	}
}

func TestWarp10_SubpixelInterpolation(t *testing.T) {
	// Verify bilinear interpolation at fractional positions.
	const w, h = 8, 4
	m, err := NewSTMap("halfpixel", w, h)
	require.NoError(t, err)

	// Map every pixel to source position (0.5, 0.5) — midpoint of the
	// first 4 pixels. This should bilinear-average src[0,0], src[1,0],
	// src[0,1], src[1,1].
	for i := range m.S {
		m.S[i] = 1.0 / float32(w) // x=0.5 in pixel coords after the -0.5 offset
		m.T[i] = 1.0 / float32(h)
	}
	p := NewProcessor(m)

	src := make([]byte, video.YUV422_10bitFrameSize(w, h))
	dst := make([]byte, video.YUV422_10bitFrameSize(w, h))

	// Set the 4 corner pixels to distinct Y values:
	// (0,0)=100, (1,0)=200, (0,1)=300, (1,1)=400
	binary.LittleEndian.PutUint16(src[(0*w+0)*2:], 100) // (0,0)
	binary.LittleEndian.PutUint16(src[(0*w+1)*2:], 200) // (1,0)
	binary.LittleEndian.PutUint16(src[(1*w+0)*2:], 300) // (0,1)
	binary.LittleEndian.PutUint16(src[(1*w+1)*2:], 400) // (1,1)

	err = p.Warp10(dst, src, w, h)
	require.NoError(t, err)

	// The midpoint bilinear average should be (100+200+300+400)/4 = 250 ± 1.
	got := read10Y(dst, 0, 0, w)
	diff := int(got) - 250
	if diff < -1 || diff > 1 {
		t.Errorf("subpixel Y[0,0]: got %d, want 250 (±1)", got)
	}
}

func TestWarp10_LargerFrame(t *testing.T) {
	// Test with a larger resolution to verify no indexing errors.
	const w, h = 64, 48
	m := Identity(w, h)
	p := NewProcessor(m)

	src := make([]byte, video.YUV422_10bitFrameSize(w, h))
	dst := make([]byte, video.YUV422_10bitFrameSize(w, h))

	// Fill with pseudo-random 10-bit pattern.
	for i := 0; i < len(src)/2; i++ {
		val := uint16((i*137 + 43) & 0x3FF) // 10-bit range
		binary.LittleEndian.PutUint16(src[i*2:], val)
	}

	err := p.Warp10(dst, src, w, h)
	require.NoError(t, err)

	// Identity should preserve within ±1 for all Y samples.
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			srcVal := read10Y(src, x, y, w)
			dstVal := read10Y(dst, x, y, w)
			diff := int(dstVal) - int(srcVal)
			if diff < -1 || diff > 1 {
				t.Errorf("Y[%d,%d]: got %d, want %d (±1)", x, y, dstVal, srcVal)
				if y > 2 {
					t.Fatal("too many errors, stopping")
				}
			}
		}
	}
}

func BenchmarkWarp10_Identity_64x48(b *testing.B) {
	const w, h = 64, 48
	m := Identity(w, h)
	p := NewProcessor(m)

	frameSize := video.YUV422_10bitFrameSize(w, h)
	src := make10Frame(w, h, 500, 300, 700)
	dst := make([]byte, frameSize)

	b.SetBytes(int64(frameSize))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = p.Warp10(dst, src, w, h)
	}
}

func BenchmarkWarp10_Identity_1080p(b *testing.B) {
	const w, h = 1920, 1080
	m := Identity(w, h)
	p := NewProcessor(m)

	frameSize := video.YUV422_10bitFrameSize(w, h)
	src := make10Frame(w, h, 500, 300, 700)
	dst := make([]byte, frameSize)

	b.SetBytes(int64(frameSize))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = p.Warp10(dst, src, w, h)
	}
}
