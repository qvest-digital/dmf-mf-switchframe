package layout

import (
	"encoding/binary"
	"testing"

	"github.com/zsiec/switchframe/server/internal/video"
)

// makeLayout10Frame creates a YUV422P10LE frame filled with constant values.
// Layout: Y plane (w*h uint16 LE), Cb plane (w/2*h uint16 LE), Cr plane (w/2*h uint16 LE).
func makeLayout10Frame(w, h int, y, cb, cr uint16) []byte {
	ySize := w * h * 2
	chromaW := w / 2
	chromaSize := chromaW * h * 2
	buf := make([]byte, ySize+chromaSize*2)

	// Fill Y plane
	for i := 0; i < w*h; i++ {
		binary.LittleEndian.PutUint16(buf[i*2:], y)
	}
	// Fill Cb plane
	for i := 0; i < chromaW*h; i++ {
		binary.LittleEndian.PutUint16(buf[ySize+i*2:], cb)
	}
	// Fill Cr plane
	crOff := ySize + chromaSize
	for i := 0; i < chromaW*h; i++ {
		binary.LittleEndian.PutUint16(buf[crOff+i*2:], cr)
	}
	return buf
}

// readLayout10Y reads a single Y sample from a YUV422P10LE frame.
func readLayout10Y(buf []byte, x, y, w int) uint16 {
	off := (y*w + x) * 2
	return binary.LittleEndian.Uint16(buf[off:])
}

// readLayout10Cb reads a single Cb sample from a YUV422P10LE frame.
func readLayout10Cb(buf []byte, x, y, w, h int) uint16 {
	ySize := w * h * 2
	chromaW := w / 2
	off := ySize + (y*chromaW+x)*2
	return binary.LittleEndian.Uint16(buf[off:])
}

// readLayout10Cr reads a single Cr sample from a YUV422P10LE frame.
func readLayout10Cr(buf []byte, x, y, w, h int) uint16 {
	ySize := w * h * 2
	chromaW := w / 2
	chromaSize := chromaW * h * 2
	off := ySize + chromaSize + (y*chromaW+x)*2
	return binary.LittleEndian.Uint16(buf[off:])
}

func TestComposePIPOpaque10_CopiesCorrectly(t *testing.T) {
	dstW, dstH := 32, 16
	srcW, srcH := 8, 4
	dst := makeLayout10Frame(dstW, dstH, 64, 512, 512)   // black background
	src := makeLayout10Frame(srcW, srcH, 940, 300, 700)   // bright PIP
	ComposePIPOpaque10(dst, dstW, dstH, src, srcW, srcH, 4, 4) // place at (4,4)

	// Check Y at PIP origin
	gotY := readLayout10Y(dst, 4, 4, dstW)
	if gotY != 940 {
		t.Errorf("PIP Y at (4,4): got %d, want 940", gotY)
	}

	// Check Y at PIP end (last pixel of PIP region)
	gotY3 := readLayout10Y(dst, 4+srcW-1, 4+srcH-1, dstW)
	if gotY3 != 940 {
		t.Errorf("PIP Y at last pixel: got %d, want 940", gotY3)
	}

	// Check Y outside PIP is still black
	gotY2 := readLayout10Y(dst, 0, 0, dstW)
	if gotY2 != 64 {
		t.Errorf("outside PIP Y at (0,0): got %d, want 64", gotY2)
	}

	// Check pixel just after PIP region
	gotY4 := readLayout10Y(dst, 4+srcW, 4, dstW)
	if gotY4 != 64 {
		t.Errorf("just after PIP Y: got %d, want 64", gotY4)
	}
}

func TestComposePIPOpaque10_ChromaCopied(t *testing.T) {
	dstW, dstH := 32, 16
	srcW, srcH := 8, 4
	dst := makeLayout10Frame(dstW, dstH, 64, 512, 512)
	src := makeLayout10Frame(srcW, srcH, 940, 300, 700)
	ComposePIPOpaque10(dst, dstW, dstH, src, srcW, srcH, 4, 4)

	// Chroma coordinates: dstX=4 -> chromaX=2, dstY=4 -> chromaY=4 (full height in 422)
	gotCb := readLayout10Cb(dst, 2, 4, dstW, dstH)
	if gotCb != 300 {
		t.Errorf("PIP Cb at chroma (2,4): got %d, want 300", gotCb)
	}

	gotCr := readLayout10Cr(dst, 2, 4, dstW, dstH)
	if gotCr != 700 {
		t.Errorf("PIP Cr at chroma (2,4): got %d, want 700", gotCr)
	}

	// Outside PIP chroma should be unchanged
	gotCb2 := readLayout10Cb(dst, 0, 0, dstW, dstH)
	if gotCb2 != 512 {
		t.Errorf("outside PIP Cb: got %d, want 512", gotCb2)
	}
}

