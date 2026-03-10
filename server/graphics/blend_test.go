package graphics

import (
	"image"
	"testing"

	"github.com/stretchr/testify/require"
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
	t.Parallel()
	width, height := 4, 4
	yuv := makeYUV420(width, height, 128, 128, 128)
	// Save original Y values
	origY := make([]byte, width*height)
	copy(origY, yuv[:width*height])

	rgba := makeRGBA(width, height, 255, 0, 0, 0) // fully transparent red

	AlphaBlendRGBA(yuv, rgba, width, height, 1.0)

	// YUV should be unchanged because overlay is fully transparent.
	for i := 0; i < width*height; i++ {
		require.Equal(t, origY[i], yuv[i], "Y[%d] should be unchanged", i)
	}
}

func TestAlphaBlendRGBA_OpaqueBlend(t *testing.T) {
	t.Parallel()
	width, height := 4, 4
	// Start with black frame: Y=0, Cb=128, Cr=128
	yuv := makeYUV420(width, height, 0, 128, 128)

	// Fully opaque white overlay (255,255,255,255)
	rgba := makeRGBA(width, height, 255, 255, 255, 255)

	AlphaBlendRGBA(yuv, rgba, width, height, 1.0)

	// White in BT.709 YUV: Y = 0.2126*255 + 0.7152*255 + 0.0722*255 = 255
	// Integer approx: (54+183+19)*255/256 = 255 (coefficients sum to 256)
	// Cb = -0.1146*255 - 0.3854*255 + 0.5*255 + 128 = 128
	// Cr = 0.5*255 - 0.4542*255 - 0.0458*255 + 128 = 128
	ySize := width * height
	for i := 0; i < ySize; i++ {
		diff := int(yuv[i]) - 255
		require.True(t, diff >= -1 && diff <= 0,
			"Y[%d] = %d, want 254-255 (white)", i, yuv[i])
	}
	uvSize := (width / 2) * (height / 2)
	for i := 0; i < uvSize; i++ {
		require.Equal(t, byte(128), yuv[ySize+i], "Cb[%d] should be 128", i)
		require.Equal(t, byte(128), yuv[ySize+uvSize+i], "Cr[%d] should be 128", i)
	}
}

func TestAlphaBlendRGBA_HalfAlpha(t *testing.T) {
	t.Parallel()
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
		require.True(t, diff >= -2 && diff <= 2,
			"Y[%d] = %d, want ~%d (50%% alpha blend)", i, yuv[i], expected)
	}
}

func TestAlphaBlendRGBA_LowerStrip(t *testing.T) {
	t.Parallel()
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
			require.Equal(t, byte(149), yuv[idx],
				"Y[%d,%d] should be 149 (unchanged green)", row, col)
		}
	}

	// Bottom 2 rows should be white (Y=254-255, integer rounding)
	for row := 6; row < 8; row++ {
		for col := 0; col < width; col++ {
			idx := row*width + col
			diff := int(yuv[idx]) - 255
			require.True(t, diff >= -1 && diff <= 0,
				"Y[%d,%d] = %d, want 254-255 (white overlay)", row, col, yuv[idx])
		}
	}
}

func TestAlphaBlendRGBA_AlphaScale(t *testing.T) {
	t.Parallel()
	width, height := 4, 4
	// Start with black frame
	yuv := makeYUV420(width, height, 0, 128, 128)

	// Fully opaque white overlay, but alphaScale = 0.0 (fully transparent)
	rgba := makeRGBA(width, height, 255, 255, 255, 255)

	AlphaBlendRGBA(yuv, rgba, width, height, 0.0)

	// Frame should be unchanged (alphaScale = 0 means skip all pixels)
	ySize := width * height
	for i := 0; i < ySize; i++ {
		require.Equal(t, byte(0), yuv[i],
			"Y[%d] should be 0 (unchanged, alphaScale=0)", i)
	}
}

// --- AlphaBlendRGBARect tests ---

func TestAlphaBlendRGBARect_FullFrame(t *testing.T) {
	t.Parallel()
	width, height := 8, 8

	// Full-frame rect should produce same result as AlphaBlendRGBA.
	yuv1 := makeYUV420(width, height, 0, 128, 128)
	yuv2 := makeYUV420(width, height, 0, 128, 128)
	rgba := makeRGBA(width, height, 255, 255, 255, 200)

	AlphaBlendRGBA(yuv1, rgba, width, height, 1.0)
	AlphaBlendRGBARect(yuv2, rgba, width, height, width, height,
		image.Rect(0, 0, width, height), 1.0)

	// Y planes should match.
	ySize := width * height
	for i := 0; i < ySize; i++ {
		diff := int(yuv1[i]) - int(yuv2[i])
		require.True(t, diff >= -1 && diff <= 1,
			"Y[%d]: full-frame=%d rect=%d", i, yuv1[i], yuv2[i])
	}
}

