package graphics

import (
	"encoding/binary"
	"image"
	"testing"
)

// make10bitGraphicsFrame creates a uniform YUV422P10LE frame filled with the given Y/Cb/Cr values.
// Layout: Y plane (w*h*2 bytes) + Cb plane (w/2*h*2 bytes) + Cr plane (w/2*h*2 bytes) = w*h*4 bytes.
func make10bitGraphicsFrame(w, h int, y, cb, cr uint16) []byte {
	buf := make([]byte, w*h*4)
	ySize := w * h
	cbSize := (w / 2) * h

	// Y plane: ySize uint16LE samples
	for i := 0; i < ySize; i++ {
		binary.LittleEndian.PutUint16(buf[i*2:], y)
	}
	// Cb plane
	cbOff := ySize * 2
	for i := 0; i < cbSize; i++ {
		binary.LittleEndian.PutUint16(buf[cbOff+i*2:], cb)
	}
	// Cr plane
	crOff := cbOff + cbSize*2
	for i := 0; i < cbSize; i++ {
		binary.LittleEndian.PutUint16(buf[crOff+i*2:], cr)
	}
	return buf
}

// readY10g reads a Y sample at pixel position (x, y) from a YUV422P10LE frame.
func readY10g(buf []byte, x, y, w int) uint16 {
	return binary.LittleEndian.Uint16(buf[(y*w+x)*2:])
}

// readCb10g reads a Cb sample at chroma position (cx, cy) from a YUV422P10LE frame.
func readCb10g(buf []byte, cx, cy, w, h int) uint16 {
	ySize := w * h * 2
	chromaW := w / 2
	return binary.LittleEndian.Uint16(buf[ySize+(cy*chromaW+cx)*2:])
}

// readCr10g reads a Cr sample at chroma position (cx, cy) from a YUV422P10LE frame.
func readCr10g(buf []byte, cx, cy, w, h int) uint16 {
	ySize := w * h * 2
	chromaW := w / 2
	chromaSize := chromaW * h
	return binary.LittleEndian.Uint16(buf[ySize+chromaSize*2+(cy*chromaW+cx)*2:])
}

func TestAlphaBlendRGBA10_TransparentOverlay(t *testing.T) {
	w, h := 16, 8
	yuv := make10bitGraphicsFrame(w, h, 500, 400, 600)
	rgba := make([]byte, w*h*4) // all zeros = transparent
	err := AlphaBlendRGBA10(yuv, rgba, w, h)
	if err != nil {
		t.Fatal(err)
	}
	// YUV should be unchanged
	gotY := readY10g(yuv, 0, 0, w)
	if gotY != 500 {
		t.Errorf("transparent: Y changed from 500 to %d", gotY)
	}
	gotCb := readCb10g(yuv, 0, 0, w, h)
	if gotCb != 400 {
		t.Errorf("transparent: Cb changed from 400 to %d", gotCb)
	}
	gotCr := readCr10g(yuv, 0, 0, w, h)
	if gotCr != 600 {
		t.Errorf("transparent: Cr changed from 600 to %d", gotCr)
	}
}

func TestAlphaBlendRGBA10_OpaqueWhiteOverlay(t *testing.T) {
	w, h := 16, 8
	yuv := make10bitGraphicsFrame(w, h, 64, 512, 512) // black
	rgba := make([]byte, w*h*4)
	for i := 0; i < w*h; i++ {
		rgba[i*4+0] = 255 // R
		rgba[i*4+1] = 255 // G
		rgba[i*4+2] = 255 // B
		rgba[i*4+3] = 255 // A (fully opaque)
	}
	err := AlphaBlendRGBA10(yuv, rgba, w, h)
	if err != nil {
		t.Fatal(err)
	}
	// White in BT.709 10-bit limited range: Y~940, Cb~512, Cr~512
	gotY := readY10g(yuv, 0, 0, w)
	if gotY < 900 || gotY > 960 {
		t.Errorf("opaque white overlay: Y=%d, want ~940", gotY)
	}
	gotCb := readCb10g(yuv, 0, 0, w, h)
	if gotCb < 490 || gotCb > 530 {
		t.Errorf("opaque white overlay: Cb=%d, want ~512", gotCb)
	}
	gotCr := readCr10g(yuv, 0, 0, w, h)
	if gotCr < 490 || gotCr > 530 {
		t.Errorf("opaque white overlay: Cr=%d, want ~512", gotCr)
	}
}

func TestAlphaBlendRGBA10_OpaqueBlackOverlay(t *testing.T) {
	w, h := 16, 8
	yuv := make10bitGraphicsFrame(w, h, 940, 512, 512) // white
	rgba := make([]byte, w*h*4)
	for i := 0; i < w*h; i++ {
		rgba[i*4+0] = 0   // R
		rgba[i*4+1] = 0   // G
		rgba[i*4+2] = 0   // B
		rgba[i*4+3] = 255 // A
	}
	err := AlphaBlendRGBA10(yuv, rgba, w, h)
	if err != nil {
		t.Fatal(err)
	}
	// Black in BT.709 10-bit limited range: Y~64, Cb~512, Cr~512
	gotY := readY10g(yuv, 0, 0, w)
	if gotY < 50 || gotY > 80 {
		t.Errorf("opaque black overlay: Y=%d, want ~64", gotY)
	}
}