func TestComposePIPOpaque10_ChromaFullHeight(t *testing.T) {
	dstW, dstH := 16, 8
	srcW, srcH := 8, 4
	dst := makeLayout10Frame(dstW, dstH, 64, 512, 512)
	src := makeLayout10Frame(srcW, srcH, 500, 300, 700)
	ComposePIPOpaque10(dst, dstW, dstH, src, srcW, srcH, 0, 0)

	// Check Cb at last row of PIP region (row 3, chroma col 0)
	// In 4:2:2, chroma has full height, so row 3 is the last PIP chroma row
	gotCb := readLayout10Cb(dst, 0, 3, dstW, dstH)
	if gotCb != 300 {
		t.Errorf("chroma last PIP row: Cb=%d, want 300", gotCb)
	}

	// Row 4 should be background (outside PIP)
	gotCb2 := readLayout10Cb(dst, 0, 4, dstW, dstH)
	if gotCb2 != 512 {
		t.Errorf("chroma row after PIP: Cb=%d, want 512", gotCb2)
	}
}

func TestComposePIPOpaque10_AtOrigin(t *testing.T) {
	dstW, dstH := 16, 8
	srcW, srcH := 16, 8
	dst := makeLayout10Frame(dstW, dstH, 64, 512, 512)
	src := makeLayout10Frame(srcW, srcH, 800, 200, 600)
	ComposePIPOpaque10(dst, dstW, dstH, src, srcW, srcH, 0, 0)

	// Every pixel should be from source
	for y := 0; y < dstH; y++ {
		for x := 0; x < dstW; x++ {
			gotY := readLayout10Y(dst, x, y, dstW)
			if gotY != 800 {
				t.Errorf("Y at (%d,%d): got %d, want 800", x, y, gotY)
			}
		}
	}
}

func TestComposePIPOpaque10_ClipsToBounds(t *testing.T) {
	dstW, dstH := 16, 8
	srcW, srcH := 8, 4
	dst := makeLayout10Frame(dstW, dstH, 64, 512, 512)
	src := makeLayout10Frame(srcW, srcH, 940, 300, 700)

	// Place so src extends past right edge
	ComposePIPOpaque10(dst, dstW, dstH, src, srcW, srcH, 12, 0)

	// Should be clipped: only 4 pixels copied (16-12=4)
	gotY := readLayout10Y(dst, 12, 0, dstW)
	if gotY != 940 {
		t.Errorf("clipped PIP Y at (12,0): got %d, want 940", gotY)
	}
	gotY2 := readLayout10Y(dst, 15, 0, dstW)
	if gotY2 != 940 {
		t.Errorf("clipped PIP Y at (15,0): got %d, want 940", gotY2)
	}
}

func TestComposePIPOpaque10_NegativeOriginNoOp(t *testing.T) {
	dstW, dstH := 16, 8
	dst := makeLayout10Frame(dstW, dstH, 64, 512, 512)
	src := makeLayout10Frame(4, 4, 940, 300, 700)

	// Negative origin should be a no-op
	ComposePIPOpaque10(dst, dstW, dstH, src, 4, 4, -2, 0)

	// Everything should still be black
	gotY := readLayout10Y(dst, 0, 0, dstW)
	if gotY != 64 {
		t.Errorf("negative origin should be no-op: got Y=%d, want 64", gotY)
	}
}

