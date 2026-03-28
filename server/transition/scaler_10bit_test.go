package transition

import (
	"encoding/binary"
	"testing"
)

func TestScaleYUV422P10_Identity(t *testing.T) {
	// Scaling from 16x8 to 16x8 should be identity (or near-identity)
	w, h := 16, 8
	src := make10bitFrame(w, h, 500, 300, 700)
	dst := make([]byte, w*h*4)
	err := ScaleYUV422P10(src, w, h, dst, w, h)
	if err != nil {
		t.Fatal(err)
	}
	// Check Y at center
	gotY := readY10(dst, w/2, h/2, w)
	if gotY != 500 {
		t.Errorf("identity scale Y: got %d, want 500", gotY)
	}
	// Check Cb at center
	gotCb := readCb10(dst, w/4, h/2, w, h)
	if gotCb != 300 {
		t.Errorf("identity scale Cb: got %d, want 300", gotCb)
	}
	// Check Cr at center
	ySize := w * h * 2
	chromaW := w / 2
	chromaPlaneSize := chromaW * h * 2
	crOff := ySize + chromaPlaneSize
	gotCr := binary.LittleEndian.Uint16(dst[crOff+(h/2*chromaW+w/4)*2:])
	if gotCr != 700 {
		t.Errorf("identity scale Cr: got %d, want 700", gotCr)
	}
}

func TestScaleYUV422P10_Downscale(t *testing.T) {
	// Scaling 32x16 uniform frame to 16x8 should preserve values
	src := make10bitFrame(32, 16, 700, 400, 600)
	dst := make([]byte, 16*8*4)
	err := ScaleYUV422P10(src, 32, 16, dst, 16, 8)
	if err != nil {
		t.Fatal(err)
	}
	gotY := readY10(dst, 0, 0, 16)
	if gotY != 700 {
		t.Errorf("downscale uniform Y: got %d, want 700", gotY)
	}
	gotCb := readCb10(dst, 0, 0, 16, 8)
	if gotCb != 400 {
		t.Errorf("downscale uniform Cb: got %d, want 400", gotCb)
	}
}

func TestScaleYUV422P10_Upscale(t *testing.T) {
	// Scaling 8x4 uniform frame to 16x8 should preserve values
	src := make10bitFrame(8, 4, 800, 350, 650)
	dst := make([]byte, 16*8*4)
	err := ScaleYUV422P10(src, 8, 4, dst, 16, 8)
	if err != nil {
		t.Fatal(err)
	}
	gotY := readY10(dst, 8, 4, 16)
	if gotY != 800 {
		t.Errorf("upscale uniform Y: got %d, want 800", gotY)
	}
	gotCb := readCb10(dst, 4, 4, 16, 8)
	if gotCb != 350 {
		t.Errorf("upscale uniform Cb: got %d, want 350", gotCb)
	}
}

func TestScaleYUV422P10_ChromaFullHeight(t *testing.T) {
	// Verify chroma is scaled at full height (not half like 4:2:0)
	src := make10bitFrame(16, 8, 500, 300, 700)
	dst := make([]byte, 32*16*4)
	err := ScaleYUV422P10(src, 16, 8, dst, 32, 16)
	if err != nil {
		t.Fatal(err)
	}
	// Read chroma at last row of destination
	dstW, dstH := 32, 16
	ySize := dstW * dstH * 2
	chromaW := dstW / 2
	lastRowCb := binary.LittleEndian.Uint16(dst[ySize+((dstH-1)*chromaW)*2:])
	if lastRowCb != 300 {
		t.Errorf("chroma last row: got %d, want 300", lastRowCb)
	}
	// Also check Cr at last row
	chromaPlaneSize := chromaW * dstH * 2
	crOff := ySize + chromaPlaneSize
	lastRowCr := binary.LittleEndian.Uint16(dst[crOff+((dstH-1)*chromaW)*2:])
	if lastRowCr != 700 {
		t.Errorf("chroma last row Cr: got %d, want 700", lastRowCr)
	}
}