func TestAlphaBlendRGBA10_HalfAlpha(t *testing.T) {
	w, h := 16, 8
	yuv := make10bitGraphicsFrame(w, h, 64, 512, 512) // black
	rgba := make([]byte, w*h*4)
	for i := 0; i < w*h; i++ {
		rgba[i*4+0] = 255 // R
		rgba[i*4+1] = 255 // G
		rgba[i*4+2] = 255 // B
		rgba[i*4+3] = 128 // ~50% alpha
	}
	err := AlphaBlendRGBA10(yuv, rgba, w, h)
	if err != nil {
		t.Fatal(err)
	}
	// Result should be roughly halfway between black (64) and white (940)
	gotY := readY10g(yuv, 0, 0, w)
	if gotY < 450 || gotY > 550 {
		t.Errorf("half alpha white on black: Y=%d, want ~500", gotY)
	}
}

func TestAlphaBlendRGBA10_OpaqueRedOverlay(t *testing.T) {
	w, h := 16, 8
	yuv := make10bitGraphicsFrame(w, h, 64, 512, 512) // black
	rgba := make([]byte, w*h*4)
	for i := 0; i < w*h; i++ {
		rgba[i*4+0] = 255 // R
		rgba[i*4+1] = 0   // G
		rgba[i*4+2] = 0   // B
		rgba[i*4+3] = 255 // A
	}
	err := AlphaBlendRGBA10(yuv, rgba, w, h)
	if err != nil {
		t.Fatal(err)
	}
	// Pure red BT.709 10-bit: Y8 = (66*255+128)>>8 + 16 = 82, Y10 = 82*4 = 328
	// Cr should be above neutral (red pulls Cr positive)
	gotY := readY10g(yuv, 0, 0, w)
	if gotY < 310 || gotY > 350 {
		t.Errorf("opaque red: Y=%d, want ~328", gotY)
	}
	gotCr := readCr10g(yuv, 0, 0, w, h)
	if gotCr <= 512 {
		t.Errorf("opaque red: Cr=%d, should be > 512", gotCr)
	}
}

func TestAlphaBlendRGBA10_ErrorOnBufferMismatch(t *testing.T) {
	w, h := 16, 8
	yuv := make([]byte, 10) // way too small
	rgba := make([]byte, w*h*4)
	err := AlphaBlendRGBA10(yuv, rgba, w, h)
	if err == nil {
		t.Fatal("expected error for undersized YUV buffer")
	}

	yuv = make10bitGraphicsFrame(w, h, 500, 512, 512)
	rgba = make([]byte, 10) // way too small
	err = AlphaBlendRGBA10(yuv, rgba, w, h)
	if err == nil {
		t.Fatal("expected error for undersized RGBA buffer")
	}
}

func TestAlphaBlendRGBA10_ErrorOnOddWidth(t *testing.T) {
	err := AlphaBlendRGBA10(nil, nil, 15, 8)
	if err == nil {
		t.Fatal("should reject odd width")
	}
}

func TestAlphaBlendRGBA10_ChromaIsFullHeight(t *testing.T) {
	// Verify chroma is processed at full height (YUV422 = half width, full height)
	w, h := 16, 8
	yuv := make10bitGraphicsFrame(w, h, 64, 512, 512) // black
	rgba := make([]byte, w*h*4)
	for i := 0; i < w*h; i++ {
		rgba[i*4+0] = 255
		rgba[i*4+1] = 255
		rgba[i*4+2] = 255
		rgba[i*4+3] = 255
	}
	err := AlphaBlendRGBA10(yuv, rgba, w, h)
	if err != nil {
		t.Fatal(err)
	}
	// Check chroma at the last row (should be modified, not left at original value)
	gotCb := readCb10g(yuv, 0, h-1, w, h)
	if gotCb < 490 || gotCb > 530 {
		t.Errorf("chroma last row: Cb=%d, want ~512 (white overlay neutral chroma)", gotCb)
	}
}