func TestAlphaBlendRGBARect_SubRegion(t *testing.T) {
	t.Parallel()
	width, height := 8, 8
	yuv := makeYUV420(width, height, 100, 128, 128)

	// 4x4 white overlay in the center (2,2)→(6,6).
	overlay := makeRGBA(4, 4, 255, 255, 255, 255)
	rect := image.Rect(2, 2, 6, 6)

	AlphaBlendRGBARect(yuv, overlay, width, height, 4, 4, rect, 1.0)

	// Pixels outside rect should be unchanged (Y=100).
	require.Equal(t, byte(100), yuv[0], "Y[0,0] outside rect should be unchanged")
	require.Equal(t, byte(100), yuv[1], "Y[0,1] outside rect should be unchanged")
	require.Equal(t, byte(100), yuv[width], "Y[1,0] outside rect should be unchanged")

	// Pixels inside rect should be white (Y≈254).
	insideIdx := 2*width + 2 // row=2, col=2
	diff := int(yuv[insideIdx]) - 254
	require.True(t, diff >= -1 && diff <= 1,
		"Y[2,2] inside rect = %d, want ~254 (white)", yuv[insideIdx])
}

func TestAlphaBlendRGBARect_ClipBounds(t *testing.T) {
	t.Parallel()
	width, height := 8, 8
	yuv := makeYUV420(width, height, 100, 128, 128)

	// Rect extends off frame: (-2,-2)→(4,4) should clip to (0,0)→(4,4).
	overlay := makeRGBA(6, 6, 255, 255, 255, 255)
	rect := image.Rect(-2, -2, 4, 4)

	AlphaBlendRGBARect(yuv, overlay, width, height, 6, 6, rect, 1.0)

	// Pixel at (0,0) should be modified (white).
	diff := int(yuv[0]) - 254
	require.True(t, diff >= -1 && diff <= 1,
		"Y[0,0] should be ~254 after clipped blend, got %d", yuv[0])

	// Pixel at (4,0) should be unchanged.
	require.Equal(t, byte(100), yuv[4], "Y[0,4] outside clipped rect should be unchanged")
}

func TestAlphaBlendRGBARect_EvenAlign(t *testing.T) {
	t.Parallel()
	width, height := 8, 8
	yuv := makeYUV420(width, height, 100, 128, 128)

	// Odd rect coords should get even-aligned.
	overlay := makeRGBA(4, 4, 255, 255, 255, 255)
	rect := image.Rect(1, 1, 5, 5) // odd → even-aligned to (0,0)→(4,4)

	AlphaBlendRGBARect(yuv, overlay, width, height, 4, 4, rect, 1.0)

	// (0,0) should be modified (even-aligned to 0).
	diff := int(yuv[0]) - 254
	require.True(t, diff >= -1 && diff <= 1,
		"Y[0,0] should be ~254 after even-aligned blend, got %d", yuv[0])
}

