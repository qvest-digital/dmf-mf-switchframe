//go:build (cgo && darwin) || (cgo && cuda)

package gpu

import (
	"encoding/binary"
	"testing"
)

func TestConvertP210ToYUV420PScaled_UniformValue(t *testing.T) {
	// Create a 4x4 P210 frame (YUV422P10LE):
	// Y: 4*4*2 = 32 bytes, U: 2*4*2 = 16 bytes, V: 2*4*2 = 16 bytes = 64 total
	srcW, srcH := 4, 4
	src := make([]byte, srcW*srcH*4) // yuv10FrameSize

	// Fill Y plane with 10-bit value 512 (= 128 in 8-bit after >>2)
	for i := 0; i < srcW*srcH; i++ {
		binary.LittleEndian.PutUint16(src[i*2:], 512)
	}
	// Fill U and V planes with 10-bit value 512
	uvOffset := srcW * srcH * 2
	chromaSamples := (srcW / 2) * srcH // 4:2:2: full height chroma
	for i := 0; i < chromaSamples*2; i++ {
		binary.LittleEndian.PutUint16(src[uvOffset+i*2:], 512)
	}

	// Scale to 2x2
	dstW, dstH := 2, 2
	dst := make([]byte, dstW*dstH*3/2) // YUV420P: 4 + 1 + 1 = 6

	convertP210ToYUV420PScaled(src, srcW, srcH, dst, dstW, dstH)

	// Check Y values (should be 128 = 512 >> 2)
	for i := 0; i < dstW*dstH; i++ {
		if dst[i] != 128 {
			t.Errorf("Y[%d] = %d, want 128", i, dst[i])
		}
	}
	// Check U and V (should be 128)
	uvStart := dstW * dstH
	for i := uvStart; i < len(dst); i++ {
		if dst[i] != 128 {
			t.Errorf("UV[%d] = %d, want 128", i-uvStart, dst[i])
		}
	}
}

func TestConvertP210ToYUV420PScaled_MaxValue(t *testing.T) {
	// Test with 10-bit max value 1023 → should become 255 in 8-bit
	srcW, srcH := 4, 4
	src := make([]byte, srcW*srcH*4)

	// Fill all planes with 10-bit value 1023
	for i := 0; i < len(src)/2; i++ {
		binary.LittleEndian.PutUint16(src[i*2:], 1023)
	}

	dstW, dstH := 2, 2
	dst := make([]byte, dstW*dstH*3/2)

	convertP210ToYUV420PScaled(src, srcW, srcH, dst, dstW, dstH)

	for i := 0; i < len(dst); i++ {
		if dst[i] != 255 {
			t.Errorf("dst[%d] = %d, want 255", i, dst[i])
		}
	}
}

func TestConvertP210ToYUV420PScaled_ZeroValue(t *testing.T) {
	// Test with 10-bit value 0 → should become 0 in 8-bit
	srcW, srcH := 4, 4
	src := make([]byte, srcW*srcH*4) // already zeroed

	dstW, dstH := 2, 2
	dst := make([]byte, dstW*dstH*3/2)
	// Pre-fill with 0xFF to verify writes happen
	for i := range dst {
		dst[i] = 0xFF
	}

	convertP210ToYUV420PScaled(src, srcW, srcH, dst, dstW, dstH)

	for i := 0; i < len(dst); i++ {
		if dst[i] != 0 {
			t.Errorf("dst[%d] = %d, want 0", i, dst[i])
		}
	}
}

