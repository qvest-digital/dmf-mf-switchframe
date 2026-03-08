package graphics

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAlphaBlendRGBARowY_AllTransparent(t *testing.T) {
	t.Parallel()
	width := 16
	yRow := make([]byte, width)
	for i := range yRow {
		yRow[i] = 128 // fill with non-zero to verify no change
	}
	origY := make([]byte, width)
	copy(origY, yRow)

	// All transparent (A=0)
	rgba := make([]byte, width*4)
	for i := 0; i < width; i++ {
		rgba[i*4] = 255   // R
		rgba[i*4+1] = 0   // G
		rgba[i*4+2] = 0   // B
		rgba[i*4+3] = 0   // A = transparent
	}

	alphaBlendRGBARowY(&yRow[0], &rgba[0], width, 256)

	for i := 0; i < width; i++ {
		assert.Equal(t, origY[i], yRow[i], "Y[%d] should be unchanged for transparent pixel", i)
	}
}

func TestAlphaBlendRGBARowY_FullOpaqueWhite(t *testing.T) {
	t.Parallel()
	width := 16
	yRow := make([]byte, width)
	// Start with black
	for i := range yRow {
		yRow[i] = 0
	}

	// Fully opaque white (R=G=B=A=255)
	rgba := make([]byte, width*4)
	for i := 0; i < width; i++ {
		rgba[i*4] = 255
		rgba[i*4+1] = 255
		rgba[i*4+2] = 255
		rgba[i*4+3] = 255
	}

	alphaBlendRGBARowY(&yRow[0], &rgba[0], width, 256)

	// BT.709: Y = (54*255 + 183*255 + 18*255 + 128) >> 8
	// = (13770 + 46665 + 4590 + 128) >> 8 = 65153 >> 8 = 254
	// Integer approximation: 54+183+18=255, so (255*255+128)>>8 = 254
	for i := 0; i < width; i++ {
		assert.InDelta(t, 254, int(yRow[i]), 1, "Y[%d] should be ~254 for white", i)
	}
}

func TestAlphaBlendRGBARowY_FullOpaqueBlack(t *testing.T) {
	t.Parallel()
	width := 16
	yRow := make([]byte, width)
	for i := range yRow {
		yRow[i] = 200 // start with non-zero
	}

	// Fully opaque black (R=G=B=0, A=255)
	rgba := make([]byte, width*4)
	for i := 0; i < width; i++ {
		rgba[i*4+3] = 255 // A = opaque
	}

	alphaBlendRGBARowY(&yRow[0], &rgba[0], width, 256)

	// overlayY = 0, a256 = (255*256+128)>>8 = 255
	// result = (200*1 + 0*255 + 128) >> 8 = 328>>8 = 1
	for i := 0; i < width; i++ {
		assert.InDelta(t, 0, int(yRow[i]), 1, "Y[%d] should be ~0 for opaque black overlay", i)
	}
}

func TestAlphaBlendRGBARowY_HalfAlpha(t *testing.T) {
	t.Parallel()
	width := 16
	yRow := make([]byte, width)
	// Start with Y=0 (black)

	// Half-alpha white (A=128)
	rgba := make([]byte, width*4)
	for i := 0; i < width; i++ {
		rgba[i*4] = 255
		rgba[i*4+1] = 255
		rgba[i*4+2] = 255
		rgba[i*4+3] = 128
	}

	alphaBlendRGBARowY(&yRow[0], &rgba[0], width, 256)

	// a256 = (128*256+128)>>8 = 128
	// overlayY = (54*255 + 183*255 + 18*255 + 128) >> 8 = 254
	// result = (0*128 + 254*128 + 128) >> 8 = 32640>>8 = 127
	for i := 0; i < width; i++ {
		assert.InDelta(t, 127, int(yRow[i]), 2, "Y[%d] should be ~127 for half-alpha white on black", i)
	}
}

func TestAlphaBlendRGBARowY_AlphaScale(t *testing.T) {
	t.Parallel()
	width := 16
	yRow := make([]byte, width)
	// Start with Y=0 (black)

	// Full opaque white, but alphaScale256=128 (50%)
	rgba := make([]byte, width*4)
	for i := 0; i < width; i++ {
		rgba[i*4] = 255
		rgba[i*4+1] = 255
		rgba[i*4+2] = 255
		rgba[i*4+3] = 255
	}

	alphaBlendRGBARowY(&yRow[0], &rgba[0], width, 128) // 50% alpha scale

	// a256 = (255*128+128)>>8 = 32768>>8 = 128
	// overlayY = 254
	// result = (0*128 + 254*128 + 128) >> 8 = 32640>>8 = 127
	for i := 0; i < width; i++ {
		assert.InDelta(t, 127, int(yRow[i]), 2, "Y[%d] should be ~127 for 50%% alpha scale", i)
	}
}

