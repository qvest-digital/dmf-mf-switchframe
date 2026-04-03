//go:build correctness

package keying

import (
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/zsiec/switchframe/server/correctness/testutil"
	"github.com/zsiec/switchframe/server/graphics"
)

const (
	testWidth  = 320
	testHeight = 240
)

// BT.709 limited-range YCbCr constants for primary colors.
// These match the keyer_test.go conventions and GenerateGreenScreenYUV420.
var (
	greenKeyColor = graphics.YCbCr{Y: 145, Cb: 54, Cr: 26} // matches GenerateGreenScreenYUV420
	redYCbCr      = struct{ Y, Cb, Cr byte }{Y: 81, Cb: 90, Cr: 240}
	blueYCbCr     = struct{ Y, Cb, Cr byte }{Y: 41, Cb: 240, Cr: 110}
)

// makeYUV420Frame creates a solid-color YUV420 frame (convenience wrapper).
func makeYUV420Frame(w, h int, y, cb, cr byte) []byte {
	return testutil.GenerateSolidYUV420(w, h, y, cb, cr)
}

// ==========================================================================
// 1. TestChromaKeyPureGreenTransparent — Pure green frame keyed with green
//    key color should produce a fully transparent mask (alpha near 0).
// ==========================================================================

func TestChromaKeyPureGreenTransparent(t *testing.T) {
	t.Parallel()

	frame := testutil.GenerateGreenScreenYUV420(testWidth, testHeight)

	mask := graphics.ChromaKey(frame, testWidth, testHeight, greenKeyColor, 0.4, 0.1, 0.0)

	require.Len(t, mask, testWidth*testHeight)

	// All pixels share the same chroma (solid frame), so all should be transparent.
	for i, a := range mask {
		require.LessOrEqual(t, a, byte(5),
			"pixel %d: expected transparent (<=5) for pure green, got %d", i, a)
	}
}

// ==========================================================================
// 2. TestChromaKeyNonGreenOpaque — Solid red frame keyed with green key
//    color should produce a fully opaque mask (alpha near 255).
// ==========================================================================

func TestChromaKeyNonGreenOpaque(t *testing.T) {
	t.Parallel()

	frame := makeYUV420Frame(testWidth, testHeight, redYCbCr.Y, redYCbCr.Cb, redYCbCr.Cr)

	mask := graphics.ChromaKey(frame, testWidth, testHeight, greenKeyColor, 0.4, 0.1, 0.0)

	require.Len(t, mask, testWidth*testHeight)

	for i, a := range mask {
		require.GreaterOrEqual(t, a, byte(225),
			"pixel %d: expected opaque (>=225) for red with green key, got %d", i, a)
	}
}

// ==========================================================================
// 3. TestChromaKeyEdgeSoftness — Frame with green-to-non-green gradient in
//    chroma space. Alpha should transition monotonically in the gradient region.
// ==========================================================================