func TestAlphaBlendRGBARect_OverlayScaling(t *testing.T) {
	t.Parallel()
	width, height := 8, 8
	yuv := makeYUV420(width, height, 0, 128, 128)

	// 2x2 white overlay scaled into 4x4 rect.
	overlay := makeRGBA(2, 2, 255, 255, 255, 255)
	rect := image.Rect(0, 0, 4, 4)

	AlphaBlendRGBARect(yuv, overlay, width, height, 2, 2, rect, 1.0)

	// All 4x4 pixels in the rect should be white.
	for row := 0; row < 4; row++ {
		for col := 0; col < 4; col++ {
			idx := row*width + col
			diff := int(yuv[idx]) - 254
			require.True(t, diff >= -1 && diff <= 1,
				"Y[%d,%d] = %d, want ~254 (scaled overlay)", row, col, yuv[idx])
		}
	}

	// Pixels outside rect should remain black.
	require.Equal(t, byte(0), yuv[4], "Y[0,4] outside rect should be 0")
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

func TestAlphaBlendRGBARect_ShortRGBA(t *testing.T) {
	yuv := makeYUV420(8, 8, 16, 128, 128)
	original := make([]byte, len(yuv))
	copy(original, yuv)

	// Short RGBA buffer should be a no-op (no panic).
	shortRGBA := make([]byte, 10)
	AlphaBlendRGBARect(yuv, shortRGBA, 8, 8, 4, 4, image.Rect(0, 0, 4, 4), 1.0)
	require.Equal(t, original, yuv)
}

func TestAlphaBlendRGBARect_PureBlueNoChromaOverflow(t *testing.T) {
	t.Parallel()
	// Bug 2: Pure blue (R=0,G=0,B=255) produces overlayCb = 256 via:
	//   (-29*0 - 99*0 + 128*255 + 128) >> 8 + 128 = 128 + 128 = 256
	// byte(256) wraps to 0, producing visually wrong chroma.
	width, height := 4, 4
	yuv := makeYUV420(width, height, 0, 128, 128)

	// Pure blue overlay, fully opaque
	rgba := makeRGBA(width, height, 0, 0, 255, 255)
	rect := image.Rect(0, 0, width, height)

	AlphaBlendRGBARect(yuv, rgba, width, height, width, height, rect, 1.0)

	ySize := width * height
	uvSize := (width / 2) * (height / 2)

	// BT.709 for pure blue: Cb should be ~255 (max), NOT 0 (overflow wrap)
	for i := 0; i < uvSize; i++ {
		cb := yuv[ySize+i]
		require.Greater(t, cb, byte(200),
			"Cb[%d] = %d, should be >200 for pure blue (not wrapped to 0)", i, cb)
	}
}

func TestAlphaBlendRGBARect_PureRedNoChromaOverflow(t *testing.T) {
	t.Parallel()
	// Pure red (R=255,G=0,B=0) produces overlayCr = 256 via:
	//   (128*255 - 116*0 - 12*0 + 128) >> 8 + 128 = 128 + 128 = 256
	// byte(256) wraps to 0, producing visually wrong chroma.
	width, height := 4, 4
	yuv := makeYUV420(width, height, 0, 128, 128)

	// Pure red overlay, fully opaque
	rgba := makeRGBA(width, height, 255, 0, 0, 255)
	rect := image.Rect(0, 0, width, height)

	AlphaBlendRGBARect(yuv, rgba, width, height, width, height, rect, 1.0)

	ySize := width * height
	uvSize := (width / 2) * (height / 2)

	// BT.709 for pure red: Cr should be ~255 (max), NOT 0 (overflow wrap)
	for i := 0; i < uvSize; i++ {
		cr := yuv[ySize+uvSize+i]
		require.Greater(t, cr, byte(200),
			"Cr[%d] = %d, should be >200 for pure red (not wrapped to 0)", i, cr)
	}
}

func TestAlphaBlendRGBARect_OpaqueNoBackgroundLeak(t *testing.T) {
	t.Parallel()
	// Bug: AlphaBlendRGBARect skips the `a += a >> 7` mapping that converts
	// alpha 255 → 256. Without it, inv = 256 - 255 = 1, leaking ~0.4% of
	// the background through fully opaque pixels. AlphaBlendRGBA (full-frame)
	// does this correctly via its assembly/generic kernel.
	//
	// We use a bright background (Y=200) and a dark overlay (Y≈18 for pure blue)
	// at alpha=255 to maximize the leak visibility. Without the fix, the leaked
	// background adds ~0.78 to the output Y (200 * 1/256 ≈ 0.78), rounding
	// to Y=19 instead of the correct Y=18.
	width, height := 4, 4

	// Bright background so any leak is detectable.
	yuv := makeYUV420(width, height, 200, 128, 128)

	// Pure blue overlay, fully opaque. BT.709 Y for blue: (19*255+128)>>8 = 19.
	rgba := makeRGBA(width, height, 0, 0, 255, 255)
	rect := image.Rect(0, 0, width, height)

	AlphaBlendRGBARect(yuv, rgba, width, height, width, height, rect, 1.0)

	// Compute the expected Y value using the full-frame variant as reference.
	yuvRef := makeYUV420(width, height, 200, 128, 128)
	AlphaBlendRGBA(yuvRef, rgba, width, height, 1.0)

	// Both should produce identical Y values for every pixel.
	ySize := width * height
	for i := 0; i < ySize; i++ {
		require.Equal(t, yuvRef[i], yuv[i],
			"Y[%d]: rect=%d, full-frame=%d — background leaked through opaque overlay", i, yuv[i], yuvRef[i])
	}

	// Also verify chroma planes match (Cb/Cr).
	uvSize := (width / 2) * (height / 2)
	for i := 0; i < uvSize; i++ {
		cbRect := yuv[ySize+i]
		cbRef := yuvRef[ySize+i]
		require.Equal(t, cbRef, cbRect,
			"Cb[%d]: rect=%d, full-frame=%d — background leaked through opaque chroma", i, cbRect, cbRef)

		crRect := yuv[ySize+uvSize+i]
		crRef := yuvRef[ySize+uvSize+i]
		require.Equal(t, crRef, crRect,
			"Cr[%d]: rect=%d, full-frame=%d — background leaked through opaque chroma", i, crRect, crRef)
	}
}

func TestAlphaBlendRGBARect_ShortYUV(t *testing.T) {
	shortYUV := make([]byte, 10)
	rgba := makeRGBA(4, 4, 255, 0, 0, 128)

	// Short YUV buffer should be a no-op (no panic).
	AlphaBlendRGBARect(shortYUV, rgba, 8, 8, 4, 4, image.Rect(0, 0, 4, 4), 1.0)
}

// BenchmarkAlphaBlendRGBARect_ScoreBug benchmarks a typical score bug overlay
// (320x80 overlay positioned at top-right of 1080p frame).
func BenchmarkAlphaBlendRGBARect_ScoreBug(b *testing.B) {
	frameW, frameH := 1920, 1080
	yuv := makeYUV420(frameW, frameH, 128, 128, 128)

	overlayW, overlayH := 320, 80
	rgba := makeRGBA(overlayW, overlayH, 200, 50, 50, 220)
	rect := image.Rect(1580, 20, 1900, 100) // top-right corner

	b.SetBytes(int64(overlayW * overlayH))
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		AlphaBlendRGBARect(yuv, rgba, frameW, frameH, overlayW, overlayH, rect, 1.0)
	}
}

