package stmap

import (
	"bytes"
	"encoding/binary"
	"math/rand"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestWarpPlane10Dispatch_IdentityMap verifies the dispatch path matches
// the Go reference for an identity ST map with uniform source data.
func TestWarpPlane10Dispatch_IdentityMap(t *testing.T) {
	const w, h = 32, 16
	m := Identity(w, h)
	p := NewProcessor(m)

	src := make10Frame(w, h, 500, 300, 700)
	ySize := w * h * 2

	// Reference: Go warpPlane10
	refDst := make([]byte, ySize)
	warpPlane10(refDst, src[:ySize], w, h, p.lutSX, p.lutSY)

	// Dispatch path
	dispDst := make([]byte, ySize)
	warpPlane10Dispatch(dispDst, src[:ySize], w, h, p.lutSX, p.lutSY)

	if !bytes.Equal(refDst, dispDst) {
		mismatches := 0
		for i := 0; i < w*h; i++ {
			rv := binary.LittleEndian.Uint16(refDst[i*2:])
			dv := binary.LittleEndian.Uint16(dispDst[i*2:])
			diff := int(rv) - int(dv)
			if diff > 1 || diff < -1 {
				mismatches++
				if mismatches <= 5 {
					t.Errorf("pixel %d: ref=%d dispatch=%d diff=%d", i, rv, dv, diff)
				}
			}
		}
		if mismatches > 0 {
			t.Fatalf("%d mismatches with >1 difference", mismatches)
		}
	}
}

// TestWarpPlane10Dispatch_MatchesReference_IntegerCoords verifies bit-exact
// match for integer LUT coordinates (no sub-pixel interpolation).
func TestWarpPlane10Dispatch_MatchesReference_IntegerCoords(t *testing.T) {
	const w, h = 32, 16
	rng := rand.New(rand.NewSource(42))

	// Build a processor with integer-only LUT coordinates.
	p := &Processor{}
	p.lutSX = make([]int32, w*h)
	p.lutSY = make([]int32, w*h)
	for i := range p.lutSX {
		p.lutSX[i] = int32(rng.Intn(w)) << 16
		p.lutSY[i] = int32(rng.Intn(h)) << 16
	}

	// Random 10-bit source
	srcSize := w * h * 2
	src := make([]byte, srcSize)
	for i := 0; i < w*h; i++ {
		binary.LittleEndian.PutUint16(src[i*2:], uint16(rng.Intn(1024)))
	}

	refDst := make([]byte, srcSize)
	warpPlane10(refDst, src, w, h, p.lutSX, p.lutSY)

	dispDst := make([]byte, srcSize)
	warpPlane10Dispatch(dispDst, src, w, h, p.lutSX, p.lutSY)

	// Integer coordinates should produce bit-identical results.
	if !bytes.Equal(refDst, dispDst) {
		mismatches := 0
		for i := 0; i < w*h; i++ {
			rv := binary.LittleEndian.Uint16(refDst[i*2:])
			dv := binary.LittleEndian.Uint16(dispDst[i*2:])
			if rv != dv {
				mismatches++
				if mismatches <= 5 {
					t.Errorf("pixel %d: ref=%d dispatch=%d", i, rv, dv)
				}
			}
		}
		t.Fatalf("%d integer-coord mismatches (expected bit-exact)", mismatches)
	}
}

// TestWarpPlane10Dispatch_MatchesReference_SubPixel verifies the dispatch
// path matches the Go reference within +-1 for sub-pixel coordinates.
func TestWarpPlane10Dispatch_MatchesReference_SubPixel(t *testing.T) {
	const w, h = 32, 16
	rng := rand.New(rand.NewSource(99))

	p := &Processor{}
	p.lutSX = make([]int32, w*h)
	p.lutSY = make([]int32, w*h)
	for i := range p.lutSX {
		// Random sub-pixel coordinates (integer + fractional part)
		ix := rng.Intn(w - 1)
		iy := rng.Intn(h - 1)
		fx := rng.Intn(65536)
		fy := rng.Intn(65536)
		p.lutSX[i] = int32((ix << 16) | fx)
		p.lutSY[i] = int32((iy << 16) | fy)
	}

	// Random 10-bit source
	srcSize := w * h * 2
	src := make([]byte, srcSize)
	for i := 0; i < w*h; i++ {
		binary.LittleEndian.PutUint16(src[i*2:], uint16(rng.Intn(1024)))
	}

	refDst := make([]byte, srcSize)
	warpPlane10(refDst, src, w, h, p.lutSX, p.lutSY)

	dispDst := make([]byte, srcSize)
	warpPlane10Dispatch(dispDst, src, w, h, p.lutSX, p.lutSY)

	if !bytes.Equal(refDst, dispDst) {
		mismatches := 0
		for i := 0; i < w*h; i++ {
			rv := binary.LittleEndian.Uint16(refDst[i*2:])
			dv := binary.LittleEndian.Uint16(dispDst[i*2:])
			diff := int(rv) - int(dv)
			if diff > 1 || diff < -1 {
				mismatches++
				if mismatches <= 5 {
					t.Errorf("pixel %d: ref=%d dispatch=%d diff=%d", i, rv, dv, diff)
				}
			}
		}
		if mismatches > 0 {
			t.Fatalf("%d sub-pixel mismatches with >1 difference", mismatches)
		}
	}
}

// TestWarpPlane10Dispatch_OutOfBounds verifies that out-of-bounds LUT
// coordinates are clamped correctly by the dispatch path.
func TestWarpPlane10Dispatch_OutOfBounds(t *testing.T) {
	const w, h = 8, 8

	p := &Processor{}
	p.lutSX = make([]int32, w*h)
	p.lutSY = make([]int32, w*h)
	for i := range p.lutSX {
		p.lutSX[i] = -1 << 16    // negative: should clamp to 0
		p.lutSY[i] = (h + 5) << 16 // above max: should clamp to h-1
	}

	srcSize := w * h * 2
	src := make([]byte, srcSize)
	// Fill with known value at the clamped pixel location
	for i := 0; i < w*h; i++ {
		binary.LittleEndian.PutUint16(src[i*2:], uint16(i&0x3FF))
	}

	refDst := make([]byte, srcSize)
	warpPlane10(refDst, src, w, h, p.lutSX, p.lutSY)

	dispDst := make([]byte, srcSize)
	warpPlane10Dispatch(dispDst, src, w, h, p.lutSX, p.lutSY)

	require.Equal(t, refDst, dispDst, "out-of-bounds dispatch should match reference")
}

// TestWarpPlane10Dispatch_EmptyPixels verifies that zero-sized inputs
// don't crash.
func TestWarpPlane10Dispatch_EmptyPixels(t *testing.T) {
	// Should not panic
	warpPlane10Dispatch(nil, nil, 0, 0, nil, nil)
}

// TestWarpPlane10Dispatch_SinglePixel verifies correct operation with
// a single pixel.
func TestWarpPlane10Dispatch_SinglePixel(t *testing.T) {
	const w, h = 2, 2 // minimum even dimensions for YUV422
	p := &Processor{}
	p.lutSX = make([]int32, w*h)
	p.lutSY = make([]int32, w*h)
	// Map all pixels to (0,0)
	for i := range p.lutSX {
		p.lutSX[i] = 0
		p.lutSY[i] = 0
	}

	srcSize := w * h * 2
	src := make([]byte, srcSize)
	binary.LittleEndian.PutUint16(src[0:], 777)

	refDst := make([]byte, srcSize)
	warpPlane10(refDst, src, w, h, p.lutSX, p.lutSY)

	dispDst := make([]byte, srcSize)
	warpPlane10Dispatch(dispDst, src, w, h, p.lutSX, p.lutSY)

	require.Equal(t, refDst, dispDst, "single-pixel dispatch should match reference")
}

// TestWarp10Dispatch_FullFrame verifies the assembly dispatch matches
// the Go reference for a full YUV422P10LE frame (Y + Cb + Cr planes).
// Compares plane-by-plane: Go warpPlane10 vs assembly warpPlane10Dispatch.
func TestWarp10Dispatch_FullFrame(t *testing.T) {
	const w, h = 32, 16
	m := Identity(w, h)
	p := NewProcessor(m)
	p.ensure422ChromaLUTs(w, h)

	src := make10Frame(w, h, 500, 300, 700)
	ySize := w * h * 2
	chromaW := w / 2
	chromaPlaneSize := chromaW * h * 2

	// Y plane
	refY := make([]byte, ySize)
	warpPlane10(refY, src[:ySize], w, h, p.lutSX, p.lutSY)
	dispY := make([]byte, ySize)
	warpPlane10Dispatch(dispY, src[:ySize], w, h, p.lutSX, p.lutSY)
	require.Equal(t, refY, dispY, "Y plane mismatch")

	// Cb plane
	cbOff := ySize
	refCb := make([]byte, chromaPlaneSize)
	warpPlane10(refCb, src[cbOff:cbOff+chromaPlaneSize], chromaW, h, p.lut422CSX, p.lut422CSY)
	dispCb := make([]byte, chromaPlaneSize)
	warpPlane10Dispatch(dispCb, src[cbOff:cbOff+chromaPlaneSize], chromaW, h, p.lut422CSX, p.lut422CSY)
	require.Equal(t, refCb, dispCb, "Cb plane mismatch")

	// Cr plane
	crOff := ySize + chromaPlaneSize
	refCr := make([]byte, chromaPlaneSize)
	warpPlane10(refCr, src[crOff:crOff+chromaPlaneSize], chromaW, h, p.lut422CSX, p.lut422CSY)
	dispCr := make([]byte, chromaPlaneSize)
	warpPlane10Dispatch(dispCr, src[crOff:crOff+chromaPlaneSize], chromaW, h, p.lut422CSX, p.lut422CSY)
	require.Equal(t, refCr, dispCr, "Cr plane mismatch")
}

// TestWarp10Dispatch_RandomFullFrame verifies the assembly dispatch matches
// the Go reference with randomized LUT and source data (Y plane only since
// that's where the interesting coordinate variation occurs).
func TestWarp10Dispatch_RandomFullFrame(t *testing.T) {
	const w, h = 32, 16
	rng := rand.New(rand.NewSource(77))

	// Random ST map
	m, err := NewSTMap("random", w, h)
	require.NoError(t, err)
	for i := range m.S {
		m.S[i] = rng.Float32()
		m.T[i] = rng.Float32()
	}
	p := NewProcessor(m)

	// Random source Y plane
	ySize := w * h * 2
	src := make([]byte, ySize)
	for i := 0; i < w*h; i++ {
		binary.LittleEndian.PutUint16(src[i*2:], uint16(rng.Intn(1024)))
	}

	refDst := make([]byte, ySize)
	warpPlane10(refDst, src, w, h, p.lutSX, p.lutSY)

	dispDst := make([]byte, ySize)
	warpPlane10Dispatch(dispDst, src, w, h, p.lutSX, p.lutSY)

	if !bytes.Equal(refDst, dispDst) {
		mismatches := 0
		for i := 0; i < w*h; i++ {
			rv := binary.LittleEndian.Uint16(refDst[i*2:])
			dv := binary.LittleEndian.Uint16(dispDst[i*2:])
			diff := int(rv) - int(dv)
			if diff > 1 || diff < -1 {
				mismatches++
			}
		}
		if mismatches > 0 {
			t.Errorf("%d random full-frame mismatches with >1 difference", mismatches)
		}
	}
}

// TestWarp10Dispatch_LargerFrame verifies no indexing errors at larger sizes.
func TestWarp10Dispatch_LargerFrame(t *testing.T) {
	const w, h = 64, 48
	m := Identity(w, h)
	p := NewProcessor(m)

	// Fill Y plane with pseudo-random 10-bit pattern.
	ySize := w * h * 2
	src := make([]byte, ySize)
	for i := 0; i < w*h; i++ {
		val := uint16((i*137 + 43) & 0x3FF)
		binary.LittleEndian.PutUint16(src[i*2:], val)
	}

	refDst := make([]byte, ySize)
	warpPlane10(refDst, src, w, h, p.lutSX, p.lutSY)

	dispDst := make([]byte, ySize)
	warpPlane10Dispatch(dispDst, src, w, h, p.lutSX, p.lutSY)

	if !bytes.Equal(refDst, dispDst) {
		mismatches := 0
		for i := 0; i < w*h; i++ {
			rv := binary.LittleEndian.Uint16(refDst[i*2:])
			dv := binary.LittleEndian.Uint16(dispDst[i*2:])
			diff := int(rv) - int(dv)
			if diff > 1 || diff < -1 {
				mismatches++
			}
		}
		if mismatches > 0 {
			t.Errorf("%d larger-frame mismatches with >1 difference", mismatches)
		}
	}
}

func BenchmarkWarpPlane10_Go(b *testing.B) {
	const w, h = 1920, 1080
	m := Identity(w, h)
	p := NewProcessor(m)

	planeSize := w * h * 2
	src := make([]byte, planeSize)
	dst := make([]byte, planeSize)
	for i := 0; i < w*h; i++ {
		binary.LittleEndian.PutUint16(src[i*2:], uint16(i&0x3FF))
	}

	b.SetBytes(int64(planeSize))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		warpPlane10(dst, src, w, h, p.lutSX, p.lutSY)
	}
}

func BenchmarkWarpPlane10_Dispatch(b *testing.B) {
	const w, h = 1920, 1080
	m := Identity(w, h)
	p := NewProcessor(m)

	planeSize := w * h * 2
	src := make([]byte, planeSize)
	dst := make([]byte, planeSize)
	for i := 0; i < w*h; i++ {
		binary.LittleEndian.PutUint16(src[i*2:], uint16(i&0x3FF))
	}

	b.SetBytes(int64(planeSize))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		warpPlane10Dispatch(dst, src, w, h, p.lutSX, p.lutSY)
	}
}