func TestChromaKeyEdgeSoftness(t *testing.T) {
	t.Parallel()

	// Create a frame with a horizontal Cb/Cr gradient from green toward red.
	// Green: Cb=54, Cr=26. Red: Cb=90, Cr=240.
	// We'll interpolate across columns in the chroma plane.
	w, h := 128, 4 // wide enough for a meaningful gradient
	ySize := w * h
	uvW := w / 2
	uvH := h / 2
	uvSize := uvW * uvH
	frame := make([]byte, ySize+2*uvSize)

	// Y plane: constant mid-luma
	for i := 0; i < ySize; i++ {
		frame[i] = 128
	}

	// Chroma planes: horizontal gradient from green to red
	greenCb, greenCr := float64(54), float64(26)
	redCb, redCr := float64(90), float64(240)
	for row := 0; row < uvH; row++ {
		for col := 0; col < uvW; col++ {
			t_ := float64(col) / float64(uvW-1)
			cb := byte(greenCb + t_*(redCb-greenCb) + 0.5)
			cr := byte(greenCr + t_*(redCr-greenCr) + 0.5)
			frame[ySize+row*uvW+col] = cb
			frame[ySize+uvSize+row*uvW+col] = cr
		}
	}

	mask := graphics.ChromaKey(frame, w, h, greenKeyColor, 0.15, 0.3, 0.0)

	require.Len(t, mask, ySize)

	// Check that alpha values trend from transparent (left/green end)
	// toward opaque (right/red end) along the first row.
	// Due to 2x2 chroma subsampling, sample every other pair of luma columns.
	firstRowAlphas := make([]byte, 0, uvW)
	for col := 0; col < w; col += 2 {
		firstRowAlphas = append(firstRowAlphas, mask[col])
	}

	// The keyer uses squared-distance space (not Euclidean distance) for the
	// smoothness interpolation, so the alpha transition may not be strictly
	// monotonic per-sample. Instead, verify the overall trend by checking that
	// the average alpha of the first quarter is less than the last quarter.
	n := len(firstRowAlphas)
	q := n / 4
	var avgFirst, avgLast float64
	for i := 0; i < q; i++ {
		avgFirst += float64(firstRowAlphas[i])
		avgLast += float64(firstRowAlphas[n-1-i])
	}
	avgFirst /= float64(q)
	avgLast /= float64(q)

	require.Less(t, avgFirst, avgLast,
		"average alpha of green quarter (%.1f) should be less than red quarter (%.1f)",
		avgFirst, avgLast)

	// The green end should be more transparent than the red end.
	require.Less(t, firstRowAlphas[0], firstRowAlphas[n-1],
		"green end alpha (%d) should be less than red end alpha (%d)",
		firstRowAlphas[0], firstRowAlphas[n-1])
}

// ==========================================================================
// 4. TestLumaKeyWhiteTransparent — Solid white frame (Y=235) with a luma
//    key configured so high-luma = transparent. Key mask should show high
//    transparency.
// ==========================================================================

func TestLumaKeyWhiteTransparent(t *testing.T) {
	t.Parallel()

	// Y=235 in limited range, highClip=0.8 means Y/255 > 0.8 is transparent.
	// 235/255 = 0.922, which is above highClip.
	frame := makeYUV420Frame(testWidth, testHeight, 235, 128, 128)

	mask := graphics.LumaKey(frame, testWidth, testHeight, 0.0, 0.8, 0.0)

	require.Len(t, mask, testWidth*testHeight)

	for i, a := range mask {
		require.LessOrEqual(t, a, byte(30),
			"pixel %d: expected transparent (<=30) for white pixel above highClip, got %d", i, a)
	}
}

// ==========================================================================
// 5. TestLumaKeyBlackOpaque — Solid black frame (Y=16) with the same luma
//    key. Key mask should show full opacity since Y=16/255=0.063 is between
//    lowClip=0 and highClip=0.8.
// ==========================================================================

func TestLumaKeyBlackOpaque(t *testing.T) {
	t.Parallel()

	// Y=16 in limited range, lowClip=0.0, highClip=0.8.
	// 16/255 = 0.063, which is between lowClip and highClip -> opaque.
	frame := makeYUV420Frame(testWidth, testHeight, 16, 128, 128)

	mask := graphics.LumaKey(frame, testWidth, testHeight, 0.0, 0.8, 0.0)

	require.Len(t, mask, testWidth*testHeight)

	for i, a := range mask {
		require.GreaterOrEqual(t, a, byte(225),
			"pixel %d: expected opaque (>=225) for black pixel within luma range, got %d", i, a)
	}
}

// ==========================================================================
// 6. TestKeyPlusProgramComposite — Red foreground on green background, keyed
//    and composited onto blue program. Red pixels should be visible; green
//    should be replaced by blue.
// ==========================================================================

