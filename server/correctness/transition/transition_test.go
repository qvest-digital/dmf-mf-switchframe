//go:build correctness

package transition

import (
	"math"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/zsiec/switchframe/server/correctness/testutil"
	"github.com/zsiec/switchframe/server/transition"
)

const (
	testWidth  = 320
	testHeight = 240
)

// yuvFrameSize returns the total byte count for a YUV420p frame.
func yuvFrameSize(w, h int) int {
	return w*h + 2*(w/2)*(h/2)
}

// copyFrame returns a deep copy of a YUV frame slice.
func copyFrame(src []byte) []byte {
	dst := make([]byte, len(src))
	copy(dst, src)
	return dst
}

// ==========================================================================
// 1. TestMixAt0IsSourceA -- position 0.0 yields byte-exact source A
// ==========================================================================

func TestMixAt0IsSourceA(t *testing.T) {
	t.Parallel()

	fb, err := transition.NewFrameBlender(testWidth, testHeight)
	require.NoError(t, err)

	srcA := testutil.GenerateSolidYUV420(testWidth, testHeight, 180, 90, 210)
	srcB := testutil.GenerateSolidYUV420(testWidth, testHeight, 40, 200, 60)

	out := fb.BlendMix(srcA, srcB, 0.0)

	// Must be byte-exact copy of source A.
	require.True(t, testutil.YUVFrameEqual(srcA, out),
		"BlendMix at position 0.0 must return byte-exact source A")
}

// ==========================================================================
// 2. TestMixAt1IsSourceB -- position 1.0 yields byte-exact source B
// ==========================================================================

func TestMixAt1IsSourceB(t *testing.T) {
	t.Parallel()

	fb, err := transition.NewFrameBlender(testWidth, testHeight)
	require.NoError(t, err)

	srcA := testutil.GenerateSolidYUV420(testWidth, testHeight, 180, 90, 210)
	srcB := testutil.GenerateSolidYUV420(testWidth, testHeight, 40, 200, 60)

	out := fb.BlendMix(srcA, srcB, 1.0)

	// Must be byte-exact copy of source B.
	require.True(t, testutil.YUVFrameEqual(srcB, out),
		"BlendMix at position 1.0 must return byte-exact source B")
}

// ==========================================================================
// 3. TestMixAt05CorrectBlend -- midpoint linear interpolation on all planes
// ==========================================================================

func TestMixAt05CorrectBlend(t *testing.T) {
	t.Parallel()

	fb, err := transition.NewFrameBlender(testWidth, testHeight)
	require.NoError(t, err)

	// Source A: Y=100, Cb=100, Cr=100
	srcA := testutil.GenerateSolidYUV420(testWidth, testHeight, 100, 100, 100)
	// Source B: Y=200, Cb=150, Cr=150
	srcB := testutil.GenerateSolidYUV420(testWidth, testHeight, 200, 150, 150)

	out := fb.BlendMix(srcA, srcB, 0.5)

	// Check every Y pixel: expected = (100+200)/2 = 150
	yPlaneSize := testWidth * testHeight
	for i := 0; i < yPlaneSize; i++ {
		require.InDelta(t, 150, int(out[i]), 1, "Y plane pixel %d", i)
	}

	// Check every Cb pixel: expected = (100+150)/2 = 125
	chromaSize := (testWidth / 2) * (testHeight / 2)
	cbOffset := yPlaneSize
	for i := 0; i < chromaSize; i++ {
		require.InDelta(t, 125, int(out[cbOffset+i]), 1, "Cb plane pixel %d", i)
	}

	// Check every Cr pixel: expected = (100+150)/2 = 125
	crOffset := cbOffset + chromaSize
	for i := 0; i < chromaSize; i++ {
		require.InDelta(t, 125, int(out[crOffset+i]), 1, "Cr plane pixel %d", i)
	}
}

// ==========================================================================
// 4. TestChromaPlanesBlended -- verify Cb/Cr interpolation for colored sources
// ==========================================================================

