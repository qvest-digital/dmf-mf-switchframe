package transition

import (
	"encoding/binary"
	"testing"
)

// Helper to create a 10-bit YUV422P10LE frame filled with a single Y/Cb/Cr value
func make10bitFrame(w, h int, y, cb, cr uint16) []byte {
	buf := make([]byte, w*h*4)
	ySize := w * h
	cbSize := (w / 2) * h
	// Y plane
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

// Helper to read Y sample at pixel position
func readY10(buf []byte, x, y, w int) uint16 {
	return binary.LittleEndian.Uint16(buf[(y*w+x)*2:])
}

// Helper to read Cb sample at chroma position
func readCb10(buf []byte, x, y, w, h int) uint16 {
	ySize := w * h * 2
	chromaW := w / 2
	return binary.LittleEndian.Uint16(buf[ySize+(y*chromaW+x)*2:])
}

func TestBlendMix10_FullA(t *testing.T) {
	w, h := 16, 8
	a := make10bitFrame(w, h, 940, 512, 512) // white
	b := make10bitFrame(w, h, 64, 512, 512)  // black
	dst := make([]byte, w*h*4)
	err := BlendMix10(dst, a, b, w, h, 0) // pos=0 -> 100% A
	if err != nil {
		t.Fatal(err)
	}
	// Should be 100% A (white)
	got := readY10(dst, 0, 0, w)
	if got != 940 {
		t.Errorf("pos=0 should be 100%% A: got Y=%d, want 940", got)
	}
}

func TestBlendMix10_FullB(t *testing.T) {
	w, h := 16, 8
	a := make10bitFrame(w, h, 940, 512, 512)
	b := make10bitFrame(w, h, 64, 512, 512)
	dst := make([]byte, w*h*4)
	err := BlendMix10(dst, a, b, w, h, 256) // pos=256 -> 100% B
	if err != nil {
		t.Fatal(err)
	}
	got := readY10(dst, 0, 0, w)
	if got != 64 {
		t.Errorf("pos=256 should be 100%% B: got Y=%d, want 64", got)
	}
}

func TestBlendMix10_Midpoint(t *testing.T) {
	w, h := 16, 8
	a := make10bitFrame(w, h, 1000, 600, 400)
	b := make10bitFrame(w, h, 200, 400, 800)
	dst := make([]byte, w*h*4)
	err := BlendMix10(dst, a, b, w, h, 128) // pos=128 -> 50/50
	if err != nil {
		t.Fatal(err)
	}
	gotY := readY10(dst, 0, 0, w)
	// (1000 * 128 + 200 * 128 + 128) >> 8 = (128000 + 25600 + 128) >> 8 = 600
	if gotY != 600 {
		t.Errorf("midpoint Y: got %d, want 600", gotY)
	}
	gotCb := readCb10(dst, 0, 0, w, h)
	// (600 * 128 + 400 * 128 + 128) >> 8 = 500
	if gotCb != 500 {
		t.Errorf("midpoint Cb: got %d, want 500", gotCb)
	}
}

func TestBlendMix10_OddDimensions(t *testing.T) {
	// Width must be even for YUV422
	err := BlendMix10(nil, nil, nil, 15, 8, 128)
	if err == nil {
		t.Fatal("should reject odd width")
	}
}

func TestBlendMix10_BufferSize(t *testing.T) {
	w, h := 16, 8
	frameSize := w * h * 4
	a := make([]byte, frameSize)
	b := make([]byte, frameSize)
	dst := make([]byte, frameSize-1) // too small
	err := BlendMix10(dst, a, b, w, h, 128)
	if err == nil {
		t.Fatal("should reject undersized buffer")
	}
}

func TestBlendFTB10_FullOpaque(t *testing.T) {
	w, h := 16, 8
	src := make10bitFrame(w, h, 940, 600, 400)
	dst := make([]byte, w*h*4)
	err := BlendFTB10(dst, src, w, h, 0, true) // pos=0 -> fully visible
	if err != nil {
		t.Fatal(err)
	}
	gotY := readY10(dst, 0, 0, w)
	if gotY != 940 {
		t.Errorf("FTB pos=0 should be source: got Y=%d, want 940", gotY)
	}
}

func TestBlendFTB10_FullBlack(t *testing.T) {
	w, h := 16, 8
	src := make10bitFrame(w, h, 940, 600, 400)
	dst := make([]byte, w*h*4)
	err := BlendFTB10(dst, src, w, h, 256, true) // pos=256 -> full black
	if err != nil {
		t.Fatal(err)
	}
	gotY := readY10(dst, 0, 0, w)
	if gotY != 64 { // 10-bit limited-range black
		t.Errorf("FTB pos=256 should be black: got Y=%d, want 64", gotY)
	}
	gotCb := readCb10(dst, 0, 0, w, h)
	if gotCb != 512 { // neutral chroma
		t.Errorf("FTB pos=256 chroma should be neutral: got Cb=%d, want 512", gotCb)
	}
}

func TestBlendDip10_Midpoint_IsBlack(t *testing.T) {
	w, h := 16, 8
	a := make10bitFrame(w, h, 940, 512, 512)
	b := make10bitFrame(w, h, 940, 512, 512)
	dst := make([]byte, w*h*4)
	err := BlendDip10(dst, a, b, w, h, 128, true) // midpoint = full black
	if err != nil {
		t.Fatal(err)
	}
	gotY := readY10(dst, 0, 0, w)
	if gotY != 64 {
		t.Errorf("dip midpoint should be black: got Y=%d, want 64", gotY)
	}
}

func TestBlendWipe10_AllA(t *testing.T) {
	w, h := 16, 8
	a := make10bitFrame(w, h, 940, 512, 512)
	b := make10bitFrame(w, h, 64, 512, 512)
	dst := make([]byte, w*h*4)
	alpha := make([]byte, w*h) // all 0 -> 100% A
	err := BlendWipe10(dst, a, b, w, h, alpha)
	if err != nil {
		t.Fatal(err)
	}
	gotY := readY10(dst, 0, 0, w)
	if gotY != 940 {
		t.Errorf("wipe alpha=0 should be A: got Y=%d, want 940", gotY)
	}
}

func TestBlendWipe10_AllB(t *testing.T) {
	w, h := 16, 8
	a := make10bitFrame(w, h, 940, 512, 512)
	b := make10bitFrame(w, h, 64, 512, 512)
	dst := make([]byte, w*h*4)
	alpha := make([]byte, w*h)
	for i := range alpha {
		alpha[i] = 255 // all 255 -> ~100% B
	}
	err := BlendWipe10(dst, a, b, w, h, alpha)
	if err != nil {
		t.Fatal(err)
	}
	gotY := readY10(dst, 0, 0, w)
	// With >>8 approximation: (940*1 + 64*255 + 128)>>8 = 67
	// Allow +-4 tolerance for the >>8 vs /255 approximation
	if gotY < 60 || gotY > 68 {
		t.Errorf("wipe alpha=255 should be near B: got Y=%d, want ~64", gotY)
	}
}

func TestBlendStinger10_ZeroAlpha(t *testing.T) {
	w, h := 16, 8
	base := make10bitFrame(w, h, 500, 512, 512)
	overlay := make10bitFrame(w, h, 900, 512, 512)
	dst := make([]byte, w*h*4)
	alpha := make([]byte, w*h) // all 0 -> base shows through
	err := BlendStinger10(dst, base, overlay, w, h, alpha)
	if err != nil {
		t.Fatal(err)
	}
	gotY := readY10(dst, 0, 0, w)
	if gotY != 500 {
		t.Errorf("stinger alpha=0 should be base: got Y=%d, want 500", gotY)
	}
}

func TestBlendStinger10_FullAlpha(t *testing.T) {
	w, h := 16, 8
	base := make10bitFrame(w, h, 500, 512, 512)
	overlay := make10bitFrame(w, h, 900, 512, 512)
	dst := make([]byte, w*h*4)
	alpha := make([]byte, w*h)
	for i := range alpha {
		alpha[i] = 255
	}
	err := BlendStinger10(dst, base, overlay, w, h, alpha)
	if err != nil {
		t.Fatal(err)
	}
	gotY := readY10(dst, 0, 0, w)
	// With >>8 approximation: (500*1 + 900*255 + 128)>>8 = 898
	// Allow +-4 tolerance for the >>8 vs /255 approximation
	if gotY < 896 || gotY > 904 {
		t.Errorf("stinger alpha=255 should be near overlay: got Y=%d, want ~900", gotY)
	}
}

func TestBlendMix10_ChromaIsFullHeight(t *testing.T) {
	// Verify that chroma blending operates on full-height planes (4:2:2)
	w, h := 16, 8
	a := make10bitFrame(w, h, 500, 300, 700)
	b := make10bitFrame(w, h, 500, 700, 300)
	dst := make([]byte, w*h*4)
	_ = BlendMix10(dst, a, b, w, h, 128) // 50/50

	// Check chroma at last row (would be out of bounds if using h/2)
	ySize := w * h * 2
	chromaW := w / 2
	lastRowCb := binary.LittleEndian.Uint16(dst[ySize+((h-1)*chromaW)*2:])
	// (300*128 + 700*128 + 128) >> 8 = 500
	if lastRowCb != 500 {
		t.Errorf("chroma last row: got Cb=%d, want 500", lastRowCb)
	}
}

func TestBlendDip10_FullA(t *testing.T) {
	w, h := 16, 8
	a := make10bitFrame(w, h, 940, 600, 400)
	b := make10bitFrame(w, h, 64, 512, 512)
	dst := make([]byte, w*h*4)
	err := BlendDip10(dst, a, b, w, h, 0, true) // pos=0 -> 100% A
	if err != nil {
		t.Fatal(err)
	}
	gotY := readY10(dst, 0, 0, w)
	if gotY != 940 {
		t.Errorf("dip pos=0 should be 100%% A: got Y=%d, want 940", gotY)
	}
}

func TestBlendDip10_FullB(t *testing.T) {
	w, h := 16, 8
	a := make10bitFrame(w, h, 940, 600, 400)
	b := make10bitFrame(w, h, 64, 512, 512)
	dst := make([]byte, w*h*4)
	err := BlendDip10(dst, a, b, w, h, 256, true) // pos=256 -> 100% B
	if err != nil {
		t.Fatal(err)
	}
	gotY := readY10(dst, 0, 0, w)
	if gotY != 64 {
		t.Errorf("dip pos=256 should be 100%% B: got Y=%d, want 64", gotY)
	}
}

func TestBlendFTB10_Midpoint(t *testing.T) {
	w, h := 16, 8
	src := make10bitFrame(w, h, 940, 600, 400)
	dst := make([]byte, w*h*4)
	err := BlendFTB10(dst, src, w, h, 128, true) // 50% fade
	if err != nil {
		t.Fatal(err)
	}
	gotY := readY10(dst, 0, 0, w)
	// (940 * 128 + 64 * 128 + 128) >> 8 = (120320 + 8192 + 128) >> 8 = 502
	if gotY != 502 {
		t.Errorf("FTB midpoint Y: got %d, want 502", gotY)
	}
	gotCb := readCb10(dst, 0, 0, w, h)
	// (600 * 128 + 512 * 128 + 128) >> 8 = (76800 + 65536 + 128) >> 8 = 556
	if gotCb != 556 {
		t.Errorf("FTB midpoint Cb: got %d, want 556", gotCb)
	}
}

func TestBlendFTB10_FullRange(t *testing.T) {
	w, h := 16, 8
	src := make10bitFrame(w, h, 940, 600, 400)
	dst := make([]byte, w*h*4)
	err := BlendFTB10(dst, src, w, h, 256, false) // full black, full-range
	if err != nil {
		t.Fatal(err)
	}
	gotY := readY10(dst, 0, 0, w)
	if gotY != 0 { // full-range black = 0
		t.Errorf("FTB full-range black: got Y=%d, want 0", gotY)
	}
}

func TestBlendWipe10_ChromaDownsample(t *testing.T) {
	// Verify chroma alpha is downsampled 2:1 horizontally (not 2:2 like 4:2:0)
	w, h := 16, 8
	a := make10bitFrame(w, h, 500, 300, 700)
	b := make10bitFrame(w, h, 500, 700, 300)
	dst := make([]byte, w*h*4)

	// Create alpha mask: left half = 0 (A), right half = 255 (B)
	alpha := make([]byte, w*h)
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			if x >= w/2 {
				alpha[y*w+x] = 255
			}
		}
	}

	err := BlendWipe10(dst, a, b, w, h, alpha)
	if err != nil {
		t.Fatal(err)
	}

	// Check chroma at bottom-right corner - should be close to B's Cb (700)
	// This tests that chroma operates on full height (last row)
	chromaW := w / 2
	ySize := w * h * 2
	lastRowLastCb := binary.LittleEndian.Uint16(dst[ySize+((h-1)*chromaW+(chromaW-1))*2:])
	// With >>8 approximation and alpha=255: (300*1 + 700*255 + 128)>>8 = 698
	if lastRowLastCb < 696 || lastRowLastCb > 704 {
		t.Errorf("wipe chroma bottom-right should be near B: got Cb=%d, want ~700", lastRowLastCb)
	}
}