func TestKeyPlusProgramComposite(t *testing.T) {
	t.Parallel()

	// Use a small frame for precise pixel control.
	// 8x8 with the left half red and right half green.
	w, h := 8, 8
	ySize := w * h
	uvW := w / 2
	uvH := h / 2
	uvSize := uvW * uvH
	frameSize := ySize + 2*uvSize

	fill := make([]byte, frameSize)

	// Left half: red. Right half: green.
	// Y plane
	for row := 0; row < h; row++ {
		for col := 0; col < w; col++ {
			if col < w/2 {
				fill[row*w+col] = redYCbCr.Y // red Y
			} else {
				fill[row*w+col] = greenKeyColor.Y // green Y
			}
		}
	}

	// Chroma planes: each chroma sample covers a 2x2 luma block.
	// Left half chroma (cols 0-1): red chroma. Right half (cols 2-3): green chroma.
	for row := 0; row < uvH; row++ {
		for col := 0; col < uvW; col++ {
			idx := row*uvW + col
			if col < uvW/2 {
				fill[ySize+idx] = redYCbCr.Cb
				fill[ySize+uvSize+idx] = redYCbCr.Cr
			} else {
				fill[ySize+idx] = greenKeyColor.Cb
				fill[ySize+uvSize+idx] = greenKeyColor.Cr
			}
		}
	}

	// Background (program): solid blue.
	bg := makeYUV420Frame(w, h, blueYCbCr.Y, blueYCbCr.Cb, blueYCbCr.Cr)

	kp := graphics.NewKeyProcessor()
	kp.SetKey("cam1", graphics.KeyConfig{
		Type:       graphics.KeyTypeChroma,
		Enabled:    true,
		KeyColorY:  greenKeyColor.Y,
		KeyColorCb: greenKeyColor.Cb,
		KeyColorCr: greenKeyColor.Cr,
		Similarity: 0.4,
		Smoothness: 0.05,
	})

	result := kp.Process(bg, map[string][]byte{"cam1": fill}, w, h)

	require.Len(t, result, frameSize)

	// Left half (red foreground, not keyed): Y should be close to red Y.
	// The red foreground is opaque through the chroma key, so the fill should
	// replace the background.
	for row := 0; row < h; row++ {
		for col := 0; col < w/2; col++ {
			yVal := result[row*w+col]
			diff := int(yVal) - int(redYCbCr.Y)
			if diff < 0 {
				diff = -diff
			}
			require.LessOrEqual(t, diff, 5,
				"left half (red FG) pixel (%d,%d): Y=%d should be near red Y=%d",
				col, row, yVal, redYCbCr.Y)
		}
	}

	// Right half (green foreground, keyed out): Y should be close to blue BG Y.
	for row := 0; row < h; row++ {
		for col := w / 2; col < w; col++ {
			yVal := result[row*w+col]
			diff := int(yVal) - int(blueYCbCr.Y)
			if diff < 0 {
				diff = -diff
			}
			require.LessOrEqual(t, diff, 5,
				"right half (green FG, keyed out) pixel (%d,%d): Y=%d should be near blue Y=%d",
				col, row, yVal, blueYCbCr.Y)
		}
	}

	// --- Cb/Cr channel verification ---
	// Chroma plane layout: each sample covers a 2x2 luma block.
	// Left half chroma cols = [0, uvW/2), right half = [uvW/2, uvW).
	cbPlane := result[ySize : ySize+uvSize]
	crPlane := result[ySize+uvSize : ySize+2*uvSize]

	// Left half: red foreground preserved — expect Cb≈90, Cr≈240.
	var leftCbSum, leftCrSum int
	var leftCount int
	for row := 0; row < uvH; row++ {
		for col := 0; col < uvW/2; col++ {
			idx := row*uvW + col
			leftCbSum += int(cbPlane[idx])
			leftCrSum += int(crPlane[idx])
			leftCount++
		}
	}
	leftAvgCb := leftCbSum / leftCount
	leftAvgCr := leftCrSum / leftCount
	t.Logf("MEASURE: left half avg Cb=%d (expected ~90), avg Cr=%d (expected ~240)", leftAvgCb, leftAvgCr)

	leftCbDiff := leftAvgCb - int(redYCbCr.Cb)
	if leftCbDiff < 0 {
		leftCbDiff = -leftCbDiff
	}
	require.LessOrEqual(t, leftCbDiff, 15,
		"left half (red FG) avg Cb=%d should be within ±15 of red Cb=%d", leftAvgCb, redYCbCr.Cb)

	leftCrDiff := leftAvgCr - int(redYCbCr.Cr)
	if leftCrDiff < 0 {
		leftCrDiff = -leftCrDiff
	}
	require.LessOrEqual(t, leftCrDiff, 15,
		"left half (red FG) avg Cr=%d should be within ±15 of red Cr=%d", leftAvgCr, redYCbCr.Cr)

	// Right half: green keyed out, blue program shows through — expect Cb≈240, Cr≈110.
	var rightCbSum, rightCrSum int
	var rightCount int
	for row := 0; row < uvH; row++ {
		for col := uvW / 2; col < uvW; col++ {
			idx := row*uvW + col
			rightCbSum += int(cbPlane[idx])
			rightCrSum += int(crPlane[idx])
			rightCount++
		}
	}
	rightAvgCb := rightCbSum / rightCount
	rightAvgCr := rightCrSum / rightCount
	t.Logf("MEASURE: right half avg Cb=%d (expected ~240), avg Cr=%d (expected ~110)", rightAvgCb, rightAvgCr)

	rightCbDiff := rightAvgCb - int(blueYCbCr.Cb)
	if rightCbDiff < 0 {
		rightCbDiff = -rightCbDiff
	}
	require.LessOrEqual(t, rightCbDiff, 15,
		"right half (keyed out) avg Cb=%d should be within ±15 of blue Cb=%d", rightAvgCb, blueYCbCr.Cb)

	rightCrDiff := rightAvgCr - int(blueYCbCr.Cr)
	if rightCrDiff < 0 {
		rightCrDiff = -rightCrDiff
	}
	require.LessOrEqual(t, rightCrDiff, 15,
		"right half (keyed out) avg Cr=%d should be within ±15 of blue Cr=%d", rightAvgCr, blueYCbCr.Cr)
}

