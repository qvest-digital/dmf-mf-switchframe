package switcher

import (
	"encoding/binary"
	"testing"

	"github.com/stretchr/testify/require"
)

// --- Test helpers for 10-bit YUV422P10LE frames ---

// make10BitFrame creates a YUV422P10LE frame with uniform Y value and neutral chroma.
func make10BitFrame(w, h int, yVal uint16) []byte {
	size := ColorYUV422_10bit.FrameSize(w, h)
	buf := make([]byte, size)

	yPlane := w * h
	chromaW := w / 2
	chromaPlane := chromaW * h

	// Fill Y plane
	for i := 0; i < yPlane; i++ {
		binary.LittleEndian.PutUint16(buf[i*2:], yVal)
	}
	// Fill Cb plane with neutral (512)
	cbOff := yPlane * 2
	for i := 0; i < chromaPlane; i++ {
		binary.LittleEndian.PutUint16(buf[cbOff+i*2:], 512)
	}
	// Fill Cr plane with neutral (512)
	crOff := cbOff + chromaPlane*2
	for i := 0; i < chromaPlane; i++ {
		binary.LittleEndian.PutUint16(buf[crOff+i*2:], 512)
	}
	return buf
}

// read10BitY reads a Y sample from a YUV422P10LE frame at pixel (x, y).
func read10BitY(buf []byte, x, y, w int) uint16 {
	off := (y*w + x) * 2
	return binary.LittleEndian.Uint16(buf[off:])
}

// make10BitYBlock creates a byte buffer of 10-bit Y samples with a uniform value.
// stride is in samples (not bytes). The buffer holds stride*rows uint16 samples.
func make10BitYBlock(stride, rows int, val uint16) []byte {
	buf := make([]byte, stride*rows*2)
	for i := 0; i < stride*rows; i++ {
		binary.LittleEndian.PutUint16(buf[i*2:], val)
	}
	return buf
}

// --- SAD10 tests ---

func TestSAD10_IdenticalBlocks(t *testing.T) {
	stride := 16
	block := make10BitYBlock(stride, 8, 500)
	got := SAD10(block, block, 0, 0, stride, 16, 8)
	require.Equal(t, 0, got, "identical blocks should have SAD=0")
}

func TestSAD10_MaxDifference(t *testing.T) {
	stride := 16
	a := make10BitYBlock(stride, 8, 0)
	b := make10BitYBlock(stride, 8, 1023)
	got := SAD10(a, b, 0, 0, stride, 16, 8)
	want := 16 * 8 * 1023
	require.Equal(t, want, got, "all 0 vs all 1023 should give max SAD")
}

func TestSAD10_KnownDelta(t *testing.T) {
	stride := 16
	a := make10BitYBlock(stride, 8, 100)
	b := make10BitYBlock(stride, 8, 150)
	got := SAD10(a, b, 0, 0, stride, 16, 8)
	want := 16 * 8 * 50 // each pixel differs by 50
	require.Equal(t, want, got)
}

func TestSAD10_WithOffset(t *testing.T) {
	// Place two 4x4 blocks at different offsets within a larger buffer.
	stride := 16
	a := make10BitYBlock(stride, 16, 200)
	b := make10BitYBlock(stride, 16, 300)

	// SAD of 4x4 block at offset (2,3) in both buffers
	aOff := (3*stride + 2) * 2 // byte offset: row 3, col 2
	bOff := (3*stride + 2) * 2
	got := SAD10(a, b, aOff, bOff, stride, 4, 4)
	want := 4 * 4 * 100
	require.Equal(t, want, got)
}