func TestChromaPlanesBlended(t *testing.T) {
	t.Parallel()

	fb, err := transition.NewFrameBlender(testWidth, testHeight)
	require.NoError(t, err)

	// Source A: reddish (Y=81, Cb=90, Cr=240)
	srcA := testutil.GenerateSolidYUV420(testWidth, testHeight, 81, 90, 240)
	// Source B: bluish (Y=41, Cb=240, Cr=110)
	srcB := testutil.GenerateSolidYUV420(testWidth, testHeight, 41, 240, 110)

	out := fb.BlendMix(srcA, srcB, 0.5)

	yPlaneSize := testWidth * testHeight
	chromaSize := (testWidth / 2) * (testHeight / 2)
	cbOffset := yPlaneSize
	crOffset := cbOffset + chromaSize

	// Expected Y = (81+41)/2 = 61
	// Expected Cb = (90+240)/2 = 165
	// Expected Cr = (240+110)/2 = 175
	for i := 0; i < yPlaneSize; i++ {
		require.InDelta(t, 61, int(out[i]), 1, "Y plane pixel %d", i)
	}
	for i := 0; i < chromaSize; i++ {
		require.InDelta(t, 165, int(out[cbOffset+i]), 1,
			"Cb plane pixel %d: must be interpolated, not copied from either source", i)
	}
	for i := 0; i < chromaSize; i++ {
		require.InDelta(t, 175, int(out[crOffset+i]), 1,
			"Cr plane pixel %d: must be interpolated, not copied from either source", i)
	}

	// Additional: confirm chroma is NOT just source A or source B values
	for i := 0; i < chromaSize; i++ {
		require.NotEqual(t, byte(90), out[cbOffset+i],
			"Cb should not be source A value at pixel %d", i)
		require.NotEqual(t, byte(240), out[cbOffset+i],
			"Cb should not be source B value at pixel %d", i)
		require.NotEqual(t, byte(240), out[crOffset+i],
			"Cr should not be source A value at pixel %d", i)
		require.NotEqual(t, byte(110), out[crOffset+i],
			"Cr should not be source B value at pixel %d", i)
	}
}

// ==========================================================================
// 5. TestWipeBoundaryPosition -- left-to-right wipe at 50%
// ==========================================================================

func TestWipeBoundaryPosition(t *testing.T) {
	t.Parallel()

	fb, err := transition.NewFrameBlender(testWidth, testHeight)
	require.NoError(t, err)

	srcA := testutil.GenerateSolidYUV420(testWidth, testHeight, 200, 90, 210)
	srcB := testutil.GenerateSolidYUV420(testWidth, testHeight, 50, 200, 60)

	// WipeHLeft: B reveals from the left. At position 0.5, left half = B, right half = A.
	out := fb.BlendWipe(srcA, srcB, 0.5, transition.WipeHLeft)

	// The soft edge is 2/width pixels wide in normalized coords.
	// For 320px: softEdge = 2/320 = 0.00625
	// Boundary is at x where threshold = x/(w-1) ~ 0.5
	// Pixels well to the left of center must be B, well to the right must be A.
	// Use a margin of 10 pixels from center to avoid soft edge.
	centerX := testWidth / 2

	// Left quarter (well into B territory)
	for y := 0; y < testHeight; y++ {
		for x := 0; x < centerX-10; x++ {
			yVal, _, _ := testutil.YUVPixelAt(out, testWidth, testHeight, x, y)
			require.Equal(t, byte(50), yVal,
				"Left side pixel (%d,%d) should be source B (Y=50)", x, y)
		}
	}

	// Right quarter (well into A territory)
	for y := 0; y < testHeight; y++ {
		for x := centerX + 10; x < testWidth; x++ {
			yVal, _, _ := testutil.YUVPixelAt(out, testWidth, testHeight, x, y)
			require.Equal(t, byte(200), yVal,
				"Right side pixel (%d,%d) should be source A (Y=200)", x, y)
		}
	}

	// Boundary region: pixels near center should have intermediate values
	// (demonstrating the soft edge exists)
	boundaryHasBlend := false
	for x := centerX - 5; x < centerX+5; x++ {
		yVal, _, _ := testutil.YUVPixelAt(out, testWidth, testHeight, x, 0)
		if yVal > 50 && yVal < 200 {
			boundaryHasBlend = true
			break
		}
	}
	require.True(t, boundaryHasBlend,
		"Wipe boundary region should contain blended (soft-edge) pixels")
}