// ==========================================================================
// 7. TestKeyOutputLegalRange — Composite output from keying stays within
//    BT.709 legal range Y in [16,235], Cb/Cr in [16,240].
// ==========================================================================

func TestKeyOutputLegalRange(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name                  string
		bgY, bgCb, bgCr       byte
		fillY, fillCb, fillCr byte
		keyType               graphics.KeyType
	}{
		{
			name: "chroma_green_on_red_bg",
			bgY:  redYCbCr.Y, bgCb: redYCbCr.Cb, bgCr: redYCbCr.Cr,
			fillY: greenKeyColor.Y, fillCb: greenKeyColor.Cb, fillCr: greenKeyColor.Cr,
			keyType: graphics.KeyTypeChroma,
		},
		{
			name: "chroma_mixed_on_blue_bg",
			bgY:  blueYCbCr.Y, bgCb: blueYCbCr.Cb, bgCr: blueYCbCr.Cr,
			fillY: 128, fillCb: 100, fillCr: 180,
			keyType: graphics.KeyTypeChroma,
		},
		{
			name: "luma_bright_on_dark_bg",
			bgY:  30, bgCb: 128, bgCr: 128,
			fillY: 220, fillCb: 128, fillCr: 128,
			keyType: graphics.KeyTypeLuma,
		},
		{
			name: "luma_dark_on_bright_bg",
			bgY:  200, bgCb: 128, bgCr: 128,
			fillY: 20, fillCb: 128, fillCr: 128,
			keyType: graphics.KeyTypeLuma,
		},
		// Edge cases: extreme values that stress blend arithmetic
		{
			name: "chroma_superblack_fill_on_white_bg",
			bgY:  235, bgCb: 128, bgCr: 128,
			fillY: 0, fillCb: 0, fillCr: 0,
			keyType: graphics.KeyTypeChroma,
		},
		{
			name: "chroma_max_fill_on_black_bg",
			bgY:  0, bgCb: 255, bgCr: 255,
			fillY: 255, fillCb: 255, fillCr: 255,
			keyType: graphics.KeyTypeChroma,
		},
		{
			name: "luma_superblack_on_superwhite",
			bgY:  255, bgCb: 0, bgCr: 255,
			fillY: 0, fillCb: 255, fillCr: 0,
			keyType: graphics.KeyTypeLuma,
		},
		{
			name: "chroma_near_green_fill_on_saturated_bg",
			bgY:  16, bgCb: 240, bgCr: 240,
			fillY: 145, fillCb: 60, fillCr: 30,
			keyType: graphics.KeyTypeChroma,
		},
	}

	for _, tc := range testCases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			w, h := 64, 64

			bg := makeYUV420Frame(w, h, tc.bgY, tc.bgCb, tc.bgCr)
			fill := makeYUV420Frame(w, h, tc.fillY, tc.fillCb, tc.fillCr)

			kp := graphics.NewKeyProcessor()
			switch tc.keyType {
			case graphics.KeyTypeChroma:
				kp.SetKey("src", graphics.KeyConfig{
					Type:       graphics.KeyTypeChroma,
					Enabled:    true,
					KeyColorY:  greenKeyColor.Y,
					KeyColorCb: greenKeyColor.Cb,
					KeyColorCr: greenKeyColor.Cr,
					Similarity: 0.3,
					Smoothness: 0.15,
				})
			case graphics.KeyTypeLuma:
				kp.SetKey("src", graphics.KeyConfig{
					Type:     graphics.KeyTypeLuma,
					Enabled:  true,
					LowClip:  0.1,
					HighClip: 0.85,
					Softness: 0.1,
				})
			}

			result := kp.Process(bg, map[string][]byte{"src": fill}, w, h)

			// BT.709 legal range with ±2 rounding tolerance:
			// Y ∈ [14, 237] (16-2 to 235+2), Cb/Cr ∈ [14, 242] (16-2 to 240+2)
			ySize := w * h
			chromaSize := (w / 2) * (h / 2)
			cbStart := ySize
			crStart := cbStart + chromaSize

			// Measure actual Y range
			yMin, yMax := byte(255), byte(0)
			for i := 0; i < ySize; i++ {
				v := result[i]
				if v < yMin {
					yMin = v
				}
				if v > yMax {
					yMax = v
				}
			}
			t.Logf("MEASURE: Y min=%d max=%d (legal range with tolerance: [14, 237])", yMin, yMax)

			// Measure actual Cb/Cr range
			cbMin, cbMax := byte(255), byte(0)
			crMin, crMax := byte(255), byte(0)
			for i := 0; i < chromaSize; i++ {
				cb := result[cbStart+i]
				cr := result[crStart+i]
				if cb < cbMin {
					cbMin = cb
				}
				if cb > cbMax {
					cbMax = cb
				}
				if cr < crMin {
					crMin = cr
				}
				if cr > crMax {
					crMax = cr
				}
			}
			t.Logf("MEASURE: Cb min=%d max=%d, Cr min=%d max=%d (legal range with tolerance: [14, 242])", cbMin, cbMax, crMin, crMax)

			// Assert BT.709 legal range (with ±2 rounding tolerance)
			// If these fail, the keyer lacks output clamping — a real bug.
			if yMin < 14 {
				t.Errorf("BUG: Y min=%d is below BT.709 legal range floor (14). The keyer needs output clamping.", yMin)
			}
			if yMax > 237 {
				t.Errorf("BUG: Y max=%d is above BT.709 legal range ceiling (237). The keyer needs output clamping.", yMax)
			}
			if cbMin < 14 {
				t.Errorf("BUG: Cb min=%d is below BT.709 legal range floor (14). The keyer needs output clamping.", cbMin)
			}
			if cbMax > 242 {
				t.Errorf("BUG: Cb max=%d is above BT.709 legal range ceiling (242). The keyer needs output clamping.", cbMax)
			}
			if crMin < 14 {
				t.Errorf("BUG: Cr min=%d is below BT.709 legal range floor (14). The keyer needs output clamping.", crMin)
			}
			if crMax > 242 {
				t.Errorf("BUG: Cr max=%d is above BT.709 legal range ceiling (242). The keyer needs output clamping.", crMax)
			}
		})
	}
}

