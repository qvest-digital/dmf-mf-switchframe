package graphics

import (
	"testing"
)

// makeYUV420 creates a YUV420 planar buffer filled with the given Y, Cb, Cr values.
func makeYUV420(width, height int, y, cb, cr byte) []byte {
	ySize := width * height
	uvSize := (width / 2) * (height / 2)
	buf := make([]byte, ySize+2*uvSize)
	for i := 0; i < ySize; i++ {
		buf[i] = y
	}
	for i := 0; i < uvSize; i++ {
		buf[ySize+i] = cb
		buf[ySize+uvSize+i] = cr
	}
	return buf
}

// makeRGBA creates an RGBA buffer filled with the given R, G, B, A values.
func makeRGBA(width, height int, r, g, b, a byte) []byte {
	buf := make([]byte, width*height*4)
	for i := 0; i < width*height; i++ {
		buf[i*4] = r
		buf[i*4+1] = g
		buf[i*4+2] = b
		buf[i*4+3] = a
	}
	return buf
}

func TestAlphaBlendRGBA_TransparentPassthrough(t *testing.T) {
	width, height := 4, 4
	yuv := makeYUV420(width, height, 128, 128, 128)
	// Save original Y values
	origY := make([]byte, width*height)
	copy(origY, yuv[:width*height])

	rgba := makeRGBA(width, height, 255, 0, 0, 0) // fully transparent red

	AlphaBlendRGBA(yuv, rgba, width, height, 1.0)

	// YUV should be unchanged because overlay is fully transparent.
	for i := 0; i < width*height; i++ {
		if yuv[i] != origY[i] {
			t.Errorf("Y[%d] = %d, want %d", i, yuv[i], origY[i])
		}
	}
}

func TestAlphaBlendRGBA_OpaqueBlend(t *testing.T) {
	width, height := 4, 4
	// Start with black frame: Y=0, Cb=128, Cr=128
	yuv := makeYUV420(width, height, 0, 128, 128)

	// Fully opaque white overlay (255,255,255,255)
	rgba := makeRGBA(width, height, 255, 255, 255, 255)

	AlphaBlendRGBA(yuv, rgba, width, height, 1.0)

	// White in BT.709 YUV: Y = 0.2126*255 + 0.7152*255 + 0.0722*255 = 255
	// Cb = -0.1146*255 - 0.3854*255 + 0.5*255 + 128 = 128
	// Cr = 0.5*255 - 0.4542*255 - 0.0458*255 + 128 = 128
	ySize := width * height
	for i := 0; i < ySize; i++ {
		if yuv[i] != 255 {
			t.Errorf("Y[%d] = %d, want 255 (white)", i, yuv[i])
		}
	}
	uvSize := (width / 2) * (height / 2)
	for i := 0; i < uvSize; i++ {
		if yuv[ySize+i] != 128 {
			t.Errorf("Cb[%d] = %d, want 128", i, yuv[ySize+i])
		}
		if yuv[ySize+uvSize+i] != 128 {
			t.Errorf("Cr[%d] = %d, want 128", i, yuv[ySize+uvSize+i])
		}
	}
}

func TestAlphaBlendRGBA_HalfAlpha(t *testing.T) {
	width, height := 4, 4
	// Start with black frame: Y=0, Cb=128, Cr=128
	yuv := makeYUV420(width, height, 0, 128, 128)

	// 50% alpha white overlay
	rgba := makeRGBA(width, height, 255, 255, 255, 128)

	AlphaBlendRGBA(yuv, rgba, width, height, 1.0)

	// At ~50% alpha (128/255 = 0.502):
	// Y = 0*(1-0.502) + 255*0.502 = 128
	expected := byte(128)
	ySize := width * height
	for i := 0; i < ySize; i++ {
		diff := int(yuv[i]) - int(expected)
		if diff < -2 || diff > 2 {
			t.Errorf("Y[%d] = %d, want ~%d (50%% alpha blend)", i, yuv[i], expected)
		}
	}
}

func TestAlphaBlendRGBA_LowerStrip(t *testing.T) {
	width, height := 8, 8
	// Start with green frame: Y=149, Cb=43, Cr=21 (BT.709 for pure green)
	yuv := makeYUV420(width, height, 149, 43, 21)

	// Create RGBA overlay: transparent everywhere except the bottom 2 rows
	// (simulating a lower-third graphic)
	rgba := make([]byte, width*height*4)
	for row := 0; row < height; row++ {
		for col := 0; col < width; col++ {
			idx := (row*width + col) * 4
			if row >= 6 { // bottom 2 rows: opaque white
				rgba[idx] = 255
				rgba[idx+1] = 255
				rgba[idx+2] = 255
				rgba[idx+3] = 255
			} else { // transparent
				rgba[idx+3] = 0
			}
		}
	}

	AlphaBlendRGBA(yuv, rgba, width, height, 1.0)

	// Top 6 rows should be unchanged (green)
	for row := 0; row < 6; row++ {
		for col := 0; col < width; col++ {
			idx := row*width + col
			if yuv[idx] != 149 {
				t.Errorf("Y[%d,%d] = %d, want 149 (unchanged green)", row, col, yuv[idx])
			}
		}
	}

	// Bottom 2 rows should be white (Y=255)
	for row := 6; row < 8; row++ {
		for col := 0; col < width; col++ {
			idx := row*width + col
			if yuv[idx] != 255 {
				t.Errorf("Y[%d,%d] = %d, want 255 (white overlay)", row, col, yuv[idx])
			}
		}
	}
}

func TestAlphaBlendRGBA_AlphaScale(t *testing.T) {
	width, height := 4, 4
	// Start with black frame
	yuv := makeYUV420(width, height, 0, 128, 128)

	// Fully opaque white overlay, but alphaScale = 0.0 (fully transparent)
	rgba := makeRGBA(width, height, 255, 255, 255, 255)

	AlphaBlendRGBA(yuv, rgba, width, height, 0.0)

	// Frame should be unchanged (alphaScale = 0 means skip all pixels)
	ySize := width * height
	for i := 0; i < ySize; i++ {
		if yuv[i] != 0 {
			t.Errorf("Y[%d] = %d, want 0 (unchanged, alphaScale=0)", i, yuv[i])
		}
	}
}

func BenchmarkAlphaBlendRGBA_TypicalLowerThird(b *testing.B) {
	width, height := 1920, 1080
	yuv := makeYUV420(width, height, 128, 128, 128)
	// Lower-third: bottom 15% opaque, rest transparent
	rgba := make([]byte, width*height*4)
	cutoff := int(float64(height) * 0.85)
	for row := 0; row < height; row++ {
		for col := 0; col < width; col++ {
			idx := (row*width + col) * 4
			if row >= cutoff {
				rgba[idx] = 255
				rgba[idx+1] = 255
				rgba[idx+2] = 255
				rgba[idx+3] = 200
			}
		}
	}

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		AlphaBlendRGBA(yuv, rgba, width, height, 1.0)
	}
}