func TestSAD10_Asymmetric(t *testing.T) {
	// One block has gradient, other is uniform
	stride := 8
	a := make([]byte, stride*4*2)
	b := make10BitYBlock(stride, 4, 0)
	expectedSAD := 0
	for row := 0; row < 4; row++ {
		for col := 0; col < 8; col++ {
			val := uint16(row*8 + col) // values 0..31
			binary.LittleEndian.PutUint16(a[(row*stride+col)*2:], val)
			expectedSAD += int(val) // SAD against all-zeros = sum of values
		}
	}
	got := SAD10(a, b, 0, 0, stride, 8, 4)
	require.Equal(t, expectedSAD, got)
}

// --- Interpolate10 tests ---

func TestMCFIState_Interpolate10_AtBoundaries(t *testing.T) {
	w, h := 16, 8
	a := make10BitFrame(w, h, 200)
	b := make10BitFrame(w, h, 800)
	s := &MCFIState{}

	// t=0 -> should be A
	result := s.Interpolate10(a, b, w, h, 0.0)
	gotY := read10BitY(result, 0, 0, w)
	require.Equal(t, uint16(200), gotY, "t=0 should return frame A")

	// t=1 -> should be B
	result = s.Interpolate10(a, b, w, h, 1.0)
	gotY = read10BitY(result, 0, 0, w)
	require.Equal(t, uint16(800), gotY, "t=1 should return frame B")
}

func TestMCFIState_Interpolate10_Midpoint(t *testing.T) {
	w, h := 16, 8
	a := make10BitFrame(w, h, 200)
	b := make10BitFrame(w, h, 800)
	s := &MCFIState{}
	result := s.Interpolate10(a, b, w, h, 0.5)
	gotY := read10BitY(result, 0, 0, w)
	require.Equal(t, uint16(500), gotY, "t=0.5 should give midpoint")
}

func TestMCFIState_Interpolate10_QuarterPoint(t *testing.T) {
	w, h := 16, 8
	a := make10BitFrame(w, h, 0)
	b := make10BitFrame(w, h, 1000)
	s := &MCFIState{}

	result := s.Interpolate10(a, b, w, h, 0.25)
	gotY := read10BitY(result, 0, 0, w)
	require.Equal(t, uint16(250), gotY, "t=0.25 should give quarter")

	result = s.Interpolate10(a, b, w, h, 0.75)
	gotY = read10BitY(result, 0, 0, w)
	require.Equal(t, uint16(750), gotY, "t=0.75 should give three-quarters")
}

func TestMCFIState_Interpolate10_ChromaInterpolation(t *testing.T) {
	w, h := 16, 8
	// Create frames with distinct chroma values
	a := make10BitFrame(w, h, 500) // Y=500, Cb=512, Cr=512
	b := make10BitFrame(w, h, 500) // same Y

	// Override chroma in frame b: Cb=612, Cr=712
	ySize := w * h
	chromaW := w / 2
	chromaSize := chromaW * h
	cbOff := ySize * 2
	crOff := cbOff + chromaSize*2
	for i := 0; i < chromaSize; i++ {
		binary.LittleEndian.PutUint16(b[cbOff+i*2:], 612) // +100 from neutral
		binary.LittleEndian.PutUint16(b[crOff+i*2:], 712) // +200 from neutral
	}

	s := &MCFIState{}
	result := s.Interpolate10(a, b, w, h, 0.5)

	// Check Cb at first sample
	gotCb := binary.LittleEndian.Uint16(result[cbOff:])
	require.Equal(t, uint16(562), gotCb, "Cb midpoint between 512 and 612")

	// Check Cr at first sample
	gotCr := binary.LittleEndian.Uint16(result[crOff:])
	require.Equal(t, uint16(612), gotCr, "Cr midpoint between 512 and 712")
}

func TestMCFIState_Interpolate10_OutputSize(t *testing.T) {
	w, h := 32, 16
	a := make10BitFrame(w, h, 100)
	b := make10BitFrame(w, h, 900)
	s := &MCFIState{}
	result := s.Interpolate10(a, b, w, h, 0.5)
	wantSize := ColorYUV422_10bit.FrameSize(w, h)
	require.Equal(t, wantSize, len(result), "output should match 10-bit 422 frame size")
}