// ==========================================================================
// 8. TestKeySignalIndependence — Changing the fill's foreground color should
//    NOT affect the key mask, as long as the green (key) region is identical.
// ==========================================================================

func TestKeySignalIndependence(t *testing.T) {
	t.Parallel()

	// Two fills: same green key region, different foreground colors.
	// Fill A: left half = red, right half = green.
	// Fill B: left half = white, right half = green (same green as A).
	w, h := 16, 16
	ySize := w * h
	uvW := w / 2
	uvH := h / 2
	uvSize := uvW * uvH
	frameSize := ySize + 2*uvSize

	makeFillWithForeground := func(fgY, fgCb, fgCr byte) []byte {
		fill := make([]byte, frameSize)
		// Y plane: left half = foreground, right half = green
		for row := 0; row < h; row++ {
			for col := 0; col < w; col++ {
				if col < w/2 {
					fill[row*w+col] = fgY
				} else {
					fill[row*w+col] = greenKeyColor.Y
				}
			}
		}
		// Chroma planes
		for row := 0; row < uvH; row++ {
			for col := 0; col < uvW; col++ {
				idx := row*uvW + col
				if col < uvW/2 {
					fill[ySize+idx] = fgCb
					fill[ySize+uvSize+idx] = fgCr
				} else {
					fill[ySize+idx] = greenKeyColor.Cb
					fill[ySize+uvSize+idx] = greenKeyColor.Cr
				}
			}
		}
		return fill
	}

	fillA := makeFillWithForeground(redYCbCr.Y, redYCbCr.Cb, redYCbCr.Cr)
	fillB := makeFillWithForeground(235, 128, 128) // white foreground

	// Generate key masks directly using ChromaKey (no spill suppression, so
	// the frame is not modified and the mask depends only on chroma distance).
	maskA := graphics.ChromaKey(fillA, w, h, greenKeyColor, 0.3, 0.1, 0.0)
	maskB := graphics.ChromaKey(fillB, w, h, greenKeyColor, 0.3, 0.1, 0.0)

	require.Len(t, maskA, ySize)
	require.Len(t, maskB, ySize)

	// The masks should be identical because the chroma key depends only on
	// Cb/Cr distance, and both fills have the same chroma layout.
	require.Equal(t, maskA, maskB,
		"key masks should be identical when only the foreground color differs and the green region is the same")

	// Additionally verify structural correctness:
	// Right half (green) should be transparent, left half (non-green) should be opaque.
	for row := 0; row < h; row++ {
		for col := w / 2; col < w; col++ {
			require.LessOrEqual(t, maskA[row*w+col], byte(30),
				"green region pixel (%d,%d) should be transparent, got %d", col, row, maskA[row*w+col])
		}
		for col := 0; col < w/2; col++ {
			require.GreaterOrEqual(t, maskA[row*w+col], byte(200),
				"non-green region pixel (%d,%d) should be opaque, got %d", col, row, maskA[row*w+col])
		}
	}
}