// ==========================================================================
// 6. TestDipToBlackMidpoint -- dip at 0.5 = broadcast black
// ==========================================================================

func TestDipToBlackMidpoint(t *testing.T) {
	t.Parallel()

	fb, err := transition.NewFrameBlender(testWidth, testHeight)
	require.NoError(t, err)
	// Default is limited range (blackY=16), which is broadcast standard.

	srcA := testutil.GenerateSolidYUV420(testWidth, testHeight, 200, 100, 200)
	srcB := testutil.GenerateSolidYUV420(testWidth, testHeight, 100, 200, 100)

	out := fb.BlendDip(srcA, srcB, 0.5)

	// At midpoint of limited-range dip: Y=16, Cb=128, Cr=128 (broadcast black)
	yPlaneSize := testWidth * testHeight
	chromaSize := (testWidth / 2) * (testHeight / 2)
	cbOffset := yPlaneSize
	crOffset := cbOffset + chromaSize

	for i := 0; i < yPlaneSize; i++ {
		require.InDelta(t, 16, int(out[i]), 1,
			"Y plane pixel %d should be broadcast black (16)", i)
	}
	for i := 0; i < chromaSize; i++ {
		require.InDelta(t, 128, int(out[cbOffset+i]), 1,
			"Cb plane pixel %d should be neutral (128)", i)
	}
	for i := 0; i < chromaSize; i++ {
		require.InDelta(t, 128, int(out[crOffset+i]), 1,
			"Cr plane pixel %d should be neutral (128)", i)
	}
}

// ==========================================================================
// 7. TestFTBComplete -- fade-to-black at 1.0 = broadcast black
// ==========================================================================

func TestFTBComplete(t *testing.T) {
	t.Parallel()

	fb, err := transition.NewFrameBlender(testWidth, testHeight)
	require.NoError(t, err)
	// Default limited range.

	// Use a bright, chromatic source to make the test meaningful.
	srcA := testutil.GenerateSolidYUV420(testWidth, testHeight, 235, 200, 40)

	out := fb.BlendFTB(srcA, 1.0)

	yPlaneSize := testWidth * testHeight
	chromaSize := (testWidth / 2) * (testHeight / 2)
	cbOffset := yPlaneSize
	crOffset := cbOffset + chromaSize

	for i := 0; i < yPlaneSize; i++ {
		require.Equal(t, byte(16), out[i],
			"Y plane pixel %d should be broadcast black (16) at FTB position 1.0", i)
	}
	for i := 0; i < chromaSize; i++ {
		require.Equal(t, byte(128), out[cbOffset+i],
			"Cb plane pixel %d should be neutral (128)", i)
	}
	for i := 0; i < chromaSize; i++ {
		require.Equal(t, byte(128), out[crOffset+i],
			"Cr plane pixel %d should be neutral (128)", i)
	}
}

// ==========================================================================
// 8. TestEasingEndpoints -- Ease(0)==0, Ease(1)==1 for all types
// ==========================================================================

func TestEasingEndpoints(t *testing.T) {
	t.Parallel()

	presets := []transition.EasingType{
		transition.EasingLinear,
		transition.EasingEase,
		transition.EasingEaseIn,
		transition.EasingEaseOut,
		transition.EasingEaseInOut,
		transition.EasingSmoothstep,
	}

	for _, et := range presets {
		et := et
		t.Run(string(et), func(t *testing.T) {
			t.Parallel()
			c := transition.NewEasingCurve(et)
			require.NotNil(t, c)

			// Exact float equality at endpoints.
			require.Equal(t, 0.0, c.Ease(0.0),
				"%s: Ease(0.0) must be exactly 0.0", et)
			require.Equal(t, 1.0, c.Ease(1.0),
				"%s: Ease(1.0) must be exactly 1.0", et)
		})
	}

	// Also test a custom cubic-bezier.
	t.Run("custom", func(t *testing.T) {
		t.Parallel()
		c, err := transition.NewCustomEasingCurve(0.42, 0.0, 0.58, 1.0)
		require.NoError(t, err)
		require.Equal(t, 0.0, c.Ease(0.0))
		require.Equal(t, 1.0, c.Ease(1.0))
	})
}