func TestMCFIState_Interpolate10_ClampedAlpha(t *testing.T) {
	w, h := 16, 8
	a := make10BitFrame(w, h, 300)
	b := make10BitFrame(w, h, 700)
	s := &MCFIState{}

	// Alpha < 0 should clamp to 0 (return A)
	result := s.Interpolate10(a, b, w, h, -0.5)
	gotY := read10BitY(result, 0, 0, w)
	require.Equal(t, uint16(300), gotY, "negative alpha clamps to 0")

	// Alpha > 1 should clamp to 1 (return B)
	result = s.Interpolate10(a, b, w, h, 1.5)
	gotY = read10BitY(result, 0, 0, w)
	require.Equal(t, uint16(700), gotY, "alpha >1 clamps to 1")
}

func TestMCFIState_Interpolate10_AllPixelsInterpolated(t *testing.T) {
	// Verify every Y pixel is interpolated, not just the first
	w, h := 16, 8
	a := make10BitFrame(w, h, 100)
	b := make10BitFrame(w, h, 900)
	s := &MCFIState{}
	result := s.Interpolate10(a, b, w, h, 0.5)

	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			gotY := read10BitY(result, x, y, w)
			require.Equal(t, uint16(500), gotY,
				"pixel (%d,%d): expected 500, got %d", x, y, gotY)
		}
	}
}

// --- mcfiWarp10 tests ---

func TestMcfiWarp10_ZeroMotion(t *testing.T) {
	// With zero motion vectors, warp should reproduce the source frame.
	w, h := 32, 32
	src := make10BitFrame(w, h, 600)

	cols := w / frcMEBlockSize
	rows := h / frcMEBlockSize
	mvX := make([]float32, cols*rows) // all zeros
	mvY := make([]float32, cols*rows) // all zeros

	frameSize := ColorYUV422_10bit.FrameSize(w, h)
	dst := make([]byte, frameSize)
	mcfiWarp10(dst, src, w, h, mvX, mvY, 0.5)

	// With zero MVs, warp should produce the source
	gotY := read10BitY(dst, 0, 0, w)
	require.Equal(t, uint16(600), gotY, "zero MVs should reproduce source")
}

func TestMcfiWarp10_OutputSize(t *testing.T) {
	w, h := 32, 32
	src := make10BitFrame(w, h, 500)

	cols := w / frcMEBlockSize
	rows := h / frcMEBlockSize
	mvX := make([]float32, cols*rows)
	mvY := make([]float32, cols*rows)

	frameSize := ColorYUV422_10bit.FrameSize(w, h)
	dst := make([]byte, frameSize)
	mcfiWarp10(dst, src, w, h, mvX, mvY, 0.5)

	// Verify output spans the full frame
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			gotY := read10BitY(dst, x, y, w)
			require.NotEqual(t, uint16(0), gotY,
				"pixel (%d,%d) should be written", x, y)
		}
	}
}

// --- detectSceneChange10 tests ---

func TestDetectSceneChange10_Identical(t *testing.T) {
	w, h := 32, 32
	frame := make10BitFrame(w, h, 500)
	got := detectSceneChange10(frame, frame, w, h)
	require.False(t, got, "identical frames should not be scene change")
}

func TestDetectSceneChange10_LargeDifference(t *testing.T) {
	w, h := 32, 32
	a := make10BitFrame(w, h, 64)
	b := make10BitFrame(w, h, 940)
	got := detectSceneChange10(a, b, w, h)
	require.True(t, got, "huge luma difference should be scene change")
}

func TestDetectSceneChange10_SmallDifference(t *testing.T) {
	w, h := 32, 32
	a := make10BitFrame(w, h, 500)
	b := make10BitFrame(w, h, 502) // tiny difference
	got := detectSceneChange10(a, b, w, h)
	require.False(t, got, "tiny luma difference should not be scene change")
}