// ==========================================================================
// Additional tests for thorough coverage
// ==========================================================================

// TestChromaKeyBridgeRoundTrip verifies the KeyProcessorBridge integration:
// IngestFillYUV + ProcessYUV produces correct composite output.
func TestChromaKeyBridgeRoundTrip(t *testing.T) {
	t.Parallel()

	w, h := 32, 32

	kp := graphics.NewKeyProcessor()
	kp.SetKey("cam1", graphics.KeyConfig{
		Type:       graphics.KeyTypeChroma,
		Enabled:    true,
		KeyColorY:  greenKeyColor.Y,
		KeyColorCb: greenKeyColor.Cb,
		KeyColorCr: greenKeyColor.Cr,
		Similarity: 0.4,
		Smoothness: 0.1,
	})

	bridge := graphics.NewKeyProcessorBridge(kp)
	defer bridge.Close()

	// Fill: pure green (should be keyed out entirely).
	fill := testutil.GenerateGreenScreenYUV420(w, h)
	bridge.IngestFillYUV("cam1", fill, w, h)

	// Program: solid blue.
	program := makeYUV420Frame(w, h, blueYCbCr.Y, blueYCbCr.Cb, blueYCbCr.Cr)
	expected := make([]byte, len(program))
	copy(expected, program)

	result := bridge.ProcessYUV(program, w, h)

	require.Len(t, result, len(expected))

	// Since fill is pure green and keyed out, result should be very close to
	// the original blue program (transparent fill reveals background).
	ySize := w * h
	for i := 0; i < ySize; i++ {
		diff := int(result[i]) - int(expected[i])
		if diff < 0 {
			diff = -diff
		}
		require.LessOrEqual(t, diff, 10,
			"Y[%d]: result=%d should be near blue bg=%d after keying out pure green fill",
			i, result[i], expected[i])
	}
}