// ==========================================================================
// 9. TestEasingMonotonicity -- all standard curves are non-decreasing
// ==========================================================================

func TestEasingMonotonicity(t *testing.T) {
	t.Parallel()

	presets := []transition.EasingType{
		transition.EasingLinear,
		transition.EasingEase,
		transition.EasingEaseIn,
		transition.EasingEaseOut,
		transition.EasingEaseInOut,
		transition.EasingSmoothstep,
	}

	const numSamples = 1000

	for _, et := range presets {
		et := et
		t.Run(string(et), func(t *testing.T) {
			t.Parallel()
			c := transition.NewEasingCurve(et)

			prev := c.Ease(0.0)
			for i := 1; i <= numSamples; i++ {
				tt := float64(i) / float64(numSamples)
				cur := c.Ease(tt)
				require.GreaterOrEqual(t, cur, prev-1e-9,
					"%s: not monotonic at t=%f (prev=%f, cur=%f)", et, tt, prev, cur)
				prev = cur
			}
		})
	}
}

// ==========================================================================
// 10. TestOutputInLegalRange -- blend results stay within BT.709 legal range
// ==========================================================================

func TestOutputInLegalRange(t *testing.T) {
	t.Parallel()

	fb, err := transition.NewFrameBlender(testWidth, testHeight)
	require.NoError(t, err)

	// Broadcast white + white
	white := testutil.GenerateSolidYUV420(testWidth, testHeight, 235, 128, 128)
	// Broadcast black + black
	black := testutil.GenerateSolidYUV420(testWidth, testHeight, 16, 128, 128)

	positions := []float64{0.0, 0.1, 0.25, 0.5, 0.75, 0.9, 1.0}

	t.Run("white+white_mix", func(t *testing.T) {
		t.Parallel()
		fbLocal, err := transition.NewFrameBlender(testWidth, testHeight)
		require.NoError(t, err)
		for _, pos := range positions {
			out := fbLocal.BlendMix(white, white, pos)
			result := copyFrame(out)
			testutil.AssertYUVLegalRange(t, result, testWidth, testHeight)
		}
	})

	t.Run("black+black_mix", func(t *testing.T) {
		t.Parallel()
		fbLocal, err := transition.NewFrameBlender(testWidth, testHeight)
		require.NoError(t, err)
		for _, pos := range positions {
			out := fbLocal.BlendMix(black, black, pos)
			result := copyFrame(out)
			testutil.AssertYUVLegalRange(t, result, testWidth, testHeight)
		}
	})

	t.Run("black+white_mix", func(t *testing.T) {
		t.Parallel()
		fbLocal, err := transition.NewFrameBlender(testWidth, testHeight)
		require.NoError(t, err)
		for _, pos := range positions {
			out := fbLocal.BlendMix(black, white, pos)
			result := copyFrame(out)
			testutil.AssertYUVLegalRange(t, result, testWidth, testHeight)
		}
	})

	t.Run("FTB_white", func(t *testing.T) {
		t.Parallel()
		fbLocal, err := transition.NewFrameBlender(testWidth, testHeight)
		require.NoError(t, err)
		for _, pos := range positions {
			out := fbLocal.BlendFTB(white, pos)
			result := copyFrame(out)
			testutil.AssertYUVLegalRange(t, result, testWidth, testHeight)
		}
	})

	t.Run("FTB_black", func(t *testing.T) {
		for _, pos := range positions {
			out := fb.BlendFTB(black, pos)
			result := copyFrame(out)
			testutil.AssertYUVLegalRange(t, result, testWidth, testHeight)
		}
	})

	t.Run("dip_black_white", func(t *testing.T) {
		t.Parallel()
		fbLocal, err := transition.NewFrameBlender(testWidth, testHeight)
		require.NoError(t, err)
		for _, pos := range positions {
			out := fbLocal.BlendDip(black, white, pos)
			result := copyFrame(out)
			testutil.AssertYUVLegalRange(t, result, testWidth, testHeight)
		}
	})

	t.Run("wipe_black_white", func(t *testing.T) {
		t.Parallel()
		fbLocal, err := transition.NewFrameBlender(testWidth, testHeight)
		require.NoError(t, err)
		for _, pos := range positions {
			out := fbLocal.BlendWipe(black, white, pos, transition.WipeHLeft)
			result := copyFrame(out)
			testutil.AssertYUVLegalRange(t, result, testWidth, testHeight)
		}
	})
}

