package video

import (
	"encoding/binary"
	"testing"
)

func TestNewBlackFrame_8bit(t *testing.T) {
	w, h := 1920, 1080
	buf := NewBlackFrame(YUV420_8bit, w, h)

	if len(buf) != YUV420FrameSize(w, h) {
		t.Fatalf("expected %d bytes, got %d", YUV420FrameSize(w, h), len(buf))
	}

	ySize := w * h
	cbOff := YUV420_8bit.CbOffset(w, h)
	crOff := YUV420_8bit.CrOffset(w, h)
	chromaSize := YUV420_8bit.ChromaPlaneSize(w, h)

	// Verify Y plane = 16 (BT.709 limited-range black)
	for i := 0; i < ySize; i++ {
		if buf[i] != 16 {
			t.Fatalf("Y[%d] = %d, want 16", i, buf[i])
		}
	}

	// Verify Cb plane = 128
	for i := 0; i < chromaSize; i++ {
		if buf[cbOff+i] != 128 {
			t.Fatalf("Cb[%d] = %d, want 128", i, buf[cbOff+i])
		}
	}

	// Verify Cr plane = 128
	for i := 0; i < chromaSize; i++ {
		if buf[crOff+i] != 128 {
			t.Fatalf("Cr[%d] = %d, want 128", i, buf[crOff+i])
		}
	}
}

func TestNewBlackFrame_10bit(t *testing.T) {
	w, h := 1920, 1080
	buf := NewBlackFrame(YUV422_10bit, w, h)

	if len(buf) != YUV422_10bitFrameSize(w, h) {
		t.Fatalf("expected %d bytes, got %d", YUV422_10bitFrameSize(w, h), len(buf))
	}

	ySamples := w * h
	cbOff := YUV422_10bit.CbOffset(w, h)
	crOff := YUV422_10bit.CrOffset(w, h)
	chromaSamples := (w / 2) * h // 4:2:2 chroma

	// Verify Y plane = 64 (10-bit limited-range black)
	for i := 0; i < ySamples; i++ {
		v := binary.LittleEndian.Uint16(buf[i*2:])
		if v != 64 {
			t.Fatalf("Y[%d] = %d, want 64", i, v)
		}
	}

	// Verify Cb plane = 512
	for i := 0; i < chromaSamples; i++ {
		v := binary.LittleEndian.Uint16(buf[cbOff+i*2:])
		if v != 512 {
			t.Fatalf("Cb[%d] = %d, want 512", i, v)
		}
	}

	// Verify Cr plane = 512
	for i := 0; i < chromaSamples; i++ {
		v := binary.LittleEndian.Uint16(buf[crOff+i*2:])
		if v != 512 {
			t.Fatalf("Cr[%d] = %d, want 512", i, v)
		}
	}
}

func TestFillBlack_InPlace(t *testing.T) {
	w, h := 64, 64
	buf := make([]byte, YUV420_8bit.FrameSize(w, h))

	// Fill with garbage first
	for i := range buf {
		buf[i] = 0xFF
	}

	FillBlack(buf, YUV420_8bit, w, h)

	// Spot check
	if buf[0] != 16 {
		t.Fatalf("Y[0] = %d, want 16", buf[0])
	}
	chromaStart := YUV420_8bit.CbOffset(w, h)
	if buf[chromaStart] != 128 {
		t.Fatalf("Cb[0] = %d, want 128", buf[chromaStart])
	}
}

func TestFillBlack_10bit_InPlace(t *testing.T) {
	w, h := 64, 64
	buf := make([]byte, YUV422_10bit.FrameSize(w, h))

	// Fill with garbage
	for i := range buf {
		buf[i] = 0xFF
	}

	FillBlack(buf, YUV422_10bit, w, h)

	// Verify Y[0] = 64
	v := binary.LittleEndian.Uint16(buf[0:])
	if v != 64 {
		t.Fatalf("Y[0] = %d, want 64", v)
	}

	// Verify Cb[0] = 512
	cbOff := YUV422_10bit.CbOffset(w, h)
	v = binary.LittleEndian.Uint16(buf[cbOff:])
	if v != 512 {
		t.Fatalf("Cb[0] = %d, want 512", v)
	}
}

func TestNewFrame_CustomColor(t *testing.T) {
	w, h := 16, 16

	// 8-bit: white frame
	buf := NewFrame(YUV420_8bit, w, h, 235, 128, 128)
	if buf[0] != 235 {
		t.Fatalf("Y[0] = %d, want 235", buf[0])
	}
	cbOff := YUV420_8bit.CbOffset(w, h)
	if buf[cbOff] != 128 {
		t.Fatalf("Cb[0] = %d, want 128", buf[cbOff])
	}

	// 10-bit: white frame
	buf10 := NewFrame(YUV422_10bit, w, h, 940, 512, 512)
	v := binary.LittleEndian.Uint16(buf10[0:])
	if v != 940 {
		t.Fatalf("Y[0] = %d, want 940", v)
	}
}