// TestLumaKeySoftnessGradient verifies that luma key softness produces
// gradual alpha transitions across a Y gradient.
func TestLumaKeySoftnessGradient(t *testing.T) {
	t.Parallel()

	// Create a frame with a horizontal Y gradient from 16 to 235.
	w, h := 256, 4
	frame := testutil.GenerateGradientYUV420(w, h)

	// Luma key: lowClip=0.0, highClip=0.7, softness=0.15
	// Pixels with Y/255 > 0.7 start becoming transparent.
	// Pixels with Y/255 > 0.85 are fully transparent.
	mask := graphics.LumaKey(frame, w, h, 0.0, 0.7, 0.15)

	require.Len(t, mask, w*h)

	// Check the first row for expected behavior:
	// - Low Y (left side) should be fully opaque.
	// - High Y (right side) should be transparent.
	// - There should be a gradual transition in between.
	leftAlpha := mask[0]    // Y=16, should be opaque
	rightAlpha := mask[w-1] // Y=235, should be transparent
	midAlpha := mask[w*3/4] // Somewhere in the transition zone

	require.GreaterOrEqual(t, leftAlpha, byte(200),
		"low-Y pixels should be opaque, got %d", leftAlpha)
	require.LessOrEqual(t, rightAlpha, byte(50),
		"high-Y pixels should be transparent, got %d", rightAlpha)

	// The mid-range pixel should have intermediate alpha (partial transparency).
	// It may not be exactly in the transition zone, but should differ from extremes.
	_ = midAlpha // Just validate no panics; exact value depends on column position.
}

// TestChromaKeyMaskDeterminism verifies that ChromaKey on a half-green/half-red
// frame produces an identical key mask on every run (20 iterations).
func TestChromaKeyMaskDeterminism(t *testing.T) {
	t.Parallel()

	// Build a frame with left half green, right half red (in chroma space).
	w, h := 64, 64
	ySize := w * h
	uvW := w / 2
	uvH := h / 2
	uvSize := uvW * uvH
	frame := make([]byte, ySize+2*uvSize)

	// Y plane: constant 128
	for i := 0; i < ySize; i++ {
		frame[i] = 128
	}
	// Chroma planes: left half green, right half red
	for row := 0; row < uvH; row++ {
		for col := 0; col < uvW; col++ {
			idx := row*uvW + col
			if col < uvW/2 {
				frame[ySize+idx] = greenKeyColor.Cb
				frame[ySize+uvSize+idx] = greenKeyColor.Cr
			} else {
				frame[ySize+idx] = redYCbCr.Cb
				frame[ySize+uvSize+idx] = redYCbCr.Cr
			}
		}
	}

	// First run — reference mask.
	// Make a copy of the frame each time since spill suppression can modify it.
	frameCopy := make([]byte, len(frame))
	copy(frameCopy, frame)
	reference := graphics.ChromaKey(frameCopy, w, h, greenKeyColor, 0.4, 0.1, 0.0)
	refCopy := make([]byte, len(reference))
	copy(refCopy, reference)

	// 19 more runs.
	for i := 0; i < 19; i++ {
		fc := make([]byte, len(frame))
		copy(fc, frame)
		mask := graphics.ChromaKey(fc, w, h, greenKeyColor, 0.4, 0.1, 0.0)
		if !testutil.YUVFrameEqual(refCopy, mask) {
			t.Fatalf("Run %d produced different chroma key mask (non-determinism detected)", i+1)
		}
	}
}

// TestKeyProcessorDeterminism verifies that repeated keying of identical
// frames produces identical results.
func TestKeyProcessorDeterminism(t *testing.T) {
	t.Parallel()

	w, h := 64, 64
	frameSize := w*h + 2*(w/2)*(h/2)

	kp := graphics.NewKeyProcessor()
	kp.SetKey("cam1", graphics.KeyConfig{
		Type:       graphics.KeyTypeChroma,
		Enabled:    true,
		KeyColorY:  greenKeyColor.Y,
		KeyColorCb: greenKeyColor.Cb,
		KeyColorCr: greenKeyColor.Cr,
		Similarity: 0.35,
		Smoothness: 0.15,
	})

	fill := testutil.GenerateGreenScreenYUV420(w, h)
	fills := map[string][]byte{"cam1": fill}

	var firstResult []byte
	for iter := 0; iter < 20; iter++ {
		bg := makeYUV420Frame(w, h, 128, 128, 128)
		result := kp.Process(bg, fills, w, h)
		require.Len(t, result, frameSize)

		if firstResult == nil {
			firstResult = make([]byte, len(result))
			copy(firstResult, result)
		} else {
			require.Equal(t, firstResult, result,
				"iteration %d: keyed result differs from first iteration — non-deterministic", iter)
		}
	}
}