// BenchmarkAlphaBlendRGBARect_LowerThird benchmarks a lower-third overlay
// (1920x200 overlay positioned at bottom of 1080p frame).
func BenchmarkAlphaBlendRGBARect_LowerThird(b *testing.B) {
	frameW, frameH := 1920, 1080
	yuv := makeYUV420(frameW, frameH, 128, 128, 128)

	overlayW, overlayH := 1920, 200
	rgba := makeRGBA(overlayW, overlayH, 30, 30, 30, 230)
	rect := image.Rect(0, 880, 1920, 1080)

	b.SetBytes(int64(overlayW * overlayH))
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		AlphaBlendRGBARect(yuv, rgba, frameW, frameH, overlayW, overlayH, rect, 1.0)
	}
}

// BenchmarkAlphaBlendRGBARectInto_ScoreBug benchmarks the Into variant with a
// pre-allocated scratch buffer (simulating Compositor steady-state).
func BenchmarkAlphaBlendRGBARectInto_ScoreBug(b *testing.B) {
	frameW, frameH := 1920, 1080
	yuv := makeYUV420(frameW, frameH, 128, 128, 128)

	overlayW, overlayH := 320, 80
	rgba := makeRGBA(overlayW, overlayH, 200, 50, 50, 220)
	rect := image.Rect(1580, 20, 1900, 100)

	// Pre-allocate scratch buffer (as Compositor would).
	scratch := make([]byte, rect.Dx()*4)

	b.SetBytes(int64(overlayW * overlayH))
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		scratch = AlphaBlendRGBARectInto(yuv, rgba, frameW, frameH, overlayW, overlayH, rect, 1.0, scratch)
	}
}

// BenchmarkAlphaBlendRGBARectInto_LowerThird benchmarks the Into variant with
// pre-allocated scratch for a lower-third overlay.
func BenchmarkAlphaBlendRGBARectInto_LowerThird(b *testing.B) {
	frameW, frameH := 1920, 1080
	yuv := makeYUV420(frameW, frameH, 128, 128, 128)

	overlayW, overlayH := 1920, 200
	rgba := makeRGBA(overlayW, overlayH, 30, 30, 30, 230)
	rect := image.Rect(0, 880, 1920, 1080)

	scratch := make([]byte, rect.Dx()*4)

	b.SetBytes(int64(overlayW * overlayH))
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		scratch = AlphaBlendRGBARectInto(yuv, rgba, frameW, frameH, overlayW, overlayH, rect, 1.0, scratch)
	}
}

// BenchmarkAlphaBlendRGBA_FullFrame_ForComparison benchmarks the SIMD full-frame
// path for comparison with the rect path.
func BenchmarkAlphaBlendRGBA_FullFrame_ForComparison(b *testing.B) {
	frameW, frameH := 1920, 1080
	yuv := makeYUV420(frameW, frameH, 128, 128, 128)
	rgba := makeRGBA(frameW, frameH, 200, 50, 50, 220)

	b.SetBytes(int64(frameW * frameH))
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		AlphaBlendRGBA(yuv, rgba, frameW, frameH, 1.0)
	}
}
