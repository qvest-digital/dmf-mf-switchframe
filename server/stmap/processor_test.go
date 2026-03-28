package stmap

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestProcessor_Identity_NoChange(t *testing.T) {
	const w, h = 8, 8
	m := Identity(w, h)
	p := NewProcessor(m)

	require.True(t, p.Active())

	frameSize := yuvFrameSize(w, h)
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

	frameSize := yuvFrameSize(w, h)
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
	src := make([]byte, 48) // yuvFrameSize(8, 4)
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

	frameSize := yuvFrameSize(w, h)
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

	frameSize := yuvFrameSize(w, h)
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

	frameSize := yuvFrameSize(w, h)
	src := make([]byte, frameSize)
	dst := make([]byte, frameSize)

	// Fill with pseudo-random pattern.
	for i := range src {
		src[i] = byte((i*137 + 43) & 0xFF)
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

func TestProcessor_Name(t *testing.T) {
	m := Identity(8, 8)
	m.Name = "test-correction"
	p := NewProcessor(m)
	require.Equal(t, "test-correction", p.Name())

	nilP := NewProcessor(nil)
	require.Equal(t, "", nilP.Name())
}

func TestProcessor_STArrays(t *testing.T) {
	m := Identity(8, 8)
	p := NewProcessor(m)

	s, tt := p.STArrays()
	require.NotNil(t, s)
	require.NotNil(t, tt)
	require.Len(t, s, 64) // 8*8
	require.Len(t, tt, 64)

	// Verify coordinates are in 0-1 range.
	for i := range s {
		require.GreaterOrEqual(t, s[i], float32(0))
		require.LessOrEqual(t, s[i], float32(1))
		require.GreaterOrEqual(t, tt[i], float32(0))
		require.LessOrEqual(t, tt[i], float32(1))
	}

	nilP := NewProcessor(nil)
	ns, nt := nilP.STArrays()
	require.Nil(t, ns)
	require.Nil(t, nt)
}

func TestNewProcessorCached_WritesAndMmaps(t *testing.T) {
	dir := t.TempDir()
	cachePath := filepath.Join(dir, "test.lut")

	m := Identity(64, 48)
	p, err := NewProcessorCached(m, cachePath)
	require.NoError(t, err)
	require.True(t, p.Active())

	// Cache file should exist.
	fi, err := os.Stat(cachePath)
	require.NoError(t, err)
	// Expected: (64*48 + 64*48 + 32*24 + 32*24) * 4 = (3072+3072+768+768)*4 = 30720
	require.Equal(t, int64(30720), fi.Size())

	// Should produce same output as heap-based.
	heapP := NewProcessor(m)
	src := make([]byte, yuvFrameSize(64, 48))
	for i := range src {
		src[i] = byte(i % 256)
	}
	dstCached := make([]byte, len(src))
	dstHeap := make([]byte, len(src))
	p.ProcessYUV(dstCached, src, 64, 48)
	heapP.ProcessYUV(dstHeap, src, 64, 48)
	require.Equal(t, dstHeap, dstCached)

	require.NoError(t, p.Close())
}

func TestNewProcessorCached_LoadsExisting(t *testing.T) {
	dir := t.TempDir()
	cachePath := filepath.Join(dir, "test.lut")

	m := Identity(64, 48)
	p1, err := NewProcessorCached(m, cachePath)
	require.NoError(t, err)

	// Get reference output.
	src := make([]byte, yuvFrameSize(64, 48))
	for i := range src {
		src[i] = byte(i % 256)
	}
	ref := make([]byte, len(src))
	p1.ProcessYUV(ref, src, 64, 48)
	require.NoError(t, p1.Close())

	// Load from cache with nil stmap.
	p2, err := NewProcessorCached(nil, cachePath)
	require.NoError(t, err)
	require.True(t, p2.Active())

	out := make([]byte, len(src))
	p2.ProcessYUV(out, src, 64, 48)
	require.Equal(t, ref, out)
	require.NoError(t, p2.Close())
}

func TestNewProcessorCached_NilStmapNoCacheFails(t *testing.T) {
	_, err := NewProcessorCached(nil, "/nonexistent/cache.lut")
	require.Error(t, err)
}

func TestProcessorClose_Idempotent(t *testing.T) {
	dir := t.TempDir()
	cachePath := filepath.Join(dir, "test.lut")
	m := Identity(64, 48)
	p, err := NewProcessorCached(m, cachePath)
	require.NoError(t, err)
	require.NoError(t, p.Close())
	require.NoError(t, p.Close()) // second close is no-op
}

func TestProcessorClose_HeapProcessor_NoOp(t *testing.T) {
	m := Identity(8, 8)
	p := NewProcessor(m)
	require.NoError(t, p.Close()) // no mmap backing, should be no-op
	require.True(t, p.Active())   // heap LUTs still valid
}

func TestNewProcessorCached_RecomputesOnSizeMismatch(t *testing.T) {
	dir := t.TempDir()
	cachePath := filepath.Join(dir, "test.lut")

	// Write cache for 8x8.
	m8 := Identity(8, 8)
	p1, err := NewProcessorCached(m8, cachePath)
	require.NoError(t, err)
	require.NoError(t, p1.Close())

	// Now request 64x48 — cache size won't match, should recompute.
	m64 := Identity(64, 48)
	p2, err := NewProcessorCached(m64, cachePath)
	require.NoError(t, err)
	require.True(t, p2.Active())

	// Verify file was overwritten with correct size.
	fi, err := os.Stat(cachePath)
	require.NoError(t, err)
	require.Equal(t, int64(30720), fi.Size())

	require.NoError(t, p2.Close())
}

func TestExpectedCacheSize(t *testing.T) {
	// 1080p: (1920*1080*2 + 960*540*2) * 4 = (4147200 + 1036800) * 4 = 20736000
	require.Equal(t, 20736000, expectedCacheSize(1920, 1080))

	// 64x48: (3072*2 + 768*2) * 4 = 7680 * 4 = 30720
	require.Equal(t, 30720, expectedCacheSize(64, 48))
}

func BenchmarkProcessor_1080p(b *testing.B) {
	const w, h = 1920, 1080
	m := Identity(w, h)
	p := NewProcessor(m)

	frameSize := yuvFrameSize(w, h)
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