// ==========================================================================
// 11. TestBlendAtMultiplePositions -- linear interpolation sweep
// ==========================================================================

func TestBlendAtMultiplePositions(t *testing.T) {
	t.Parallel()

	fb, err := transition.NewFrameBlender(testWidth, testHeight)
	require.NoError(t, err)

	// Use values well within byte range to avoid clamping artifacts.
	srcA := testutil.GenerateSolidYUV420(testWidth, testHeight, 60, 100, 100)
	srcB := testutil.GenerateSolidYUV420(testWidth, testHeight, 200, 180, 180)

	positions := []float64{0.0, 0.25, 0.5, 0.75, 1.0}

	for _, pos := range positions {
		pos := pos
		t.Run(posName(pos), func(t *testing.T) {
			out := fb.BlendMix(srcA, srcB, pos)

			expectedY := 60.0*(1.0-pos) + 200.0*pos
			expectedCb := 100.0*(1.0-pos) + 180.0*pos
			expectedCr := 100.0*(1.0-pos) + 180.0*pos

			// Check a sampling of Y pixels (every 100th to keep it fast).
			yPlaneSize := testWidth * testHeight
			for i := 0; i < yPlaneSize; i += 100 {
				require.InDelta(t, expectedY, float64(out[i]), 1,
					"Y pixel %d at position %f", i, pos)
			}

			// Check chroma planes.
			chromaSize := (testWidth / 2) * (testHeight / 2)
			cbOffset := yPlaneSize
			crOffset := cbOffset + chromaSize
			for i := 0; i < chromaSize; i += 50 {
				require.InDelta(t, expectedCb, float64(out[cbOffset+i]), 1,
					"Cb pixel %d at position %f", i, pos)
				require.InDelta(t, expectedCr, float64(out[crOffset+i]), 1,
					"Cr pixel %d at position %f", i, pos)
			}
		})
	}
}

// ==========================================================================
// Additional: TestDipSymmetry -- dip is symmetric around midpoint
// ==========================================================================

func TestDipSymmetry(t *testing.T) {
	t.Parallel()

	fb, err := transition.NewFrameBlender(testWidth, testHeight)
	require.NoError(t, err)

	// Use identical sources so the dip is purely about fade-to-black-and-back.
	src := testutil.GenerateSolidYUV420(testWidth, testHeight, 200, 100, 200)

	// Phase 1 at 0.25 and Phase 2 at 0.75 should produce similar brightness
	// since both are halfway between their respective endpoints and black.
	out025 := copyFrame(fb.BlendDip(src, src, 0.25))
	out075 := copyFrame(fb.BlendDip(src, src, 0.75))

	// At pos=0.25: gain = 1.0 - 2*0.25 = 0.5; Y = 200*0.5 + 16*0.5 = 108
	// At pos=0.75: gain = 2*0.75 - 1.0 = 0.5; Y = 200*0.5 + 16*0.5 = 108
	for i := 0; i < testWidth*testHeight; i++ {
		require.InDelta(t, int(out025[i]), int(out075[i]), 1,
			"Dip should be symmetric: Y pixel %d at 0.25 vs 0.75", i)
	}
}

// ==========================================================================
// Additional: TestWipeAllDirections -- all 6 directions at 50%
// ==========================================================================

func TestWipeAllDirections(t *testing.T) {
	t.Parallel()

	directions := []transition.WipeDirection{
		transition.WipeHLeft,
		transition.WipeHRight,
		transition.WipeVTop,
		transition.WipeVBottom,
		transition.WipeBoxCenterOut,
		transition.WipeBoxEdgesIn,
	}

	srcA := testutil.GenerateSolidYUV420(testWidth, testHeight, 200, 90, 210)
	srcB := testutil.GenerateSolidYUV420(testWidth, testHeight, 50, 200, 60)

	for _, dir := range directions {
		dir := dir
		t.Run(string(dir), func(t *testing.T) {
			t.Parallel()
			fb, err := transition.NewFrameBlender(testWidth, testHeight)
			require.NoError(t, err)

			out := fb.BlendWipe(srcA, srcB, 0.5, dir)

			// Output must have correct size.
			require.Equal(t, yuvFrameSize(testWidth, testHeight), len(out))

			// At position 0.5, both source A and source B regions should be present.
			hasA, hasB := false, false
			yPlaneSize := testWidth * testHeight
			for i := 0; i < yPlaneSize; i++ {
				if out[i] == 200 {
					hasA = true
				}
				if out[i] == 50 {
					hasB = true
				}
			}
			require.True(t, hasA, "dir=%s should have source A pixels at pos=0.5", dir)
			require.True(t, hasB, "dir=%s should have source B pixels at pos=0.5", dir)
		})
	}
}