func TestNewFrame_SeparateCbCr(t *testing.T) {
	w, h := 16, 16

	// Verify Cb and Cr can have different values
	buf := NewFrame(YUV420_8bit, w, h, 100, 50, 200)
	cbOff := YUV420_8bit.CbOffset(w, h)
	crOff := YUV420_8bit.CrOffset(w, h)
	if buf[cbOff] != 50 {
		t.Fatalf("Cb[0] = %d, want 50", buf[cbOff])
	}
	if buf[crOff] != 200 {
		t.Fatalf("Cr[0] = %d, want 200", buf[crOff])
	}
}

func TestFillFrame_DoesNotExceedBounds(t *testing.T) {
	w, h := 16, 16
	size := YUV420_8bit.FrameSize(w, h)
	buf := make([]byte, size+10) // extra bytes at end

	// Fill sentinel after the frame
	for i := size; i < len(buf); i++ {
		buf[i] = 0xAA
	}

	FillFrame(buf, YUV420_8bit, w, h, 16, 128, 128)

	// Sentinel must be untouched
	for i := size; i < len(buf); i++ {
		if buf[i] != 0xAA {
			t.Fatalf("sentinel at %d was overwritten: got 0x%02X", i, buf[i])
		}
	}
}

func TestNewBlackFrame_MatchesExistingBehavior_8bit(t *testing.T) {
	// Reproduce the exact pattern from playout/idle.go to verify equivalence
	w, h := 320, 240
	ySize := w * h
	uvSize := ySize / 4

	// Original pattern
	original := make([]byte, ySize+2*uvSize)
	for i := 0; i < ySize; i++ {
		original[i] = 16
	}
	for i := ySize; i < len(original); i++ {
		original[i] = 128
	}

	// New pattern
	got := NewBlackFrame(YUV420_8bit, w, h)

	if len(got) != len(original) {
		t.Fatalf("size mismatch: got %d, want %d", len(got), len(original))
	}
	for i := range original {
		if got[i] != original[i] {
			t.Fatalf("mismatch at byte %d: got %d, want %d", i, got[i], original[i])
		}
	}
}

func TestNewBlackFrame_MatchesExistingBehavior_10bit(t *testing.T) {
	// Reproduce the exact pattern from playout/idle_10bit.go
	w, h := 320, 240
	frameSize := w * h * 4
	ySize := w * h
	chromaW := w / 2
	chromaSize := chromaW * h

	original := make([]byte, frameSize)
	for i := 0; i < ySize; i++ {
		binary.LittleEndian.PutUint16(original[i*2:], 64)
	}
	cbOff := ySize * 2
	for i := 0; i < chromaSize; i++ {
		binary.LittleEndian.PutUint16(original[cbOff+i*2:], 512)
	}
	crOff := cbOff + chromaSize*2
	for i := 0; i < chromaSize; i++ {
		binary.LittleEndian.PutUint16(original[crOff+i*2:], 512)
	}

	got := NewBlackFrame(YUV422_10bit, w, h)

	if len(got) != len(original) {
		t.Fatalf("size mismatch: got %d, want %d", len(got), len(original))
	}
	for i := range original {
		if got[i] != original[i] {
			t.Fatalf("mismatch at byte %d: got 0x%02X, want 0x%02X", i, got[i], original[i])
		}
	}
}

func BenchmarkNewBlackFrame_8bit_1080p(b *testing.B) {
	for i := 0; i < b.N; i++ {
		_ = NewBlackFrame(YUV420_8bit, 1920, 1080)
	}
}

func BenchmarkNewBlackFrame_10bit_1080p(b *testing.B) {
	for i := 0; i < b.N; i++ {
		_ = NewBlackFrame(YUV422_10bit, 1920, 1080)
	}
}

func BenchmarkFillBlack_8bit_1080p(b *testing.B) {
	buf := make([]byte, YUV420_8bit.FrameSize(1920, 1080))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		FillBlack(buf, YUV420_8bit, 1920, 1080)
	}
}

func BenchmarkFillBlack_10bit_1080p(b *testing.B) {
	buf := make([]byte, YUV422_10bit.FrameSize(1920, 1080))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		FillBlack(buf, YUV422_10bit, 1920, 1080)
	}
}