func TestFillRectBlack10_FillsCorrectly(t *testing.T) {
	w, h := 16, 8
	frame := makeLayout10Frame(w, h, 940, 300, 700) // white frame
	FillRectBlack10(frame, w, h, 4, 2, 8, 4)        // fill 8x4 rect at (4,2)

	// Inside rect should be black
	gotY := readLayout10Y(frame, 4, 2, w)
	if gotY != 64 {
		t.Errorf("inside rect Y at (4,2): got %d, want 64", gotY)
	}
	gotY2 := readLayout10Y(frame, 11, 5, w)
	if gotY2 != 64 {
		t.Errorf("inside rect Y at (11,5): got %d, want 64", gotY2)
	}

	// Outside rect should be unchanged
	gotY3 := readLayout10Y(frame, 0, 0, w)
	if gotY3 != 940 {
		t.Errorf("outside rect Y at (0,0): got %d, want 940", gotY3)
	}
	gotY4 := readLayout10Y(frame, 3, 2, w)
	if gotY4 != 940 {
		t.Errorf("outside rect Y at (3,2): got %d, want 940", gotY4)
	}
}

func TestFillRectBlack10_ChromaNeutral(t *testing.T) {
	w, h := 16, 8
	frame := makeLayout10Frame(w, h, 940, 300, 700)
	FillRectBlack10(frame, w, h, 0, 0, w, h) // fill entire frame

	// Check Y
	gotY := readLayout10Y(frame, 0, 0, w)
	if gotY != 64 {
		t.Errorf("Y: got %d, want 64", gotY)
	}

	// Check Cb
	gotCb := readLayout10Cb(frame, 0, 0, w, h)
	if gotCb != 512 {
		t.Errorf("Cb: got %d, want 512", gotCb)
	}

	// Check Cr
	gotCr := readLayout10Cr(frame, 0, 0, w, h)
	if gotCr != 512 {
		t.Errorf("Cr: got %d, want 512", gotCr)
	}
}

func TestFillRectBlack10_PartialChroma(t *testing.T) {
	w, h := 16, 8
	frame := makeLayout10Frame(w, h, 940, 300, 700)
	FillRectBlack10(frame, w, h, 4, 2, 8, 4) // fill 8x4 at (4,2)

	// Inside chroma rect: chromaX=2, chromaY=2, chromaW=4, chromaH=4
	gotCb := readLayout10Cb(frame, 2, 2, w, h)
	if gotCb != 512 {
		t.Errorf("inside Cb: got %d, want 512", gotCb)
	}

	// Outside chroma rect
	gotCb2 := readLayout10Cb(frame, 0, 0, w, h)
	if gotCb2 != 300 {
		t.Errorf("outside Cb: got %d, want 300", gotCb2)
	}
}

func TestFillRectBlack10_ClipsToBounds(t *testing.T) {
	w, h := 16, 8
	frame := makeLayout10Frame(w, h, 940, 300, 700)

	// Fill extending past bottom-right
	FillRectBlack10(frame, w, h, 12, 6, 8, 4)

	// Should fill clipped region
	gotY := readLayout10Y(frame, 12, 6, w)
	if gotY != 64 {
		t.Errorf("clipped fill Y: got %d, want 64", gotY)
	}

	// Last pixel in frame should be filled
	gotY2 := readLayout10Y(frame, 15, 7, w)
	if gotY2 != 64 {
		t.Errorf("last pixel Y: got %d, want 64", gotY2)
	}
}

func TestFillRectBlack10_ZeroSizeNoOp(t *testing.T) {
	w, h := 16, 8
	frame := makeLayout10Frame(w, h, 940, 300, 700)
	original := make([]byte, len(frame))
	copy(original, frame)

	FillRectBlack10(frame, w, h, 4, 2, 0, 4) // zero width

	for i := range frame {
		if frame[i] != original[i] {
			t.Fatalf("zero-width fill modified frame at byte %d", i)
		}
	}
}