// ==========================================================================
// Additional: TestFTBAtZeroIsSource -- FTB at 0.0 preserves source
// ==========================================================================

func TestFTBAtZeroIsSource(t *testing.T) {
	t.Parallel()

	fb, err := transition.NewFrameBlender(testWidth, testHeight)
	require.NoError(t, err)

	src := testutil.GenerateSolidYUV420(testWidth, testHeight, 180, 100, 200)
	out := fb.BlendFTB(src, 0.0)

	require.True(t, testutil.YUVFrameEqual(src, out),
		"BlendFTB at position 0.0 must return byte-exact source")
}

// ==========================================================================
// Additional: TestBlendUniformBytesExported -- test the exported SIMD entry
// ==========================================================================

func TestBlendUniformBytesExported(t *testing.T) {
	t.Parallel()

	const size = 1024
	a := make([]byte, size)
	b := make([]byte, size)
	dst := make([]byte, size)

	for i := range a {
		a[i] = 100
		b[i] = 200
	}

	// pos=128 out of 256 is midpoint
	transition.BlendUniformBytes(dst, a, b, 128)

	for i := 0; i < size; i++ {
		require.InDelta(t, 150, int(dst[i]), 1,
			"BlendUniformBytes midpoint pixel %d", i)
	}

	// pos=0: all A
	transition.BlendUniformBytes(dst, a, b, 0)
	for i := 0; i < size; i++ {
		require.Equal(t, byte(100), dst[i], "pos=0 should be all A at pixel %d", i)
	}

	// pos=256: all B
	transition.BlendUniformBytes(dst, a, b, 256)
	for i := 0; i < size; i++ {
		require.Equal(t, byte(200), dst[i], "pos=256 should be all B at pixel %d", i)
	}
}

// ==========================================================================
// Additional: TestEasingRangeOutputBounds -- eased values used as blend
//   position stay in [0,1] for standard presets
// ==========================================================================

func TestEasingRangeOutputBounds(t *testing.T) {
	t.Parallel()

	presets := []transition.EasingType{
		transition.EasingLinear,
		transition.EasingEase,
		transition.EasingEaseIn,
		transition.EasingEaseOut,
		transition.EasingEaseInOut,
		transition.EasingSmoothstep,
	}

	for _, et := range presets {
		et := et
		t.Run(string(et), func(t *testing.T) {
			t.Parallel()
			c := transition.NewEasingCurve(et)
			for i := 0; i <= 10000; i++ {
				tt := float64(i) / 10000.0
				v := c.Ease(tt)
				require.GreaterOrEqual(t, v, 0.0-1e-9,
					"%s: Ease(%f) = %f is below 0", et, tt, v)
				require.LessOrEqual(t, v, 1.0+1e-9,
					"%s: Ease(%f) = %f exceeds 1", et, tt, v)
			}
		})
	}
}

// ==========================================================================
// Additional: TestMixGradientPSNR -- blending gradient frames maintains quality
// ==========================================================================

func TestMixGradientPSNR(t *testing.T) {
	t.Parallel()

	fb, err := transition.NewFrameBlender(testWidth, testHeight)
	require.NoError(t, err)

	gradA := testutil.GenerateGradientYUV420(testWidth, testHeight)
	gradB := testutil.GenerateGradientYUV420(testWidth, testHeight)

	// Blending identical gradients at any position should give back the same gradient.
	out := fb.BlendMix(gradA, gradB, 0.5)
	psnr := testutil.YUVFramePSNR(gradA, out, testWidth, testHeight)

	// Should be extremely high (identical frames, fixed-point rounding only).
	require.Greater(t, psnr, 40.0,
		"Blending identical gradients should produce high PSNR, got %f dB", psnr)
}