func TestBlendStinger10_MidAlpha(t *testing.T) {
	w, h := 16, 8
	base := make10bitFrame(w, h, 200, 512, 512)
	overlay := make10bitFrame(w, h, 800, 512, 512)
	dst := make([]byte, w*h*4)
	alpha := make([]byte, w*h)
	for i := range alpha {
		alpha[i] = 128 // ~50% blend
	}
	err := BlendStinger10(dst, base, overlay, w, h, alpha)
	if err != nil {
		t.Fatal(err)
	}
	gotY := readY10(dst, 0, 0, w)
	// (200 * (256-128) + 800 * 128 + 128) >> 8 = (25600 + 102400 + 128) >> 8 = 500
	if gotY != 500 {
		t.Errorf("stinger mid-alpha Y: got %d, want 500", gotY)
	}
}

func TestBlendMix10_ClampToMax(t *testing.T) {
	// Ensure values are clamped to 1023 max
	w, h := 16, 8
	a := make10bitFrame(w, h, 1023, 1023, 1023)
	b := make10bitFrame(w, h, 1023, 1023, 1023)
	dst := make([]byte, w*h*4)
	err := BlendMix10(dst, a, b, w, h, 128)
	if err != nil {
		t.Fatal(err)
	}
	gotY := readY10(dst, 0, 0, w)
	if gotY > 1023 {
		t.Errorf("should clamp to 1023: got Y=%d", gotY)
	}
}