func TestAlphaBlendRGBARect10_SmallRectModifiesOnlyInsidePixels(t *testing.T) {
	frameW, frameH := 1920, 1080
	yuv := make10bitGraphicsFrame(frameW, frameH, 500, 400, 600)

	// Save a copy for comparison of untouched pixels.
	original := make([]byte, len(yuv))
	copy(original, yuv)

	// 100x50 overlay at position (200, 100).
	overlayW, overlayH := 100, 50
	rgba := make([]byte, overlayW*overlayH*4)
	for i := 0; i < overlayW*overlayH; i++ {
		rgba[i*4+0] = 255 // R
		rgba[i*4+1] = 255 // G
		rgba[i*4+2] = 255 // B
		rgba[i*4+3] = 255 // A (opaque)
	}

	rect := image.Rect(200, 100, 300, 150) // 100x50 region
	err := AlphaBlendRGBARect10(yuv, rgba, frameW, frameH, overlayW, overlayH, rect, 1.0)
	if err != nil {
		t.Fatal(err)
	}

	// Pixels inside rect should be modified (white overlay on Y=500 background).
	insideY := readY10g(yuv, 210, 110, frameW)
	if insideY == 500 {
		t.Errorf("pixel inside rect was NOT modified: Y=%d, should have changed from 500", insideY)
	}
	// White is ~940 in 10-bit; should be close to that with opaque overlay.
	if insideY < 900 || insideY > 960 {
		t.Errorf("pixel inside rect: Y=%d, want ~940 (white)", insideY)
	}

	// Pixels outside rect should be unchanged.
	outsideY := readY10g(yuv, 10, 10, frameW) // well outside
	if outsideY != 500 {
		t.Errorf("pixel outside rect was modified: Y=%d, want 500", outsideY)
	}
	outsideY2 := readY10g(yuv, 500, 500, frameW) // also outside
	if outsideY2 != 500 {
		t.Errorf("pixel outside rect was modified: Y=%d, want 500", outsideY2)
	}

	// Check chroma outside rect unchanged.
	outsideCb := readCb10g(yuv, 5, 5, frameW, frameH) // well outside
	if outsideCb != 400 {
		t.Errorf("chroma outside rect was modified: Cb=%d, want 400", outsideCb)
	}

	// Check chroma inside rect is changed (white = neutral ~512, was 400).
	insideCb := readCb10g(yuv, 110, 120, frameW, frameH) // inside
	if insideCb == 400 {
		t.Errorf("chroma inside rect was NOT modified: Cb=%d, should differ from 400", insideCb)
	}
}

func TestAlphaBlendRGBARect10_TransparentOverlayNoChange(t *testing.T) {
	frameW, frameH := 320, 240
	yuv := make10bitGraphicsFrame(frameW, frameH, 500, 400, 600)
	original := make([]byte, len(yuv))
	copy(original, yuv)

	overlayW, overlayH := 100, 50
	rgba := make([]byte, overlayW*overlayH*4) // all zero = transparent

	rect := image.Rect(50, 50, 150, 100)
	err := AlphaBlendRGBARect10(yuv, rgba, frameW, frameH, overlayW, overlayH, rect, 1.0)
	if err != nil {
		t.Fatal(err)
	}

	// Frame should be completely unchanged.
	for i := range yuv {
		if yuv[i] != original[i] {
			t.Fatalf("transparent overlay changed frame at byte %d: got %d, want %d", i, yuv[i], original[i])
		}
	}
}

func TestAlphaBlendRGBARect10_HalfAlpha(t *testing.T) {
	frameW, frameH := 320, 240
	yuv := make10bitGraphicsFrame(frameW, frameH, 64, 512, 512) // black

	overlayW, overlayH := 50, 50
	rgba := make([]byte, overlayW*overlayH*4)
	for i := 0; i < overlayW*overlayH; i++ {
		rgba[i*4+0] = 255
		rgba[i*4+1] = 255
		rgba[i*4+2] = 255
		rgba[i*4+3] = 128 // ~50% alpha
	}

	rect := image.Rect(100, 100, 150, 150)
	err := AlphaBlendRGBARect10(yuv, rgba, frameW, frameH, overlayW, overlayH, rect, 1.0)
	if err != nil {
		t.Fatal(err)
	}

	// Inside: should be roughly halfway between black (64) and white (940) = ~500
	insideY := readY10g(yuv, 110, 110, frameW)
	if insideY < 450 || insideY > 550 {
		t.Errorf("half alpha rect: Y=%d, want ~500", insideY)
	}

	// Outside: unchanged at 64
	outsideY := readY10g(yuv, 10, 10, frameW)
	if outsideY != 64 {
		t.Errorf("outside rect: Y=%d, want 64", outsideY)
	}
}

func TestAlphaBlendRGBARect10_ErrorOnOddWidth(t *testing.T) {
	err := AlphaBlendRGBARect10(nil, nil, 15, 8, 10, 5, image.Rect(0, 0, 10, 5), 1.0)
	if err == nil {
		t.Fatal("should reject odd frame width")
	}
}

func TestAlphaBlendRGBARect10_ClipsToFrameBounds(t *testing.T) {
	frameW, frameH := 320, 240
	yuv := make10bitGraphicsFrame(frameW, frameH, 500, 400, 600)

	overlayW, overlayH := 100, 100
	rgba := make([]byte, overlayW*overlayH*4)
	for i := 0; i < overlayW*overlayH; i++ {
		rgba[i*4+0] = 255
		rgba[i*4+1] = 255
		rgba[i*4+2] = 255
		rgba[i*4+3] = 255
	}

	// Rect extends beyond frame bounds.
	rect := image.Rect(280, 200, 380, 300)
	err := AlphaBlendRGBARect10(yuv, rgba, frameW, frameH, overlayW, overlayH, rect, 1.0)
	if err != nil {
		t.Fatal(err)
	}

	// Should not panic — test passes if we reach here.
	// Pixel just inside the clipped region should be modified.
	insideY := readY10g(yuv, 290, 210, frameW)
	if insideY == 500 {
		t.Errorf("pixel inside clipped rect was NOT modified")
	}
}