// ==========================================================================
// Additional: TestWipeHRightMirrorOfHLeft -- h-right is mirror of h-left
// ==========================================================================

func TestWipeHRightMirrorOfHLeft(t *testing.T) {
	t.Parallel()

	fbL, err := transition.NewFrameBlender(testWidth, testHeight)
	require.NoError(t, err)
	fbR, err := transition.NewFrameBlender(testWidth, testHeight)
	require.NoError(t, err)

	srcA := testutil.GenerateSolidYUV420(testWidth, testHeight, 200, 128, 128)
	srcB := testutil.GenerateSolidYUV420(testWidth, testHeight, 50, 128, 128)

	outL := copyFrame(fbL.BlendWipe(srcA, srcB, 0.5, transition.WipeHLeft))
	outR := copyFrame(fbR.BlendWipe(srcA, srcB, 0.5, transition.WipeHRight))

	// For h-left at pos 0.5: left side is B, right side is A.
	// For h-right at pos 0.5: right side is B, left side is A (mirrored).
	// Check that the Y plane of h-right is the horizontal mirror of h-left.
	for y := 0; y < testHeight; y++ {
		for x := 0; x < testWidth; x++ {
			mirrorX := testWidth - 1 - x
			lVal := outL[y*testWidth+x]
			rVal := outR[y*testWidth+mirrorX]
			require.InDelta(t, int(lVal), int(rVal), 2,
				"h-right should mirror h-left: pixel (%d,%d)", x, y)
		}
	}
}

// ==========================================================================
// Additional: TestDipMonotonicity -- brightness decreases then increases
// ==========================================================================

func TestDipMonotonicity(t *testing.T) {
	t.Parallel()

	fb, err := transition.NewFrameBlender(testWidth, testHeight)
	require.NoError(t, err)

	src := testutil.GenerateSolidYUV420(testWidth, testHeight, 200, 128, 128)

	// Phase 1: brightness should decrease from 0.0 to 0.5
	prevY := 255
	for i := 0; i <= 50; i++ {
		pos := float64(i) / 100.0
		out := fb.BlendDip(src, src, pos)
		curY := int(out[0])
		require.LessOrEqual(t, curY, prevY+1,
			"Dip phase 1: Y should decrease at pos=%f", pos)
		prevY = curY
	}

	// Phase 2: brightness should increase from 0.5 to 1.0
	prevY = 0
	for i := 50; i <= 100; i++ {
		pos := float64(i) / 100.0
		out := fb.BlendDip(src, src, pos)
		curY := int(out[0])
		require.GreaterOrEqual(t, curY, prevY-1,
			"Dip phase 2: Y should increase at pos=%f", pos)
		prevY = curY
	}
}

// ==========================================================================
// Additional: TestFTBMonotonicity -- brightness decreases throughout
// ==========================================================================

func TestFTBMonotonicity(t *testing.T) {
	t.Parallel()

	fb, err := transition.NewFrameBlender(testWidth, testHeight)
	require.NoError(t, err)

	src := testutil.GenerateSolidYUV420(testWidth, testHeight, 235, 128, 128)

	prevY := 255
	for i := 0; i <= 100; i++ {
		pos := float64(i) / 100.0
		out := fb.BlendFTB(src, pos)
		curY := int(out[0])
		require.LessOrEqual(t, curY, prevY+1,
			"FTB: Y should decrease at pos=%f", pos)
		prevY = curY
	}
}

// ==========================================================================
// Additional: TestBlendMixCommutesAtHalf -- mix(A,B,0.5) ~ mix(B,A,0.5)
// ==========================================================================