func TestConvertP210ToYUV420PScaled_Upscale(t *testing.T) {
	// Test upscaling: 2x2 → 4x4
	srcW, srcH := 2, 2
	src := make([]byte, srcW*srcH*4)

	// Fill with distinct Y values per pixel
	// Y plane: (0,0)=400, (1,0)=800, (0,1)=200, (1,1)=600
	yVals := []uint16{400, 800, 200, 600}
	for i, v := range yVals {
		binary.LittleEndian.PutUint16(src[i*2:], v)
	}

	// Fill U/V with 512 (neutral chroma)
	uvOffset := srcW * srcH * 2
	chromaSamples := (srcW / 2) * srcH
	for i := 0; i < chromaSamples*2; i++ {
		binary.LittleEndian.PutUint16(src[uvOffset+i*2:], 512)
	}

	dstW, dstH := 4, 4
	dst := make([]byte, dstW*dstH*3/2)

	convertP210ToYUV420PScaled(src, srcW, srcH, dst, dstW, dstH)

	// In nearest-neighbor upscale of 2x2→4x4, each src pixel maps to a 2x2 dst block.
	// Expected Y values (8-bit): 400>>2=100, 800>>2=200, 200>>2=50, 600>>2=150
	expectedY := []byte{
		100, 100, 200, 200,
		100, 100, 200, 200,
		50, 50, 150, 150,
		50, 50, 150, 150,
	}
	for i := 0; i < dstW*dstH; i++ {
		if dst[i] != expectedY[i] {
			t.Errorf("Y[%d] = %d, want %d", i, dst[i], expectedY[i])
		}
	}
}

func TestConvertP210ToYUV420PScaled_ChromaSubsampling(t *testing.T) {
	// Verify 4:2:2→4:2:0 chroma height reduction.
	// Source: 4x4 4:2:2 (chroma height = 4), dest: 4x4 4:2:0 (chroma height = 2)
	srcW, srcH := 4, 4
	src := make([]byte, srcW*srcH*4)

	// Fill Y with 10-bit 512
	for i := 0; i < srcW*srcH; i++ {
		binary.LittleEndian.PutUint16(src[i*2:], 512)
	}

	// Fill U plane: top two rows = 256, bottom two rows = 768
	uvOffset := srcW * srcH * 2
	chromaW := srcW / 2
	for row := 0; row < srcH; row++ {
		for col := 0; col < chromaW; col++ {
			val := uint16(256)
			if row >= 2 {
				val = 768
			}
			off := uvOffset + (row*chromaW+col)*2
			binary.LittleEndian.PutUint16(src[off:], val)
		}
	}

	// Fill V plane with 512
	vOffset := uvOffset + chromaW*srcH*2
	for i := 0; i < chromaW*srcH; i++ {
		binary.LittleEndian.PutUint16(src[vOffset+i*2:], 512)
	}

	// Same resolution output (4x4) to isolate chroma subsampling effect
	dstW, dstH := 4, 4
	dst := make([]byte, dstW*dstH*3/2) // Y:16 + U:4 + V:4 = 24

	convertP210ToYUV420PScaled(src, srcW, srcH, dst, dstW, dstH)

	// U plane in output: 2x2 (half width, half height)
	// Row 0 maps to src row 0 (val=256, 8-bit=64)
	// Row 1 maps to src row 2 (val=768, 8-bit=192)
	uStart := dstW * dstH // = 16
	dstChromaW := dstW / 2
	for row := 0; row < dstH/2; row++ {
		for col := 0; col < dstChromaW; col++ {
			idx := uStart + row*dstChromaW + col
			var expected byte
			if row == 0 {
				expected = 64 // 256 >> 2
			} else {
				expected = 192 // 768 >> 2
			}
			if dst[idx] != expected {
				t.Errorf("U[row=%d,col=%d] = %d, want %d", row, col, dst[idx], expected)
			}
		}
	}
}

func TestConvertP210ToYUV420PScaled_1080pTo480p(t *testing.T) {
	// Realistic size test: ensure no panics at production-like dimensions.
	// We use 1920x1080 → 854x480 to match the preview encoder's typical use case.
	srcW, srcH := 1920, 1080
	src := make([]byte, srcW*srcH*4)

	// Fill with a known pattern: 10-bit value 400 (8-bit 100)
	for i := 0; i < len(src)/2; i++ {
		binary.LittleEndian.PutUint16(src[i*2:], 400)
	}

	dstW, dstH := 854, 480
	dst := make([]byte, dstW*dstH*3/2)

	convertP210ToYUV420PScaled(src, srcW, srcH, dst, dstW, dstH)

	// Spot check: all values should be 100 (400 >> 2)
	for i := 0; i < 100; i++ {
		if dst[i] != 100 {
			t.Fatalf("dst[%d] = %d, want 100", i, dst[i])
		}
	}
	// Check a chroma sample
	uvStart := dstW * dstH
	if dst[uvStart] != 100 {
		t.Fatalf("U[0] = %d, want 100", dst[uvStart])
	}
}