func TestDrawBorderYUV10_DrawsBorder(t *testing.T) {
	w, h := 32, 16
	frame := makeLayout10Frame(w, h, 64, 512, 512) // black

	// Draw a 2-pixel border around rect at (4,4) size 16x8
	DrawBorderYUV10(frame, w, h, 4, 4, 16, 8, 2, 250, 409, 819)

	// Top border pixel (row 2, col 4 — border extends outward)
	gotY := readLayout10Y(frame, 4, 2, w)
	if gotY != 250 {
		t.Errorf("top border Y at (4,2): got %d, want 250", gotY)
	}

	// Left border pixel
	gotY2 := readLayout10Y(frame, 2, 4, w)
	if gotY2 != 250 {
		t.Errorf("left border Y at (2,4): got %d, want 250", gotY2)
	}

	// Interior (inside the rect) should be untouched (black)
	gotY3 := readLayout10Y(frame, 6, 6, w)
	if gotY3 != 64 {
		t.Errorf("interior Y at (6,6): got %d, want 64", gotY3)
	}

	// Bottom border pixel
	gotY4 := readLayout10Y(frame, 4, 12, w)
	if gotY4 != 250 {
		t.Errorf("bottom border Y at (4,12): got %d, want 250", gotY4)
	}

	// Right border pixel
	gotY5 := readLayout10Y(frame, 20, 4, w)
	if gotY5 != 250 {
		t.Errorf("right border Y at (20,4): got %d, want 250", gotY5)
	}
}

func TestDrawBorderYUV10_BorderChroma(t *testing.T) {
	w, h := 32, 16
	frame := makeLayout10Frame(w, h, 64, 512, 512)
	DrawBorderYUV10(frame, w, h, 4, 4, 16, 8, 2, 250, 409, 819)

	// Top border chroma (chromaX=1, chromaY=2 for pixel (2,2))
	gotCb := readLayout10Cb(frame, 1, 2, w, h)
	if gotCb != 409 {
		t.Errorf("border Cb: got %d, want 409", gotCb)
	}
	gotCr := readLayout10Cr(frame, 1, 2, w, h)
	if gotCr != 819 {
		t.Errorf("border Cr: got %d, want 819", gotCr)
	}
}

func TestDrawBorderYUV10_ZeroThicknessNoOp(t *testing.T) {
	w, h := 16, 8
	frame := makeLayout10Frame(w, h, 64, 512, 512)
	original := make([]byte, len(frame))
	copy(original, frame)

	DrawBorderYUV10(frame, w, h, 4, 2, 8, 4, 0, 250, 409, 819)

	for i := range frame {
		if frame[i] != original[i] {
			t.Fatalf("zero-thickness border modified frame at byte %d", i)
		}
	}
}

func TestDrawBorderYUV10_ClampsToFrame(t *testing.T) {
	w, h := 16, 8
	frame := makeLayout10Frame(w, h, 64, 512, 512)

	// Border around rect that fills the entire frame — border expands outward
	// and gets clamped to frame bounds, so no visible border, but must not panic.
	DrawBorderYUV10(frame, w, h, 0, 0, 16, 8, 2, 250, 409, 819)

	// Interior is untouched (border is outside, clamped away)
	gotY := readLayout10Y(frame, 0, 0, w)
	if gotY != 64 {
		t.Errorf("interior Y at (0,0): got %d, want 64 (border clamped away)", gotY)
	}

	// Now test with a rect that has room: border should be visible at top-left
	frame2 := makeLayout10Frame(w, h, 64, 512, 512)
	DrawBorderYUV10(frame2, w, h, 2, 2, 8, 4, 2, 250, 409, 819)

	// Top border pixel at (2,0) should have border color
	gotY2 := readLayout10Y(frame2, 2, 0, w)
	if gotY2 != 250 {
		t.Errorf("border Y at (2,0): got %d, want 250", gotY2)
	}
	// Left border pixel at (0,2) should have border color
	gotY3 := readLayout10Y(frame2, 0, 2, w)
	if gotY3 != 250 {
		t.Errorf("border Y at (0,2): got %d, want 250", gotY3)
	}
}

func TestYUV422P10FrameSize(t *testing.T) {
	// Verify our frame size calculation matches the helpers
	w, h := 32, 16
	frame := makeLayout10Frame(w, h, 64, 512, 512)
	expected := video.YUV422_10bitFrameSize(w, h)
	if len(frame) != expected {
		t.Errorf("frame size: got %d, want %d", len(frame), expected)
	}

	// For 32x16: Y=32*16*2=1024, Cb=16*16*2=512, Cr=16*16*2=512 = 2048
	if expected != 2048 {
		t.Errorf("video.YUV422_10bitFrameSize(32,16): got %d, want 2048", expected)
	}
}