func TestBlendMixCommutesAtHalf(t *testing.T) {
	t.Parallel()

	fb1, err := transition.NewFrameBlender(testWidth, testHeight)
	require.NoError(t, err)
	fb2, err := transition.NewFrameBlender(testWidth, testHeight)
	require.NoError(t, err)

	srcA := testutil.GenerateSolidYUV420(testWidth, testHeight, 80, 100, 200)
	srcB := testutil.GenerateSolidYUV420(testWidth, testHeight, 180, 200, 100)

	outAB := copyFrame(fb1.BlendMix(srcA, srcB, 0.5))
	outBA := copyFrame(fb2.BlendMix(srcB, srcA, 0.5))

	// At midpoint, mix(A,B,0.5) should equal mix(B,A,0.5).
	for i := range outAB {
		require.InDelta(t, int(outAB[i]), int(outBA[i]), 1,
			"Mix should commute at 0.5: byte %d", i)
	}
}

// ==========================================================================
// Additional: TestEasingLinearIsIdentity -- linear easing preserves position
// ==========================================================================

func TestEasingLinearIsIdentity(t *testing.T) {
	t.Parallel()

	c := transition.NewEasingCurve(transition.EasingLinear)
	for i := 0; i <= 1000; i++ {
		tt := float64(i) / 1000.0
		require.Equal(t, tt, c.Ease(tt),
			"Linear easing should be identity at t=%f", tt)
	}
}

// ==========================================================================
// Additional: TestEasingSmoothstepMidpoint -- smoothstep(0.5) = 0.5
// ==========================================================================

func TestEasingSmoothstepMidpoint(t *testing.T) {
	t.Parallel()

	c := transition.NewEasingCurve(transition.EasingSmoothstep)
	require.Equal(t, 0.5, c.Ease(0.5),
		"Smoothstep at 0.5 must be exactly 0.5")
}

// ==========================================================================
// Additional: TestLargeFrameBlend -- verify correctness at realistic size
// ==========================================================================

func TestLargeFrameBlend(t *testing.T) {
	t.Parallel()

	const w, h = 1920, 1080
	fb, err := transition.NewFrameBlender(w, h)
	require.NoError(t, err)

	srcA := testutil.GenerateSolidYUV420(w, h, 16, 128, 128)  // broadcast black
	srcB := testutil.GenerateSolidYUV420(w, h, 235, 128, 128) // broadcast white

	out := fb.BlendMix(srcA, srcB, 0.5)

	// Expected Y = (16+235)/2 = 125.5, rounds to 126 in fixed-point
	expectedY := byte(math.Round((16.0 + 235.0) / 2.0))

	// Spot-check 4 corners and center.
	corners := [][2]int{{0, 0}, {w - 1, 0}, {0, h - 1}, {w - 1, h - 1}, {w / 2, h / 2}}
	for _, pt := range corners {
		yVal, cb, cr := testutil.YUVPixelAt(out, w, h, pt[0], pt[1])
		require.InDelta(t, int(expectedY), int(yVal), 1,
			"Y at (%d,%d)", pt[0], pt[1])
		require.Equal(t, byte(128), cb, "Cb at (%d,%d)", pt[0], pt[1])
		require.Equal(t, byte(128), cr, "Cr at (%d,%d)", pt[0], pt[1])
	}
}

// ==========================================================================
// Determinism: TestBlendDeterminism -- same inputs 20x → byte-exact output
// ==========================================================================

func TestBlendDeterminism(t *testing.T) {
	t.Parallel()

	frameA := testutil.GenerateSolidYUV420(320, 240, 100, 100, 100)
	frameB := testutil.GenerateSolidYUV420(320, 240, 200, 150, 150)

	// First run — reference result.
	fb, err := transition.NewFrameBlender(320, 240)
	require.NoError(t, err)
	reference := copyFrame(fb.BlendMix(frameA, frameB, 0.37))

	// 19 more runs — must be byte-exact identical.
	for i := 0; i < 19; i++ {
		fbI, err := transition.NewFrameBlender(320, 240)
		require.NoError(t, err)
		result := fbI.BlendMix(frameA, frameB, 0.37)
		if !testutil.YUVFrameEqual(reference, result) {
			t.Fatalf("Run %d produced different output (non-determinism detected)", i+1)
		}
	}
}

// ==========================================================================
// Helpers
// ==========================================================================

func posName(pos float64) string {
	switch pos {
	case 0.0:
		return "pos_0.00"
	case 0.25:
		return "pos_0.25"
	case 0.5:
		return "pos_0.50"
	case 0.75:
		return "pos_0.75"
	case 1.0:
		return "pos_1.00"
	default:
		return "pos_other"
	}
}