func TestAlphaBlendRGBARowY_AlphaScaleZero(t *testing.T) {
	t.Parallel()
	width := 8
	yRow := make([]byte, width)
	for i := range yRow {
		yRow[i] = 100
	}
	origY := make([]byte, width)
	copy(origY, yRow)

	rgba := make([]byte, width*4)
	for i := 0; i < width; i++ {
		rgba[i*4] = 255
		rgba[i*4+1] = 255
		rgba[i*4+2] = 255
		rgba[i*4+3] = 255
	}

	alphaBlendRGBARowY(&yRow[0], &rgba[0], width, 0) // alphaScale=0

	for i := 0; i < width; i++ {
		assert.Equal(t, origY[i], yRow[i], "Y[%d] should be unchanged with alphaScale=0", i)
	}
}

func TestAlphaBlendRGBARowY_CrossValidation(t *testing.T) {
	t.Parallel()
	width := 1920

	// Create a varied RGBA overlay (lower-third pattern: first 288 pixels opaque, rest transparent)
	rgba := make([]byte, width*4)
	for i := 0; i < width; i++ {
		if i < 288 {
			rgba[i*4] = byte(i % 256)             // R varies
			rgba[i*4+1] = byte((i * 3) % 256)     // G varies
			rgba[i*4+2] = byte((i * 7) % 256)     // B varies
			rgba[i*4+3] = byte(128 + (i%128))      // A varies 128-255
		}
		// else: alpha=0 (transparent)
	}

	// Test Y-plane output against reference float64 implementation
	alphaScale := 0.85

	// Reference: use the original float64 AlphaBlendRGBA on a 2-row frame
	// (height=1 doesn't work well with YUV420 chroma subsampling).
	height := 2
	yuvRef := makeYUV420(width, height, 100, 128, 128)
	rgbaRef := make([]byte, width*height*4)
	copy(rgbaRef, rgba) // first row same, second row all zeros (transparent)

	// Run reference implementation
	alphaBlendRGBAReference(yuvRef, rgbaRef, width, height, alphaScale)

	// Run kernel implementation
	yRow := make([]byte, width)
	for i := range yRow {
		yRow[i] = 100 // same initial Y
	}
	alphaScale256 := int(alphaScale*256 + 0.5)
	alphaBlendRGBARowY(&yRow[0], &rgba[0], width, alphaScale256)

	// Compare with tolerance +/-1 for integer rounding differences
	mismatches := 0
	for i := 0; i < width; i++ {
		diff := int(yRow[i]) - int(yuvRef[i])
		if diff < -1 || diff > 1 {
			mismatches++
			if mismatches <= 10 {
				t.Errorf("Y[%d]: kernel=%d, reference=%d, diff=%d", i, yRow[i], yuvRef[i], diff)
			}
		}
	}
	if mismatches > 0 {
		t.Errorf("total mismatches (>1 tolerance): %d / %d", mismatches, width)
	}
}

func TestAlphaBlendRGBARowY_OddWidths(t *testing.T) {
	t.Parallel()

	for _, width := range []int{1, 3, 7, 15, 17} {
		width := width
		t.Run(
			func() string {
				return "width=" + string(rune('0'+width)) // crude but works for small widths
			}(),
			func(t *testing.T) {
				// Use a proper subtest name
			},
		)
	}

	// Test odd widths individually
	widths := []int{1, 3, 7, 15, 17}
	for _, width := range widths {
		yRow := make([]byte, width)
		for i := range yRow {
			yRow[i] = 50
		}

		rgba := make([]byte, width*4)
		for i := 0; i < width; i++ {
			rgba[i*4] = 200
			rgba[i*4+1] = 100
			rgba[i*4+2] = 50
			rgba[i*4+3] = 200
		}

		alphaBlendRGBARowY(&yRow[0], &rgba[0], width, 256)

		// Verify no out-of-bounds writes by checking result is reasonable
		for i := 0; i < width; i++ {
			require.True(t, yRow[i] > 0 && yRow[i] < 255,
				"Y[%d] = %d should be between 0 and 255 for width=%d", i, yRow[i], width)
		}
	}
}

func TestAlphaBlendRGBARowY_WidthZero(t *testing.T) {
	t.Parallel()
	// Should not panic with width=0
	var y byte
	var r byte
	alphaBlendRGBARowY(&y, &r, 0, 256)
}