func TestBlend10bit_StingerAnimates(t *testing.T) {
	// Verify that the 10-bit stinger path produces animated alpha composites
	// instead of a hard cut. Create stinger frames with varying alpha.
	w, h := 16, 8
	frameSize10 := w * h * 4

	// Create 8-bit YUV420 stinger frames with alpha.
	numFrames := 10
	yuvSize420 := w*h + 2*(w/2)*(h/2) // 8-bit 420
	stingerFrames := make([]StingerFrameData, numFrames)
	for i := range stingerFrames {
		yuv := make([]byte, yuvSize420)
		// Fill with bright white (8-bit Y=235).
		for j := 0; j < w*h; j++ {
			yuv[j] = 235
		}
		for j := 0; j < (w/2)*(h/2); j++ {
			yuv[w*h+j] = 128
			yuv[w*h+(w/2)*(h/2)+j] = 128
		}

		alpha := make([]byte, w*h)
		// Middle frames have ~50% alpha.
		alphaVal := byte(128)
		for j := range alpha {
			alpha[j] = alphaVal
		}

		stingerFrames[i] = StingerFrameData{YUV: yuv, Alpha: alpha}
	}

	sd := &StingerData{
		Frames:   stingerFrames,
		Width:    w,
		Height:   h,
		CutPoint: 0.5,
	}

	// Create the engine with 10-bit format.
	e := NewEngine(EngineConfig{
		Stinger:    sd,
		HintWidth:  w,
		HintHeight: h,
	})
	e.colorFormat = ColorFormat10bit
	e.width = w
	e.height = h
	e.transitionType = Stinger

	// Set up source frames.
	e.latestYUVA = make10bitFrame(w, h, 64, 512, 512)  // black
	e.latestYUVB = make10bitFrame(w, h, 940, 512, 512) // white

	// Blend at pos=0.3 (before cut point, should show frame A + stinger overlay).
	result := e.blend10bit(e.latestYUVA, e.latestYUVB, 0.3)
	if result == nil {
		t.Fatal("blend10bit returned nil")
	}
	if len(result) < frameSize10 {
		t.Fatalf("result too small: got %d, want %d", len(result), frameSize10)
	}

	// With ~50% alpha stinger overlay on black base, Y should be somewhere between
	// black (64) and white (940). If it was a hard cut, it would be exactly 64.
	gotY := readY10(result, 4, 4, w)
	if gotY == 64 || gotY == 940 {
		t.Errorf("stinger should produce alpha-blended output, not hard cut: Y=%d (64=black, 940=white)", gotY)
	}
	if gotY < 200 || gotY > 800 {
		t.Logf("stinger blend Y=%d (expected between black and white)", gotY)
	}
}

func TestUpconvert420to422_10_Transition(t *testing.T) {
	// Test the local upconvert function in the transition package.
	w, h := 16, 8
	src420 := make([]byte, w*h*3/2)
	// Y=235 (white), Cb=128 (neutral), Cr=128 (neutral)
	for i := 0; i < w*h; i++ {
		src420[i] = 235
	}
	for i := 0; i < (w/2)*(h/2); i++ {
		src420[w*h+i] = 128
		src420[w*h+(w/2)*(h/2)+i] = 128
	}

	dst422 := make([]byte, w*h*4)
	err := upconvert420to422_10(src420, dst422, w, h)
	if err != nil {
		t.Fatal(err)
	}

	// Y should be 235*4 = 940
	gotY := readY10(dst422, 0, 0, w)
	if gotY != 940 {
		t.Errorf("upconvert Y: got %d, want 940", gotY)
	}

	// Cb should be 128*4 = 512
	gotCb := readCb10(dst422, 0, 0, w, h)
	if gotCb != 512 {
		t.Errorf("upconvert Cb: got %d, want 512", gotCb)
	}
}