func TestScaleYUV422P10_OddWidth(t *testing.T) {
	err := ScaleYUV422P10(nil, 15, 8, nil, 16, 8)
	if err == nil {
		t.Fatal("should reject odd src width")
	}
	err = ScaleYUV422P10(nil, 16, 8, nil, 15, 8)
	if err == nil {
		t.Fatal("should reject odd dst width")
	}
}

func TestScaleYUV422P10_BufferValidation(t *testing.T) {
	src := make([]byte, 16*8*4)
	dst := make([]byte, 8*4*4-1) // too small
	err := ScaleYUV422P10(src, 16, 8, dst, 8, 4)
	if err == nil {
		t.Fatal("should reject undersized dst")
	}

	src = make([]byte, 16*8*4-1) // too small src
	dst = make([]byte, 8*4*4)
	err = ScaleYUV422P10(src, 16, 8, dst, 8, 4)
	if err == nil {
		t.Fatal("should reject undersized src")
	}
}

func TestScaleYUV422P10_ZeroDimensions(t *testing.T) {
	err := ScaleYUV422P10(nil, 0, 8, nil, 16, 8)
	if err == nil {
		t.Fatal("should reject zero src width")
	}
	err = ScaleYUV422P10(nil, 16, 0, nil, 16, 8)
	if err == nil {
		t.Fatal("should reject zero src height")
	}
}

func TestScaleYUV422P10_Gradient(t *testing.T) {
	// Create a horizontal gradient in Y: left=100, right=900
	srcW, srcH := 32, 8
	src := make([]byte, srcW*srcH*4)
	ySize := srcW * srcH
	chromaW := srcW / 2

	// Fill Y with gradient
	for y := 0; y < srcH; y++ {
		for x := 0; x < srcW; x++ {
			val := uint16(100 + (800*x)/(srcW-1))
			binary.LittleEndian.PutUint16(src[(y*srcW+x)*2:], val)
		}
	}
	// Fill chroma with neutral
	cbOff := ySize * 2
	cbSize := chromaW * srcH
	for i := 0; i < cbSize; i++ {
		binary.LittleEndian.PutUint16(src[cbOff+i*2:], 512)
	}
	crOff := cbOff + cbSize*2
	for i := 0; i < cbSize; i++ {
		binary.LittleEndian.PutUint16(src[crOff+i*2:], 512)
	}

	// Scale down to 16x4
	dstW, dstH := 16, 4
	dst := make([]byte, dstW*dstH*4)
	err := ScaleYUV422P10(src, srcW, srcH, dst, dstW, dstH)
	if err != nil {
		t.Fatal(err)
	}

	// Left edge should be near 100, right edge near 900
	leftY := readY10(dst, 0, 0, dstW)
	rightY := readY10(dst, dstW-1, 0, dstW)
	if leftY < 90 || leftY > 110 {
		t.Errorf("gradient left Y: got %d, want ~100", leftY)
	}
	if rightY < 890 || rightY > 910 {
		t.Errorf("gradient right Y: got %d, want ~900", rightY)
	}
	// Middle should be roughly 500
	midY := readY10(dst, dstW/2, 0, dstW)
	if midY < 450 || midY > 550 {
		t.Errorf("gradient mid Y: got %d, want ~500", midY)
	}
}

func TestScaleYUV422P10_LargeDownscale(t *testing.T) {
	// 1920x1080 -> 480x270 (4x downscale), uniform frame
	srcW, srcH := 1920, 1080
	dstW, dstH := 480, 270
	src := make10bitFrame(srcW, srcH, 600, 400, 700)
	dst := make([]byte, dstW*dstH*4)
	err := ScaleYUV422P10(src, srcW, srcH, dst, dstW, dstH)
	if err != nil {
		t.Fatal(err)
	}
	// Uniform source should produce uniform destination
	gotY := readY10(dst, dstW/2, dstH/2, dstW)
	if gotY != 600 {
		t.Errorf("large downscale Y center: got %d, want 600", gotY)
	}
	gotCb := readCb10(dst, dstW/4, dstH/2, dstW, dstH)
	if gotCb != 400 {
		t.Errorf("large downscale Cb center: got %d, want 400", gotCb)
	}
}