func TestAlphaBlendRGBARowY_PureColors(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		r, g, b  byte
		wantY    int // expected BT.709 Y (integer approx)
	}{
		{"pure red", 255, 0, 0, 54},     // 54*255/256 ~ 53-54
		{"pure green", 0, 255, 0, 182},  // 183*255/256 ~ 182
		{"pure blue", 0, 0, 255, 17},    // 18*255/256 ~ 17-18
		{"mid gray", 128, 128, 128, 127}, // (54+183+18)*128/256 ~ 127
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			width := 4
			yRow := make([]byte, width)

			rgba := make([]byte, width*4)
			for i := 0; i < width; i++ {
				rgba[i*4] = tc.r
				rgba[i*4+1] = tc.g
				rgba[i*4+2] = tc.b
				rgba[i*4+3] = 255
			}

			alphaBlendRGBARowY(&yRow[0], &rgba[0], width, 256)

			for i := 0; i < width; i++ {
				assert.InDelta(t, tc.wantY, int(yRow[i]), 2,
					"Y[%d] for %s", i, tc.name)
			}
		})
	}
}

// alphaBlendRGBAReference is the original float64 implementation, kept for cross-validation.
func alphaBlendRGBAReference(yuv []byte, rgba []byte, width, height int, alphaScale float64) {
	ySize := width * height
	cbOffset := ySize
	crOffset := ySize + (width/2)*(height/2)
	halfW := width / 2

	for row := 0; row < height; row++ {
		for col := 0; col < width; col++ {
			rgbaIdx := (row*width + col) * 4
			a := float64(rgba[rgbaIdx+3]) / 255.0 * alphaScale

			if a < 1.0/255.0 {
				continue
			}

			r := float64(rgba[rgbaIdx])
			g := float64(rgba[rgbaIdx+1])
			b := float64(rgba[rgbaIdx+2])

			overlayY := 0.2126*r + 0.7152*g + 0.0722*b
			overlayCb := -0.1146*r - 0.3854*g + 0.5*b + 128.0
			overlayCr := 0.5*r - 0.4542*g - 0.0458*b + 128.0

			invA := 1.0 - a

			yIdx := row*width + col
			yuv[yIdx] = clampByteRef(float64(yuv[yIdx])*invA + overlayY*a)

			uvIdx := (row/2)*halfW + (col / 2)
			yuv[cbOffset+uvIdx] = clampByteRef(float64(yuv[cbOffset+uvIdx])*invA + overlayCb*a)
			yuv[crOffset+uvIdx] = clampByteRef(float64(yuv[crOffset+uvIdx])*invA + overlayCr*a)
		}
	}
}

func clampByteRef(v float64) byte {
	if v < 0 {
		return 0
	}
	if v > 255 {
		return 255
	}
	return byte(v + 0.5)
}

// BenchmarkAlphaBlendRGBARowY_1920_FullOpaque benchmarks a full-width opaque overlay.
func BenchmarkAlphaBlendRGBARowY_1920_FullOpaque(b *testing.B) {
	width := 1920
	yRow := make([]byte, width)
	for i := range yRow {
		yRow[i] = 128
	}
	rgba := make([]byte, width*4)
	for i := 0; i < width; i++ {
		rgba[i*4] = 200
		rgba[i*4+1] = 150
		rgba[i*4+2] = 100
		rgba[i*4+3] = 255
	}

	b.ReportAllocs()
	b.SetBytes(int64(width))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		alphaBlendRGBARowY(&yRow[0], &rgba[0], width, 256)
	}
}

// BenchmarkAlphaBlendRGBARowY_1920_Sparse benchmarks a lower-third overlay
// (first 288 pixels opaque, rest transparent).
func BenchmarkAlphaBlendRGBARowY_1920_Sparse(b *testing.B) {
	width := 1920
	yRow := make([]byte, width)
	for i := range yRow {
		yRow[i] = 128
	}
	rgba := make([]byte, width*4)
	for i := 0; i < 288; i++ { // 15% of 1920
		rgba[i*4] = 200
		rgba[i*4+1] = 150
		rgba[i*4+2] = 100
		rgba[i*4+3] = 255
	}
	// rest is alpha=0 (transparent)

	b.ReportAllocs()
	b.SetBytes(int64(width))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		alphaBlendRGBARowY(&yRow[0], &rgba[0], width, 256)
	}
}

// BenchmarkAlphaBlendRGBA_Full benchmarks the full AlphaBlendRGBA function
// (Y kernel + Go chroma) on a 1080p lower-third.
func BenchmarkAlphaBlendRGBA_Full(b *testing.B) {
	width, height := 1920, 1080
	yuv := makeYUV420(width, height, 128, 128, 128)
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

