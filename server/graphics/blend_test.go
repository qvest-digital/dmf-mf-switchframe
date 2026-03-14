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
	// Start with black frame: Y=16 (limited-range black), Cb=128, Cr=128
	yuv := makeYUV420(width, height, 16, 128, 128)

	// Fully opaque white overlay (255,255,255,255)
	rgba := makeRGBA(width, height, 255, 255, 255, 255)

	AlphaBlendRGBA(yuv, rgba, width, height, 1.0)

	// White in limited-range BT.709 YUV: Y = 16 + ((47+157+16)*255+128)>>8
	// = 16 + (56228>>8) = 16 + 219 = 235
	// Cb = (-26*255 - 86*255 + 112*255 + 128) >> 8 + 128 = 128
	// Cr = (112*255 - 102*255 - 10*255 + 128) >> 8 + 128 = 128
	ySize := width * height
	for i := 0; i < ySize; i++ {
		require.InDelta(t, 235, int(yuv[i]), 1,
			"Y[%d] = %d, want ~235 (limited-range white)", i, yuv[i])
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
	// Start with limited-range black frame: Y=16, Cb=128, Cr=128
	yuv := makeYUV420(width, height, 16, 128, 128)

	// 50% alpha white overlay
	rgba := makeRGBA(width, height, 255, 255, 255, 128)

	AlphaBlendRGBA(yuv, rgba, width, height, 1.0)

	// At ~50% alpha (128/255 → a256=128):
	// overlayY = 235 (limited-range white)
	// Y = (16*128 + 235*128 + 128) >> 8 = (2048 + 30080 + 128) >> 8 = 125
	expected := byte(125)
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

	// Bottom 2 rows should be limited-range white (Y≈235)
	for row := 6; row < 8; row++ {
		for col := 0; col < width; col++ {
			idx := row*width + col
			require.InDelta(t, 235, int(yuv[idx]), 1,
				"Y[%d,%d] = %d, want ~235 (limited-range white overlay)", row, col, yuv[idx])
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
	yuv1 := makeYUV420(width, height, 16, 128, 128)
	yuv2 := makeYUV420(width, height, 16, 128, 128)
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

	// Pixels inside rect should be limited-range white (Y≈235).
	insideIdx := 2*width + 2 // row=2, col=2
	require.InDelta(t, 235, int(yuv[insideIdx]), 1,
		"Y[2,2] inside rect = %d, want ~235 (limited-range white)", yuv[insideIdx])
}

func TestAlphaBlendRGBARect_ClipBounds(t *testing.T) {
	t.Parallel()
	width, height := 8, 8
	yuv := makeYUV420(width, height, 100, 128, 128)

	// Rect extends off frame: (-2,-2)→(4,4) should clip to (0,0)→(4,4).
	overlay := makeRGBA(6, 6, 255, 255, 255, 255)
	rect := image.Rect(-2, -2, 4, 4)

	AlphaBlendRGBARect(yuv, overlay, width, height, 6, 6, rect, 1.0)

	// Pixel at (0,0) should be modified (limited-range white).
	require.InDelta(t, 235, int(yuv[0]), 1,
		"Y[0,0] should be ~235 after clipped blend, got %d", yuv[0])

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
	require.InDelta(t, 235, int(yuv[0]), 1,
		"Y[0,0] should be ~235 after even-aligned blend, got %d", yuv[0])
}

func TestAlphaBlendRGBARect_OverlayScaling(t *testing.T) {
	t.Parallel()
	width, height := 8, 8
	yuv := makeYUV420(width, height, 16, 128, 128)

	// 2x2 white overlay scaled into 4x4 rect.
	overlay := makeRGBA(2, 2, 255, 255, 255, 255)
	rect := image.Rect(0, 0, 4, 4)

	AlphaBlendRGBARect(yuv, overlay, width, height, 2, 2, rect, 1.0)

	// All 4x4 pixels in the rect should be limited-range white.
	for row := 0; row < 4; row++ {
		for col := 0; col < 4; col++ {
			idx := row*width + col
			require.InDelta(t, 235, int(yuv[idx]), 1,
				"Y[%d,%d] = %d, want ~235 (scaled overlay)", row, col, yuv[idx])
		}
	}

	// Pixels outside rect should remain at limited-range black.
	require.Equal(t, byte(16), yuv[4], "Y[0,4] outside rect should be 16")
}

func TestAlphaBlendRGBARectInto_Basic(t *testing.T) {
	t.Parallel()
	frameW, frameH := 1920, 1080
	yuv := makeYUV420(frameW, frameH, 100, 128, 128)

	// Save original Y values for comparison.
	origY := make([]byte, frameW*frameH)
	copy(origY, yuv[:frameW*frameH])

	overlayW, overlayH := 200, 50
	rgba := makeRGBA(overlayW, overlayH, 255, 255, 255, 255) // opaque white
	rect := image.Rect(100, 900, 1800, 1000)

	scratch, colLUT := AlphaBlendRGBARectInto(yuv, rgba, frameW, frameH, overlayW, overlayH,
		rect, 1.0, nil, nil)

	// Verify scratch buffers are non-nil.
	require.NotNil(t, scratch, "scratch should be non-nil after call")
	require.NotNil(t, colLUT, "colLUT should be non-nil after call")

	// Verify Y values are modified inside the rect region.
	// The rect is even-aligned: (100, 900) → (1800, 1000).
	insideIdx := 900*frameW + 100
	require.InDelta(t, 235, int(yuv[insideIdx]), 1,
		"Y inside rect at (100,900) = %d, want ~235 (limited-range white overlay)", yuv[insideIdx])

	// Verify Y values are unchanged outside the rect region.
	outsideIdx := 0 // top-left corner, well outside rect
	require.Equal(t, byte(100), yuv[outsideIdx],
		"Y outside rect at (0,0) should be 100 (unchanged)")

	outsideIdx2 := 800*frameW + 50 // above the rect
	require.Equal(t, byte(100), yuv[outsideIdx2],
		"Y outside rect at (50,800) should be 100 (unchanged)")

	// Verify Cb/Cr values are modified in the rect region.
	ySize := frameW * frameH
	uvSize := (frameW / 2) * (frameH / 2)
	halfFrameW := frameW / 2
	// Chroma sample at (50, 450) maps to rect region (100/2=50, 900/2=450).
	chromaInsideIdx := 450*halfFrameW + 50
	require.Equal(t, byte(128), yuv[ySize+chromaInsideIdx],
		"Cb inside rect should be 128 (white)")
	require.Equal(t, byte(128), yuv[ySize+uvSize+chromaInsideIdx],
		"Cr inside rect should be 128 (white)")

	// Verify chroma outside rect is unchanged.
	chromaOutsideIdx := 0
	require.Equal(t, byte(128), yuv[ySize+chromaOutsideIdx],
		"Cb outside rect should be 128 (unchanged)")
}

func TestAlphaBlendRGBARectInto_ScratchReuse(t *testing.T) {
	t.Parallel()
	frameW, frameH := 1920, 1080
	yuv := makeYUV420(frameW, frameH, 100, 128, 128)

	overlayW, overlayH := 200, 50
	rgba := makeRGBA(overlayW, overlayH, 255, 0, 0, 200)
	rect := image.Rect(100, 900, 1800, 1000)

	// First call: scratch buffers start as nil.
	scratch1, colLUT1 := AlphaBlendRGBARectInto(yuv, rgba, frameW, frameH, overlayW, overlayH,
		rect, 1.0, nil, nil)

	require.NotNil(t, scratch1, "scratch should be non-nil after first call")
	require.NotNil(t, colLUT1, "colLUT should be non-nil after first call")
	cap1Scratch := cap(scratch1)
	cap1ColLUT := cap(colLUT1)

	// Second call: pass returned scratch buffers back in.
	scratch2, colLUT2 := AlphaBlendRGBARectInto(yuv, rgba, frameW, frameH, overlayW, overlayH,
		rect, 1.0, scratch1, colLUT1)

	require.NotNil(t, scratch2, "scratch should be non-nil after second call")
	require.NotNil(t, colLUT2, "colLUT should be non-nil after second call")

	// Verify buffers were reused (same capacity, no new allocation).
	require.Equal(t, cap1Scratch, cap(scratch2),
		"scratch buffer should be reused (same capacity)")
	require.Equal(t, cap1ColLUT, cap(colLUT2),
		"colLUT buffer should be reused (same capacity)")
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
	// Bug 2: Pure blue (R=0,G=0,B=255) formerly produced overlayCb = 256 via
	// full-range coefficients. Limited-range coefficients (112*B) avoid the overflow:
	//   (-26*0 - 86*0 + 112*255 + 128) >> 8 + 128 = 112 + 128 = 240.
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
	// Pure red (R=255,G=0,B=0) formerly produced overlayCr = 256 via
	// full-range coefficients. Limited-range coefficients (112*R) avoid the overflow:
	//   (112*255 - 102*0 - 10*0 + 128) >> 8 + 128 = 112 + 128 = 240.
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
	// We use a bright background (Y=200) and a dark overlay (Y≈32 for pure blue
	// in limited-range BT.709) at alpha=255 to maximize the leak visibility.
	// Without the fix, the leaked background adds ~0.78 to the output Y
	// (200 * 1/256 ≈ 0.78).
	width, height := 4, 4

	// Bright background so any leak is detectable.
	yuv := makeYUV420(width, height, 200, 128, 128)

	// Pure blue overlay, fully opaque. Limited-range BT.709 Y for blue: 16+((16*255+128)>>8) = 32.
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

func TestRGBAToYUV_LimitedRange(t *testing.T) {
	t.Parallel()

	// Test that pure white RGBA overlay produces Y=235 (not 255) in the blended output.
	frameW, frameH := 4, 2
	yuv := makeYUV420(frameW, frameH, 16, 128, 128)      // black in limited range
	rgba := makeRGBA(frameW, frameH, 255, 255, 255, 255) // pure white, full alpha

	AlphaBlendRGBA(yuv, rgba, frameW, frameH, 1.0)

	// Y plane: white in limited range should be 235
	for i := 0; i < frameW*frameH; i++ {
		require.InDelta(t, 235, int(yuv[i]), 1, "Y[%d] should be ~235 (limited range white)", i)
	}

	// Test pure black: Y should be 16
	yuv2 := makeYUV420(frameW, frameH, 235, 128, 128)   // white in limited range
	rgbaBlack := makeRGBA(frameW, frameH, 0, 0, 0, 255) // pure black, full alpha
	AlphaBlendRGBA(yuv2, rgbaBlack, frameW, frameH, 1.0)
	for i := 0; i < frameW*frameH; i++ {
		require.InDelta(t, 16, int(yuv2[i]), 1, "Y[%d] should be ~16 (limited range black)", i)
	}

	// White overlay should produce Cb=128, Cr=128 (neutral chroma)
	yuv3 := makeYUV420(frameW, frameH, 16, 64, 200) // some chroma values
	rgbaWhite := makeRGBA(frameW, frameH, 255, 255, 255, 255)
	AlphaBlendRGBA(yuv3, rgbaWhite, frameW, frameH, 1.0)
	cbOffset := frameW * frameH
	crOffset := cbOffset + (frameW/2)*(frameH/2)
	for i := 0; i < (frameW/2)*(frameH/2); i++ {
		require.InDelta(t, 128, int(yuv3[cbOffset+i]), 1, "Cb[%d] should be ~128 (neutral)", i)
		require.InDelta(t, 128, int(yuv3[crOffset+i]), 1, "Cr[%d] should be ~128 (neutral)", i)
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

// BenchmarkAlphaBlendRGBARectInto_ScoreBug benchmarks the Into variant with
// pre-allocated scratch buffers (simulating Compositor steady-state).
func BenchmarkAlphaBlendRGBARectInto_ScoreBug(b *testing.B) {
	frameW, frameH := 1920, 1080
	yuv := makeYUV420(frameW, frameH, 128, 128, 128)

	overlayW, overlayH := 320, 80
	rgba := makeRGBA(overlayW, overlayH, 200, 50, 50, 220)
	rect := image.Rect(1580, 20, 1900, 100)

	// Pre-allocate scratch buffers (as Compositor would).
	scratch := make([]byte, rect.Dx()*4)
	var colLUT []int

	b.SetBytes(int64(overlayW * overlayH))
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		scratch, colLUT = AlphaBlendRGBARectInto(yuv, rgba, frameW, frameH, overlayW, overlayH, rect, 1.0, scratch, colLUT)
	}
}

// BenchmarkAlphaBlendRGBARectInto_LowerThird benchmarks the Into variant with
// pre-allocated scratch buffers for a lower-third overlay.
func BenchmarkAlphaBlendRGBARectInto_LowerThird(b *testing.B) {
	frameW, frameH := 1920, 1080
	yuv := makeYUV420(frameW, frameH, 128, 128, 128)

	overlayW, overlayH := 1920, 200
	rgba := makeRGBA(overlayW, overlayH, 30, 30, 30, 230)
	rect := image.Rect(0, 880, 1920, 1080)

	scratch := make([]byte, rect.Dx()*4)
	var colLUT []int

	b.SetBytes(int64(overlayW * overlayH))
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		scratch, colLUT = AlphaBlendRGBARectInto(yuv, rgba, frameW, frameH, overlayW, overlayH, rect, 1.0, scratch, colLUT)
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
